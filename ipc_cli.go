package main

import (
	"github.com/fhs/gompd/v2/mpd"
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)
func startIPC(path string) {
  ln, err := net.Listen("unix", path)
  if err != nil {
    log.Fatalf("[sIPC] Failed to listen on %s: %v", path, err)
  }
  defer ln.Close()

  // Now the socket exists → set ownership and permissions
  //  os.Chown(socketPath, uid, gid) // uid/gid for mpd user
  os.Chmod(socketPath, 0660)     // owner+group read/write

  log.Printf("[sIPC] IPC listening on %s", path)

  for {
    conn, err := ln.Accept()
    if err != nil {
      log.Printf("[sIPC] IPC accept error: %v", err)
      continue
    }
    go handleUDS(conn)
//    go ipcHandler(conn)
  }
} // func startIPC(path string)


// setPaused sets the paused state and returns whether the block limit expired
func setPaused(paused bool) (expired bool, limit int) {
  state.mu.Lock()
  defer state.mu.Unlock()

  state.paused = paused

  limit = state.baseLimit
  if state.blockOn && state.blockLimit > 0 {
    limit = state.blockLimit
  }

  expired = state.count >= limit
  if expired && !paused {
    state.transition = true
  }

  return
} // func setPaused(paused bool) (expired bool, limit int)


