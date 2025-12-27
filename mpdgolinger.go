package main


import (
//  "flag"
   flag "github.com/spf13/pflag"
  "github.com/fhs/gompd/v2/mpd"
  "regexp"
  "bufio"
  "bytes"
  "fmt"
  "log"
  "net"
  "io"
  "os"
  "os/signal"
  "os/exec"
  "syscall"
  "path/filepath"
  "strconv"
  "strings"
  "sync"
  "time"
  "sync/atomic"
)


//const version = "0.03.0"


// State holds daemon state
type State struct {
  mu           sync.Mutex
  paused       bool
  count        int    // current position in block
  blockLimit   int    // temporary override (later)
  transition   bool   // true between last-song-start and next-song-start
  lastSongID   string
  baseLimit    int
  blockOn      bool
} // type State struct


type configFile struct {
  path         string
  data         string
  exists       bool
} // type configFile struct


type derivedState struct {
  WriteTime      string
  SongID         string
  Paused         int
  Count          int
  BaseLimit      int
  Limit          int
  BlockLimit     int
  PID            int
} // type derivedState struct


const (
  defaultMPDhost = "localhost"
  defaultMPDport = 6600
  defaultMPDsocket = "/run/mpd/socket"
  defaultState = "/var/lib/mpd/mpdlinger/mpdgolinger.state"
  defaultListenIP = "0.0.0.0"
  defaultListenPort = 6599
  defaultSocketPath = "/var/lib/mpd/mpdlinger/mpdgolinger.sock"
  defaultDaemonIP = "localhost"
  defaultDaemonPort = 6559
  defaultMPDpath = "/usr/bin/mpd"
) // const


var (
  version = "dev"
  // Core daemon state
  state = &State{
    baseLimit: defaultLimit,
  }

  defaultLimit = 4

  shutdown = make(chan struct{})
  shutdownOnce sync.Once

  // State file management
  statePath string
  stateEnabled bool

  // MPD connection parameteres
  mpdHost string = ""
  mpdPort int    = 0
  mpdSocket string = ""
  mpdPath = defaultMPDpath

  // IPC socket
  socketPath = defaultSocketPath

  // TCP listener for TCP client connections
  listenIP   string
  listenPort int

  // Client flags for TCP client connections
  daemonIP   string
  daemonPort int

  execPost   string

  daemonMode bool

  configFlag string
  verbose    bool
  allowed = map[string]bool{
    "status": true, "toggle": true, "pause": true, "resume": true,
    "limit": true, "block": true, "blocklimit": true, "version": true,
    "next": true, "skip": true, "quit": true, "exit": true,
  }
) // var


// loadConfig loads the config file from a given path or defaults to ~/.config/mpdgolinger.conf
func loadConfig(cliPath string) configFile {
  var path string

  if cliPath != "" {
    path = cliPath
  } else {
    home, err := os.UserHomeDir()
    if err != nil {
      return configFile{}
    }
    path = filepath.Join(home, ".config", "mpdgolinger.conf")
  }

  cf := configFile{path: path}

  data, err := os.ReadFile(path)
  if err != nil {
    return cf
  }

  cf.exists = true
  cf.data = string(data)
  return cf
} // func loadConfig(cliPath string) configFile {


// dumpConfig prints the config path and optionally its contents if verbose
func dumpConfig(cf configFile) {
  fmt.Fprintf(os.Stderr, "config path: %s\n", cf.path)

  if !cf.exists {
    fmt.Fprintln(os.Stderr, "config file: not found")
    return
  }

  if verbose {
    fmt.Fprintln(os.Stderr, "config contents:")
    fmt.Fprintln(os.Stderr, "-----")
    fmt.Fprint(os.Stderr, cf.data)
    if !strings.HasSuffix(cf.data, "\n") {
      fmt.Fprintln(os.Stderr)
    }
    fmt.Fprintln(os.Stderr, "-----")
  }
} // func dumpConfig(cf configFile) {


// parseConfig parses key=value lines from a string into a map
func parseConfig(data string) map[string]string {
  cfg := make(map[string]string)

  for _, line := range strings.Split(data, "\n") {
    line = strings.TrimSpace(line)
    if line == "" || strings.HasPrefix(line, "#") {
      continue
    }

    k, v, ok := strings.Cut(line, "=")
    if !ok {
      continue
    }

    k = strings.TrimSpace(k)
    v = strings.TrimSpace(v)

    if k != "" {
      cfg[k] = v
    }
  }
  return cfg
} // func parseConfig(data string) map[string]string


