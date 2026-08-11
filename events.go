package main

import (
	"github.com/fhs/gompd/v2/mpd"
	"github.com/coder/websocket"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"time"
)
  type wsMsg struct {
    typ websocket.MessageType
    data []byte
  }

type eventState struct {
    lastPlaylistRev int
    lastPlayerEV    IdleEvent
    lastArtHash     uint64
    lastTime        float64
}

  // Globals for wsWatcher:
  var (
//    lastArtHash     uint64
//    lastPlayerEV    IdleEvent
    evState           eventState
  )

/*
func wsWatcher(ctx *wsCtx) {
  log.Printf("[wsW] started")
  log.Printf("[wsW] START watcher ptr=%p conn=%p", ctx, ctx.conn)
  var watcherID = atomic.LoadInt64(&wsWatcherCount)
  log.Printf("[wsW] START watcher id=%d time=%d conn=%p", watcherID, time.Now().UnixNano(), ctx.conn)

  // add this connection to the set
  ctx.mu.Lock()
  if ctx.conns == nil {
    ctx.conns = make(map[*websocket.Conn]struct{})
  }
  ctx.conns[ctx.conn] = struct{}{}
  ctx.mu.Unlock()

  defer func() {
    ctx.mu.Lock()
    delete(ctx.conns, ctx.conn)
    ctx.mu.Unlock()
    log.Println("[wsW] stopped for connection %q", ctx.conn)
  }()

  // persistent state trackers
  log.Printf("[wsW] start initializing func-scoped vars")
  var (
//    lastPlayerEV    IdleEvent
//    lastSongID      int
//    lastArtHash     uint64
//    lastAlbumKey string
  )
  log.Printf("[wsW] finsh initializing func-scoped vars")

//  for ev := range idleEvents {
//    var (
//        msgs []wsMsg
//    )
//
//    msgs, _ = eventProcessor(ev)
//
//
//    }
  time.Sleep(100 * time.Millisecond)
} //func wsWatcher()
*/

