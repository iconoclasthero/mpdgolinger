package main

import (
  "flag"
  "bufio"
  "bytes"
  "fmt"
  "log"
  "net"
  "os"
  "os/exec"
  "strconv"
  "strings"
  "sync"
  "time"
  "sync/atomic"
  "github.com/fhs/gompd/v2/mpd"
)

const version = "0.01.0-alpha"

// State holds daemon state
type State struct {
  mu         sync.Mutex
  paused       bool
  count        int    // current position in block
//limit        int    // persistent block size  // removed from state as it will be computed
  blockLimit   int    // temporary override (later)
  transition   bool   // true between last-song-start and next-song-start
  lastSongID   string
  pollMode     int
  baseLimit    int
  blockOn  bool
}

const (
  defaultLimit = 4
  PollOff = iota
  PollLogging
  PollOn
  stateDefault = "/var/lib/mpd/mpdlinger/mpdgolinger.state"
)

var (
  // Core daemon state
  state = &State{
    baseLimit: defaultLimit,
    pollMode:  PollOff,
  }

  shutdown = make(chan struct{})
  shutdownOnce sync.Once

  // State file management
  statePath string
  stateEnabled bool

  // MPD connection parameteres
  mpdHost string = "localhost"
  mpdPort int    = 6600
  mpdSocket string = "/run/mpd/socket"
  // IPC socket
  socketPath = "/var/lib/mpd/mpdlinger/mpdgolinger.sock"

  // TCP listener for remote client connections (placeholders)
  listenIP string
  listenPort int

  daemonMode bool
)

//// central safe MPD executor
//func mpdDo(fn func(c *mpd.Client) error, src string) error {
//  c, err := mpd.Dial("tcp", mpdHost)
//  if err != nil {
//    log.Printf("[%s] MPD dial failed: %v", src, err)
//    return err
//  }
//  defer c.Close()
//
//  if err := fn(c); err != nil {
//    log.Printf("[%s] MPD command failed: %v", src, err)
//    return err
//  }
//  return nil
//}

func mpdDo(fn func(c *mpd.Client) error, src string) error {
    c, err := dialMPD()
    if err != nil {
        log.Printf("[%s] MPD dial failed: %v", src, err)
        return err
    }
    defer c.Close()

    return fn(c)
}

func mpdProtocolVersion() string {
	var (
		c   *mpd.Client
		err error
	)

	if mpdSocket != "" {
		c, err = mpd.Dial("unix", mpdSocket)
	} else {
		addr := fmt.Sprintf("%s:%d", mpdHost, mpdPort)
		c, err = mpd.Dial("tcp", addr)
	}

	if err != nil {
		return "unavailable"
	}
	defer c.Close()

	return c.Version()
}

//func mpdBinaryVersion(path string) string {
//	cmd := exec.Command(path, "--version")
//	out, err := cmd.Output()
//	if err != nil {
//		return "unavailable"
//	}
//
//	scanner := bufio.NewScanner(bytes.NewReader(out))
//	if scanner.Scan() {
//		return strings.TrimSpace(scanner.Text())
//	}
//
//	return "unavailable"
//}

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
}




// wrapper to set MPD random state
func setRandom(on bool, src string) {
  _ = mpdDo(func(c *mpd.Client) error {
    return c.Random(on)
  }, src)
  log.Printf("STATE CHANGE: [%s] mpd random=%v", src, on)
}

// wrapper to skip next track
func mpdNext(src string) {
  _ = mpdDo(func(c *mpd.Client) error {
    return c.Next()
  }, src)
  log.Printf("STATE CHANGE: [%s] mpd next track", src)
}

// wrapper to play/pause
func mpdPlayPause(play bool, src string) {
  _ = mpdDo(func(c *mpd.Client) error {
    if play {
      return c.Play(-1)
    }
    return c.Pause(true)
  }, src)
  log.Printf("STATE CHANGE: [%s] mpd play=%v", src, play)
}

//// dialMPD returns a connected MPD client using either TCP or UNIX socket
//func dialMPD() (*mpd.Client, error) {
//  if mpdSocket != "" {
//    return mpd.Dial("unix", mpdSocket)
//  }
//  return mpd.Dial("tcp", fmt.Sprintf("%s:%d", mpdHost, mpdPort))
//}