// mpdDo runs a function with a short-lived MPD client and logs errors
func mpdDo(fn func(*mpd.Client) error, ctx string) error {
    var client *mpd.Client
    var err error

    useSocket := false
    useTCP := false

    // 1. User defined TCP + socket → prefer socket, fallback TCP
    if mpdHost != "" && mpdPort != 0 && mpdSocket != "" {
        if conn, err := net.DialTimeout("unix", mpdSocket, 500*time.Millisecond); err == nil {
            conn.Close()
            log.Printf("[mpdDo] socket usable: %s", mpdSocket)
            useSocket = true
        } else if client, err = mpd.Dial("tcp", fmt.Sprintf("%s:%d", mpdHost, mpdPort)); err == nil {
            log.Printf("[mpdDo] tcp usable: %s:%d", mpdHost, mpdPort)
            useTCP = true
        } else {
            log.Printf("[mpdDo] neither user socket nor tcp usable")
        }
    }

    // 2. Only socket defined
    if !useSocket || !useTCP && mpdSocket != "" {
        if conn, err := net.DialTimeout("unix", mpdSocket, 500*time.Millisecond); err == nil {
            conn.Close()
            log.Printf("[mpdDo] socket usable: %s", mpdSocket)
            useSocket = true
        } else {
            log.Printf("[mpdDo] socket unusable: %v", err)
        }
    }

    // 3. Only TCP defined
    if !useSocket || !useTCP && mpdHost != "" && mpdPort != 0 {
        if client, err = mpd.Dial("tcp", fmt.Sprintf("%s:%d", mpdHost, mpdPort)); err == nil {
            log.Printf("[mpdDo] tcp usable: %s:%d", mpdHost, mpdPort)
            useTCP = true
        } else {
            log.Printf("[mpdDo] tcp unusable: %v", err)
        }
    }

    // 4. Fallback defaults
    if !useSocket && !useTCP {
        if conn, err := net.DialTimeout("unix", defaultMPDsocket, 500*time.Millisecond); err == nil {
            conn.Close()
            log.Printf("[mpdDo] default socket usable: %s", defaultMPDsocket)
            mpdSocket = defaultMPDsocket
            useSocket = true
        } else if client, err = mpd.Dial("tcp", fmt.Sprintf("%s:%d", defaultMPDhost, defaultMPDport)); err == nil {
            log.Printf("[mpdDo] default tcp usable: %s:%d", defaultMPDhost, defaultMPDport)
            useTCP = true
        } else {
            return fmt.Errorf("mpdDo: no usable MPD connection")
        }
    }

    // Final connect (socket happens here once)
    if useSocket {
        client, err = mpd.Dial("unix", mpdSocket)
        if err != nil {
            return fmt.Errorf("mpdDo: socket connect failed: %v", err)
        }
    }

    // Timeout-wrapped execution
    done := make(chan error, 1)
    go func() {
        done <- fn(client)
    }()

    select {
    case err := <-done:
        client.Close()
        return err
    case <-time.After(3 * time.Second):
        client.Close()
        return fmt.Errorf("mpdDo: timeout (%s)", ctx)
    }
} // func mpdDo(fn func(*mpd.Client) error, ctx string) error


// mpdBinaryVersion returns the binary version of MPD from the executable path
func mpdBinaryVersion(path string) string {
  cmd := exec.Command(path, "--version")
  out, err := cmd.Output()
  if err != nil {
    return "unavailable"
  }

  scanner := bufio.NewScanner(bytes.NewReader(out))
  if scanner.Scan() {
    version := strings.TrimSpace(scanner.Text())

    // Strip prefix up to "(v"
    if idx := strings.Index(version, "(v"); idx != -1 {
      version = version[idx+2:] // skip "(v"
    }

    // Strip trailing ")"
    version = strings.TrimSuffix(version, ")")

    return version
  }

  return "unavailable"
} // func mpdBinaryVersion(path string) string {


// setRandom toggles MPD random mode and logs the change
func setRandom(on bool, src string) {
  _ = mpdDo(func(c *mpd.Client) error {
    return c.Random(on)
  }, src)
  log.Printf("STATE CHANGE: [%s] mpd random=%v", src, on)
} // func setRandom(on bool, src string)


// mpdNext skips to the next track in MPD and logs the action
func mpdNext(src string) {
  _ = mpdDo(func(c *mpd.Client) error {
    return c.Next()
  }, src)
  log.Printf("STATE CHANGE: [%s] mpd next track", src)
} // func mpdNext(src string)


