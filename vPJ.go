package main

import (
	"github.com/fhs/gompd/v2/mpd"
	"github.com/coder/websocket"
	"github.com/mitchellh/mapstructure"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"sort"
)
func verbProcessorJSON(js map[string]interface{}, req Request, conn *websocket.Conn) []string {

  // --- validate cmd ---
  cmdIface, ok := js["cmd"]
  if !ok {
    js["response"] = "error"
    js["error"] = "missing cmd"
    out, _ := json.Marshal(js)
    return []string{string(out)}
  }
  cmd, ok := cmdIface.(string)
  if !ok {
    js["response"] = "error"
    js["error"] = "cmd not string"
    out, _ := json.Marshal(js)
    return []string{string(out)}
  }

  // --- optional args ---
  argsIface, _ := js["args"]

  // --- validate system ---
  systemIface, ok := js["system"]
  if !ok {
    js["response"] = "error"
    js["error"] = "missing system"
    out, _ := json.Marshal(js)
    return []string{string(out)}
  }
  system, ok := systemIface.(string)
  if !ok {
    js["response"] = "error"
    js["error"] = "system not string"
    out, _ := json.Marshal(js)
    return []string{string(out)}
  }

  // --- poll MPD for authoritative state ---
  var URI, songIDstr, playState, songZIstr, title string
  var randomState, consumeState, repeatState, singleState string
  var songZI, songID int
  var playing = true

  err := mpdDo(func(c *mpd.Client) error {
    song, err := c.CurrentSong()
    if err != nil {
      return err
    }

    status, err := c.Status()
    if err != nil {
      return err
    }

    songIDstr = status["songid"]
    songZIstr = status["song"]
    playState = status["state"]
    title     = song["Title"]
    URI       = song["file"]

    songID, _ = strconv.Atoi(songIDstr)
    songZI, _ = strconv.Atoi(songZIstr)

    if playState != "play" {
      playing = false
    }

    if r, ok := status["random"]; ok {
      randomState = r
    }
    if r, ok := status["consume"]; ok {
      consumeState = r
    }
    if r, ok := status["repeat"]; ok {
      repeatState = r
    }
    if r, ok := status["single"]; ok {
      singleState = r
    }

    return nil
  }, "JSON-preSwitch")

  if err != nil {
    js["response"] = "error"
    js["error"] = err.Error()
    out, _ := json.Marshal(js)
    return []string{string(out)}
  }

  // --- system switch ---
  switch system {   // mpd, player, playlist; linger; websocket; pulseaudio; search; pauseTimer //
  case "artwork":

    switch cmd {

      case "resolve":
      // EXPECTS:
      // '{"system":"artwork","cmd":"resolve","args":"<path>"}'

      // want to expand to incude "args":{"path1","path2","path3", ..., "pathn"}

      b := req.Args

      cmd := exec.Command("go", "run", ".")
      cmd.Dir = "/opt/mgl"

        cmd.Stdin = bytes.NewReader(b)

        var stdout, stderr bytes.Buffer
        cmd.Stdout = &stdout
        cmd.Stderr = &stderr

        err := cmd.Run()
        if err != nil {
          js["response"] = "error"
          js["error"] = err.Error()
          js["stderr"] = stderr.String()
          js["stdout"] = stdout.String()
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

      var resp any

      if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
        js["response"] = "error"
        js["error"] = err.Error()
        js["stdout"] = stdout.String()

        out, _ := json.Marshal(js)
        return []string{string(out)}
      }

      js["response"] = resp

      out, _ := json.Marshal(js)
      return []string{string(out)}
      } // case "resolve"

    return nil

  case "pause_timer":
    state.mu.Lock()
    defer state.mu.Unlock()

    switch cmd {
      case "end":
        if !state.MPDplaying {
          js["error"] = "pause_timer cannot be set when mpd is not playing"
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        var status mpd.Attrs
        if err := mpdDo(func(c *mpd.Client) error {
          var err error
          status, err = c.Status()
          return err
        }, "JSON-pauseTimer-end-Status"); err != nil {
          js["response"] = "error"
          js["error"] = fmt.Sprintf("failed to get mpd status: %s", err)
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        durf, _ := strconv.ParseFloat(status["duration"], 64)
        elf, _ := strconv.ParseFloat(status["elapsed"], 64)

        remaining := int(math.Round(durf - elf))
        if remaining <= 0 {
          remaining = 1
        }

        state.pauseTimer.Active = true
        state.pauseTimer.Duration = remaining
        state.pauseTimer.EndTime = time.Now().Add(time.Duration(remaining) * time.Second)

        select {
        case idleEvents <- IdleEvent{Subsystem: "pauseTimer"}:
        default:
        }

        js["response"] = fmt.Sprintf("Pause timer set to end of track (%d sec)", remaining)
        out, _ := json.Marshal(js)
        return []string{string(out)}
      case "on":
        if ! state.MPDplaying {
          js["error"] = "pause_timer cannot be set when mpd is not playing"
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }
        f, ok := argsIface.(float64)
        if ! ok {
          dbg("The argsIface is not a number")
          js["response"] = "error"
          js["error"] = "argument must be integer seconds for cmd=on"
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }
        argsSeconds := int(f)
        if argsSeconds <= 0 {
          js["response"] = "error"
          js["error"] = "duration must be > 0"
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }
        dbg("The argsSeconds is an integer with value: %d", argsSeconds)
        state.pauseTimer.Active = true
        state.pauseTimer.Duration = argsSeconds
        state.pauseTimer.EndTime = time.Now().Add(time.Duration(argsSeconds) * time.Second)
        dbg("[vPJ] state.pauseTimer.EndTime: %s", state.pauseTimer.EndTime)
        // Inject event so wsWatcher can pick it up immediately
        select {
        case idleEvents <- IdleEvent{Subsystem: "pauseTimer"}:
        default:
          // channel full, skip
        }

        js["response"] = fmt.Sprintf("Pause timer set to %d, update status", argsSeconds)
        out, _ := json.Marshal(js)
        return []string{string(out)}
      case "reset":
        if ! state.MPDplaying {
          js["error"] = "pause_timer cannot be set when mpd is not playing"
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }
        if argsIface != nil {
          js["notes"] = "system:pause_timer cmd:reset & cmd:off do not take arguments"
        }
        if state.pauseTimer.Duration > 0 {
          state.pauseTimer.Active = true
          state.pauseTimer.EndTime = time.Now().Add(time.Duration(state.pauseTimer.Duration) * time.Second)
          js["response"] = fmt.Sprintf("Pause timer reset to %d, update status", state.pauseTimer.Duration)
        } else {
          js["response"] = "error"
          js["error"] = "No existing pause timer to reset"
        }
        out, _ := json.Marshal(js)
        return []string{string(out)}
      case "off":
        if argsIface != nil {
          js["notes"] = "system:pause_timer cmd:reset & cmd:off do not take arguments"
        }
        state.pauseTimer.Duration = 0
        state.pauseTimer.Active = false
        state.pauseTimer.EndTime = time.Time{}
        state.pauseTimer.Remaining = 0
        js["response"] = "Pause timer turned off"
        out, _ := json.Marshal(js)
        return []string{string(out)}
      default:
        js["response"] = "error"
        js["error"] = `Unknown cmd: must be "on", "off", or "reset".`
        out, _ := json.Marshal(js)
        return []string{string(out)}
    }

  case "mpd", "player", "playlist":
    switch cmd {
      case "start_mpdzmq":
        cmd := exec.Command("pgrep", "nginx")

        // Run the command and capture the output
        output, err := cmd.Output()
        if err != nil {
          // pgrep exits with 1 if no processes match
          js["error"]=fmt.Sprintf("Error or no processes found: %s\n", err)
        }

        // Convert output bytes to string
        pids := string(output)

        js["response"] = pids
        out, _ := json.Marshal(js)
        return []string{string(out)}

      case "previous", "lastsong":
        responses := []string{}
        errors := []string{}

        js["response"] = fmt.Sprintf("state.prevSongID = %d; state.prevSongURI = %s", state.prevSongID, state.prevSongURI)

        // Step 0: if prevSongID = 0 && state.prevSongURI = "" then it's not going to work and we're not polling the log
        if state.prevSongID == 0 && state.prevSongURI == "" {
          responses = append(responses, "error")
          errors = append(errors, "There is no previous songID or songURI available, unable to play the previous song.")
        } else if state.prevSongURI == URI {
          responses = append(responses, "error")
          errors = append(errors, "Cannot go back any further than one song due to random.")
        // Step 1: attempt to change to prevSongID
        } else if err := mpdDo(func(c *mpd.Client) error { return c.PlayID(state.prevSongID) }, "JSON-previous-PlayID"); err == nil {
          responses = append(responses, fmt.Sprintf("Playing state.prevSongID: %d", state.prevSongID))
        // Step 2: attemt to add and play the prevSongURI because the prevSongID no longer exists in playlist
        } else {
          errors = append(errors, fmt.Sprintf("PlayID failed: %s", err))
          responses = append(responses, fmt.Sprintf("previous songID (%d) not in current playlist; attempting to readd previous songURI", state.prevSongID))
          var prevSongID int
          if errAdd := mpdDo(func(c *mpd.Client) error {
            var err error
            prevSongID, err = c.AddID(state.prevSongURI, songZI)
            return err
          }, "JSON-previous-AddID"); errAdd != nil {
            errors = append(errors, fmt.Sprintf("AddID failed for previous songURI (%s): %s", state.prevSongURI, errAdd))
          } else {
            if err := mpdDo(func(c *mpd.Client) error { return c.PlayID(prevSongID) }, "JSON-previous-new-PlayID"); err == nil {
              // since we succeeded in playing nee new ID, clear the errors and move other responses to notes:
              errors = []string{}
              js["notes"] = responses
              responses = []string{}
              responses = append(responses, fmt.Sprintf("Playing readded prevSongURI: %s", state.prevSongURI))
            } else {
              responses = append(responses, fmt.Sprintf("Unable to play newly-added prevSongID: %d", prevSongID))
              errors = append(errors, fmt.Sprintf("PlayID failed: %s", err))
            }
          }
        }
        js["response"] = responses
        js["error"] = errors
        out, _ := json.Marshal(js)
        return []string{string(out)}

      case "pause_timer":
        js["response"] = "error"
        js["error"] = "Usage: '{system:pause_timer, cmd:<on|off|reset>, args:<seconds>}'"
        out, _ := json.Marshal(js)
        return []string{string(out)}

      case "json-log":
        dbg("[vPJ] received json-log command")
        nlines := 24 // default

        if f, ok := argsIface.(float64); ok {
          nlines = int(f)
          dbg("The nlines is an integer with value: %d\n", nlines)
        } else if argsIface == nil {
          dbg("Number of log lines unspecified, defaulting to %d\n", nlines)
        } else {
          dbg("The variable is not an integer")
          js["response"] = "error"
          js["error"] = "Argument must be nil or an integer."
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        lines := mpdLogParse(nlines)
//        responses := []string{}
        responses := []LogEntryV1{}

        err := mpdDo(func(c *mpd.Client) error {
          for _, ll := range lines {
            tags, notes, err := MPDtags(c, ll.Path, ll.Action)
            if err != nil {
              return err
            }

            dbg("DBG json-log tags=%+v", tags)
            dbg("DBG json-log notes=%v", notes)

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

//            b, _ := json.Marshal(entry)
            responses = append(responses, *entry)
          }
          return nil
        }, "JSONLog")

        if err != nil {
          js["response"] = "error"
          js["error"] = err.Error()
        } else {
          js["response"] = responses
        }

        out, _ := json.Marshal(js)
        return []string{string(out)}

      case "json-log-stream":
        log.Printf("vPJ: received json-log-stream command")
        nlines := 24 // default

        if f, ok := argsIface.(float64); ok {
          nlines = int(f)
          dbg("The nlines is an integer with value: %d\n", nlines)
        } else if argsIface == nil {
          dbg("Number of log lines unspecified, defaulting to %d\n", nlines)
        } else {
          dbg("The variable is not an integer")
          js["response"] = "error"
          js["error"] = "Argument must be nil or an integer."
          out, _ := json.Marshal(js)
          conn.Write(context.Background(), websocket.MessageText, out)
          return nil
        }

        lines := mpdLogParse(nlines)

        err := mpdDo(func(c *mpd.Client) error {

          for _, ll := range lines {
            if ll.Path == "" { continue }

            tags, notes, err := MPDtags(c, ll.Path, ll.Action)
            if err != nil {
              dbg("json-log-stream MPDtags error: %v", err)
              continue
            }

            dbg("DBG json-log-stream tags=%+v", tags)
            dbg("DBG json-log-stream notes=%v", notes)

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

//            js := map[string]any{
//              "response": "log-data",
//              "data": entry,
//            }
//            out, _ := json.Marshal(js)

            out, _ := json.Marshal(entry)
            err = conn.Write(context.Background(), websocket.MessageText, out)
            if err != nil {
              return err
            }
          }

          return nil

        }, "JSONLogStream")

        if err != nil {
          js := map[string]any{
            "response": "error",
            "error": err.Error(),
          }
          out, _ := json.Marshal(js)
          conn.Write(context.Background(), websocket.MessageText, out)
          return nil
        }

        js := map[string]any{
          "response": "log-end",
        }

        out, _ := json.Marshal(js)
        conn.Write(context.Background(), websocket.MessageText, out)

        return nil


      case "playlist":

        var (
          albumKey string
          search   string
          rangeStr string
          window   *int
        )

        // ----------------------------
        // Parse argsIface
        // ----------------------------
        if argsIface == nil {
          js["response"] = "error"
          js["error"] = "playlist requires args"
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        switch v := argsIface.(type) {

        case string:
          if v == "album" {
            albumKey = state.lastAlbumKey
          }

        case map[string]interface{}:

          if ak, ok := v["albumkey"].(string); ok {
            albumKey = ak
          }

          if s, ok := v["search"].(string); ok {
            search = s
          }

          if r, ok := v["range"].(string); ok {
            rangeStr = r
          }

          if c, ok := v["current"].(float64); ok {
            n := int(c)
            window = &n
          }
        }

        // ============================================================
        // 1️⃣ ALBUM OR SEARCH → RAW MPD SOCKET
        // ============================================================
        if albumKey != "" || search != "" {

          key := albumKey
          if search != "" {
//          key = fmt.Sprintf(`any == "%s"`, search)
//            key = fmt.Sprintf("any == %s", search)
            key = fmt.Sprintf("search %s", search)
          }

          results, err := mpdPlaylist(key)
          if err != nil {
            js["response"] = "error"
            js["error"] = err.Error()
            out, _ := json.Marshal(js)
            return []string{string(out)}
          }

          js["response"] = results
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        // ============================================================
        // 2️⃣ RANGE OR CURRENT → gompd PlaylistInfo
        // ============================================================
        if rangeStr != "" || window != nil {

          err := mpdDo(func(c *mpd.Client) error {

            var lower, upper int

            // ---- CURRENT WINDOW ----
            if window != nil {

              status, err := c.Status()
              if err != nil {
                return err
              }

              posStr, ok := status["song"]
              if !ok {
                return fmt.Errorf("no current song position")
              }

              pos, err := strconv.Atoi(posStr)
              if err != nil {
                return err
              }

              lower = pos - *window
              if lower < 0 {
                lower = 0
              }

              upper = pos + *window + 1
            }

            // ---- EXPLICIT RANGE ----
            if rangeStr != "" {

              parts := strings.Split(rangeStr, "-")
              if len(parts) != 2 {
                return fmt.Errorf("invalid range format")
              }

              x, err := strconv.Atoi(parts[0])
              if err != nil {
                return err
              }

              y, err := strconv.Atoi(parts[1])
              if err != nil {
                return err
              }

              // convert 1-indexed → 0-indexed
              x--
              y--

              if x > y {
                x, y = y, x
              }

              if x < 0 {
                x = 0
              }

              lower = x
              upper = y + 1
            }

            songs, err := c.PlaylistInfo(lower, upper)
            if err != nil {
              return err
            }

            var resp []AudioV1

            for _, song := range songs {
              raw := map[string]string{}
              for k, v := range song {
                raw[strings.ToLower(k)] = v
              }

              var a AudioV1
              if err := convert2json(raw, &a); err == nil {
                resp = append(resp, a)
              }
            }

            js["response"] = resp
            return nil
          }, "playlist")

          if err != nil {
            js["response"] = "error"
            js["error"] = err.Error()
          }

          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        // ============================================================
        // 3️⃣ FALLTHROUGH
        // ============================================================
        js["response"] = "error"
        js["error"] = "invalid playlist args"
        out, _ := json.Marshal(js)
        return []string{string(out)}
      //// case "playlist" /////////////////////////////////////////////


//      case "add":
//
//        type AddItem struct {
//          URI string          `json:"uri"`
//          Pos json.RawMessage `json:"pos"`
//        }
//
//        var items []AddItem
//
//        // args may be a single URI string
//        var single string
//        if err := json.Unmarshal(req.Args, &single); err == nil {
//          items = []AddItem{{URI: single}}
//        } else {
//
//          // otherwise expect array of objects
//          if err := json.Unmarshal(req.Args, &items); err != nil {
//            js["response"] = "error"
//            js["error"] = err.Error()
//            out,_ := json.Marshal(js)
//            return []string{string(out)}
//          }
//        }
//
//        conn, err := directDialMPD()
//        if err != nil {
//          js["response"] = "error"
//          js["error"] = err.Error()
//          out,_ := json.Marshal(js)
//          return []string{string(out)}
//        }
//        defer conn.Close()
//
//        w := bufio.NewWriter(conn)
//        r := bufio.NewReader(conn)
//
//        fmt.Fprintln(w,"command_list_begin")
//
//        // reverse so ordering is preserved when inserting
//        for i := len(items)-1; i >= 0; i-- {
//
//          uri := items[i].URI
//          if uri == "" {
//            continue
//          }
//
//          // append if no pos
//          if len(items[i].Pos) == 0 {
//
////            fmt.Fprintf(w,"add \"%s\"\n",uri)
//            cmd := (&mpd.Client{}).Command("add %s", uri)
//            fmt.Fprintln(w, cmd.String())
//            continue
//          }
//
//          // try numeric position
//          var posNum int
//          if err := json.Unmarshal(items[i].Pos,&posNum); err == nil {
//
//            if posNum < 0 {
////              fmt.Fprintf(w,"add \"%s\"\n",uri)
//              cmd := (&mpd.Client{}).Command("add %s", uri)
//              fmt.Fprintln(w, cmd.String())
//            } else {
//              zi := posNum - 1
////              fmt.Fprintf(w,"add \"%s\" %d\n",uri,zi)
//              cmd := (&mpd.Client{}).Command("add %s %d", uri, zi)
//              fmt.Fprintln(w, cmd.String())
//            }
//
//            continue
//          }
//
//          // try relative string
//          var posStr string
//          if err := json.Unmarshal(items[i].Pos,&posStr); err == nil {
//
//            if strings.HasPrefix(posStr,"+") || strings.HasPrefix(posStr,"-") {
////              fmt.Fprintf(w,"add \"%s\" %s\n",uri,posStr)
//              cmd := (&mpd.Client{}).Command("add %s %s", uri, posStr)
//              fmt.Fprintln(w, cmd.String())
//              continue
//            }
//
//            js["response"] = "error"
//            js["error"] = "invalid relative position"
//            out,_ := json.Marshal(js)
//            return []string{string(out)}
//          }
//
//        }
//
//        fmt.Fprintln(w,"command_list_end")
//        w.Flush()
//
//        for {
//
//          line,err := r.ReadString('\n')
//          if err != nil {
//            js["response"] = "error"
//            js["error"] = err.Error()
//            out,_ := json.Marshal(js)
//            return []string{string(out)}
//          }
//
//          dbg("MPD: %s",line)
//
//          if strings.HasPrefix(line,"ACK") {
//            js["response"] = "error"
//            js["error"] = strings.TrimSpace(line)
//            out,_ := json.Marshal(js)
//            return []string{string(out)}
//          }
//
//          if strings.HasPrefix(line,"OK") {
//            break
//          }
//        }
//
//        js["response"] = "ok"
//
//        out,_ := json.Marshal(js)
//        return []string{string(out)}
//      //// case "add" /////////////////////////////////////////////

      case "add":
log.Printf("Started case \"add\"")

        type AddItem struct {
          URI string          `json:"uri"`
          Pos json.RawMessage `json:"pos"`
        }

        var items []AddItem

        // args may be a single URI string
        var single string
        if err := json.Unmarshal(req.Args, &single); err == nil {
          items = []AddItem{{URI: single}}
        } else {

          // otherwise expect array of objects
          if err := json.Unmarshal(req.Args, &items); err != nil {
            js["response"] = "error"
            js["error"] = err.Error()
            out,_ := json.Marshal(js)
            return []string{string(out)}
          }
        }

log.Printf("[MPD add] received %d tracks", len(items))

        conn, err := directDialMPD()
        if err != nil {
          js["response"] = "error"
          js["error"] = err.Error()
          out,_ := json.Marshal(js)
          return []string{string(out)}
        }
        defer conn.Close()
log.Printf("Connected to directDialMPD")

        w := bufio.NewWriter(conn)
        r := bufio.NewReader(conn)

        var abs []AddItem
        var rel []AddItem

        // classify absolute vs relative
        for _,it := range items {

          if len(it.Pos) == 0 {
            abs = append(abs,it)
            continue
          }

          var posNum int
          if err := json.Unmarshal(it.Pos,&posNum); err == nil {
            abs = append(abs,it)
            continue
          }

          var posStr string
          if err := json.Unmarshal(it.Pos,&posStr); err == nil {
            if strings.HasPrefix(posStr,"+") || strings.HasPrefix(posStr,"-") {
              rel = append(rel,it)
              continue
            }

            js["response"] = "error"
            js["error"] = "invalid relative position"
            out,_ := json.Marshal(js)
            return []string{string(out)}
          }
log.Printf("[MPD add] received %d tracks", len(items))

          abs = append(abs,it)
        }
log.Printf("abs: %s", abs)
        fmt.Fprintln(w,"command_list_begin")

        // capture playlist length before
        fmt.Fprintln(w,"status")

        // ---- absolute inserts (reversed) ----
        for i := len(abs)-1; i >= 0; i-- {

          uri := abs[i].URI
          if uri == "" {
            continue
          }

          if len(abs[i].Pos) == 0 {
            cmd := (&mpd.Client{}).Command("add %s", uri)
            fmt.Fprintln(w, cmd.String())
            continue
          }

          var posNum int
          if err := json.Unmarshal(abs[i].Pos,&posNum); err == nil {

            if posNum <= 0 {
              cmd := (&mpd.Client{}).Command("add %s", uri)
              fmt.Fprintln(w, cmd.String())
            } else {
              zi := posNum - 1
              cmd := (&mpd.Client{}).Command("add %s %d", uri, zi)
              fmt.Fprintln(w, cmd.String())
            }
          }
        }

        // ---- relative inserts (reversed) ----
        for i := len(rel)-1; i >= 0; i-- {

          uri := rel[i].URI
          if uri == "" {
            continue
          }

          var posStr string
          if err := json.Unmarshal(rel[i].Pos,&posStr); err == nil {

            cmd := (&mpd.Client{}).Command("add %s %s", uri, posStr)
            fmt.Fprintln(w, cmd.String())
          }
        }

        // capture playlist length after
        fmt.Fprintln(w,"status")

        fmt.Fprintln(w,"command_list_end")
        w.Flush()

        var plBefore int
        var plAfter int
        found := 0

        for {

          line,err := r.ReadString('\n')
          if err != nil {
            js["response"] = "error"
            js["error"] = err.Error()
            out,_ := json.Marshal(js)
            return []string{string(out)}
          }

          dbg("MPD: %s",line)

          if strings.HasPrefix(line,"playlistlength:") {
            val := strings.TrimSpace(strings.TrimPrefix(line,"playlistlength:"))
            n,_ := strconv.Atoi(val)

            if found == 0 {
              plBefore = n
            } else {
              plAfter = n
            }
            found++
          }

          if strings.HasPrefix(line,"ACK") {
            js["response"] = "error"
            js["error"] = strings.TrimSpace(line)
            out,_ := json.Marshal(js)
            return []string{string(out)}
          }

          if strings.HasPrefix(line,"OK") {
            break
          }
        }

        js["response"] = "ok"
        js["added"] = plAfter - plBefore

        out,_ := json.Marshal(js)
        return []string{string(out)}

      //// case "add" /////////////////////////////////////////////

      case "delete":

        type delrange struct {
          start int
          end   int
        }

        var dr []delrange

        err := mpdDo(func(c *mpd.Client) error {

          status, err := c.Status()
          if err != nil {
            return err
          }
          plBefore,_ := strconv.Atoi(status["playlistlength"])

        // ======================
        // parse argsIface
        // ======================
        switch v := argsIface.(type) {

        case []interface{}:
          for i,item := range v {
            m, ok := item.(map[string]interface{})
            if !ok {
              return fmt.Errorf("args[%d] not object",i)
            }

            r, ok := m["range"].(string)
            if !ok {
              return fmt.Errorf("args[%d] missing range",i)
            }

            interval, _ := m["int"].(bool)
            dr = append(dr,delrange{start:0,end:0}) // placeholder, will parse next
            dr[len(dr)-1].start = 0
            dr[len(dr)-1].end = 0

            parts := strings.SplitN(r,":",2)
            s,_ := strconv.Atoi(parts[0])
            e,_ := strconv.Atoi(parts[1])

            var start,end int
            if interval {
              if e == 0 {
                start = s
                end   = plBefore
              } else if e > 0 {
                start = s
                end   = s + e
              } else {
                start = s + e
                end   = s
              }
            } else {
              if e == 0 {
                start = s
                end   = s
              } else if s <= e {
                start = s
                end   = e
              } else {
                start = e
                end   = s
              }
            }

            if start < 1 {
              start = 1
            }

            dr[len(dr)-1] = delrange{start:start,end:end}
          }

        case map[string]interface{}:
          r, ok := v["range"].(string)
          if !ok {
            return fmt.Errorf("missing range")
          }
          interval, _ := v["int"].(bool)
          s,e := 0,0
          parts := strings.SplitN(r,":",2)
          s,_ = strconv.Atoi(parts[0])
          e,_ = strconv.Atoi(parts[1])

          var start,end int
          if interval {
            if e == 0 {
              start = s
              end   = plBefore
            } else if e > 0 {
              start = s
              end   = s + e
            } else {
              start = s + e
              end   = s
            }
          } else {
            if e == 0 {
              start = s
              end   = s
            } else if s <= e {
              start = s
              end   = e
            } else {
              start = e
              end   = s
            }
          }
          if start < 1 {
            start = 1
          }
          dr = append(dr, delrange{start:start,end:end})

        case string:
          n,_ := strconv.Atoi(v)
          dr = append(dr, delrange{start:n,end:n})

        case float64:
          dr = append(dr, delrange{start:int(v),end:int(v)})

        default:
          fmt.Errorf("invalid args type")
        }

      // ======================
      // merge overlapping / adjacent
      // ======================
      sort.Slice(dr, func(i,j int) bool { return dr[i].start < dr[j].start })

      var merged []delrange
      for _, r := range dr {
        if len(merged) == 0 {
          merged = append(merged,r)
          continue
        }
        last := &merged[len(merged)-1]
        if (r.start <= last.end+1 && last.end != 0 && r.end != 0) || last.end == 0 {
          if r.end > last.end {
            last.end = r.end
          }
          continue
        }
        merged = append(merged,r)
      }

      // ======================
      // sort descending for safe delete
      // ======================
      sort.Slice(merged, func(i,j int) bool { return merged[i].start > merged[j].start })

      // ======================
      // execute deletes
      // ======================

        for _, r := range merged {
          startZI := r.start - 1
          var endZI int
          endZI = r.end
          if err := c.Delete(startZI,endZI); err != nil {
            return err
          }
        }

        status2, err := c.Status()
        if err != nil {
          return err
        }
        plAfter, _ := strconv.Atoi(status2["playlistlength"])
        js["deleted"] = plBefore - plAfter
        return nil
      },"delete")

      if err != nil {
        js["response"] = "error"
        js["error"] = err.Error()
      } else {
        js["response"] = "ok"
      }

      out,_ := json.Marshal(js)
      return []string{string(out)}
      //// case "delete" /////////////////////////////////////////////


      case "play":
        var songPos int
        var songID  int
        var havePos bool
        var haveID  bool

        // ----------------------------
        // Parse argsIface
        // ----------------------------

        switch v := argsIface.(type) {
        case nil:
          songPos = -1
          havePos = true

        case string:
          i, err := strconv.Atoi(v)
          if err != nil {
            js["response"] = "error"
            js["error"] = err.Error()
            break
          }
          songPos = i - 1
          havePos = true

        case float64:
          songPos = int(v) - 1
          havePos = true

        case map[string]interface{}:
          if s, ok := v["song_position"].(string); ok {
            i, err := strconv.Atoi(s)
            if err != nil {
              js["response"] = "error"
              js["error"] = err.Error()
              break
            }
            songPos = i - 1
            havePos = true
          }

          if f, ok := v["song_position"].(float64); ok {
             songPos = int(f) - 1
             havePos = true
          }

          if sid, ok := v["songID"].(string); ok {
            id, err := strconv.Atoi(sid)
            if err != nil {
              js["response"] = "error"
              js["error"] = err.Error()
              break
            }
            songID = id
            haveID = true
          }

          if fid, ok := v["songID"].(float64); ok {
            songID = int(fid)
            haveID = true
          }

          if havePos && haveID {
            js["response"] = "error"
            js["error"] = `"song_position" and "songID" are mutually exclusive`
            break
          }

        default:
          js["response"] = "error"
          js["error"] = `for "cmd":"play", "args" must be a number, nil, or object containing song_position or songID.`
        }

        // If an error was set during the parsing, return early:
        if js["response"] == "error" {
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        // =============================================================
        // Execute MPD command
        // =============================================================
        err := mpdDo(func(c *mpd.Client) error {
          var current int

          // ============================================================
          // 1️⃣  havePos = true, zero-indexed song position
          // ============================================================
          if havePos {
            status, err := c.Status()
            if err != nil {
              return err
            }
            plLength, _ := strconv.Atoi(status["playlistlength"])
            current, _ = strconv.Atoi(status["song"])
            if songPos < plLength {
              err = c.Play(songPos)
              if err != nil {
                return err
              }
              if songPos < 0 {
                js["response"] = fmt.Sprintf("Playing %d", current + 1)
              } else {
                js["response"] = fmt.Sprintf("Playing %d", songPos + 1)
              }
            } else {
              return fmt.Errorf("Song position %d out of range: 1-%d", songPos + 1, plLength)
            }
          } else if haveID {
          // ============================================================
          // 2️⃣  haveID = true
          // ============================================================
            err := c.PlayID(songID)
            if err != nil {
              return err
            }
            status, err := c.Status()
            if err != nil {
              return err
            }
            current, _ = strconv.Atoi(status["song"])
            js["response"] = fmt.Sprintf("Playing %d, songID %d", current + 1, songID)
          }
          return nil
        }, "play")

        if err != nil {
          js["response"] = "error"
          js["error"] = err.Error()
        }

        out, _ := json.Marshal(js)
        return []string{string(out)}
      //// case "play" /////////////////////////////////////////////

      // --- pause/togglestate unified ---
      case "pause", "resume", "togglestate", "toggleplayback":
        var target bool //2083
        if cmd == "togglestate" || cmd == "toggleplayback" {
          target = !playing
        } else if cmd == "pause" {
          target = false
        } else { // play/resume
          target = true
        }

        if target == playing {
          if target {
            js["response"] = "mpd already playing"
          } else {
            js["response"] = "mpd already paused"
          }
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        err := mpdDo(func(c *mpd.Client) error {
          return c.Pause(!target)
        }, "JSON-togglestate")

        if err != nil {
          js["response"] = "error"
          js["error"] = err.Error()
        } else {
          if target {
            js["response"] = "play"
          } else {
            js["response"] = "pause"
          }
        }

        out, _ := json.Marshal(js)
        return []string{string(out)}

      // --- random/consume/repeat/single ---
      case "random", "togglerandom",
           "consume", "toggleconsume",
           "repeat", "togglerepeat",
           "single", "togglesingle":

        type toggleCmd struct {
          method func(bool) error
          state  string
        }

        cmds := map[string]toggleCmd{
          "random":       {func(v bool) error { return mpdDo(func(c *mpd.Client) error {
                            return c.Random(v) }, "JSON-random") }, randomState},
          "togglerandom": {func(v bool) error { return mpdDo(func(c *mpd.Client) error {
                            return c.Random(v) }, "JSON-random") }, randomState},
          "consume":      {func(v bool) error { return mpdDo(func(c *mpd.Client) error {
                            return c.Consume(v) }, "JSON-consume") }, consumeState},
          "toggleconsume":{func(v bool) error { return mpdDo(func(c *mpd.Client) error {
                            return c.Consume(v) }, "JSON-consume") }, consumeState},
          "repeat":       {func(v bool) error { return mpdDo(func(c *mpd.Client) error {
                            return c.Repeat(v) }, "JSON-repeat") }, repeatState},
          "togglerepeat": {func(v bool) error { return mpdDo(func(c *mpd.Client) error {
                            return c.Repeat(v) }, "JSON-repeat") }, repeatState},
          "single":       {func(v bool) error { return mpdDo(func(c *mpd.Client) error {
                            return c.Single(v) }, "JSON-single") }, singleState},
          "togglesingle": {func(v bool) error { return mpdDo(func(c *mpd.Client) error {
                            return c.Single(v) }, "JSON-single") }, singleState},
        }

        cmdData, ok := cmds[cmd]
        if !ok {
          js["response"] = "error"
          js["error"] = "unknown toggle cmd"
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        // Determine target value
        var toggle bool
        if len(cmd) >= 6 && cmd[:6] == "toggle" {
          toggle = cmdData.state != "1"
        } else if b, ok := argsIface.(bool); ok {
          toggle = b
        } else {
          toggle = cmdData.state == "0"
        }

        // apply and respond
        var toggleStr string
        if toggleStr = "0"; toggle { toggleStr = "1" }
        if cmdData.state != toggleStr {
          if err := cmdData.method(toggle); err != nil {
            js["response"] = "error"
            js["error"] = err.Error()
          } else {
            js["response"] = toggleStr
          }
        } else {
          js["response"] = toggleStr
        }

        out, _ := json.Marshal(js)
        return []string{string(out)}


      // --- next/restart remain unchanged ---
      case "restart":
        err := mpdDo(func(c *mpd.Client) error { return c.SeekCur(0, false) }, "JSON-restart")
        if err != nil {
          js["response"] = "error"
          js["error"] = err.Error()
        } else {
          js["response"] = "restarted"
        }
        out, _ := json.Marshal(js)
        return []string{string(out)}

      case "next":
        responses := []string{}
        errors := []string{}

        // Step 1: advance to next song
        if err := mpdDo(func(c *mpd.Client) error { return c.Next() }, "JSON-next"); err != nil {
          errors = append(errors, fmt.Sprintf("Next failed: %s", err))
        } else {
          responses = append(responses, fmt.Sprintf("Skipped %d: %s", songZI+1, title))
        }

        // Step 2: add previous song to skipped list if URI exists
        if URI != "" {
          if err = mpdDo(func(c *mpd.Client) error { return c.PlaylistAdd(skippedList, URI) }, "JSON-next-addSkip"); err != nil {
            errors = append(errors, fmt.Sprintf("Add to %s failed: %s", skippedList, err))
          } else {
            responses = append(responses, fmt.Sprintf("%s added to %s", URI, skippedList))
          }
        }

        // Step 3: fetch new status
        var newSongID int
        var newSongZI int
        if err = mpdDo(func(c *mpd.Client) error {
          status, err := c.Status()
          if err != nil {
            return err
          }

          if idStr, ok := status["songid"]; ok {
            id, err := strconv.Atoi(status[idStr])
            if err == nil {
              newSongID = id
            }
          }

          if ziStr, ok := status["song"]; ok {
            zi, err := strconv.Atoi(ziStr)
            if err == nil {
              newSongZI = zi
            }
          }
          return nil
        }, "JSON-next-postStatus"); err != nil {
          errors = append(errors, fmt.Sprintf("Fetch status failed: %s", err))
        } else if newSongID == songID {
          errors = append(errors, fmt.Sprintf("Song ID unchanged; newSongID: %d; lastSongID: %d", newSongID, songID))
        } else {
          responses = append(responses, fmt.Sprintf("playing %d", newSongZI+1))
        }

        // Step 4: return combined response
        if len(errors) > 0 {
          responses = append(responses, "error")
          js["response"] = responses
          js["errors"] = errors
        } else {
          js["response"] = responses
        }

        out, _ := json.Marshal(js)
        return []string{string(out)}

      case "ignore":
        p, ok := argsIface.(string)
        ext := filepath.Ext(p)
        if !ok && argsIface != nil {
          js["response"] = "error"
          js["error"] = "Argument must be a valid path"
        } else if ok && filepath.IsAbs(p) {
          URI = strings.TrimPrefix(p, defaultLibPrefix + string(os.PathSeparator))
          if URI == p {
            js["response"] = "error"
            js["error"] = fmt.Sprintf("Absolute path provided must be %s%s...", defaultLibPrefix, string(os.PathSeparator))
          }
        } else if ok && ext != "" && ext != "." {
          URI = p
        } else if ok {                // here we reject "args":""
          js["response"] = "error"
          js["error"] = "Argument must be a valid path"
        }

        if js["error"] == nil {
          if URI != "" {    // if we didn't reset URI above then it will still be the on we got from MPS earier
            var (
              before int64
              after int64
            )
            mpdignorePath := filepath.Join(defaultLibPrefix, filepath.Dir(URI), ".mpdignore")
            fileInfo, err := os.Stat(mpdignorePath)
            if err == nil {
              before = fileInfo.Size()
            }
            log.Printf("Adding %s to %s...", URI, ignoredList)
            mpderr := mpdDo(func(c *mpd.Client) error { return c.PlaylistAdd(ignoredList, URI) }, "JSON-ignore-addIgnored")
            if mpderr != nil {
              js["response"] = "error"
              js["error"] = fmt.Sprintf("Failed to add %s: %v", URI, mpderr.Error())
              log.Printf("%v", js["error"])
            } else {
              for i := 0; i < 10; i++ {
                time.Sleep(500 * time.Millisecond)
                if fileInfo, err := os.Stat(mpdignorePath); err == nil {
                  after = fileInfo.Size()
                  if after > before { break }
                }
              }
              if after > before {
                js["response"] = fmt.Sprintf("Added to %s playlist: %s", ignoredList, URI)
                log.Printf("%v", js["response"])
              } else {
                js["response"] = "error"
                js["error"] = fmt.Sprintf("Failed to detect a change to %s; investigate ignore service.", mpdignorePath)
              }
            }
          } else {
            js["response"] = "error"
            js["error"] = "Ignore failed: MPD returned no URI to add to ignored playlist"
          }
        }

        out, _ := json.Marshal(js)
        return []string{string(out)}

      case "playlist_current":
        window := 12 // default window size

        if argsIface != nil {
          switch v := argsIface.(type) {
          case map[string]interface{}:
            if w, ok := v["window"].(float64); ok {
              window = int(w)
            }
          case float64:
            window = int(v)
          case string:
            if n, err := strconv.Atoi(v); err == nil {
              window = n
            }
          }
        }

        err := mpdDo(func(c *mpd.Client) error {

          // 1. get current position
          status, err := c.Status()
          if err != nil {
            return err
          }

          posStr, ok := status["song"]
          if !ok {
            return fmt.Errorf("no current song position")
          }

          pos, err := strconv.Atoi(posStr)
          if err != nil {
            return err
          }

          // 2. calculate bounds
          lower := pos - window
          if lower < 0 {
            lower = 0
          }

          upper := pos + window + 1

          // 3. fetch window slice
          songs, err := c.PlaylistInfo(lower, upper)
          if err != nil {
            return err
          }

          // 4. format response
          resp := []map[string]string{}

          for i, song := range songs {
            posZI := lower + i

            resp = append(resp, map[string]string{
              "pos":         fmt.Sprintf("%d", posZI+1),
              "albumartist": song["AlbumArtist"],
              "artist":      song["Artist"],
              "title":       song["Title"],
              "album":       song["Album"],
              "disc":        song["Disc"],
              "track":       song["Track"],
              "time":        song["Time"],
            })
          }

          js["response"] = resp
          return nil

        }, "playlist_current")

        if err != nil {
          js["response"] = "error"
          js["error"] = err.Error()
        }

        out, _ := json.Marshal(js)
        return []string{string(out)}

      case "playlist_album":
        js := make(map[string]interface{})

        err := mpdDo(func(c *mpd.Client) error {
          cur, err := c.CurrentSong()
          if err != nil {
            return err
          }

          mbAlbumID := cur["MUSICBRAINZ_ALBUMID"]
          if mbAlbumID == "" {
            mbAlbumID = cur["musicbrainz_albumid"]
          }
          albumArtist := cur["AlbumArtist"]
          album := cur["Album"]

          entries, err := c.PlaylistInfo(-1000, -1000)
          if err != nil {
            return err
          }

          var filtered []map[string]string
          for _, e := range entries {
            // If MusicBrainz album ID exists, use it for filtering
            if mbAlbumID != "" {
              if id, ok := e["MUSICBRAINZ_ALBUMID"]; ok && id == mbAlbumID {
                filtered = append(filtered, e)
                continue
              }
              if id, ok := e["musicbrainz_albumid"]; ok && id == mbAlbumID {
                filtered = append(filtered, e)
                continue
              }
            }
            // fallback to album artist + album match
            if e["AlbumArtist"] == albumArtist && e["Album"] == album {
              filtered = append(filtered, e)
            }
          }

          resp := make([]map[string]string, len(filtered))
          for i, song := range filtered {
            resp[i] = map[string]string{
              "pos":         fmt.Sprintf("%d", i+1),
              "albumartist": song["AlbumArtist"],
              "artist":      song["Artist"],
              "title":       song["Title"],
              "album":       song["Album"],
              "disc":        song["Disc"],
              "track":       song["Track"],
              "time":        song["Time"],
            }
          }

          js["response"] = resp
          return nil
        }, "playlist_album")

        if err != nil {
          js["response"] = []map[string]string{}
          js["error"] = err.Error()
        }

        out, _ := json.Marshal(js)
        return []string{string(out)}

      // Add to verbProcessorJSON switch statement, under system case "mpd", "player":
//      case "albumart":
//        dbg("Entered case albumart")
//        var (
//          img []byte
//          imgAA []byte  // image from AlbumArt
//          imgRP []byte  // image from ReadPicture
//          cfgAA image.Config
//          cfgRP image.Config
//          errAA error
//          errRP error
//          mimeType string
//          uri string
//          ErrNoResponse = fmt.Errorf("no response")
////          ErrNoResponse = errors.New("no response")
////          fixArt bool
//        )
//
//        err := mpdDo(func(c *mpd.Client) error {
//          var err error
//          if s, ok := argsIface.(string); ok && s == "fix_art" {
//            cur, err := c.CurrentSong()
//            if err != nil {
//              return err
//            }
//            uri = cur["file"]
////            fixArt = true
//            if err = c.PlaylistAdd(fixArtList, uri); err != nil {
//              log.Printf("[vPJ] failed adding %q to %s: %v", uri, fixArtList, err)
//            }
//            return ErrNoResponse
//          } else if s, ok := argsIface.(string); ok && strings.Contains(s, string(os.PathSeparator)) {
//            uri = s
//          } else {
//            cur, err := c.CurrentSong()
//            if err != nil {
//              return err
//            }
//            uri = cur["file"]
//          }
////          log.Printf("[vPJ] albumart uri requested: %s", uri)
////          img, err = c.ReadPicture(uri)  // Returns []byte directly
////          if err != nil || len(img) == 0 {
////            log.Printf("[vPJ] albumart: ReadPicture failed, falling back to AlbumArt for %q", uri)
////            img, err = c.AlbumArt(uri)
////            if err != nil || len(img) == 0 {
////              log.Printf("[vPJ] albumart: AlbumArt failed, no image available for %q", uri)
////              if err = c.PlaylistAdd(noCoverList, uri); err != nil {
////                log.Printf("[vPJ] failed adding %q to %s: %v", uri, noCoverList, err)
////              }
////              img = nil
////            } else {
////              dbg("[vPJ] albumart: AlbumArt succeeded, %d bytes", len(img))
////            }
////          } else {
////            dbg("[vPJ] albumart: ReadPicture succeeded, %d bytes", len(img))
////          }
//          imgRP, errRP = c.ReadPicture(uri)  // Returns []byte directly
//          imgAA, errAA = c.AlbumArt(uri)
//          if errRP != nil || len(imgRP) == 0 {
//            log.Printf("[vPJ] albumart: ReadPicture failed for %q", uri)
//            if err = c.PlaylistAdd(noEmbedList, uri); err != nil {
//              log.Printf("[vPJ] failed adding %q to %s: %v", uri, noEmbedList, err)
//            }
//          } else {
//            dbg("[vPJ] albumart: ReadPicture succeeded, %d bytes", len(imgRP))
//            cfgRP, _, errRP = image.DecodeConfig(bytes.NewReader(imgRP))
//          }
//
//          if errAA != nil || len(imgAA) == 0 {
//            log.Printf("[vPJ] albumart: AlbumArt failed, no image available for %q", uri)
//            if err = c.PlaylistAdd(noAAlist, uri); err != nil {
//              log.Printf("[vPJ] failed adding %q to %s: %v", uri, noAAlist, err)
//            }
//          } else {
//            dbg("[vPJ] albumart: AlbumArt succeeded, %d bytes", len(imgAA))
//            cfgAA, _, errAA = image.DecodeConfig(bytes.NewReader(imgAA))
//          }
//
//          if errAA != nil && errRP != nil {
//            if err = c.PlaylistAdd(noCoverList, uri); err != nil {
//              log.Printf("[vPJ] failed adding %q to %s: %v", uri, noCoverList, err)
//            }
//            img = nil
//          }
//
//          if errAA != nil || errRP != nil {
//            log.Printf("decode failure reached comparison: errAA=%v errRP=%v", errAA, errRP)
//          }
//
//          if cfgAA.Width + cfgAA.Height > cfgRP.Width + cfgRP.Height {
//            if errAA == nil {
//              img = imgAA
//            }
//          } else if errRP == nil {
//            img = imgRP
//          } else if errAA == nil {
//            img = imgAA
//          }
//
//          if cfgAA.Width <= 500 || cfgAA.Height <= 500 {
//            if err = c.PlaylistAdd(smallAAlist, uri); err != nil {
//              log.Printf("[vPJ] failed adding %q to %s: %v", uri, smallAAlist, err)
//            }
//          }
//
//          if cfgRP.Width <= 500 || cfgRP.Height <= 500 {
//            if err = c.PlaylistAdd(smallRPlist, uri); err != nil {
//              log.Printf("[vPJ] failed adding %q to %s: %v", uri, smallRPlist, err)
//            }
//          }
//
//          if err != nil {
//            return err
//          }
//          return nil
//        }, "JSON-albumart")
//
//        if err == ErrNoResponse {
//          return nil
//        }
//
//        if err != nil {
//          js["response"] = "error"
//          js["uri"] = uri
//          js["error"] = fmt.Sprintf("AlbumArt failed: %v", err)
//          out, _ := json.Marshal(js)
//          return []string{string(out)}
//        }
//
//        // Detect MIME type
//        mimeType = "image/jpeg"
//        if len(img) > 3 {
//          // PNG magic bytes: 89 50 4E 47
//          if img[0] == 0x89 && img[1] == 0x50 &&
//             img[2] == 0x4E && img[3] == 0x47 {
//            mimeType = "image/png"
//          }
//        }
//
////      base64Data := base64.StdEncoding.EncodeToString(img)
//        js["response"] = "ok"
//        js["size"] = len(img)
//        js["uri"] = uri
////      js["data"] = base64Data
//        js["mime_type"] = mimeType
//        out, _ := json.Marshal(js)
//
//          if len(img) > 0 {
//            ctx.conn.Write(context.Background(), websocket.MessageText, out)
//            ctx.conn.Write(context.Background(), websocket.MessageBinary, img)
//            log.Printf("[vPJ] pushed album art (%d bytes) to albumart request", len(img))
//          } else {
//            ctx.conn.Write(context.Background(), websocket.MessageText, out)
//            ctx.conn.Write(context.Background(), websocket.MessageBinary, []byte{})
//            log.Printf("[vPJ] pushed empty album art frame to albumart request")
//          }
//        return nil
      case "albumart":
        dbg("Entered case albumart")

        var (
          img []byte
          uri string
          ErrNoResponse = fmt.Errorf("no response")
          art *AlbumArtResult
        )

        err := mpdDo(func(c *mpd.Client) error {
          var err error

          // --- special fix_art mode ---
          if s, ok := argsIface.(string); ok && s == "fix_art" {
            cur, err := c.CurrentSong()
            if err != nil {
              return err
            }

            uri = cur["file"]

            if err = c.PlaylistAdd(fixArtList, uri); err != nil {
              log.Printf("[vPJ] failed adding %q to %s: %v", uri, fixArtList, err)
            }

            return ErrNoResponse
          }

          // --- resolve uri ---
          if s, ok := argsIface.(string); ok && strings.Contains(s, string(os.PathSeparator)) {
            uri = s
          } else {
            cur, err := c.CurrentSong()
            if err != nil {
              return err
            }
            uri = cur["file"]
          }

          // --- unified album art pipeline ---
          art, err = getBestAlbumArt(c, uri)
          if err != nil {
            return err
          }

          if art != nil {
            img = art.Img
          } else {
            img = nil
          }

          return nil
        }, "JSON-albumart")

        if err == ErrNoResponse {
          return nil
        }

        if err != nil {
          js["response"] = "error"
          js["uri"] = uri
          js["error"] = fmt.Sprintf("AlbumArt failed: %v", err)
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

//        // --- MIME detection ---
//        mimeType = "image/jpeg"
//        if len(img) > 3 {
//          if img[0] == 0x89 &&
//            img[1] == 0x50 &&
//            img[2] == 0x4E &&
//            img[3] == 0x47 {
//            mimeType = "image/png"
//          }
//        }
//
//        // --- response JSON ---
//        js["response"] = "ok"
//        js["size"] = len(img)
//        js["uri"] = uri
//        js["mime_type"] = mimeType
        js["response"] = art

        out, _ := json.Marshal(js)

        // --- websocket push ---
        if len(img) > 0 {
          conn.Write(context.Background(), websocket.MessageText, out)
          conn.Write(context.Background(), websocket.MessageBinary, img)
          log.Printf("[vPJ] pushed album art (%d bytes) to albumart request", art.Size)
        } else {
          conn.Write(context.Background(), websocket.MessageText, out)
          conn.Write(context.Background(), websocket.MessageBinary, []byte{})
          log.Printf("[vPJ] pushed empty album art frame to albumart request")
        }

        return nil


/*    DO NOT DELETE THIS IS A AWORK IN PROGRESS TO INCLUDE A BASE64 OPTION!



      case "album-art":
        type AlbumArtRequest struct {
          Type string `json:"type,omitempty"` // "base64", "blob", "binary"
          URI  string `json:"uri,omitempty"`  // optional file path
        }

       var reqArgs AlbumArtRequest
       var raw string
       var items []json.RawMessage

       // Try unmarshaling as a string first
       if err := json.Unmarshal(req.Args, &raw); err == nil {
          switch raw {
          case "base64", "blob", "binary":
            reqArgs.Type = raw
          default:
            if strings.Contains(raw, string(os.PathSeparator)) {
              reqArgs.Type = "blob"   // default type
              reqArgs.URI = raw
            } else {
              js["response"] = "error"
              js["error"] = fmt.Sprintf("Invalid album art arg string: %s", raw)
              out, _ := json.Marshal(js)
              return []string{string(out)}
            }
          }
        } else if err := json.Unmarshal(req.Args, &reqArgs); err == nil {
          // object case, optional URI
          if reqArgs.Type == "" {
            reqArgs.Type = "blob"
          }
        } else if err := json.Unmarshal(req.Args, &items); err == nil {
          // array of objects case
          if len(items) > 0 {
            if err := json.Unmarshal(items[0], &reqArgs); err != nil {
              js["response"] = "error"
              js["error"] = err.Error()
              out, _ := json.Marshal(js)
              return []string{string(out)}
            }
            if reqArgs.Type == "" {
              reqArgs.Type = "blob"
            }
          }
        } else {
          // nothing specified, use defaults
          reqArgs.Type = "blob"
        }

        // At this point, you have:
        // reqArgs.Type = "base64"|"blob"|"binary"
        // reqArgs.URI = optional file path ("" if none)
        // Then just branch your code:
        var binary bool
        switch reqArgs.Type {
        case "base64":
          binary = false
        case "blob", "binary":
          binary = true
        default:
          js["response"] = "error"
          js["error"] = "Unknown type: " + reqArgs.Type
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        // Now load the file if URI is set, or default behavior
        var data []byte
        if reqArgs.URI != "" {
          var err error
          data, err = os.ReadFile(reqArgs.URI)
          if err != nil {
            js["response"] = "error"
            js["error"] = fmt.Sprintf("Failed to read file %s: %v", reqArgs.URI, err)
            out, _ := json.Marshal(js)
            return []string{string(out)}
          }
        }

        if !binary && len(data) > 0 {
          dataStr := base64.StdEncoding.EncodeToString(data)
          js["data"] = dataStr
        } else if binary && len(data) > 0 {
          js["data"] = data // send raw blob
        } else {
          // no file, default blob/base64 behavior
        }
*/

      case "seek":
        var relative bool
        var seconds int
        var percent float64

        // parse argsIface
        if argsIface != nil {
          switch v := argsIface.(type) {

          case float64:
            if v < 1 {
              percent = v
              relative = true
            } else {
              seconds = int(v)
            }

          case string:
            if strings.HasSuffix(v, "%") {
              n, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
              if err != nil {
                js["response"] = "error"
                js["error"] = err.Error()
                return []string{string(js["response"].(string))}
              }
              percent = n / 100.0
              relative = true
            } else {
              n, err := strconv.Atoi(v)
              if err != nil {
                js["response"] = "error"
                js["error"] = err.Error()
                return []string{string(js["response"].(string))}
              }
              seconds = n
            }
          }
        }

        // perform the seek
        err := mpdDo(func(c *mpd.Client) error {

          if relative {
            status, err := c.Status()
            if err != nil {
              js["response"] = "error"
              js["error"] = err.Error()
              return err
            }

            durationStr, ok := status["duration"]
            if !ok {
              err := fmt.Errorf("duration not found in status")
              js["response"] = "error"
              js["error"] = err.Error()
              return err
            }

            duration, err := strconv.ParseFloat(durationStr, 64)
            if err != nil {
              js["response"] = "error"
              js["error"] = err.Error()
              return err
            }

            seconds = int(duration * percent)
          }

          err := c.SeekCur(time.Duration(seconds)*time.Second, false)
          if err != nil {
            js["response"] = "error"
            js["error"] = err.Error()
            return err
          }

          js["response"] = fmt.Sprintf("Seek to %d", seconds)
          return nil
        }, "Seek")

        if err != nil {
          js["response"] = "error"
          js["error"] = err.Error()
        }

        out, _ := json.Marshal(js)
        return []string{string(out)}


      default: // of system case "mpd" switch cmd
        js["response"] = "error"
        js["error"] = "unknown mpd cmd"
        out, _ := json.Marshal(js)
        return []string{string(out)}
      }

  case "search":
    switch cmd {
      case "playlistsearch","find","search":
//        var conditions []Condition
        if argsIface == nil {
          js["response"]="error"
          js["error"]="missing args"
          out,_ := json.Marshal(js)
          return []string{string(out)}
        }

        args, ok := argsIface.(map[string]interface{})
        if !ok {
          js["response"]="error"
          js["error"]="invalid args"
          out,_ := json.Marshal(js)
          return []string{string(out)}
        }
        raw, ok := args["conds"].([]interface{})
        if !ok {
          js["response"] = "error"
          js["error"] = "missing conds"
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        conditions := make([]Condition, 0, len(raw))

        for i, item := range raw {
          m, ok := item.(map[string]interface{})
          if !ok {
            js["response"] = "error"
            js["error"] = fmt.Sprintf("condition[%d] is not an object", i)
            out, _ := json.Marshal(js)
            return []string{string(out)}
          }
          f, _ := m["field"].(string)
          o, _ := m["op"].(string)
          v, _ := m["value"].(string)
          u, _ := m["unary"].(bool)
          n, _ := m["not"].(string)

          dbg("f='%s'", f)
          dbg("o='%s'", o)
          dbg("v='%s'", v)
          dbg("u='%v'", u)
          dbg("n='%s'", n)

          if strings.ToLower(n) == "null" { n = "" }
          if strings.ToLower(n) == "not" { n="!" }
          if n != "" && n != "!" {
            js["response"] = "error"
            js["error"] = fmt.Sprintf("Condition[%d]: 'not' operator may only be '!' or null (got '%s')", i, n)
            out, _ := json.Marshal(js)
            return []string{string(out)}
          }

          if f == "" {
            js["response"] = "error"
            js["error"] = fmt.Sprintf("condition[%d]: field missing", i)
            out, _ := json.Marshal(js)
            return []string{string(out)}
          }

          if v == "" {
            js["response"] = "error"
            js["error"] = fmt.Sprintf("condition[%d]: value missing", i)
            out, _ := json.Marshal(js)
            return []string{string(out)}
          }

          if u {
            if o != "" {
              js["note"] = fmt.Sprintf("condition[%d]: operator '%s' ignored for unary filter '%s'", i, o, f)
              o = ""
            }
          } else if o == "" {
            js["response"] = "error"
            js["error"] = fmt.Sprintf("condition[%d]: operator cannot be empty when unary=false", i)
            out, _ := json.Marshal(js)
            return []string{string(out)}
          }

          conditions = append(conditions, Condition{
            Field: f,
            Op:    o,
            Value: v,
            Unary: u,
            Not:   n,
          })
        }

        if len(conditions) == 0 {
          js["response"] = "error"
          js["error"] = "No valid conditions supplied"
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        log.Println("cmd:", cmd)
        log.Println("conds:", conditions)

        conn, err := directDialMPD()
        if err != nil {
          js["response"] = "error"
          js["error"] = fmt.Sprintf("MPD connect failed: %v", err)
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }
        defer conn.Close()

        results, err := mpdSearch(conn, cmd, conditions)
        if err != nil {
          js["response"] = "error"
          js["error"] = fmt.Sprintf("MPD search failed: %v", err)
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        log.Printf("Found %d entries\n", len(results))

//        for i, r := range results {
//          for k, v := range r {
//          }
//          log.Println()
//        }

        var resp []AudioV1
        for _, song := range results {
//          for k, v := range song {
//            lk := strings.ToLower(k)
//            if lk != k {
//              delete(song, k)
//              song[lk] = v
//            }
//          }
//
          var a AudioV1
          if err := convert2json(song, &a); err == nil {
            resp = append(resp, a)
          }
          mpdMapPool.Put(song)
        }

        js["response"] = resp
        out,_ := json.Marshal(js)
        return []string{string(out)}
  ///////////// case "playlistsearch", "find", "search": ////////////////////

      default: // of system case "search" switch cmd
        js["response"] = "error"
        js["error"] = "unknown search cmd"
        out, _ := json.Marshal(js)
        return []string{string(out)}
      }

  case "linger":
    switch cmd {
      case "next":
//        var respArr []string
        log.Printf("vPJ: received linger next")

        // ------------------------------------------------------------
        // 1. Update block state
        // ------------------------------------------------------------
        state.mu.Lock()
        state.transition = true
        state.paused = false
        state.mu.Unlock()


        // ------------------------------------------------------------
        // ORIGINAL CODE
        // Problem:
        // 1. json.Unmarshal overwrote respArr each time
        // 2. repeated verbProcessorJSON + map boilerplate
        // ------------------------------------------------------------
        /*
        resp := verbProcessorJSON(map[string]interface{}{
          "system":"mpd",
             "cmd":"random",
            "args":1,
        }, ctx)

        if err := json.Unmarshal([]byte(resp[0]), &respArr); err != nil {
          respArr = []string{"error unmarshaling response"}
        }

        resp = verbProcessorJSON(map[string]interface{}{
          "system":"mpd",
             "cmd":"next",
        }, ctx)

        if err := json.Unmarshal([]byte(resp[0]), &respArr); err != nil {
          respArr = append([]string{"error unmarshaling response"})
        }

        resp = verbProcessorJSON(map[string]interface{}{
          "system":"mpd",
             "cmd":"play",
        }, ctx)

        if err := json.Unmarshal([]byte(resp[0]), &respArr); err != nil {
          respArr = append([]string{"error unmarshaling response"})
        }
        */


        // ------------------------------------------------------------
        // NEW CODE
        //
        // Helper: runs an MPD command through verbProcessorJSON
        // and APPENDS the resulting response(s) into respArr.
        //
        // This removes repeated JSON handling and prevents
        // overwriting previous responses.
        // ------------------------------------------------------------
        run := func(cmd string, args interface{}) {
          jsreq := map[string]interface{}{
            "system":"mpd",
            "cmd":cmd,
          }

          if args != nil {
            jsreq["args"] = args
          }

          resp := verbProcessorJSON(jsreq, req, conn)

          var obj map[string]any
          log.Printf("%#v", obj["response"])
          if err := json.Unmarshal([]byte(resp[0]), &obj); err != nil {
            js["response"] = append(js["response"].([]any), map[string]any{
                "error": "unmarshal failed",
                "detail": err.Error(),
                "raw": resp[0],
            })
            log.Printf("unmarshal failed: %v", err)
            return
          }

          r := js["response"].([]any)
          r = append(r, obj)
          js["response"] = r

        }

        // ------------------------------------------------------------
        // Equivalent to CLI handler behaviour
        // ------------------------------------------------------------

        js["response"] = []any{}

        // turn random on
        run("random", 1)

        // advance playlist
        run("next", nil)

        // resume playback if paused (vPJ play/nil → play -1)
        run("play", nil)

        // ------------------------------------------------------------
        // Linger message
        // ------------------------------------------------------------
        r := js["response"].([]any)
        r = append(r, "linger block advanced")
        js["response"] = r
        out, _ := json.Marshal(js)
        return []string{string(out)}

      case "toggle":
        log.Printf("vPJ: received toggle command")

        state.mu.Lock()
        paused := !state.paused
        state.mu.Unlock()
        expired, limit := setPaused(paused)
        state.mu.Lock()
        state.paused = paused
        state.mu.Unlock()

        if !paused && expired {
          _ = mpdDo(func(c *mpd.Client) error {
            return c.Random(true)
          }, "IPC-toggle-resume")
        }

        if !paused {
          mpdPlayPause(true, "IPC-toggle-resume")
        }

        log.Printf("STATE CHANGE: paused=%v expired=%v count=%d limit=%d transition=%v",
          state.paused, expired, state.count, limit, state.transition)

        respArr := []string{
          map[bool]string{true: "Paused", false: "Resumed"}[paused],
        }

        out, _ := json.Marshal(respArr)
        return []string{string(out)}

      case "count":
        n := -1
        hasArg := false
        var err error
        var x   int
        var y   int


        switch v := argsIface.(type) {
        case []interface{}:
          if len(v) > 0 {
            hasArg = true
            switch x := v[0].(type) {
            case float64:
              n = int(x)
            case string:
              if y, err = strconv.Atoi(x); err == nil {
                n = y
              }
            }
          }
        case float64:
          n = int(v)
          hasArg = true
        case string:
          if x, err = strconv.Atoi(v); err == nil {
            n = x
            hasArg = true
          }
        }

        if !hasArg {
          js["response"] = "error"
          js["error"] = "count requires a value"
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        if err != nil || n < 0 {
          js["response"] = "error"
          js["error"] = "invalid count"
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        state.mu.Lock()

        state.count = n

        limit := state.baseLimit
        if state.blockOn && state.blockLimit > 0 && state.blockLimit != state.baseLimit {
          limit = state.blockLimit
        }

        state.transition = state.count >= limit
        transition := state.transition

        deriveStateLocked(state.lastSongZI, state.lastSongID, state.lastSongURI, limit)

        state.mu.Unlock()

        _ = mpdDo(func(c *mpd.Client) error {
          return c.Random(transition)
        }, "count")

        idleEvents <- IdleEvent{
          Subsystem: "linger",
        }

        log.Printf("[vPJ] STATE CHANGE: count set=%d", n)
        js["response"] = fmt.Sprintf("Count set to %d", n)
        out, _ := json.Marshal(js)
        return []string{string(out)}

      case "limit":
        n := 0
        hasArg := false

        if args, ok := argsIface.([]interface{}); ok && len(args) > 0 {
          switch v := args[0].(type) {
          case float64:
            n = int(v)
            hasArg = true
          case string:
            if x, err := strconv.Atoi(v); err == nil {
              n = x
              hasArg = true
            }
          }
        }

        state.mu.Lock()

        if !hasArg || n <= 0 {
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

        log.Printf("STATE CHANGE: [vPJ] persistent limit set=%d (effective=%d)",
          state.baseLimit, limit)

        out, _ := json.Marshal([]string{
          fmt.Sprintf("Persistent limit set to %d", state.baseLimit),
        })

        return []string{string(out)}

      case "blocklimit":
        log.Printf("[vPJ] Entered blocklimit case.")
        log.Printf("[vPJ] raw argsIface: %#v", argsIface)

        n := 0
        hasArg := false

        switch v := argsIface.(type) {
        case []interface{}:
          if len(v) > 0 {
            switch x := v[0].(type) {
            case float64:
              n = int(x)
              hasArg = true
            case string:
              if y, err := strconv.Atoi(x); err == nil {
                n = y
                hasArg = true
              }
            }
          }
        case float64:
          n = int(v)
          hasArg = true
        case string:
          if x, err := strconv.Atoi(v); err == nil {
            n = x
            hasArg = true
          }
        }

       log.Printf("[vPJ] n=%d; hasArg=%v", n, hasArg)


        state.mu.Lock()

        if n <= 0 || !hasArg {
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

        _ = mpdDo(func(c *mpd.Client) error {
          return c.Random(false)
        }, "IPC-blocklimit")

        log.Printf("STATE CHANGE: [IPC] block limit set=%d (effective=%d), count=%d, transition=%v",
          state.blockLimit, limit, state.count, state.transition)

        js["response"] = "ok"

        out, _ := json.Marshal(js)
        return []string{string(out)}

      case "version":
        respArr := []string{}
        respArr = append(respArr,
          fmt.Sprintf("mpdgolinger daemon %s", version))

        err := mpdDo(func(c *mpd.Client) error {
          proto := c.Version()
          respArr = append(respArr,
            fmt.Sprintf("mpd protocol version %s", proto))

          return nil
        }, "version")

        if err != nil {
          respArr = append(respArr,
            "mpd protocol version unavailable")
        }

        if mpdPath != "" {
          cmd := exec.Command(mpdPath, "--version")
          stdout, err := cmd.StdoutPipe()

          if err == nil && cmd.Start() == nil {
            scanner := bufio.NewScanner(stdout)

            if scanner.Scan() {
              line := strings.TrimSpace(scanner.Text())
              re := regexp.MustCompile(`^Music Player Daemon .* \(v([0-9]+\.[0-9]+\.[0-9]+)\)$`)
              matches := re.FindStringSubmatch(line)

              if len(matches) == 2 {
                respArr = append(respArr,
                  fmt.Sprintf("%s version %s", mpdPath, matches[1]))
              } else {
                respArr = append(respArr,
                  fmt.Sprintf("%s version %s", mpdPath, line))
              }
            }

            _ = cmd.Wait()
          }
        } else {
          respArr = append(respArr,
            "No path available for mpd binary")
        }

        out, _ := json.Marshal(respArr)

        return []string{string(out)}

      case "xy":
        js := make(map[string]interface{})
        var xyErr error
        // parse args iface
        var args struct {
          LingerXY bool `json:"lingerxy"`
          LingerX  int  `json:"lingerx"`
          LingerY  int  `json:"lingery"`
          XYInc    bool `json:"xyinc"`
        }
        if err := mapstructure.Decode(argsIface, &args); err != nil {
          log.Printf("[IPC] xy: failed to decode args: %v", err)
          js["error"] = "failed to parse XY args"
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        if !args.LingerXY {
          xy.active = false
          _ = mpdDo(func(c *mpd.Client) error {
            if err := c.Consume(state.consume); err != nil {
              log.Printf("[XY] failed to restore consume=%t: %v", state.consume, err)
            }
            return nil
          }, "xy-restore-consume")
          log.Printf("[IPC] XY mode turned off")

          js["response"] = "XY mode turned off"
          resp := verbProcessor("json-status")
          if len(resp) > 0 {
            var status map[string]interface{}
            if err := json.Unmarshal([]byte(resp[0]), &status); err == nil {
              for k, v := range status {
                js[k] = v
              }
            }
          }
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }


        err := mpdDo(func(c *mpd.Client) error {
          st, err := c.Status()
          if err != nil { return err }

          state.mu.Lock()
          state.consume = (st["consume"] == "1")      // save current consume state
          state.mu.Unlock()

          if err := c.Consume(true); err != nil {
            log.Printf("[XY] failed to enable consume: %v", err)
          }
          if err := c.Random(false); err != nil {
            log.Printf("[XY] failed to disable random: %v", err)
          }

          songZI, _ := strconv.Atoi(st["song"])
          pllength, _ := strconv.Atoi(st["playlistlength"])

          // normalize
          xy.active = true
          xy.start = args.LingerX - 1                 // convert from user 1I to mpd ZI

          if xy.start == -1 {
            xy.start = songZI
          }

          if args.XYInc {
            xy.end = xy.start + args.LingerY
            dbg("xy.start + args.LingerY = %d + %d = %d", xy.start, args.LingerY, xy.start + args.LingerY)
          } else {
            xy.end = args.LingerY - 1                 // convert from user 1I to mpd ZI
            if xy.end == -1 { xy.end = pllength - 1 } // pllength is not zero-indexed!
          }

          if xy.start > xy.end {
            xy.start, xy.end = xy.end, xy.start       // order start/end as needed
          }

          if args.LingerY < 0 {
            xy.end = xy.end + 1
          }

          if xy.end > pllength - 1 { xy.end = pllength -1 }

          if xy.start < 0 {
            xyErr = fmt.Errorf("X value must be greater than zero: X=%d → Y=%d", xy.start + 1, xy.end + 1)
            return xyErr
          }

          if songZI != xy.start {
            if err := c.Play(xy.start); err != nil {
              log.Printf("[XY] failed to jump to start: %v", err)
            }
          }

          if xy.start == xy.end {
            xyErr = fmt.Errorf("Nothing to do, X = Y: %d = %d", xy.start, xy.end)
            xy.active = false
            if err := c.Consume(state.consume); err != nil {
              log.Printf("[XY] failed to return consume or original %v state: %v", state.consume, err)
            }
          }

          return nil
        }, "xy-init")

        if err != nil {
          js["error"] = err.Error()
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        if xyErr != nil {
          js["error"] = xyErr.Error()
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        log.Printf("[IPC] XY mode enabled: %d → %d", xy.start, xy.end)
        js["response"] = fmt.Sprintf("XY mode enabled: %d → %d", xy.start+1, xy.end+1)
        js["lingerxy"] = true
        js["lingerx"] = xy.start + 1
        js["lingery"] = xy.end + 1
        out, _ := json.Marshal(js)
        return []string{string(out)}

      default: // of system case "linger" switch cmd
        js["response"] = "error"
        js["error"] = "unknown linger cmd"
        out, _ := json.Marshal(js)
        return []string{string(out)}
    } // switch cmd
// new pulseaudio
  case "pulseaudio":
    switch cmd {
      case "set_volume", "mute_volume", "DOWN_VOLUME", "down_volume", "up_volume", "UP_VOLUME":
        var (
          vol int
          pulseWord string
          cmdStr string
          errParse error
          errPulse error
          relative bool
          hasArg bool
          outBytes []byte
        )
        if cmd != "set_volume" && argsIface != "" && argsIface != nil {
          js["notes"] = fmt.Sprintf("cmd does not take an argument: %v", argsIface)
        }

        if cmd == "set_volume" {
          pulseWord = "set-sink-volume"
          switch v := argsIface.(type) {
            case float64:
              vol = int(v)
              hasArg = true
            case string:
              if strings.HasPrefix(v, "-") || strings.HasPrefix(v, "+") {
                if _, errParse = strconv.Atoi(v); errParse == nil {
                  cmdStr = v+"%"
                  relative = true
                }
              } else {
                vol, errParse = strconv.Atoi(v)
              }
              hasArg = true
          }

          log.Printf("[vPJ] pulseaudio set_volume vol: %d", vol)

          if !hasArg {
            js["response"] = "error"
            js["error"] = "pulseaudio set_volume requires a value"
            out, _ := json.Marshal(js)
            return []string{string(out)}
          }

          if errParse != nil || ( vol < 0 && ! relative ) {
            js["response"] = "error"
            js["error"] = fmt.Sprintf("invalid set_volume value provided: %v", argsIface)
            out, _ := json.Marshal(js)
            return []string{string(out)}
          }

          if ! relative {
            cmdStr = fmt.Sprintf("%d%%", vol)
          }
        } else if cmd == "mute_volume" {
          pulseWord = "set-sink-mute"
          cmdStr    = "toggle"
        } else {
          var sign string
          var val string
          pulseWord = "set-sink-volume"
          if strings.HasPrefix(strings.ToLower(cmd), "down") {
            sign = "-"
          } else {
            sign = "+"
          }
          if cmd == strings.ToUpper(cmd) {
            val = "5%"
          } else {
            val = "1%"
          }
          cmdStr = sign + val
          log.Printf("[vPJ] pulseaudio cmdStr: %s", cmdStr)
        }

        serverFlag := fmt.Sprintf("--server=%s:%d", PulseData.PulseServer, PulseData.PulsePort)
        pulseArgs := []string{
          serverFlag,
          pulseWord,
          PulseData.SinkName,
        }
        if cmdStr != "" {
          pulseArgs = append(pulseArgs, cmdStr)
        }

        outBytes, errPulse = exec.Command( PulseData.PulsePath, pulseArgs... ).CombinedOutput()

        log.Printf("[vPJ] pulseaudio: %v", pulseArgs)

        if errPulse != nil {
          js["response"] = "error"
          js["error"] = fmt.Sprintf("pulse command failed: %v|%v", errPulse, strings.TrimSpace(string(outBytes)))
          log.Printf("[vPJ] pulseaudio error: %v", js["error"])
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }


        if js["notes"] != nil {
          js["response"] = "warning"
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        return nil

      case "get_volume":
        if argsIface != "" && argsIface != nil {
          js["error"] = fmt.Sprintf("cmd does not take an argument: %v", argsIface)
        }
        js["response"] = PulseData
        out, _ := json.Marshal(js)
        return []string{string(out)}

      default: // of system case "pulseaudio" switch cmd
        js["response"] = "error"
        js["error"] = "unknown pulseaudio cmd"
        out, _ := json.Marshal(js)
        return []string{string(out)}
      } // pulseaudio switch cmd

  case "websocket":
    switch cmd {
      case "subscribe":
        log.Printf("[vPJ] SUBSCRIBE handler entered")
        clone := func(in map[string]any) map[string]any {
          out := make(map[string]any, len(in))
          for k, v := range in {
            out[k] = v
          }
          return out
        }

        // push subscribed first
        jsSubscribe := clone(js)
        jsSubscribe["response"] = "subscribed"
        out, _ := json.Marshal(jsSubscribe)
        conn.Write(context.Background(), websocket.MessageText, out)

        idleEvents <- IdleEvent{
          Subsystem: "player",
          Subscribe: true,
             Target: conn,
        }

        return nil

    case "ping":
      js["response"] = "pong"
      out, _ := json.Marshal(js)
      conn.Write(context.Background(), websocket.MessageText, out)
      return nil

    case "debug":
      if b, ok := argsIface.(bool); ok {
        debug = b
      } else {
        debug = true
      }
      js["response"] = map[string]bool{"debug": debug}
      out, _ := json.Marshal(js)
      return []string{string(out)}

   default: // of system case "websocket" switch cmd
      js["response"] = "error"
      js["error"] = "unknown websocket cmd"
      out, _ := json.Marshal(js)
      return []string{string(out)}
    } // websocket switch cmd

  case "scrobbler":
    switch cmd {
    case "toggle":
      var toggleState bool
      var toggleCmd string
      toggleState = !state.scrobbler.Enabled
      if toggleState {
        toggleCmd = "start"
      } else {
        toggleCmd = "stop"
      }

      err := exec.Command("sudo", "/usr/bin/systemctl", toggleCmd, "mpd-scrobbler").Run()

      if err != nil {
        js["response"] = "error"
        js["system"] = "scrobbler"
        js["cmd"] = cmd
        js["error"] = err.Error()

        out, _ := json.Marshal(js)
        return []string{string(out)}
      }

      state.mu.Lock()
      state.scrobbler.Enabled = toggleState
      log.Printf("state.scrobblerOn: %t", state.scrobbler.Enabled)
      state.mu.Unlock()

      idleEvents <- IdleEvent{Subsystem: "scrobbler"}

      js["response"] = "ok"
      js["system"] = "scrobbler"
      js["cmd"] = cmd

      out, _ := json.Marshal(js)
      return []string{string(out)}


    case "on":
      log.Printf("------------------------------in scrobbler on-----------------")
      err := exec.Command("sudo", "/usr/bin/systemctl", "start", "mpd-scrobbler").Run()

      if err != nil {
        js["response"] = "error"
        js["system"] = "scrobbler"
        js["cmd"] = cmd
        js["error"] = err.Error()

        out, _ := json.Marshal(js)
        return []string{string(out)}
      }

      state.mu.Lock()
      state.scrobbler.Enabled = true
      log.Printf("state.scrobblerOn: %t", state.scrobbler.Enabled)
      state.mu.Unlock()

      idleEvents <- IdleEvent{Subsystem: "scrobbler"}

      js["response"] = "ok"
      js["system"] = "scrobbler"
      js["cmd"] = cmd

      out, _ := json.Marshal(js)
      return []string{string(out)}


    case "off":
      log.Printf("------------------------------in scrobbler off-----------------")
      err := exec.Command("sudo", "/usr/bin/systemctl", "stop", "mpd-scrobbler").Run()
//      cmd := exec.Command("sudo", "/usr/bin/systemctl", "stop", "mpd-scrobbler")
//
//      out1, err := cmd.CombinedOutput()
//
//      log.Printf("[vPJ] systemctl out: %s", string(out1))
//      if err != nil {
//        log.Printf("[vPJ] systemctl error: %v", err)
//        out, _ := json.Marshal(js)
//        return []string{string(out)}
//      }
      if err != nil {
        js["response"] = "error"
        js["system"] = "scrobbler"
        js["cmd"] = cmd
        js["error"] = err.Error()

        out, _ := json.Marshal(js)
        return []string{string(out)}
      }

      state.mu.Lock()
      state.scrobbler.Enabled = false
      state.mu.Unlock()

      idleEvents <- IdleEvent{Subsystem: "scrobbler"}

      js["response"] = "ok"
      js["system"] = "scrobbler"
      js["cmd"] = cmd

      out, _ := json.Marshal(js)
      return []string{string(out)}

    case "restart":
      err := exec.Command("/usr/local/bin/mpd-scrobblerctl", "restart").Run()

      if err != nil {
        js["response"] = "error"
        js["system"] = "scrobbler"
        js["cmd"] = cmd
        js["error"] = err.Error()

        out, _ := json.Marshal(js)
        return []string{string(out)}
      }

      idleEvents <- IdleEvent{Subsystem: "scrobbler"}

      js["response"] = "ok"
      js["system"] = "scrobbler"
      js["cmd"] = cmd

      out, _ := json.Marshal(js)
      return []string{string(out)}

    case "status":
      err := exec.Command("/usr/local/bin/mpd-scrobblerctl", "status").Run()

      if err != nil {
        js["response"] = "error"
        js["system"] = "scrobbler"
        js["cmd"] = cmd
        js["error"] = err.Error()

        out, _ := json.Marshal(js)
        return []string{string(out)}
      }

      idleEvents <- IdleEvent{Subsystem: "scrobbler"}

      js["response"] = "ok"
      js["system"] = "scrobbler"
      js["cmd"] = cmd

      out, _ := json.Marshal(js)
      return []string{string(out)}


    default:
      js["response"] = "error"
      js["system"] = "scrobbler"
      js["cmd"] = cmd
      js["error"] = "unknown scrobbler cmd"

      out, _ := json.Marshal(js)
      return []string{string(out)}
  } // scrobbler switch cmd


  default: // of switch system
    switch cmd {
      case "ping":
        js["response"] = "pong"
        js["timestamp"] = "implement timestamp"
        out, _ := json.Marshal(js)
        conn.Write(context.Background(), websocket.MessageText, out)
        return nil

    default:
      js["response"] = "error"
      js["error"] = "invalid system"
      out, _ := json.Marshal(js)
      return []string{string(out)}
    }
  } // switch system

} // func verbProcessorJSON()