// shellQuote returns a shell-escaped string
func verbProcessor(csv string) []string {
  var responses []string
  var green = "\033[32m"
  var tput0 = "\033[0m"    // name derrived from `tput[ sgr]0`
  var red = "\033[31m"

  parts := strings.Split(csv, ",")
  for _, part := range parts {
    line := strings.TrimSpace(part)
    if line == "" {
      continue
    }

    fields := strings.Fields(line)
    if len(fields) == 0 {
      continue
    }

    cmd := strings.ToLower(fields[0])
    var resp string

    switch cmd {
//    case "test":
//      testMPDtags()
//      continue

    case "json-log":
      log.Printf("[vP] IPC: received json-log command")

      lines := mpdLogParse(24)
      responses = responses[:0]

      err := mpdDo(func(c *mpd.Client) error {
        for _, ll := range lines {
          tags, notes, err := MPDtags(c, ll.Path, ll.Action)
          if err != nil {
            return err
          }

          dbg("DBG json-log tags=%+v", tags)
          dbg("DBG json-log notes=%v", notes)

          // --- convert map to LogEntryV1 using convert2json() ---
          entry := &LogEntryV1{}

          err = convert2json(
            tags,
            entry,
            ll.Timestamp,
            ll.Action,
            notes,
            ll.Path,
          )
          if err != nil {
            return fmt.Errorf("convert2json failed: %v", err)
          }

          // append as JSON string
          b, _ := json.Marshal(entry)
          responses = append(responses, string(b))
        }

        return nil
      }, "JSONLog")

      if err != nil {
        responses = append(responses, fmt.Sprintf("error=%v", err))
      }

    case "json-status":
      log.Printf("[vP] IPC: received json-status command")

      // --- fetch status/current/next via MPDtags ---
      data, notes, err := MPDtags(nil, "status", "status")
      if err != nil {
        responses = append(responses, fmt.Sprintf("ERR fetching status: %v", err))
        break
      }
      dbg("DBG json-status data=%+v", data)

      // --- convert to JSON struct ---
      js := &StatusV1{}
      err = convert2json(data, js)
      if err != nil {
        responses = append(responses, fmt.Sprintf("ERR JSON conversion: %v", err))
        break
      }

      // --- marshal JSON struct to string ---
      jsonBytes, err := json.Marshal(js)
      if err != nil {
        responses = append(responses, fmt.Sprintf("ERR JSON marshal: %v", err))
        break
      }
      responses = append(responses, string(jsonBytes))

      // --- optionally append notes if any ---
      if len(notes) > 0 {
        for _, note := range notes {
          if note != "" {
            responses = append(responses, note)
          }
        }
      }

    case "getlog":
      var numEntries = 24
      log.Printf("IPC: received getlog command")
      json := getLogEntriesJSON(numEntries)
//    json := mpdLogParse(numEntries)
      for _, ll := range json {
        // simplest: format as string
        resp := fmt.Sprintf("%s", ll)
        responses = append(responses, resp)
      }

    case "status":
      state.mu.Lock()
      limit := state.baseLimit
      if state.blockOn && state.blockLimit > 0 {
        limit = state.blockLimit
      }
      ds := deriveStateLocked(state.lastSongZI, state.lastSongID, state.lastSongURI, limit)
      state.mu.Unlock()

      // append lines to responses slice
      responses = append(responses,
        fmt.Sprintf("writetime=%s", ds.WriteTime),
        fmt.Sprintf("lingersong=%d", ds.SongZI+1),
        fmt.Sprintf("lingersongid=%d", ds.SongID),
        fmt.Sprintf("lingerpause=%d", ds.Paused),
        fmt.Sprintf("lingercount=%d", ds.Count),
        fmt.Sprintf("lingerbase=%d", ds.BaseLimit),
        fmt.Sprintf("lingerlimit=%d", ds.Limit),
        fmt.Sprintf("lingerblocklmt=%d", ds.BlockLimit),
        fmt.Sprintf("lingerpid=%d", ds.PID),
      )
      if xy.active {
        responses = append(responses,
          fmt.Sprintf("lingerxy=%d", ds.LingerXY),
          fmt.Sprintf("lingerx=%d", ds.LingerX+1),
          fmt.Sprintf("lingery=%d", ds.LingerY+1),
        )
      }

    case "mpc":
      var st mpd.Attrs // declare outside so we can use it later

      log.Printf("IPC: received mpc command")

      // --- update persistent state ---
      state.mu.Lock()
      deriveStateLocked(state.lastSongZI, state.lastSongID, state.lastSongURI, state.baseLimit)
      state.mu.Unlock()

      // --- first batch: status + current song ---
      // We batch these to save one round-trip to MPD
      err := mpdDo(func(c *mpd.Client) error {
        var err error
        // --- MPD status ---
        st, err = c.Status()
        if err != nil {
          return err
        }
        for k, v := range st {
          k = strings.ToLower(k)
          responses = append(responses,
            fmt.Sprintf("%s=%s", k, v),
          )
        }

        // --- current song ---
        cs, err := c.CurrentSong()
        if err != nil {
          return err
        }
        for k, v := range cs {
          k = strings.ToLower(k)
          responses = append(responses,
            fmt.Sprintf("%s=%s", k, v),
          )
        }

        return nil
      }, "Status, CurrentSong") // <- command name for logging

      if err != nil {
        responses = append(responses,
          fmt.Sprintf("ERR mpc failed: %v", err),
        )
      }

      // --- extract next song index from MPD status ---
      nidStr, ok := st["nextsong"]
      if !ok {
        // no next song available
        log.Printf("mpc: no next song in status")
        break
      }

      nextID, err := strconv.Atoi(nidStr)
      if err != nil {
        log.Printf("mpc: invalid nextsongid %q", nidStr)
        break
      }

      // --- second call: get next song info using playlistindex (0-based) ---
      // playlistinfo expects 0-indexed, nextID from status is 1-indexed
      err = mpdDo(func(c *mpd.Client) error {
        ne, err := c.PlaylistInfo(nextID, -1)
        if err != nil {
          return err
        }
        for _, song := range ne {
          for k, v := range song {
            kv := ("next_"+strings.ToLower(k)+"="+v)
            responses = append(responses,
//            fmt.Sprintf("next_%s=%s", k, v),
              fmt.Sprintf("%s", kv),
            )
          }
        }
        return nil
      }, "PlaylistInfo nextID")

      if err != nil {
        responses = append(responses,
          fmt.Sprintf("ERR mpc failed: %v", err),
        )
      }

      for i, line := range responses {
        responses[i] = shellQuoteKV(line)
      }

      responses = append(responses, verbProcessor("status")...)
    // esac "mpc"

    case "mpd-current":
      var st mpd.Attrs // keep status around for next-song lookup

      log.Printf("IPC: received mpd-current command")

      // --- update persistent mpdlinger state ---
      state.mu.Lock()
      deriveStateLocked(state.lastSongZI, state.lastSongID, state.lastSongURI, state.baseLimit)
      state.mu.Unlock()

      // --- first batch: status + current song ---
      // Same batching pattern as mpc to avoid extra MPD round trips
      err := mpdDo(func(c *mpd.Client) error {
        var err error

        // --- MPD status ---
        st, err = c.Status()
        if err != nil {
          return err
        }

        for k, v := range st {
          k = strings.ToLower(k)

          switch k {
          case "state":
            // keep raw for logic
            u_state := v

            // normalize for display
            switch v {
            case "play":
              v = green + "playing" + tput0
            case "pause":
              v = red + "paused" + tput0
            case "stop":
              v = red + "stopped" + tput0
            }

            // emit both
            responses = append(responses,
              fmt.Sprintf("%s=%s", k, v),         // normalized display
              fmt.Sprintf("u_%s=%s", k, u_state), // raw
            )
            continue

          case "time":
            // mpd format: elapsed:duration (both already rounded by mpd)
            // mpd-current uses THIS for percent
            parts := strings.SplitN(v, ":", 2)
            if len(parts) == 2 {
              elap, e1 := strconv.Atoi(parts[0])
              dur,  e2 := strconv.Atoi(parts[1])
              if e1 == nil && e2 == nil && dur > 0 {
                pct := (100 * elap) / dur

//                // mpd-current emits empty percent_time first
//                responses = append(responses, "percent_time=")

                responses = append(responses,
                  fmt.Sprintf("percent=%d%%", pct),
                  fmt.Sprintf("percent_time=%d", pct),
                )
              }
            }
            continue

          case "duration":
//             responses = append(responses,
//                 fmt.Sprintf("total_time=%s", v),
//             )
             continue

          case "song":
            songpos, err := strconv.Atoi(v) // convert string to int
            if err != nil {
              songpos = 0 // fallback if conversion fails
            }
            songpos++ // add 1, like in bash
            responses = append(responses,
              fmt.Sprintf("song_position=%d", songpos),
              fmt.Sprintf("songpos=%d", songpos),
            )
            continue

          case "playlistlength":
            responses = append(responses,
              fmt.Sprintf("pllength=%s", v),
              fmt.Sprintf("song_length=%s", v),
              fmt.Sprintf("%s=%s", k, v),
            )
            continue

          case "repeat":
            u_repeat := v
            if v == "1" {
              v = green + "⟳" + tput0
            } else {
              v = "⟳"
            }
            responses = append(responses,
              fmt.Sprintf("%s=%s", k, v),
              fmt.Sprintf("u_%s=%s", k, u_repeat),
            )
            continue

          case "consume":
            u_consume := v
            if v == "1" {
              v = "✅"
            } else {
              v = "❌"
            }
            responses = append(responses,
              fmt.Sprintf("%s=%s", k, v),
              fmt.Sprintf("u_%s=%s", k, u_consume),
            )
            continue

          case "random":
            u_random := v
            if v == "1" {
              v = "✅"
            } else {
              v = "❌"
            }
            responses = append(responses,
              fmt.Sprintf("%s=%s", k, v),
              fmt.Sprintf("u_%s=%s", k, u_random),
            )
            continue
          }

          // generic append for everything else
          responses = append(responses,
            fmt.Sprintf("%s=%s", k, v),
          )
        }

        // --- current song ---
        cs, err := c.CurrentSong()
        if err != nil {
          return err
        }
        for k, v := range cs {
          k = strings.ToLower(k)

          if strings.HasPrefix(k, "musicbrainz_") {
            if k == "musicbrainz_releasetrackid" {
              k = "musicbrainz_reltrackid"
            }
            nk := "mb" + k[len("musicbrainz_"):]
            responses = append(responses, fmt.Sprintf("%s=%s", nk, v))
            continue
          }

          // mpd-current emits filepath, not file
          if k == "file" {
            responses = append(responses,
              fmt.Sprintf("filepath=%s", v),
            )
            continue
          }

          if k == "duration" {
              responses = append(responses,
                  fmt.Sprintf("duration=%s", v),
                  fmt.Sprintf("total_time=%s", v),  //legacy support value
              )
          }

          // remove next_last-modified
          if k == "last-modified" {
            continue
          }

          responses = append(responses,
            fmt.Sprintf("%s=%s", k, v),
          )
        }

        return nil
      }, "Status, CurrentSong (mpd-current)")

      if err != nil {
        responses = append(responses,
          fmt.Sprintf("ERR mpd-current failed: %v", err),
        )
        break
      }

      // --- next song ---
      nidStr, ok := st["nextsong"]
      if ok {
        nextID, err := strconv.Atoi(nidStr)
        if err == nil {
          err = mpdDo(func(c *mpd.Client) error {
            ne, err := c.PlaylistInfo(nextID, -1)
            if err != nil {
              return err
            }
            for _, song := range ne {
              for k, v := range song {
                k = strings.ToLower(k)

                if strings.HasPrefix(k, "musicbrainz_") {
                  if k == "musicbrainz_releasetrackid" {
                    k = "musicbrainz_reltrackid"
                  }
                  nk := "next_mb" + k[len("musicbrainz_"):]
                  responses = append(responses, fmt.Sprintf("%s=%s", nk, v))
                  continue
                }

                // filepath, not file
                if k == "file" {
                  responses = append(responses,
                    fmt.Sprintf("next_filepath=%s", v),
                  )
                  continue
                }

                // remove next_last-modified
                if k == "last-modified" {
                  continue
                }

                responses = append(responses,
                  fmt.Sprintf("next_%s=%s", k, v),
                )
              }
            }
            return nil
          }, "PlaylistInfo nextID (mpd-current)")
        }
      }

      if err != nil {
        responses = append(responses,
          fmt.Sprintf("ERR mpd-current failed: %v", err),
        )
      }

      // --- shell-quote everything once, at the end ---
      for i, line := range responses {
        responses[i] = shellQuoteKV(line)
      }

      // --- append mpdlinger status (same as mpc) ---
      responses = append(responses, verbProcessor("status")...)

    // esac "mpd-current"

    case "pause":
      log.Printf("IPC: received pause command")
      expired, limit := setPaused(true)
      log.Printf("STATE CHANGE: paused=%v expired=%v count=%d limit=%d transition=%v",
        state.paused, expired, state.count, limit, state.transition)
      state.mu.Lock()
      state.paused = true
      deriveStateLocked(state.lastSongZI, state.lastSongID, state.lastSongURI, limit)
      state.mu.Unlock()
      resp = "Paused"

    case "resume":
      log.Printf("IPC: received resume command")
      expired, limit := setPaused(false)
      if expired {
        _ = mpdDo(func(c *mpd.Client) error { return c.Random(true) }, "IPC-resume")
      }
      mpdPlayPause(true, "IPC-resume")
      log.Printf("STATE CHANGE: paused=%v expired=%v count=%d limit=%d transition=%v",
        state.paused, expired, state.count, limit, state.transition)
      resp = "Resumed"

    case "toggle":
      paused := !state.paused
      log.Printf("IPC: received toggle command")
      expired, limit := setPaused(paused)
      if !paused && expired {
        _ = mpdDo(func(c *mpd.Client) error { return c.Random(true) }, "IPC-toggle-resume")
      }
      if !paused {
        mpdPlayPause(true, "IPC-toggle-resume")
      }
      log.Printf("STATE CHANGE: paused=%v expired=%v count=%d limit=%d transition=%v",
        state.paused, expired, state.count, limit, state.transition)
      resp = map[bool]string{true: "Paused", false: "Resumed"}[paused]

    case "next", "skip":
      log.Printf("IPC: received %s command", cmd)
      isNext := (cmd == "next")

      if isNext {
        state.mu.Lock()
        state.transition = true
        state.paused = false
        state.mu.Unlock()
      }

      _ = mpdDo(func(c *mpd.Client) error {
        cs, err := c.CurrentSong()
        if err != nil {
          return err
        }
        csfile := cs["file"]
        if csfile != "" && skippedList != "" {
          if err := c.PlaylistAdd(skippedList, csfile); err != nil {
            return err
          }
        }
        if isNext {
          if err := c.Random(true); err != nil {
            return err
          }
        }
        if err := c.Next(); err != nil {
          return err
        }
        return c.Play(-1)
      }, "IPC-"+cmd)

      log.Printf(
        "STATE CHANGE: [IPC-%s] count=%d baseLimit=%d blockLimit=%d transition=%v paused=%v",
        cmd, state.count, state.baseLimit, state.blockLimit, state.transition, state.paused,
      )
      resp = map[bool]string{
        true:  "Advanced to next block",
        false: "Skipped to next track",
      }[isNext]

    case "count":
     if len(fields) != 2 {
        return []string{"ERR count requires a value"}
      }

      n, err := strconv.Atoi(fields[1])
      if err != nil || n < 0 {
        return []string{"ERR invalid count"}
      }

      state.mu.Lock()
      state.count = n

      limit := state.baseLimit
      if state.blockOn && state.blockLimit > 0 && state.blockLimit != state.baseLimit {
        limit = state.blockLimit
      }

      state.transition = state.count >= limit
      deriveStateLocked(state.lastSongZI, state.lastSongID, state.lastSongURI, limit)
      state.mu.Unlock()

      err = mpdDo(func(c *mpd.Client) error {
        return c.Random(state.transition)
      }, "count")

      log.Printf("STATE CHANGE: [IPC] count set=%d", n)

      resp = fmt.Sprintf("Count set to %d", n)

    case "limit":
      n := 0
      if len(fields) >= 2 {
        n, _ = strconv.Atoi(fields[1])
      }

      state.mu.Lock()
      if n <= 0 {
        state.baseLimit = defaultLimit
      } else {
        state.baseLimit = n
      }

      limit := state.baseLimit
      if state.blockOn && state.blockLimit > 0 && state.blockLimit != state.baseLimit {
        limit = state.blockLimit
      }
      deriveStateLocked(state.lastSongZI, state.lastSongID, state.lastSongURI, limit)
      state.mu.Unlock()

      log.Printf("STATE CHANGE: [IPC] persistent limit set=%d (effective=%d)",
        state.baseLimit, limit)
      resp = fmt.Sprintf("Persistent limit set to %d", n)

    case "blocklimit":
      n := 0
      if len(fields) >= 2 {
        n, _ = strconv.Atoi(fields[1])
      }

      state.mu.Lock()
      if n < 0 {
        resp = "Invalid block limit"
        state.mu.Unlock()
        responses = append(responses, resp)
        continue
      }

      if n == 0 {
        state.blockLimit = 0
        state.blockOn = false
        state.transition = state.count >= state.baseLimit
      } else {
        state.blockLimit = n
        state.blockOn = true
        state.transition = false
      }

      limit := state.baseLimit
      if state.blockLimit > 0 && state.blockLimit != state.baseLimit {
        limit = state.blockLimit
      }

      deriveStateLocked(state.lastSongZI, state.lastSongID, state.lastSongURI, limit)
      state.mu.Unlock()

      err := mpdDo(func(c *mpd.Client) error {
        return c.Random(false)
      }, "IPC-blocklimit")
      if err != nil {
        log.Printf("[IPC-blocklimit] MPD command failed: %v", err)
      }

      log.Printf("STATE CHANGE: [IPC] block limit set=%d (effective=%d), count=%d, transition=%v",
        state.blockLimit, limit, state.count, state.transition)
      resp = fmt.Sprintf("Block limit set to %d", n)

    case "verbose":
      val := ""
      if len(fields) >= 2 {
        val = strings.ToLower(fields[1])
      }
      state.mu.Lock()
      verbose = (val == "on")
      state.mu.Unlock()
      log.Printf("[IPC] Verbose mode turned %s", val)
      resp = "Verbose mode " + val

    case "state":
      if len(fields) < 2 {
        resp = "State requires a path argument"
        continue
      }

      path := filepath.Clean(fields[1])

      f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0644)
      if err != nil {
        resp = "State path not writable: " + err.Error()
        log.Printf("State path not writable: %v", err)
        continue
      }
      f.Close()

      state.mu.Lock()
      statePath = path
      stateEnabled = true
      state.mu.Unlock()

      log.Printf("[IPC] State file enabled at %q", path)
      resp = "State file enabled at " + path

    case "version":
      writeOut := func(msg string) {
        fmt.Println(msg)
        resp += msg + "\n"
      }

      writeOut(fmt.Sprintf("mpdgolinger daemon   %s", version))

      err := mpdDo(func(c *mpd.Client) error {
        proto := c.Version()
        writeOut(fmt.Sprintf("mpd protocol version %s", proto))
        return nil
      }, "version")

      if err != nil {
        writeOut("mpd protocol version unavailable\n")
      }

      if mpdPath != "" {
        cmd := exec.Command(mpdPath, "--version")
        stdout, err := cmd.StdoutPipe()
        if err != nil {
          writeOut(fmt.Sprintf("failed to get %s version: %v", mpdPath, err))
          break
        }

        if err = cmd.Start(); err != nil {
          writeOut(fmt.Sprintf("failed to start %s: %v", mpdPath, err))
          break
        }

        scanner := bufio.NewScanner(stdout)
        if scanner.Scan() {
          line := strings.TrimSpace(scanner.Text())
          re := regexp.MustCompile(`^Music Player Daemon .* \(v([0-9]+\.[0-9]+\.[0-9]+)\)$`)
          matches := re.FindStringSubmatch(line)
          verStr := line
          if len(matches) == 2 {
            verStr = matches[1]
          }
          writeOut(fmt.Sprintf("%s version %s", mpdPath, verStr))
        }
        _ = cmd.Wait()
      } else {
        writeOut(fmt.Sprintf("No path available for mpd binary"))
      }


    case "exit", "quit":
      log.Printf("[IPC] Received %s, shutting down", cmd)
      requestShutdown()
      resp = "OK"

    case "xy":
      x, _ := strconv.Atoi(fields[1])
      y, _ := strconv.Atoi(fields[2])
      inc, _ := strconv.Atoi(fields[3])

      xy.start = x
      xy.end = y
      xy.active = true

      if verbose {
        log.Printf("[IPC] xy.active: %t\n", xy.active)
        log.Printf("[IPC]  xy.start: %d\n", xy.start)
        log.Printf("[IPC]    xy.end: %d\n", xy.end)
        log.Printf("[IPC]       inc: %d\n", inc)
        log.Printf("[IPC] XY mode initiated: %d → %d (ZI)", x, y)
      }

      _ = mpdDo(func(c *mpd.Client) error {
        st, err := c.Status()
        if err != nil {
          return err
        }
        state.mu.Lock()
        state.consume = (st["consume"] == "1")  // immediately record consume mode in the state struct
        state.mu.Unlock()
        if err := c.Consume(true); err != nil {
          log.Printf("[XY] failed to enable consume: %v", err)
        }
        if err := c.Random(false); err != nil {
          log.Printf("[XY] failed to disable random: %v", err)
        }
        // jump to playlist position corresponding to xy.start
        songZI, _ := strconv.Atoi(st["song"])
        pllength, _ := strconv.Atoi(st["playlistlength"])

        if xy.start == -1 {
          xy.start = songZI
        }
        if inc == 1 {
          xy.end = xy.end + xy.start + 1
          if xy.start > xy.end {
            xy.start, xy.end = xy.end, xy.start // fix it if the increment was negative
          }
        }
        if xy.end == -1 { xy.end = pllength - 1 } // pllength is not zero-indexed!

        if songZI != xy.start {
          if err := c.Play(xy.start); err != nil {
            log.Printf("[XY] failed to jump to start: %v", err)
          }
        }
        return nil
      }, "xy-init")

      log.Printf("[IPC] XY mode enabled: %d → %d (ZI)", xy.start, xy.end)
      if verbose { log.Printf("[IPC] Previous consume state: %t", state.consume) }
      resp = fmt.Sprintf("XY mode enabled: %d → %d", xy.start+1, xy.end+1)

    case "xyoff":
      xy.active = false
      _ = mpdDo(func(c *mpd.Client) error {
        if err := c.Consume(state.consume); err != nil {
          log.Printf("[XY] failed to restore consume=%t: %v", state.consume, err)
        }
        return nil
      }, "xy-restore-consume")
      log.Printf("[IPC] XY mode turned off")
      resp = "XY Mode turned off"

    default:
      resp = "Unknown command: " + cmd
    }
//    if len(resp) > 0 {
      responses = append(responses, resp)
//    }
  }


  state.mu.Lock()
    songID  := state.lastSongID
    songZI  := state.lastSongZI
    songURI := state.lastSongURI
    limit   := state.baseLimit
    if state.blockOn && state.blockLimit > 0 {
      limit = state.blockLimit
    }
    deriveStateLocked(songZI, songID, songURI, limit)
  state.mu.Unlock()

  if debug {
    for i, r := range responses {
      log.Printf("[VP DEBUG] resp[%d]=%q len=%d", i, r, len(r))
    }
  }

  return responses
} // func verbProcessor(csv string) []string