// mpdPlayPause plays or pauses MPD and logs the action
func mpdPlayPause(play bool, src string) {
  _ = mpdDo(func(c *mpd.Client) error {
    if play {
      return c.Play(-1)
    }
    return c.Pause(true)
  }, src)
  log.Printf("STATE CHANGE: [%s] mpd play=%v", src, play)
} // func mpdPlayPause(play bool, src string)


// dialMPD returns a connected MPD client using UNIX socket or TCP
func dialMPD() (*mpd.Client, error) {
  if mpdSocket != "" {
    // Use UNIX socket if provided
    return mpd.Dial("unix", mpdSocket)
  }
  // Otherwise, use TCP host:port
  return mpd.Dial("tcp", fmt.Sprintf("%s:%d", mpdHost, mpdPort))
} // func dialMPD() (*mpd.Client, error)


// newWatcherMPD returns a new MPD watcher for idle events
func newWatcherMPD() (*mpd.Watcher, error) {
  if mpdSocket != "" {
    return mpd.NewWatcher("unix", mpdSocket, "", "player")
  }
  return mpd.NewWatcher("tcp", fmt.Sprintf("%s:%d", mpdHost, mpdPort), "", "player")
} //vfunc newWatcherMPD() (*mpd.Watcher, error)


// logStateChange logs standardized state transition information
func logStateChange(src, songID string, count, limit int, transition bool) {
  log.Printf(
    "STATE CHANGE: [%s]: songID=%s count=%d/limit=%d transition=%v",
    src, songID, count, limit, transition,
  )
} // func logStateChange(src, songID string, count, limit int, transition bool)


// logCurrentSong logs the current song’s file for debugging
func logCurrentSong(c *mpd.Client, prefix string) {
  if c == nil {
    log.Printf("%s: currentsong skipped (nil client)", prefix)
    return
  }

  song, err := c.CurrentSong()
  if err != nil {
    log.Printf("%s: currentsong error: %v", prefix, err)
    return
  }

  file := song["file"]
  if file == "" {
    log.Printf("%s: currentsong: <no file field>", prefix)
    return
  }

  log.Printf("%s: currentsong file=%q", prefix, file)
} // func logCurrentSong(c *mpd.Client, prefix string)


// daemonSupervisor ensures the daemon’s main loops, reconnects, and IPC listener
func daemonSupervisor() {
  var ipcRunning int32

  for {
    select {
    case <-shutdown:
      log.Println("Daemon supervisor shutting down")
      return
    default:
    }

    // Ensure IPC socket exists (daemon responsibility)
    if _, err := os.Stat(socketPath); err != nil {
      if os.IsNotExist(err) && atomic.LoadInt32(&ipcRunning) == 0 {
        log.Printf("IPC socket missing, recreating: %s", socketPath)
        os.Remove(socketPath)
        go func() {
          atomic.StoreInt32(&ipcRunning, 1)
          startIPC(socketPath)
          atomic.StoreInt32(&ipcRunning, 0)
        }()
      } else {
        log.Printf("IPC socket stat error: %v", err)
      }
    }


    log.Println("Connecting to MPD for idle loop...")

    var w *mpd.Watcher
    var err error
    if mpdSocket != "" {
      w, err = mpd.NewWatcher("unix", mpdSocket, "", "player")
    } else {
      w, err = mpd.NewWatcher("tcp", fmt.Sprintf("%s:%d", mpdHost, mpdPort), "", "player")
    }

    if err != nil {
      log.Printf("Watcher init failed: %v — retrying in 2s", err)
      time.Sleep(2 * time.Second)
      continue
    }

    if err := runIdleLoop(w); err != nil {
      log.Printf("Idle loop exited: %v — reconnecting in 2s", err)
    }
    w.Close()
    time.Sleep(2 * time.Second)
  }
} // func daemonSupervisor()