// dialMPD returns a connected MPD client using either TCP or UNIX socket
func dialMPD() (*mpd.Client, error) {
    if mpdSocket != "" {
        // Use UNIX socket if provided
        return mpd.Dial("unix", mpdSocket)
    }
    // Otherwise, use TCP host:port
    return mpd.Dial("tcp", fmt.Sprintf("%s:%d", mpdHost, mpdPort))
}

// newWatcherMPD returns a new watcher for idle loop
func newWatcherMPD() (*mpd.Watcher, error) {
  if mpdSocket != "" {
    return mpd.NewWatcher("unix", mpdSocket, "", "player")
  }
  return mpd.NewWatcher("tcp", fmt.Sprintf("%s:%d", mpdHost, mpdPort), "", "player")
}

// logStateChange logs a standardized message for state transitions
func logStateChange(src, songID string, count, limit int, transition bool) {
  log.Printf(
    "STATE CHANGE: [%s]: songID=%s count=%d/limit=%d transition=%v",
    src, songID, count, limit, transition,
  )
}

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
}

// daemonSupervisor maintains all long-lived daemon invariants including MPD connection, idle loop, and socket
func daemonSupervisor() { // renamed from idleSupervisor
//  var ipcRunning bool
var ipcRunning int32

  for {
    select {
    case <-shutdown:
      log.Println("Daemon supervisor shutting down")
      return
    default:
    }

    // Ensure IPC socket exists (daemon responsibility)
//    if _, err := os.Stat(socketPath); err != nil {
//      if os.IsNotExist(err) && !ipcRunning {
//        log.Printf("IPC socket missing, recreating: %s", socketPath)
//        os.Remove(socketPath)
//        go func() {
//          ipcRunning = true
//          startIPC(socketPath)
//          ipcRunning = false
//        }()
//      } else if err != nil {
//        log.Printf("IPC socket stat error: %v", err)
//      }
//    }

// Ensure IPC socket exists (daemon responsibility)
if _, err := os.Stat(socketPath); err != nil {
  if os.IsNotExist(err) && atomic.LoadInt32(&ipcRunning) == 0 {     // #274
    log.Printf("IPC socket missing, recreating: %s", socketPath)
    os.Remove(socketPath)
    go func() {
      atomic.StoreInt32(&ipcRunning, 1)
      startIPC(socketPath)
      atomic.StoreInt32(&ipcRunning, 0)                             // #280
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
        // writeStateLocked(songID)
        writeStateLocked(songID, limit)

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


// poller optionally polls MPD based on pollMode
func poller(client *mpd.Client) {
	for {
		state.mu.Lock()
		mode := state.pollMode
		state.mu.Unlock()

		if mode == PollOff {
			time.Sleep(time.Second)
			continue
		}

		mpdDo(func(c *mpd.Client) error {
			status, err := c.Status()
			if err != nil {
				return err
			}
			songID := status["songid"]

			state.mu.Lock()
			defer state.mu.Unlock()

			if mode == PollLogging {
				log.Printf("Poll logging: songid=%s", songID)
			} else if mode == PollOn && songID != state.lastSongID && !state.paused {
				state.count++
				if state.count >= state.blockLimit {
					state.count = 0
					log.Println("Poll: block finished — starting new block")
				} else {
					log.Printf("Poll: song %d/%d in current block", state.count, state.blockLimit)
				}
			}
			state.lastSongID = songID
			return nil
		}, "poller")

		time.Sleep(1 * time.Second)
	}
}

// startIPC listens on UNIX socket for client commands
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
		go ipcHandler(conn)
	}
}

// ipcHandler parses commands and updates state
func ipcHandler(conn net.Conn) {
  defer conn.Close()
  scanner := bufio.NewScanner(conn)
  for scanner.Scan() {
    line := scanner.Text()
    fields := strings.Fields(line)
    if len(fields) == 0 {
      continue
    }

    cmd := strings.ToLower(fields[0])
    switch cmd {
    case "pause":
      log.Printf("IPC: received pause command")

      state.mu.Lock()
      state.paused = true
      state.mu.Unlock()

      fmt.Fprintln(conn, "Paused")

    case "resume":
      state.mu.Lock()
      state.paused = false

      // If block already exhausted while paused,
      // force a clean block boundary on next song.
      limit := state.baseLimit
      if state.blockOn && state.blockLimit > 0 {
        limit = state.blockLimit
      }
      expired := state.count >= limit
      if expired {
        state.transition = true
      }

      state.mu.Unlock()

      if expired {
        _ = mpdDo(func(c *mpd.Client) error {
          return c.Random(true)
        }, "IPC-resume")
      }

      // Resume playback unconditionally
      mpdPlayPause(true, "IPC-resume")

      log.Printf(
        "STATE CHANGE: [IPC-resume] paused=false expired=%v count=%d limit=%d transition=%v",
        expired, state.count, limit, state.transition,
      )

      fmt.Fprintln(conn, "Resumed")

    // next: force start of a new block; always resumes playback;
    // idle loop owns count reset and random-off transition
    case "next":
      log.Printf("IPC: received next command")
      // imperative block advance
      state.mu.Lock()
      state.transition = true   // tell idle loop “new block boundary”
      state.paused = false      // we are explicitly resuming
      state.mu.Unlock()

      _ = mpdDo(func(c *mpd.Client) error {
        // ensure we break out of the current block
        if err := c.Random(true); err != nil {
          return err
        }

        // advance to next song (randomized start of block)
        if err := c.Next(); err != nil {
          return err
        }

        // always resume playback
        return c.Play(-1)
      }, "IPC-nextBlock")

      log.Printf("STATE CHANGE: [IPC-nextBlock] forced block advance, count reset")
      fmt.Fprintln(conn, "OK")

    // skip is to advance song within block without requiring another client, e.g., mpc
    case "skip":
      log.Printf("IPC: received skip command")
      _ = mpdDo(func(c *mpd.Client) error {
        // advance to next song
        if err := c.Next(); err != nil {
          return err
        }
        // always resume playback
        return c.Play(-1)
      }, "IPC-skip")

      state.mu.Lock()
      log.Printf(
        "STATE CHANGE: [IPC-skip] count=%d baseLimit=%d blockLimit=%d transition=%v paused=%v",
        state.count, state.baseLimit, state.blockLimit, state.transition, state.paused,
      )
      state.mu.Unlock()

      fmt.Fprintln(conn, "Skipped to next track")

    case "limit":
      if len(fields) < 2 {
        fmt.Fprintln(conn, "Usage: limit N")
        return
      }
      n, err := strconv.Atoi(fields[1])
      if err != nil || n <= 0 {
        fmt.Fprintln(conn, "Invalid limit")
        return
      }

      state.mu.Lock()
      state.baseLimit = n
      state.mu.Unlock()

      log.Printf("STATE CHANGE: [IPC] persistent limit set=%d", n)
      fmt.Fprintln(conn, "Persistent limit set")

    case "setblock", "blocklimit":
      if len(fields) < 2 {
        fmt.Fprintln(conn, "Usage: setblock N")
        continue
      }
      n, err := strconv.Atoi(fields[1])
      if err != nil || n <= 0 {
        fmt.Fprintln(conn, "Invalid block size")
        continue
      }

      state.mu.Lock()
      state.blockOn = true   // activate blockLimit immediately
      state.blockLimit = n       // update the running block limit
//    state.count = 0            // reset count so next song starts fresh
      state.transition = false
      block := state.blockLimit
      state.mu.Unlock()
      _ = mpdDo(func(c *mpd.Client) error {
        // ensure we break out of the current block
        if err := c.Random(false); err != nil {
          return err
        }
        return nil
      }, "IPC-blocklimit")


      log.Printf("STATE CHANGE: [IPC] block limit set=%d, count=%d, transition=%v", block, state.count, state.transition)
      fmt.Fprintf(conn, "Block limit set to %d\nOK\n", n)

    case "status":
      state.mu.Lock()
      limit := state.baseLimit
      if state.blockOn && state.blockLimit > 0 {
        limit = state.blockLimit
      }
      fmt.Fprintf(conn, "paused=%v count=%d limit=%d baseLimit=%d blockLimit=%d lastSongID=%s pollMode=%d\n",
        state.paused, state.count, limit, state.baseLimit, state.blockLimit, state.lastSongID, state.pollMode)
      state.mu.Unlock()

    case "exit", "quit":
      log.Printf("IPC: received %s, shutting down", cmd)
      fmt.Fprintln(conn, "OK")
      requestShutdown()
      return

    default:
      fmt.Fprintln(conn, "Unknown command")
    }
  }

  // Derive final state once per IPC session
  state.mu.Lock()
  songID := state.lastSongID
  limit := state.baseLimit
  if state.blockOn && state.blockLimit > 0 {
    limit = state.blockLimit
  }
  writeStateLocked(songID, limit)
  state.mu.Unlock()


  if err := scanner.Err(); err != nil {
    log.Printf("IPC connection error: %v", err)
  }
} // func ipcHandler()

func sendIPCCommand(cmd string) error {
  conn, err := net.Dial("unix", socketPath)
  if err != nil {
    return err
  }
  defer conn.Close()

  _, err = fmt.Fprintln(conn, cmd)
  if err != nil {
    return err
  }

  // read single-line response
  scanner := bufio.NewScanner(conn)
  if scanner.Scan() {
    fmt.Println(scanner.Text())
  }


  // Derive final state once per IPC session
  state.mu.Lock()

  songID := state.lastSongID

  limit := state.baseLimit
  if state.blockOn && state.blockLimit > 0 {
    limit = state.blockLimit
  }

  state.mu.Unlock()

  writeStateLocked(songID, limit)

  if err := scanner.Err(); err != nil {
    return err
  }

  return nil
}

func writeStateLocked(songID string, limit int) {
  if !stateEnabled {
    return
  }

  // state.mu MUST already be held
  tmp := statePath + ".tmp"

  f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0664)
  if err != nil {
    log.Printf("state write failed: %v", err)
    return
  }

  now := time.Now().Format(time.RFC3339Nano)

//fmt.Fprintf(f, "writepid=%d\n", os.Getpid())
//fmt.Fprintf(f, "writesrc=ipc\n") // we’ll change this later in idle loop
  fmt.Fprintf(f, "writetime=%s\n", now)
  fmt.Fprintf(f, "lingersongid=%s\n",               songID)
  fmt.Fprintf(f, "lingerpause=%d\n",    btoi(state.paused))
  fmt.Fprintf(f, "lingercount=%d\n",           state.count)
  fmt.Fprintf(f, "lingerlimit=%d\n",                 limit)   // This is the working limit of runIdleLoop derived from:
  fmt.Fprintf(f, "lingerbaselmt=%d\n",     state.baseLimit)   // Either the persistent limit unless
  fmt.Fprintf(f, "lingerblockon=%d\n", btoi(state.blockOn))
  if state.blockOn {
    fmt.Fprintf(f, "lingerblocklmt=%d\n", state.blockLimit) // A temprorary block limit override exists
  }
  fmt.Fprintf(f, "lingerpid=%d\n",             os.Getpid())

  f.Close()
  os.Rename(tmp, statePath)
} // func writeStateLocked(songID string, limit int)

func btoi(b bool) int {
  if b {
    return 1
  }
  return 0
} // func btoi(b bool)

func requestShutdown() {
  shutdownOnce.Do(func() {
    close(shutdown)
  })
} // func requestShutdown()

func main() {
  // CLI subcommands (client mode)
  if len(os.Args) > 1 {
    switch os.Args[1] {

    case "status":
      if err := sendIPCCommand("status"); err != nil {
        log.Fatalf("IPC error: %v", err)
      }
      return

    case "pause":
      if err := sendIPCCommand("pause"); err != nil {
        log.Fatalf("IPC error: %v", err)
      }
      fmt.Println("OK")
      return

    case "resume":
      if err := sendIPCCommand("resume"); err != nil {
        log.Fatalf("IPC error: %v", err)
      }
      fmt.Println("OK")
      return

    case "limit":
      if len(os.Args) < 3 {
        log.Fatal("Usage: mpdgolinger limit N")
      }
      n, err := strconv.Atoi(os.Args[2])
      if err != nil || n <= 0 {
        log.Fatalf("Invalid limit: %q", os.Args[2])
      }
      sendIPCCommand(fmt.Sprintf("limit %d", n))
      return

    case "block", "blocklimit":
      if len(os.Args) >= 3 {
        n, err := strconv.Atoi(os.Args[2])
        if err != nil || n <= 0 {
          log.Fatalf("Invalid block size: %s", os.Args[2])
        }
        // Send IPC command to set running block limit
        if err := sendIPCCommand(fmt.Sprintf("setblock %d", n)); err != nil {
          log.Fatalf("IPC error: %v", err)
        }
        fmt.Println("OK")
        return
      }
      log.Fatalf("Usage: %s %s N", os.Args[0], os.Args[1])

    case "next":
      if err := sendIPCCommand("next"); err != nil {
        log.Fatalf("IPC error: %v", err)
      }
      fmt.Println("OK")
      return

    case "skip":
      if err := sendIPCCommand("skip"); err != nil {
        log.Fatalf("IPC error: %v", err)
      }
      fmt.Println("OK")
      return

    case "quit", "exit":
      if err := sendIPCCommand("quit"); err != nil {
        log.Fatalf("IPC error: %v", err)
      }
      fmt.Println("OK")
      return
    }
  }

  // ------------------------------------------------------------------
  // Normal daemon startup
  //
  // --daemon now gates startup
  // --mpdhost, --mpdport, --listen, --listenport are placeholders
  // ------------------------------------------------------------------

  // ------------------------------------------------------------------
  var startupLimit int
  var daemonMode bool
  var showVersion bool
  var showHelp bool
  // ------------------------------------------------------------------

  flag.BoolVar(&showVersion, "version", false, "Print version and exit")
  flag.BoolVar(&showHelp, "help", false, "Print help and exit")
  flag.IntVar(&startupLimit, "limit", 0, "Set initial persistent <limit>")
  flag.BoolVar(&daemonMode, "daemon", false, "Run as daemon")
  flag.StringVar(&mpdSocket, "mpdsocket", "", "MPD unix socket <path>")
  flag.StringVar(&mpdHost, "mpdhost", mpdHost, "MPD host <address>")
  flag.IntVar(&mpdPort, "mpdport", mpdPort, "MPD host <port>")

  // Placeholders for future flags
//  var mpdPort int
//  flag.StringVar(&mpdHost, "mpdhost", mpdHost, "MPD host")
//  flag.IntVar(&mpdPort, "mpdport", 6600, "MPD port")
  var listenIP string
  flag.StringVar(&listenIP, "listen", "", "Daemon listen IP for remote clients")
  var listenPort int
  flag.IntVar(&listenPort, "listenport", 0, "Daemon listen port for remote clients")

  flag.Func("state", "Write state file to <path>", func(v string) error {
    stateEnabled = true
    statePath = v
    return nil
  })

  flag.Parse()

  // ------------------------------------------------------------------
  // Version flag: print & exit
  // ------------------------------------------------------------------
//  if showVersion {
//    fmt.Println("mpdgolinger version", version)
//    return
//  }
	if showVersion {
    fmt.Println()
		fmt.Println(" mpdgolinger version:", version)

		fmt.Println("mpd protocol version:", mpdProtocolVersion())

		mpdPath := "/usr/bin/mpd"
		fmt.Println(mpdPath, "version:", mpdBinaryVersion(mpdPath))
    fmt.Println()
		return
	}
	if showHelp {
    fmt.Println()
		fmt.Println(" mpdgolinger version:", version)

		fmt.Println("mpd protocol version:", mpdProtocolVersion())

		mpdPath := "/usr/bin/mpd"
		fmt.Println(mpdPath, "version:", mpdBinaryVersion(mpdPath))
//    fmt.Printf("mpdgolinger %s\n", version)
    fmt.Println()
    fmt.Println("Usage: mpdgolinger --daemon [flags] or as client subcommands")
    flag.PrintDefaults()

		return
	}
  // ------------------------------------------------------------------
  // Enforce daemon mode (new behavior)
  // ------------------------------------------------------------------
  if !daemonMode {
    log.Fatalf("Refusing to start daemon without --daemon")
  }
  // ------------------------------------------------------------------

  if startupLimit > 0 {
    state.baseLimit = startupLimit
  }

  // Connect to MPD for initial state
//  client, err := mpd.Dial("tcp", mpdHost)
  client, err := dialMPD() //mpd.Dial("tcp", mpdHost)
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

  // Start IPC listener
  os.Remove(socketPath)
  go startIPC(socketPath)

  if stateEnabled {
    state.mu.Lock()
    writeStateLocked(state.lastSongID, state.baseLimit)
    state.mu.Unlock()
  }

  // Start idle supervisor
  go daemonSupervisor()

//  // Block forever
//  select {}

  // Block until shutdown received
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