// handleTCP handles a single TCP client, reading commands and writing responses.
func handleTCP(conn net.Conn) {
  defer conn.Close()
  scanner := bufio.NewScanner(conn)

  if scanner.Scan() {
    line := scanner.Text()
    if line != "" {
      responses := verbProcessor(line)
      for _, resp := range responses {
        fmt.Fprintln(conn, resp)
      }
      log.Printf("Executed command(s) from %s: %q", conn.RemoteAddr(), line)
    }
  }

  if err := scanner.Err(); err != nil {
    log.Printf("TCP client %s scanner error: %v", conn.RemoteAddr(), err)
  }
} // func handleTCP()


// handleUDS handles a single UDS client, reading commands and writing responses.
func handleUDS(conn net.Conn) {
  defer conn.Close()
  scanner := bufio.NewScanner(conn)

  if scanner.Scan() {
    line := scanner.Text()
    if line != "" {
      responses := verbProcessor(line)
      for _, resp := range responses {
        fmt.Fprintln(conn, resp)
      }
      log.Printf("Executed UDS command(s): %q", line)
    }
  }

  if err := scanner.Err(); err != nil {
    log.Printf("UDS connection error: %v", err)
  }
} // func handleUDS()


// sendIPCCommand sends a command to the daemon via IPC socket returns all response lines
func sendIPCCommand(cmd string) ([]string, error) {
    conn, err := net.Dial("unix", socketPath)
    if err != nil {
        return nil, fmt.Errorf("failed to connect to daemon: %w", err)
    }
    defer conn.Close()

    // send command
    if _, err := fmt.Fprintln(conn, cmd); err != nil {
        return nil, fmt.Errorf("failed to send command: %w", err)
    }

    // read all lines
    scanner := bufio.NewScanner(conn)
    var lines []string
    for scanner.Scan() {
        lines = append(lines, scanner.Text())
    }

    if err := scanner.Err(); err != nil {
        return lines, fmt.Errorf("scanner error: %w", err)
    }

    return lines, nil
} // func sendIPCCommand(cmd string) ([]string, error) {