// runIdleLoop runs the MPD idle loop, updating state on song changes
func runIdleLoop(w *mpd.Watcher) error {
  log.Println("MPD connection established, entering idle loop")

  // mpd.Watcher exposes an Error channel that reports lower-level
  // connection issues (EOF, broken pipe, protocol errors).
  //
  // IMPORTANT:
  //   These errors do *not* necessarily stop the Event channel,
  //   so we log them asynchronously for correlation only.
  //
  go func() {
    for err := range w.Error {
      // keep this very explicit so we can trace EOF vs broken pipe
      log.Printf("Watcher error: %v", err)
    }
  }()

  // Main idle loop:
  // For each idle event, open a short-lived command connection and run
  // status/commands via pdDo (your safe wrapper).
  // Each value received from w.Event represents an MPD "idle"
  // notification for a specific subsystem (usually "player").
  //
  // MPD does NOT tell us *what* changed — only that *something*
  // in that subsystem did.
  //
  for {
    select {
    case <-shutdown:
      log.Println("Idle loop received shutdown")
      return nil

    case subsystem, ok := <-w.Event:
      if !ok {
        return fmt.Errorf("watcher closed")
      }

      log.Printf("Idle event subsystem=%s", subsystem)

      // For *every* idle event we:
      //   • Open a fresh short-lived MPD connection
      //   • Query current status
      //   • Optionally toggle random
      //
      // This avoids:
      //   • idle/command protocol conflicts
      //   • stale connections after EOF
      //
      // Use mpdDo to make a fresh short-lived command connection
      err := mpdDo(func(c *mpd.Client) error {
        // Log the current song immediately on idle receipt.
        //
        // This gives us a "pre-status" snapshot so we can see:
        //   • what MPD *thought* was playing when idle fired
        //   • whether seek/pause events keep the same file
        //
        logCurrentSong(c, "idle event (pre-status)")

        // Fetch current player status.
        //
        // If this fails, the connection is already bad and the
        // supervisor must reconnect.
        //
        status, err := c.Status()
        if err != nil {
          // return so outer code treats this as a status error and reconnects
          return err
        }
        songID := status["songid"]

        // Protect shared FSM state.
        state.mu.Lock()

        // Compute the current limit
        limit := state.baseLimit
        if state.blockOn && state.blockLimit > 0 {
          limit = state.blockLimit
        }

        // If the user pauses the linger functionality (and state.paused is true):
        if state.paused {
          if songID != state.lastSongID {
            state.lastSongID = songID
            state.count++ // keep counting while paused (matches bash behavior)
            log.Printf("Paused: song advanced, count=%d (limit=%d)", state.count, limit)
          } else {
            log.Printf("Paused: idle event, song unchanged")
          }
          deriveStateLocked(songID, limit)  // <--- write after increment to update state count while paused

          state.mu.Unlock()
          return nil
        }

        // If songID did not change, this idle event was caused by:
        //   • seek
        //   • pause/resume
        //   • repeat/random toggles
        //   • other non-track-boundary events
        //
        if songID == state.lastSongID {
          // nothing changed; keep trace so we can see frequent idle hits
          log.Printf("Idle event received but songID unchanged: %s", songID)
          state.mu.Unlock()
          // Log currentsong again so we can verify the *file*
          // really stayed the same across the idle break.
          logCurrentSong(c, "idle event (songID unchanged)")
          return nil
        }

        // From here on, MPD has started a *new song*.
        // Update the FSM accordingly.
        //
        prevCount := state.count
        prevTransition := state.transition
        state.lastSongID = songID

        if state.transition {
          // We were waiting for the first song *after* a random block.
          //
          // This is the moment to:
          //   • turn random OFF
          //   • reset the block counter
          //
          if err := c.Random(false); err != nil {
            log.Printf("random(false) failed: %v", err)
            // Do NOT abort: random failure should not desync FSM
          }

          state.blockLimit = 0
          state.blockOn = false
          state.count = 1
          state.transition = false
          log.Printf("Transition: random off, count reset to 1")

        } else if state.count == limit-1 { // <- uses computed local limit
          // This song completes the block.
          //
          // We:
          //   • increment count
          //   • turn random ON
          //   • mark that the *next* song is a transition
          //
          state.count++
          if err := c.Random(true); err != nil {
            log.Printf("random(true) failed: %v", err)
          }

          state.transition = true
          log.Printf("Last song in block reached: random on, transition true")

          // Clear blockLimit after block completes
          state.blockLimit = 0
          state.blockOn = false

        } else {
          // Normal in-block advance.
          state.count++
          log.Printf("Normal increment: count=%d/%d", state.count, limit)
        }

        // Emit a structured state-change log so we can replay FSM
        // behavior purely from logs.
        //
        logStateChange(
          "idleLoop-event",
          songID,
          state.count,
          limit,
          state.transition,
        )

        // Write out to the statefile
        deriveStateLocked(songID, limit)

        // Log the song again *after* state changes and random toggles.
        // This lets us confirm whether MPD advanced tracks as expected.
        //
        logCurrentSong(c, "idle event (post-state-change)")

        _ = prevCount
        _ = prevTransition
        state.mu.Unlock()

        return nil

      }, "idleLoop-event")

      if err != nil {
        // Make the error explicit and return so daemonSupervisor can reconnect
        log.Printf("MPD status fetch failed: %v — will attempt reconnect", err)

        // Best-effort logging of currentsong during failure path.
        _ = mpdDo(func(c *mpd.Client) error {
          logCurrentSong(c, "idle error path")
          return nil
        }, "idle-error-log")

        log.Println("MPD connection closed")
        return fmt.Errorf("status error: %w", err)
      }
    }
  }

  // If we exit the loop, the watcher closed.
  return fmt.Errorf("watcher closed")
} // func runIdleLoop()