func eventProcessor(){
  // ε = Threhold for dedupe elapsed to be false; Δ = 0394 ε = 03b5
    var (
      msgs            []wsMsg  // accumulate messages to send
      ε               float64 = 1.0
//    data            []byte
//    notes           []string
//      img             []byte
      lastPlaylistRev int
      pushArt         bool
      uri             string
      art             *AlbumArtResult
      lastEVtime      float64
      epDebug         bool
      ePdebug         bool
      lastSongID      int
//    allLogEntries   [][]byte
//    songChanged     bool
//    status          map[string]string
//    playlistChanged bool
//    playlistRev     int
    )

  // set the local function debug to true if global debug is true:
  // need to standardize on ePdebug later...
  if debug { epDebug = true; ePdebug = true }

  checkDuration := func(s1, s2 string, ε float64) (bool, float64, error) {
  	// 1. Parse strings to float64
  	v1, err := strconv.ParseFloat(s1, 64)
  	if err != nil {
  		return false, 0, fmt.Errorf("failed to parse first string: %w", err)
  	}

  	v2, err := strconv.ParseFloat(s2, 64)
  	if err != nil {
  		return false, 0, fmt.Errorf("failed to parse second string: %w", err)
  	}

  	// 2. Safety check: Catch strings like "NaN", "Inf", or "-Inf"
  	if math.IsNaN(v1) || math.IsNaN(v2) || math.IsNaN(ε) {
  		return false, 0, fmt.Errorf("cannot compare: one or more values are NaN")
  	}
  	if math.IsInf(v1, 0) || math.IsInf(v2, 0) || math.IsInf(ε , 0) {
  		return false, 0, fmt.Errorf("cannot compare: one or more values are Infinite")
  	}

    Δ := math.Abs(v1-v2)
  	// 3. Perform standard threshold check
  	return math.Abs(v1-v2) > ε, Δ, nil
  }

  dedupePlayerEV := func(current, last IdleEvent) bool { // func dedupePlayerEV

    if epDebug { log.Printf("[eP/dPEV] Started dedupePlayerEV") }
    // Handle nil pointer protection for Status if it is a pointer
    if (current.Status == nil) != (last.Status == nil) {
      return false
    }

    // 1. Compare Top Level Fields
    if current.Subsystem != last.Subsystem ||
      current.SongID != last.SongID ||
      current.SongURI != last.SongURI ||
      current.PlaylistRev != last.PlaylistRev ||
      current.AlbumKey != last.AlbumKey ||
      current.Subscribe != last.Subscribe ||
    // 2. Compare Nested PulseData Fields
      !reflect.DeepEqual(current.PulseData, last.PulseData){
      return false
    }

//    // 2. Compare Nested PulseData Fields
//    if current.PulseData.SinkName != last.PulseData.SinkName ||
//      current.PulseData.Volume != last.PulseData.Volume ||
//      current.PulseData.Mute != last.PulseData.Mute ||
//      current.PulseData.VolString != last.PulseData.VolString ||
//      current.PulseData.PulsePath != last.PulseData.PulsePath ||
//      current.PulseData.PulseServer != last.PulseData.PulseServer ||
//      current.PulseData.PulsePort != last.PulseData.PulsePort {
//      return false
//    }

    // 3. Compare Map Status Fields
    if len(current.Status) != len(last.Status) {
      // If map sizes don't match, something structural changed
      return false
    }

    for key, currentVal := range current.Status {
      // --- CHOOSE WHAT TO IGNORE HERE ---
      if key == "bitrate" || key == "time" {
        continue // Skip checking these keys entirely
      }

      lastVal, exists := last.Status[key]
      if !exists {
        return false // A meaningful value changed or is missing
      }

      // --- ignore only elapsed changes <= delta of n seconds --- //
      // ---    required for user time changes to function     --- //
      if key == "elapsed" {
        // epsilon := 1.0
        e0, _ := strconv.ParseFloat(lastVal, 64)
        e1, _ := strconv.ParseFloat(currentVal, 64)
        Δt := float64(time.Now().UnixNano()) / 1e9 - lastEVtime
        if epDebug { log.Printf("e0: %.3f; e1: %.3f; Δtime: %.3f; ε: %.3f", e0, e1, Δt, ε) }
        if Δt > ε && e0 == 0 && e1 == 0 {
          if epDebug {
            log.Printf("-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-")
            log.Printf("[ep/dPEV] %.3f=%.3f=0 but player events %.3f > %.3f s apart: return false", e0, e1, Δt, ε)
            log.Printf("-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-")
          }
          return false
        } else if Δt > ε || ( e0 == 0 && e1 == 0 ) {
          if epDebug {
            log.Printf("[eP/dPEV] either/or case")
            log.Printf("-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-")
            log.Printf("[eP/dPEV] true? player events    Δt > ε     s apart || (lastElapsed:   e0 = elapsed: e1 = 0)")
            log.Printf("[eP/dPEV] true? player events %.3f > %.3f s apart || (lastElapsed: %.3f ? elapsed: %.3f ? 0)", Δt, ε, e0, e1)
            log.Printf("-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-")
          }
        } else if Δt <= ε && ( e0 != 0 && e1 != 0 ) {
          if epDebug {
            log.Printf("[eP/dPEV] neither/nor case")
            log.Printf("-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-")
            log.Printf("[eP/dPEV] true? player events    Δt <= ε     s apart || (lastElapsed:    e0 != elapsed: e1 != 0)")
            log.Printf("[eP/dPEV] true? player events %.3f <= %.3f s apart || (lastElapsed: %.3f && elapsed: %.3f)", Δt, ε, e0, e1)
            log.Printf("-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-")
          }
        } else if Δt <= ε && e0 == 0 && e1 == 0 {
          if epDebug {
            log.Printf("[eP/dPEV] both true case: return true")
            log.Printf("-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-")
            log.Printf("[eP/dPEV] true? player events    Δt <= ε     s apart && (lastElapsed:    e0 == elapsed: e1 != 0)")
            log.Printf("[eP/dPEV] true? player events %.3f <= %.3f s apart && (lastElapsed: %.3f == elapsed: %.3f)", Δt, ε, e0, e1)
            log.Printf("-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-=-")
          }
        }

//        if float64(time.Now().UnixNano()) / 1e9 - lastEVtime > ε {
//          if t0 == 0 && t1 == 0 {
//            log.Printf("[ep/dPEV] elapsed=lastElapsed=0 but player events > %.3f s apart: return false", ε)
//            return false
//          }
//        } else if t0 == 0 && t1 == 0 {
//          log.Printf("[ep/dPEV] elapsed=lastElapsed=0 but player events <= %.3f s apart: likely duplicate", ε)
//        }

        exceedsEpsilon, _, err := checkDuration(currentVal, lastVal, ε)
        if err != nil || exceedsEpsilon {
          return false
        }
        continue
      }
      if currentVal != lastVal {
        return false // A meaningful value changed or is missing
      }
    }
    log.Printf("[eP/dPEV] dedupePlayerEV returning TRUE")
    return true
  } // func dedupePlayerEV

//  log.Printf("[eP] RECV watcher=%d rev=%d songID=%d", watcherID, ev.PlaylistRev, ev.SongID)
//  log.Printf("[eP] RECV watcher=%d evPtr=%p songID=%d", watcherID, &ev, ev.SongID)

  for ev := range idleEvents {
    msgs = nil
    log.Printf("[eP] for ev := range idleEvents started, ev.Subsystem: %s, # msgs: %d", ev.Subsystem, len(msgs))
    switch ev.Subsystem {
      case "pulseaudio":
        log.Printf("[eP] pulseaudio case started; ev:PulseData: %v", ev.PulseData)
        pd := ev.PulseData

        pulseMu.Lock()
        PulseData = pd
        pulseMu.Unlock()

        msg := map[string]interface{}{
            "system": "pulseaudio",
               "cmd": "changed",
          "response": pd,
        }

        log.Printf("[eP] pulseaudio case, adding msg: %v", msg)

        data, _ := json.Marshal(msg)
        msgs = append(msgs, wsMsg{websocket.MessageText, data})

      case "pauseTimer":
        var (
          err     error
          status  map[string]string
          notes   []string
        )
        js := &StatusV1{}  // declare a fresh StatusV1 just for the pauseTimer case
        // fetch MPD status first
        status, notes, err = MPDtags(nil, "status", "status")
        if err != nil {
          log.Printf("[eP] MPDtags error in pauseTimer case: %v", err)
          continue
        }

        if err := convert2json(status, js); err != nil {
          log.Printf("[eP] convert2json error in pauseTimer case: %v", err)
          continue
        }

        // also add js to msgs
        data, err := json.Marshal(js)
        if err != nil {
          log.Printf("[eP] pauseTimer CASE marshal %v", err)
          break
        }

        msgs = append(msgs, wsMsg{websocket.MessageText, data})
        for _, note := range notes {
          // msgs = append(msgs, []byte(note))
          msgs = append(msgs, wsMsg{websocket.MessageBinary, []byte(note)})
        }

      case "player", "linger", "scrobbler":
        if ev.Subsystem == "scrobbler" { log.Printf("----------------------------BOOOOOOOOOBS---------------") }
        if ! ( ev.Subscribe || ev.Subsystem == "linger" || ev.Subsystem == "scrobbler" ) && dedupePlayerEV(ev, evState.lastPlayerEV) {
//          log.Printf("[eP] Skipping duplicate idle event!---------------------")
          if epDebug { log.Printf("[eP] SKIPPING DUPLICATE IDLE EVENT!---------------------") }
    	    if true {
            if epDebug {
              log.Printf(
                "[eP] dedupePlayerEV ev: songID=%d bitrate=%v time=%v elapsed=%v",
                ev.SongID,
                ev.Status["bitrate"],
                ev.Status["time"],
                ev.Status["elapsed"],
              )
  //            b, _ := json.MarshalIndent(ev, "", "  ")
  //        	  log.Printf("[eP] skipped ev:--------------------------------------\n%s", string(b))
              log.Printf(
                "[eP] dedupePlayerEV last.ev: songID=%d bitrate=%v time=%v elapsed=%v",
                evState.lastPlayerEV.SongID,
                evState.lastPlayerEV.Status["bitrate"],
                evState.lastPlayerEV.Status["time"],
                evState.lastPlayerEV.Status["elapsed"],
              )
            }
            _, delta, _ := checkDuration(ev.Status["elapsed"], evState.lastPlayerEV.Status["elapsed"], 0)
            log.Printf("[ep] elapsed Δ: %.3f ε: %.3f ", delta, ε)
            log.Printf("[eP] Skipping duplicate idle event!---------------------")
          }
          lastEVtime = float64(time.Now().UnixNano()) / 1e9
          continue
        } else {
          if ePdebug {
            log.Printf("[eP] Idle player event!---------------------------------")
            log.Printf(
              "[eP] dedupePlayerEV ev: songID=%d bitrate=%v time=%v elapsed=%v",
              ev.SongID,
              ev.Status["bitrate"],
              ev.Status["time"],
              ev.Status["elapsed"],
            )
//          b, _ := json.MarshalIndent(ev, "", "  ")
//          log.Printf("[eP] ev:------------------------------------------------\n%s", string(b))
            log.Printf(
              "[eP] dedupePlayerEV last.ev: songID=%d bitrate=%v time=%v elapsed=%v",
              evState.lastPlayerEV.SongID,
              evState.lastPlayerEV.Status["bitrate"],
              evState.lastPlayerEV.Status["time"],
              evState.lastPlayerEV.Status["elapsed"],
            )
          }
          _, delta, _ := checkDuration(ev.Status["elapsed"], evState.lastPlayerEV.Status["elapsed"], 0)
          if ev.SongID == evState.lastPlayerEV.SongID {
            log.Printf("[ep] elapsed Δ: %.3f ε: %.3f ", delta, ε)
          }
          if ePdebug { log.Printf("[eP] Idle player event!---------------------------------") }
        }

        evState.lastPlayerEV = ev
//        evState.lastTime = float64(time.Now().UnixNano()) / 1e9
        lastEVtime = float64(time.Now().UnixNano()) / 1e9

        var (
          st          StatusV1
          img         []byte
  //      albumKey    string
  //      songChanged bool
  //      uri         string
  //      err error
  //      status map[string]string
  //      n []string
        )

        log.Printf("[eP] idle event subsystem=%s rev=%d", ev.Subsystem, ev.PlaylistRev)

        // -----------------------
        // STATUS FRAME
        // -----------------------
        resp := verbProcessor("json-status")

        for _, entry := range resp {
          if entry == "" {
            continue
          }
        }

        // ---------------------------
        // UNMARSHAL JSON TO STATUSV1
        // ---------------------------
        if len(resp) > 0 {
          if err := json.Unmarshal([]byte(resp[0]), &st); err != nil {
            log.Printf("[eP] unmarshal error: %v", err)
            continue
          }
        }

        // -----------------------
        // SONG / ALBUM STATE
        // -----------------------
        song := st.Current
        songID := st.Player.SongID
        uri = song.File               // uri needs to exist outside scope

        state.mu.Lock()
        songChanged := false

        if state.lastSongID == 0 || songID != state.lastSongID || ev.PrevSongID != songID || ev.PrevSongID != ev.SongID || lastSongID != songID {
          songChanged = true

          log.Printf("[eP] ==== 1 setting songChanged: %t", songChanged)
          log.Printf("[eP] ==== ev.PrevSongID: %d; songID: %d ====", ev.PrevSongID, songID)
          log.Printf("[eP] ==== ev.PrevSongID: %d; ev.SongID: %d ====", ev.PrevSongID, ev.SongID)
          log.Printf("[eP] ==== lastSongID: %d; SongID: %d ====", lastSongID, songID)


          log.Printf("[eP] ==== setting lastSongID = songID: %d ⟶ %d", lastSongID, songID)

          lastSongID = songID

        }

        log.Printf("[eP] ==== 2 songChanged: %t ====", songChanged)

        state.mu.Unlock()

        // ----------------------
        // SCROBBLER CHECK
        // ----------------------

        if songChanged || ev.Subsystem == "scrobbler" {
          cmd := exec.Command("sudo", "/usr/bin/systemctl", "is-active", "mpd-scrobbler")
          out, err := cmd.CombinedOutput()
          code := cmd.ProcessState.ExitCode()

          raw := strings.TrimSpace(string(out))
          s := strings.Fields(raw)[0]

          log.Printf("[eP] scrobbler: %s (code=%d)", s, code)
          if err != nil && code != 3 {
            log.Printf("[eP] scrobbler status error: %v", err)
            st.Scrobbler.Running = false
            st.Scrobbler.Enabled = state.scrobbler.Enabled
            st.Scrobbler.LastErr = err.Error()
            st.Scrobbler.ExitCode = code
          } else {
            switch s {
              case "active":
                st.Scrobbler.Enabled = state.scrobbler.Enabled
                st.Scrobbler.Running = true
                st.Scrobbler.ExitCode = 0
                st.Scrobbler.LastErr = ""

              case "inactive":
                st.Scrobbler.Enabled = state.scrobbler.Enabled
                st.Scrobbler.Running = false
                st.Scrobbler.ExitCode = 0   // IMPORTANT: not an error state
                st.Scrobbler.LastErr = ""

              default:
                st.Scrobbler.Enabled = state.scrobbler.Enabled
                st.Scrobbler.Running = false
                st.Scrobbler.ExitCode = code
                st.Scrobbler.LastErr = raw
            }
          }
          state.mu.Lock()
          state.scrobbler.Running = st.Scrobbler.Running
          state.scrobbler.LastErr = st.Scrobbler.LastErr
          state.scrobbler.ExitCode = st.Scrobbler.ExitCode
          state.mu.Unlock()
        }


//          out, err := exec.Command("sudo", "/usr/bin/systemctl", "is-active", "mpd-scrobbler").Output()
//
//          if err != nil {
//            log.Printf("[eP] scrobbler status error: %v", err)
//            st.Scrobbler.Running = false
//            st.Scrobbler.Enabled = state.scrobbler.Enabled
//            st.Scrobbler.LastErr = err.Error()
//            st.Scrobbler.ExitCode = 1
//          } else {
//            raw := strings.TrimSpace(string(out))
//
//            s := strings.Fields(raw)[0]
//            log.Printf("[eP] scrobbler: %s", s)
//
//            switch s {
//            case "active":
//              st.Scrobbler.Running = true
//              st.Scrobbler.Enabled = state.scrobbler.Enabled
//              st.Scrobbler.LastErr = ""
//              st.Scrobbler.ExitCode = 0
//
//            case "inactive":
//              st.Scrobbler.Running = false
//              st.Scrobbler.Enabled = state.scrobbler.Enabled
//              st.Scrobbler.LastErr = ""
//              st.Scrobbler.ExitCode = 0
//
//            default:
//              st.Scrobbler.Running = false
//              st.Scrobbler.Enabled = state.scrobbler.Enabled
//              st.Scrobbler.LastErr = raw
//              st.Scrobbler.ExitCode = 1
//            }
//          }
//        state.mu.Lock()
//        state.scrobbler.Running = st.Scrobbler.Running
//        state.scrobbler.LastErr = st.Scrobbler.LastErr
//        state.scrobbler.ExitCode = st.Scrobbler.ExitCode
//        state.mu.Unlock()
//        }

        // -----------------------
        // STATUS FRAME
        // -----------------------
        log.Printf("[eP] PUSH subscribed status subsystem=%s subscribe=%v", ev.Subsystem, ev.Subscribe)
        env := map[string]any{
            "system": "subscribed",
               "cmd": "json-status",
          "response": st,
  //           "debug": time.Now().UnixNano(),
        }
        data, _ := json.Marshal(env)
        if debug { log.Printf("OUTGOING JSON: %s", data) }
        msgs = append(msgs, wsMsg{websocket.MessageText, data})



        // -----------------------
        // LOG FRAME (SEPARATE)
        // -----------------------
          log.Printf("[eP] ==== 3 songChanged: %t ====", songChanged)
        if songChanged || ev.Subscribe {
          log.Printf("[eP] ==== Entering Log Frame ====")
          resp := verbProcessor("json-log")

          var logs []any
          for _, entry := range resp {
            if entry == "" {
              continue
            }

            var parsed any
            if err := json.Unmarshal([]byte(entry), &parsed); err != nil {
              parsed = entry
            }

            logs = append(logs, parsed)
          }

          msg := map[string]any{
            "system": "subscribed",
            "cmd":    "json-log",
            "response": logs,
  //          "debug": time.Now().UnixNano(),
          }

          data, err := json.Marshal(msg)
          if err != nil {
            log.Printf("[eP] log marshal failed: %v", err)
            continue
          }

          msgs = append(msgs, wsMsg{websocket.MessageText, data})
        }

        // -----------------------
        // ALBUM ART (HASH-BASED)
        // -----------------------
        img = nil
        art = nil
        err := mpdDo(func(c *mpd.Client) error {
          var err2 error
          var jsArt map[string]any
          art, err2 = getBestAlbumArt(c, uri)
          if err2 != nil {
            log.Printf("[eP] getBestAlbumArt error for %q: %v", uri, err2)
            img = nil
            return err2
          }

          if art != nil {
            img = art.Img
            if evState.lastArtHash == 0 {
              if epDebug {
                log.Printf("[eP] !!!!!!!!!!!!!!!!!!!!!!!!--> lastArtHash=%d <--!!!!!!!!!!!!!!!!!!!!!!!!",
                evState.lastArtHash)
              }
            }
            hashMatch := art.Hash ==  evState.lastArtHash
            log.Printf(
              "[eP] AlbumArt selected %s hash=%d (%d bytes)",
              art.Source,
              art.Hash,
              len(img),
            )

            log.Printf("[eP] ART CHECK hashMatch: %v art=%d last=%d subscribe=%v subsystem=%s",
              hashMatch,
              art.Hash,
              evState.lastArtHash,
              ev.Subscribe,
              ev.Subsystem,
            )
            // dedupe based on image hash (NOT albumKey)
            pushArt = false
            if art.Size > 0 && (art.Hash != evState.lastArtHash || ev.Subscribe) {
              pushArt = true
              if art.Hash != evState.lastArtHash {
                if epDebug {
                  log.Printf("[eP] AAAAAAAAAAAALLLLLLLLLLLLERRRRRRRRRRRRRRRRRT! artHash=%d / lastArtHash=%d",
                  art.Hash, evState.lastArtHash)
                }
                evState.lastArtHash = art.Hash
              }
              jsArt = map[string]any{
                  "system": "subscribed",
                     "cmd": "albumart",
                    "note": "update",
                "response": art,
  //                 "debug": time.Now().UnixNano(),
              }
            } else if art.Size > 0 {
              pushArt = false
              jsArt = map[string]any{
                  "system": "subscribed",
                     "cmd": "albumart",
                    "note": "noop",
  //                 "debug": time.Now().UnixNano(),
                "response": map[string]any{
                       "msg": "album art unchanged",
                      "hash": art.Hash,
                },
              }
            } else if art.Size == 0 {
              log.Printf("[eP] The art.Size == 0 branch should be unreachable!")
              pushArt = false
              jsArt = map[string]any{
                  "system": "subscribed",
                     "cmd": "albumart",
                "response": map[string]any{
                       "msg": "no albumart retrieved",
                      "hash": 0,
                },
  //                 "debug": time.Now().UnixNano(),
              }
            }
          } else {
            img = nil

            // synthesize a fake "art" state WITHOUT touching art
            jsArt = map[string]any{
                "system": "subscribed",
                   "cmd": "albumart",
                  "note": "noart",
              "response": map[string]any{
                     "msg": "no albumart retrieved",
                    "hash": 0,
                },
  //                 "debug": time.Now().UnixNano(),
            }
          }

          data, err := json.Marshal(jsArt)
          if err != nil {
            log.Printf("[eP] Art mashal failed %v", err)
          } else {
            // Important: enforce ordering contract
            msgs = append(msgs, wsMsg{websocket.MessageText, data})

            if pushArt && len(img) > 0 {
              msgs = append(msgs, wsMsg{websocket.MessageBinary, img})
              log.Printf("[eP] Attaching img, len: %d", len(img))
  //          } else {
  //            msgs = append(msgs, wsMsg{websocket.MessageBinary, []byte{}})
            }
          }

          return err
        }, "wsWatcher-gBAA")

        if err != nil {
          log.Printf("[eP] Art: mpdDo/gBAA failed: %v", err)
          continue
        }
      // case "player"

      case "playlist":
        if ev.PlaylistRev != lastPlaylistRev {
          lastPlaylistRev = ev.PlaylistRev

        msg := map[string]interface{}{
          "system": ev.Subsystem,
          "cmd": "changed",
          "response": map[string]interface{} {
            "playlist_rev": ev.PlaylistRev,
          },
        }
        data, _ := json.Marshal(msg)
        msgs = append(msgs, wsMsg{websocket.MessageText, data})
        } else {
          continue
        }

      default:
        dbg("[eP] default case of eventProcessor switch, continuing; ev.Subsytem: %s", ev.Subsystem)
        continue
    } // end of switch

    if msgs != nil {
      if ev.Target == nil {
        log.Printf("[eP] calling wsBroadcastMgr, ev.Subsystem: %s", ev.Subsystem)
      } else {
        log.Printf("[eP] calling wsBroadcastMgr, ev.Subsystem: %s, TARGET=%s", ev.Subsystem, cid(ev.Target))
      }
      wsBroadcastMgr(msgs, ev.Target)
    } else {
      log.Printf("[eP] NOT calling wsBroadcastMgr, ev.Subsystem: %s", ev.Subsystem)
    }
  }
} // func eventProcessor