// acceptClients accepts TCP client connections and delegates handling
func acceptClients(ln net.Listener) {
  log.Printf("[aC] acceptClients started")
  for {
    conn, err := ln.Accept()
    if err != nil {
      log.Printf("[aC] Accept error: %v", err)
      continue
    }
    log.Printf("[aC] Client connected from %s", conn.RemoteAddr())
    go handleTCP(conn)
  }
} // func acceptClients(ln net.Listener)


// deriveStateLocked writes the current state to disk (state.mu must be held)
func deriveStateLocked(songZI int, songID int, songURI string, limit int) *derivedState {
  // state.mu MUST already be held
  now := time.Now().Format(time.RFC3339Nano)

  ds := &derivedState{
    WriteTime:  now,
    SongZI:     songZI,
    SongID:     songID,
    SongURI:    songURI,
    Paused:     btoi(state.paused),
    Count:      state.count,
    BaseLimit:  state.baseLimit,
    Limit:      limit,
    BlockLimit: state.blockLimit,
    PID:        os.Getpid(),
  }

  if xy.active {
    ds.LingerXY = 1
    ds.LingerX = xy.start
    ds.LingerY = xy.end
  }


  // ---- ONLY disk I/O is optional ----
  if !stateEnabled {
    return ds
  }

  tmp := statePath + ".tmp"
  f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0664)
  if err != nil {
    log.Printf("state write failed: %v", err)
    return ds
  }

  formatState(f, ds)

  f.Close()
  os.Rename(tmp, statePath)

  return ds
} // func deriveStateLocked(songID string, limit int)