// startIPC starts a UNIX socket server to accept client commands
func startIPC(path string) {
  ln, err := net.Listen("unix", path)
  if err != nil {
    log.Fatalf("Failed to listen on %s: %v", path, err)
  }
  defer ln.Close()

  // Now the socket exists → set ownership and permissions
  //  os.Chown(socketPath, uid, gid) // uid/gid for mpd user
  os.Chmod(socketPath, 0660)     // owner+group read/write

  log.Printf("IPC listening on %s", path)

  for {
    conn, err := ln.Accept()
    if err != nil {
      log.Printf("IPC accept error: %v", err)
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
func shellQuote(s string) string {
  return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// shellQuoteSlice quotes an entire slice of strings
func shellQuoteSlice(slice []string) []string {
  out := make([]string, len(slice))
  for i, s := range slice {
    out[i] = shellQuote(s)
  }
  return out
}

// shellQuoteKV quotes only the value part of a key=value string for shell safety
func shellQuoteKV(line string) string {
  parts := strings.SplitN(line, "=", 2)
  if len(parts) != 2 {
    // not a key=value pair, just quote the whole thing
    return shellQuote(line)
  }
  key, val := parts[0], parts[1]
  return key + "=" + shellQuote(val)
}


func verbProcessor(csv string) []string {
  var responses []string

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
    case "status":
      state.mu.Lock()
      limit := state.baseLimit
      if state.blockOn && state.blockLimit > 0 {
        limit = state.blockLimit
      }
      ds := deriveStateLocked(state.lastSongID, limit)
      state.mu.Unlock()

      // append lines to responses slice
      responses = append(responses,
        fmt.Sprintf("writetime=%s", ds.WriteTime),
        fmt.Sprintf("lingersongid=%s", ds.SongID),
        fmt.Sprintf("lingerpause=%d", ds.Paused),
        fmt.Sprintf("lingercount=%d", ds.Count),
        fmt.Sprintf("lingerbase=%d", ds.BaseLimit),
        fmt.Sprintf("lingerlimit=%d", ds.Limit),
        fmt.Sprintf("lingerblocklmt=%d", ds.BlockLimit),
        fmt.Sprintf("lingerpid=%d", ds.PID),
      )

    case "mpc":
      var st mpd.Attrs // declare outside so we can use it later

      log.Printf("IPC: received mpc command")

      // --- update persistent state ---
      state.mu.Lock()
      deriveStateLocked(state.lastSongID, state.baseLimit)
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
          responses = append(responses,
            fmt.Sprintf("mpd.%s=%s", k, v),
          )
        }

        // --- current song ---
        cs, err := c.CurrentSong()
        if err != nil {
          return err
        }
        for k, v := range cs {
          responses = append(responses,
            fmt.Sprintf("current.%s=%s", k, v),
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
            responses = append(responses,
              fmt.Sprintf("next.%s=%s", k, v),
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

    case "pause":
      log.Printf("IPC: received pause command")
      expired, limit := setPaused(true)
      log.Printf("STATE CHANGE: paused=%v expired=%v count=%d limit=%d transition=%v",
        state.paused, expired, state.count, limit, state.transition)
      state.mu.Lock()
      state.paused = true
      deriveStateLocked(state.lastSongID, limit)
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

    case "next":
      log.Printf("IPC: received next command")
      state.mu.Lock()
      state.transition = true
      state.paused = false
      state.mu.Unlock()

      _ = mpdDo(func(c *mpd.Client) error {
        if err := c.Random(true); err != nil {
          return err
        }
        if err := c.Next(); err != nil {
          return err
        }
        return c.Play(-1)
      }, "IPC-nextBlock")

      log.Printf("STATE CHANGE: [IPC-nextBlock] forced block advance, count reset")
      resp = "Advanced to next block"

    case "skip":
      log.Printf("IPC: received skip command")
      _ = mpdDo(func(c *mpd.Client) error {
        if err := c.Next(); err != nil {
          return err
        }
        return c.Play(-1)
      }, "IPC-skip")

      state.mu.Lock()
      log.Printf(
        "STATE CHANGE: [IPC-skip] count=%d baseLimit=%d blockLimit=%d transition=%v paused=%v",
        state.count, state.baseLimit, state.blockLimit, state.transition, state.paused,
      )
      state.mu.Unlock()
      resp = "Skipped to next track"

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
      deriveStateLocked(state.lastSongID, limit)
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
      deriveStateLocked(state.lastSongID, limit)
      state.mu.Unlock()

      log.Printf("STATE CHANGE: [IPC] persistent limit set=%d (effective=%d)",
        state.baseLimit, limit)
      resp = "Persistent limit set"

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

      deriveStateLocked(state.lastSongID, limit)
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

    case "exit", "quit":
      log.Printf("IPC: received %s, shutting down", cmd)
      requestShutdown()
      resp = "OK"

    default:
      resp = "Unknown command: " + cmd
    }

    responses = append(responses, resp)
  }

  state.mu.Lock()
  songID := state.lastSongID
  limit := state.baseLimit
  if state.blockOn && state.blockLimit > 0 {
    limit = state.blockLimit
  }
  deriveStateLocked(songID, limit)
  state.mu.Unlock()

  return responses
} // func verbProcessor(csv string) []string


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
  log.Printf("acceptClients started")
  for {
    conn, err := ln.Accept()
    if err != nil {
      log.Printf("Accept error: %v", err)
      continue
    }
    log.Printf("Client connected from %s", conn.RemoteAddr())
    go handleTCP(conn)
  }
} // func acceptClients(ln net.Listener)


// deriveStateLocked writes the current state to disk (state.mu must be held)
func deriveStateLocked(songID string, limit int) *derivedState {
  // state.mu MUST already be held
  now := time.Now().Format(time.RFC3339Nano)

  ds := &derivedState{
    WriteTime:  now,
    SongID:     songID,
    Paused:     btoi(state.paused),
    Count:      state.count,
    BaseLimit:  state.baseLimit,
    Limit:      limit,
    BlockLimit: state.blockLimit,
    PID:        os.Getpid(),
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
} // func writeStateLocked(songID string, limit int)


// formatState formats a derivedState struct into key=value lines
func formatState(w io.Writer, ds *derivedState) {
  fmt.Fprintf(w, "writetime=%s\n", ds.WriteTime)
  fmt.Fprintf(w, "lingersongid=%s\n", ds.SongID)
  fmt.Fprintf(w, "lingerpause=%d\n", ds.Paused)
  fmt.Fprintf(w, "lingercount=%d\n", ds.Count)
  fmt.Fprintf(w, "lingerbase=%d\n", ds.BaseLimit)
  fmt.Fprintf(w, "lingerlimit=%d\n", ds.Limit)
  fmt.Fprintf(w, "lingerblocklmt=%d\n", ds.BlockLimit)
  fmt.Fprintf(w, "lingerpid=%d\n", ds.PID)
} // func formatState(w io.Writer, ds *derivedState)


// sendStatus sends the current state to a writer (IPC/TCP client)
func sendStatus(w io.Writer) {
  state.mu.Lock()

  limit := state.baseLimit
  if state.blockOn && state.blockLimit > 0 {
    limit = state.blockLimit
  }

  ds := deriveStateLocked(state.lastSongID, limit)
  state.mu.Unlock()

  formatState(w, ds)
} // func sendStatus(w io.Writer)


// clientCommandHandler parses client command-line args and sends them to the daemon
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
           "pause",
           "resume",
           "next",
           "skip",
           "toggle",
           "quit",
           "mpc",
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
func btoi(b bool) int {
  if b {
    return 1
  }
  return 0
} // func btoi(b bool)


// requestShutdown closes the shutdown channel exactly once
func requestShutdown() {
  shutdownOnce.Do(func() {
    close(shutdown)
  })
} // func requestShutdown()


// initShutdownHandler installs a signal handler to trigger shutdown
func initShutdownHandler() {
  sigs := make(chan os.Signal, 1)
  signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
  go func() {
    <-sigs
    close(shutdown)
  }()
} // func initShutdownHandler()


// main parses flags, initializes state, and optionally runs as daemon
func main() {
  // ------------------------------------------------------------------
  // Shared flags (client + daemon)
  // ------------------------------------------------------------------
  var (
      configFlag   string
      startupLimit int
      daemonMode   bool
      showVersion  bool
      showHelp     bool
      socketFlag   string
      logPath      string
  )

  flag.StringVar(&daemonIP, "daemonip", "", "client: daemon IP to connect to")
  flag.IntVar(&daemonPort, "daemonport", 0, "client: daemon port to connect to")
  flag.StringVar(&execPost, "execpost", "", "post-execution action")
  flag.StringVar(&logPath, "log", "", "write logs to file instead of stderr")
  flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging")
  flag.StringVar(&configFlag, "config", "", "path to config file")
  flag.StringVar(&socketFlag, "socket", "", "mpdgolinger IPC socket path")
  flag.BoolVar(&showVersion, "version", false, "Print version and exit")
  flag.BoolVar(&showHelp, "help", false, "Print help and exit")
  flag.IntVar(&startupLimit, "limit", 0, "Set initial persistent <limit>")
  flag.BoolVar(&daemonMode, "daemon", false, "Run as daemon")
  flag.StringVar(&mpdSocket, "mpdsocket", "", "MPD unix socket <path>")
  flag.StringVar(&mpdHost, "mpdhost", mpdHost, "MPD host <address>")
  flag.IntVar(&mpdPort, "mpdport", mpdPort, "MPD host <port>")
  flag.StringVar(&listenIP, "listenip", "", "Daemon listen IP")
  flag.IntVar(&listenPort, "listenport", 0, "Daemon listen port")
  flag.Func("state", "Write state file to <path>", func(v string) error {
       stateEnabled = true
       statePath = v
       return nil
  })

  // ------------------------------------------------------------------
  // Parse flags ONCE
  // ------------------------------------------------------------------
  flag.Parse()

  // ------------------------------------------------------------------
  // Load + dump config
  // ------------------------------------------------------------------
  cfg := loadConfig(configFlag)
  dumpConfig(cfg)

  // parse config key/value map
  kv := parseConfig(cfg.data)

  // ------------------------------------------------------------------
  // Apply config values with precedence: CLI > config > default
  // ------------------------------------------------------------------

  if socketFlag != "" {
    socketPath = socketFlag
  } else if v, ok := kv["socket"]; ok && v != "" {
    socketPath = v
  }

  if socketPath == "none" {
    socketPath = ""
  }

  if v, ok := kv["mpdsocket"]; ok && v != "" && mpdSocket == "" {
    mpdSocket = v
  }
  if v, ok := kv["mpdhost"]; ok && v != "" && mpdHost == "" {
    mpdHost = v
  }
  if v, ok := kv["mpdport"]; ok && v != "" && mpdPort == 0 {
    if n, err := strconv.Atoi(v); err == nil {
      mpdPort = n
    }
  }

  if listenIP == "" {
    if v, ok := kv["listenip"]; ok && v != "" {
      listenIP = v
    } else {
      listenIP = defaultListenIP
    }
  }

  if listenPort == 0 {
    if v, ok := kv["listenport"]; ok && v != "" {
      if n, err := strconv.Atoi(v); err == nil {
        listenPort = n
      }
    } else {
      listenPort = defaultListenPort
    }
  }


  if daemonIP == "" {
    if v, ok := kv["daemonip"]; ok && v != "" {
      daemonIP = v
    } else {
      daemonIP = defaultDaemonIP
    }
 }

  if daemonPort == 0 {
    if v, ok := kv["daemonport"]; ok && v != "" {
      if n, err := strconv.Atoi(v); err == nil {
        daemonPort = n
      }
    } else {
      daemonPort = defaultDaemonPort
    }
  }


  if v, ok := kv["state"]; ok && v != "" && statePath == "" {
    stateEnabled = true
    statePath = v
  }

  if v, ok := kv["limit"]; ok && v != "" && startupLimit == 0 {
    if n, err := strconv.Atoi(v); err == nil && n > 0 {
      startupLimit = n
      state.baseLimit = n
      defaultLimit = n
    }
  }

  if v, ok := kv["execPost"]; ok && v != "" && execPost == "" {
    execPost = v
  } else if v, ok := kv["execpost"]; ok && v != "" && execPost == "" {
    execPost = v
  }

  if v, ok := kv["log"]; ok && v != "" && logPath == "" {
    logPath = v
  }

  // ------------------------------------------------------------------
  // OPTIONAL: redirect logs if --log is set
  // MUST be before any log.Printf / log.Fatalf
  // ------------------------------------------------------------------
  if logPath != "" {
    f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
    if err != nil {
      log.Fatalf("failed to open log file %s: %v", logPath, err)
    }
    log.SetOutput(f)
  }

  // ------------------------------------------------------------------
  // Client command handling (positional args only)
  // ------------------------------------------------------------------

  args := flag.Args()
  if len(args) > 0 {
    if err := clientCommandHandler(args); err != nil {
      fmt.Fprintln(os.Stderr, err)
      os.Exit(1)
    }
    return
  }

  // ------------------------------------------------------------------
  // END: Client command handling if args are provided
  // ------------------------------------------------------------------

  // ------------------------------------------------------------------
  // Version / help
  // ------------------------------------------------------------------
//  if showVersion {
//    fmt.Println()
//    fmt.Println(" mpdgolinger version:", version)
//    fmt.Println("mpd protocol version:", mpdProtocolVersion())
//    mpdPath := "/usr/bin/mpd"
//    fmt.Println(mpdPath, "version:", mpdBinaryVersion(mpdPath))
//    fmt.Println()
//    return
//  }
  if showVersion {
    fmt.Printf("\nmpdgolinger binary version %s\n\n", version)
    os.Exit(0)
  }


  if showHelp {
//    fmt.Println()
//    fmt.Println(" mpdgolinger version:", version)
//    fmt.Println("mpd protocol version:", mpdProtocolVersion())
//    mpdPath := "/usr/bin/mpd"
//    fmt.Println(mpdPath, "version:", mpdBinaryVersion(mpdPath))
//    fmt.Println()
    fmt.Printf("\nmpdgolinger binary version %s\n\n", version)
    fmt.Println("Usage: mpdgolinger --daemon [flags] or client subcommands")
    flag.PrintDefaults()
    return
  }

  // ------------------------------------------------------------------
  // Enforce daemon gate
  // ------------------------------------------------------------------
  if !daemonMode {
    log.Fatalf("Refusing to start daemon without --daemon")
  }

  // ------------------------------------------------------------------
  // Daemon startup continues unchanged
  // ------------------------------------------------------------------
  if startupLimit > 0 {
    state.baseLimit = startupLimit
  }

  client, err := dialMPD()
  if err != nil {
    log.Printf("Failed to connect to MPD at startup: %v", err)
    state.count = 0
    state.lastSongID = ""
  } else {
    status, err := client.Status()
    if err != nil {
      log.Printf("Status error at startup: %v", err)
      state.count = 0
      state.lastSongID = ""
    } else {
      state.lastSongID = status["songid"]
      switch status["state"] {
      case "paused", "stop":
        state.count = 0
      default:
        state.count = 1
      }
    }
    client.Close()
  }

  log.Printf(
      "Starting block count=%d, lastSongID=%s, baseLimit=%d, blockLimit=%d",
      state.count,
      state.lastSongID,
      state.baseLimit,
      state.blockLimit,
  )
  setRandom(false, "startup")

  os.Remove(socketPath)
  go startIPC(socketPath)

  if stateEnabled {
    state.mu.Lock()
    deriveStateLocked(state.lastSongID, state.baseLimit)
    state.mu.Unlock()
  }

  // ------------------------------------------------------------------
  // Start daemon TCP listener for remote clients
  // ------------------------------------------------------------------
  log.Printf("About to listen on %s:%d", listenIP, listenPort)

  addr := fmt.Sprintf("%s:%d", listenIP, listenPort)
  ln, err := net.Listen("tcp", addr)
  if err != nil {
    log.Fatalf("Failed to listen on %s: %v", addr, err)
  }
  log.Printf("Listening for clients on %s", addr)

  // Spawn a goroutine to accept clients
  go acceptClients(ln)


  go daemonSupervisor()

  initShutdownHandler()

  select {
  case <-shutdown:
    log.Println("Shutdown requested")
    exitCode := 0

    if err := os.Remove(socketPath); err != nil {
      log.Printf("Failed to remove socket: %v\n", err)
      exitCode = 1
    } else {
      log.Println("Socket removed")
    }

    if stateEnabled {
      if err := os.Remove(statePath); err != nil {
        log.Printf("Failed to remove state file: %v\n", err)
        exitCode = 1
      } else {
        log.Println("State file removed")
      }
    }

    log.Println("Cleanup steps completed, exiting")
    os.Exit(exitCode)
  }
} // func main()
// End of mpdgolinger