// formatState formats a derivedState struct into key=value lines
func formatState(w io.Writer, ds *derivedState) {
  fmt.Fprintf(w, "writetime=%s\n", ds.WriteTime)
  fmt.Fprintf(w, "lingersong=%d\n", ds.SongZI+1)
  fmt.Fprintf(w, "lingersongid=%d\n", ds.SongID)
  fmt.Fprintf(w, "lingerpause=%d\n", ds.Paused)
  fmt.Fprintf(w, "lingercount=%d\n", ds.Count)
  fmt.Fprintf(w, "lingerbase=%d\n", ds.BaseLimit)
  fmt.Fprintf(w, "lingerlimit=%d\n", ds.Limit)
  fmt.Fprintf(w, "lingerblocklmt=%d\n", ds.BlockLimit)
  fmt.Fprintf(w, "lingerpid=%d\n", ds.PID)
  if xy.active {
    fmt.Fprintf(w, "lingerxy=%d\n", ds.LingerXY)
    fmt.Fprintf(w, "lingerx=%d\n", ds.LingerX+1)
    fmt.Fprintf(w, "lingery=%d\n", ds.LingerY+1)
  }
} // func formatState(w io.Writer, ds *derivedState)


// sendStatus sends the current state to a writer (IPC/TCP client)
func sendStatus(w io.Writer) {
  state.mu.Lock()

  limit := state.baseLimit
  if state.blockOn && state.blockLimit > 0 {
    limit = state.blockLimit
  }

  ds := deriveStateLocked(state.lastSongZI, state.lastSongID, state.lastSongURI, limit)
  state.mu.Unlock()

  formatState(w, ds)
} // func sendStatus(w io.Writer)


// clientCommandHandler [cCH()] parses client command-line args and sends them to the daemon
func clientCommandHandler(args []string) error {
    if len(args) == 0 {
        return fmt.Errorf("No client commands provided")
    }

    // ---- phase 1: normalize (strip commas, split tokens) ----
    var toks []string
    for _, a := range args {
        a = strings.ReplaceAll(a, ",", " ")
        for _, f := range strings.Fields(a) {
            toks = append(toks, f)
        }
    }

    // ---- phase 2: validate + batch ----
    var batch []string
    var numberRegex = regexp.MustCompile(`^[0123456789]+$`)

    for i := 0; i < len(toks); {
        tok := toks[i]

        switch tok {

      case "status",
           "test",
           "json-status",
           "json-log",
           "pause",
           "resume",
           "toggle",
           "next",
           "skip",
           "quit",
           "mpc",
           "xyoff",
           "getlog",
           "exit":
        batch = append(batch, tok)
        i++

      case "version":
        batch = append(batch, tok)
        i++
        fmt.Printf("\nmpdgolinger client   %s\n", version)

      case "count":
        if i+1 < len(toks) && numberRegex.MatchString(toks[i+1]) {
          batch = append(batch, tok+" "+toks[i+1])
          i += 2
        }

      case "limit", "blocklimit", "block":
        if tok == "block" {
          tok = "blocklimit"
        }

        if i+1 < len(toks) && numberRegex.MatchString(toks[i+1]) {
          batch = append(batch, tok+" "+toks[i+1])
          i += 2
        } else {
          batch = append(batch, tok)
          i++
        }

      case "verbose":
        if i+1 < len(toks) {
          val := strings.ToLower(toks[i+1])
          if val == "on" || val == "off" {
            batch = append(batch, "verbose "+val)
            i += 2
            continue
          }
        }
        return fmt.Errorf("verbose requires 'on' or 'off' argument")

      case "state":
        if i+1 < len(toks) {
          // path-like check
          val := toks[i+1]
          if !filepath.IsAbs(val) {
            return fmt.Errorf("state requires a valid, absolute daemon path as argument")
          }
          batch = append(batch, "state "+val)
          i += 2
          continue
        }
        return fmt.Errorf("state requires a valid, absolute daemon path as argument")

      case "xy":
        if i+2 < len(toks) {
          inc := 0
          x, err1 := strconv.Atoi(toks[i+1])
          if err1 != nil || x < 0 {
            return fmt.Errorf("invalid xy range")
          }

          ytok := toks[i+2]
          var y int
          var err error

          if strings.HasPrefix(ytok, "+") || strings.HasPrefix(ytok, "-") {
            inc = 1
            y, err = strconv.Atoi(ytok)
//            delta, err := strconv.Atoi(ytok)
            if err != nil {
              return fmt.Errorf("invalid xy delta")
            }
//            y = x + delta
          } else {
            y, err = strconv.Atoi(ytok)
            if err != nil {
              return fmt.Errorf("invalid xy range")
            }
          }

          if y < 0 && inc == 0 {
            return fmt.Errorf("invalid xy range")
          }

          if y < x && inc == 0 && y != 0 {
            x, y = y, x
          }

          batch = append(batch, fmt.Sprintf("xy %d %d %d", x-1, y-1, inc))
          i += 3
          continue
        }
        return fmt.Errorf("usage: xy <startSongID> <endSongID|+N|-N>")

/*--------------------------------------------------------------*/
      default:
          return fmt.Errorf("unknown client verb: %s", tok)
      }
    }

    if verbose {
      log.Printf("[client] raw args: %#v", args)
      log.Printf("[client] normalized toks: %#v", toks)
      log.Printf("[client] batch cmds: %#v", batch)
    }

    // ---- phase 3: send ----
    cmd := strings.Join(batch, ", ")
    if verbose { log.Printf("[client] final send string: %q", cmd) }
//    return sendClientCommand(cmd)


  // send the command first
  err := sendClientCommand(cmd)
  if err != nil {
    return err
  }

  // post-exec action (exec replaces the current process)
  if execPost != "" && execPost != "none" && execPost != "-" {
    log.Printf("[execPost] executing %s", execPost)
    if err := syscall.Exec(execPost, []string{execPost}, os.Environ()); err != nil {
      log.Printf("[execpost] failed: %v", err)
      os.Exit(1)
    }
  }

  return nil

} // func clientCommandHandler(args []string)


// sendClientCommand sends a string command to daemon via IPC or TCP
func sendClientCommand(cmd string) error {
  var (
    conn net.Conn
    err  error
  )

  useSocket := false
  useTCP := false

  // 1. User defined socket + daemon TCP → prefer socket, fallback TCP
  if socketPath != "" && daemonIP != "" && daemonPort != 0 {
    if c, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond); err == nil {
      c.Close()
      if verbose { log.Printf("[clientDo] socket usable: %s", socketPath); }
      useSocket = true
    } else if c, err := net.DialTimeout(
      "tcp",
      fmt.Sprintf("%s:%d", daemonIP, daemonPort),
      500*time.Millisecond,
    ); err == nil {
      c.Close()
      if verbose { log.Printf("[clientDo] tcp usable: %s:%d", daemonIP, daemonPort); }
      useTCP = true
    } else {
      log.Printf("[clientDo] neither user socket nor tcp usable")
    }
  }

  // 2. Only socket defined
  if !useSocket && socketPath != "" {
    if c, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond); err == nil {
      c.Close()
      log.Printf("[clientDo] socket usable: %s", socketPath)
      useSocket = true
    } else {
      log.Printf("[clientDo] socket unusable: %v", err)
    }
  }

  // 3. Only TCP defined
  if !useSocket && !useTCP && daemonIP != "" && daemonPort != 0 {
    if c, err := net.DialTimeout(
      "tcp",
      fmt.Sprintf("%s:%d", daemonIP, daemonPort),
      500*time.Millisecond,
    ); err == nil {
      c.Close()
      if verbose { log.Printf("[clientDo] tcp usable: %s:%d", daemonIP, daemonPort); }
      useTCP = true
    } else {
      log.Printf("[clientDo] tcp unusable: %v", err)
    }
  }

  // 4. Fallback defaults
  if !useSocket && !useTCP {
    if c, err := net.DialTimeout("unix", defaultSocketPath, 500*time.Millisecond); err == nil {
      c.Close()
      socketPath = defaultSocketPath
      log.Printf("[clientDo] default socket usable: %s", defaultSocketPath)
      useSocket = true
    } else if c, err := net.DialTimeout(
      "tcp",
      fmt.Sprintf("%s:%d", defaultDaemonIP, defaultDaemonPort),
      500*time.Millisecond,
    ); err == nil {
      c.Close()
      daemonIP = defaultDaemonIP
      daemonPort = defaultDaemonPort
      log.Printf("[clientDo] default tcp usable: %s:%d", daemonIP, daemonPort)
      useTCP = true
    } else {
      return fmt.Errorf("sendClientCommand: no usable daemon connection")
    }
  }

  // ---- final connect ----
  if useSocket {
    conn, err = net.Dial("unix", socketPath)
    if err != nil {
      return fmt.Errorf("sendClientCommand: socket connect failed: %v", err)
    }
  } else {
    conn, err = net.Dial(
      "tcp",
      fmt.Sprintf("%s:%d", daemonIP, daemonPort),
    )
    if err != nil {
      return fmt.Errorf("sendClientCommand: tcp connect failed: %v", err)
    }
  }
  defer conn.Close()

  _ = conn.SetDeadline(time.Now().Add(3 * time.Second))

  // ---- send ----
  if verbose {
    log.Printf("[clientDo] sending: %q", cmd)
  }

  if _, err := fmt.Fprintf(conn, "%s\n", cmd); err != nil {
    return fmt.Errorf("sendClientCommand: write failed: %v", err)
  }

  // ---- receive ----
  scanner := bufio.NewScanner(conn)
  for scanner.Scan() {
    fmt.Println(scanner.Text())
  }

  if err := scanner.Err(); err != nil {
    return fmt.Errorf("sendClientCommand: read failed: %v", err)
  }

  return nil
} // func sendClientCommand(cmd string) error {


// btoi converts a bool to int (true=1, false=0)
