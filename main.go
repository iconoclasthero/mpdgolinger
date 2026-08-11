package main

import (
	// "flag"
	flag "github.com/spf13/pflag"
	"github.com/fhs/gompd/v2/mpd"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
	// "encoding/base64"
)
func daemonSupervisor() {
  var ipcRunning int32

  for {
    select {
    case <-shutdown:
      log.Println("[dS] Daemon supervisor shutting down")
      return
    default:
    }

    // Ensure IPC socket exists (daemon responsibility)
    if _, err := os.Stat(socketPath); err != nil {
      if os.IsNotExist(err) && atomic.LoadInt32(&ipcRunning) == 0 {
        log.Printf("[dS] IPC socket missing, recreating: %s", socketPath)
        os.Remove(socketPath)
        go func() {
          atomic.StoreInt32(&ipcRunning, 1)
          startIPC(socketPath)
          atomic.StoreInt32(&ipcRunning, 0)
        }()
      } else {
        log.Printf("[dS] IPC socket stat error: %v", err)
      }
    }


    log.Println("[dS] Connecting to MPD for idle loop...")

    var w *mpd.Watcher
    var err error
    if mpdSocket != "" {
      w, err = mpd.NewWatcher("unix", mpdSocket, "",
        "player", "options", "mixer", "playlist", "stored_playlist", "output")
    } else {
      w, err = mpd.NewWatcher("tcp", fmt.Sprintf("%s:%d", mpdHost, mpdPort), "",
        "player", "options", "mixer", "playlist", "stored_playlist", "output")
    }

    if err != nil {
      log.Printf("[dS] Watcher init failed: %v — retrying in 2s", err)
      time.Sleep(2 * time.Second)
      continue
    }

    if err := runIdleLoop(w); err != nil {
      log.Printf("[dS] Idle loop exited: %v — reconnecting in 2s", err)
    }
    w.Close()
    time.Sleep(2 * time.Second)
  }
} // func daemonSupervisor()


// runIdleLoop [rIL()] runs the MPD idle loop, updating state on song changes
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
//    configFlag   string  // this is shadowing the global needed elsewhere, remove. and rename global?
      startupLimit int
      daemonMode   bool
      showVersion  bool
      showHelp     bool
      socketFlag   string
      logPath      string
      WSport       int = 0
  )

  flag.StringVar(&daemonIP, "daemonip", "", "client: daemon IP to connect to")
  flag.IntVar(&daemonPort, "daemonport", 0, "client: daemon port to connect to")
  flag.StringVar(&execPost, "execpost", "", "post-execution action")
  flag.StringVar(&logPath, "log", "", "write logs to file instead of stderr")
  flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging")
  flag.StringVar(&configFlag, "config", "", "path to config file")
  flag.StringVar(&mpdLog, "mpdlog", "", "path to MPD log")
  flag.StringVar(&socketFlag, "socket", "", "mpdgolinger IPC socket path")
  flag.BoolVar(&showVersion, "version", false, "Print version and exit")
  flag.BoolVar(&showHelp, "help", false, "Print help and exit")
  flag.IntVar(&startupLimit, "limit", 0, "Set initial persistent <limit>")
  flag.BoolVar(&daemonMode, "daemon", false, "Run as daemon")
  flag.StringVar(&mpdSocket, "mpdsocket", "", "MPD unix socket <path>")
  flag.StringVar(&mpdHost, "mpdhost", mpdHost, "MPD host <address>")
  flag.IntVar(&mpdPort, "mpdport", mpdPort, "MPD host <port>")
  flag.StringVar(&mpdPass, "mpdpass", "", "MPD server password")
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

  // parse environmental variables MPD_HOST MPD_PORT
  if daemonMode {
    parseMPDEnv()
  }

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

  if v, ok := kv["state"]; ok && v != "" && statePath == "" {
    stateEnabled = true
    statePath = v
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

//  if mpdSocket == "" {
//    if v, ok := kv["mpdsocket"]; ok && v != "" {
//      mpdSocket = v
//    }
//  }
//
//  if mpdHost == "" {
//    if v, ok := kv["mpdhost"]; ok && v != "" {
//      mpdHost = v
//    }
//  }
//
//  if mpdPort == 0 {
//    if v, ok := kv["mpdport"]; ok && v != "" {
//      if n, err := strconv.Atoi(v); err == nil {
//        mpdPort = n
//      }
//    }
//  }
//
//  if mpdPass == "" {
//    if v, ok := kv["mpdpass"]; ok && v != "" {
//      mpdPass = v
//    }
//  }

  if mpdLog == "" {
    if v, ok := kv["mpdlog"]; ok && v != "" {
      mpdLog = v
      log.Printf("[main] mpdLog set to .conf mpdlog: %s\n", mpdLog)
    } else if env.mpdLog != "" {
      mpdLog = env.mpdLog
      log.Printf("[main] mpdLog set to env.mpdLog: %s\n", env.mpdLog)
    } else if defaultMPDlog != "" {
      mpdLog = defaultMPDlog
      log.Printf("[main] mpdLog set to defaultMPDlog: %s\n", defaultMPDlog)
    } else {
      log.Printf("[main] ERROR: mpdLog unset!\n")
    }
  } else {
    log.Printf("mpdLog set to --mpdlog: %s\n", mpdLog)
  }


  if mpdSocket == "" {
    if v, ok := kv["mpdsocket"]; ok && v != "" {
      mpdSocket = v
      log.Printf("[main] mpdSocket set to .conf mpdsocket: %s\n", mpdSocket)
    } else if env.mpdSocket != "" { // final fallback
      mpdSocket = env.mpdSocket
      log.Printf("[main] mpdSocket set to env.mpdSocket: %s\n", env.mpdSocket)
    } else if defaultMPDsocket != "" {
      mpdSocket = defaultMPDsocket
    }
  } else {
    log.Printf("[main] mpdSocket set to --mpdsocket: %s\n", mpdSocket)
  }

  if mpdHost == "" {
    if v, ok := kv["mpdhost"]; ok && v != "" {
      mpdHost = v
      log.Printf("[main] mpdHost set to .conf mpdhost: %s\n", mpdHost)
    } else if env.mpdHost != "" { // final fallback
      mpdHost = env.mpdHost
      log.Printf("[main] mpdHost set to env.mpdHost: %s\n", env.mpdHost)
    }
  } else {
    log.Printf("[main] mpdHost set to --mpdhost: %s\n", mpdHost)
  }

  if mpdPort == 0 {
    if v, ok := kv["mpdport"]; ok && v != "" {
      if n, err := strconv.Atoi(v); err == nil {
        mpdPort = n
        log.Printf("[main] mpdPort set to .conf mpdport: %d\n", mpdPort)
      }
    } else if env.mpdPort != 0 { // final fallback
      mpdPort = env.mpdPort
      log.Printf("[main] mpdPort set to env.mpdPort: %d\n", env.mpdPort)
    }
  } else {
    log.Printf("[main] mpdPort set to --mpdport: %d\n", mpdPort)
  }

  if mpdPass == "" {
    if v, ok := kv["mpdpass"]; ok && v != "" {
      mpdPass = v
    } else if env.mpdPass != "" { // final fallback
      mpdPass = env.mpdPass
      log.Printf("[main] mpdPass set to env.mpdPass: %s\n", env.mpdPass)
    }
  } else {
    log.Printf("[main] mpdPass set to --mpdpass: %s\n", mpdPass)
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

  // no flag implemented for skippedList; assignment defaults to ""
  // this sets up recording of skipped files in a designated mpd playlist.m3u
  // to allow for further processing
  if v, ok := kv["skiplist"]; ok && v != "" && skippedList == "" {
    skippedList = v
  }
  // fallback to default if still empty
  if skippedList == "" {
    skippedList = defaultSkippedList
  }

  // no flag implemented for ignoredList; assignment defaults to ""
  // this sets up recording of ignored files in a designated mpd playlist.m3u
  // to allow for further processing
  if v, ok := kv["ignorelist"]; ok && v != "" && ignoredList == "" {
    ignoredList = v
  }
  // fallback to default if still empty
  if ignoredList == "" {
    ignoredList = defaultIgnoredList
  }

  if v, ok := kv["log"]; ok && v != "" && logPath == "" {
    logPath = v
  }

  // no flag implemented for mpdpath; will default to /usr/bin/mpd if unset
  // `mpdpath=none` in daemon .conf disables mpd binary checking
  mpdPath = kv["mpdpath"]
  if mpdPath == "" {
    mpdPath = defaultMPDpath
  } else if mpdPath == "none" {
    mpdPath = ""
  }

  // ------------------------------------------------------------------
  // OPTIONAL: redirect logs if --log is set
  // MUST be before any log.Printf / log.Fatalf
  // ------------------------------------------------------------------
  if logPath != "" {
    f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
    if err != nil {
      log.Fatalf("[main] Failed to open log file %s: %v", logPath, err)
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
    log.Fatalf("[main] Refusing to start daemon without --daemon")
  }

  // ------------------------------------------------------------------
  // Daemon startup continues unchanged
  // ------------------------------------------------------------------
  if startupLimit > 0 {
    state.baseLimit = startupLimit
  }

  client, err := dialMPD()
  if err != nil {
    log.Printf("[main] Failed to connect to MPD at startup: %v", err)
    state.count = 0
    state.lastSongID = 0
    state.lastSongZI = 0
    state.lastSongURI = ""
  } else {
    status, err := client.Status()
    current, err := client.CurrentSong()
    if err != nil {
      log.Printf("[main] Status error at startup: %v", err)
      state.count = 0
      state.lastSongID = 0
      state.lastSongZI = 0
      state.lastSongURI = ""
    } else {
      state.lastSongID, _ = strconv.Atoi(status["songid"])
      log.Printf("[main] ==== setting state.lastSongID = strconf.Atoi(status[\"songid\"]): %d", state.lastSongID)
      state.lastSongZI, _ = strconv.Atoi(status["song"])  // Zero-Indezed song playlist position
      state.lastSongURI, _ = current["file"]
      switch status["state"] {                            // This doesn't look right: why is count being asigned this way?
        case "pause", "stop":                             // Especially when paused??
          state.count = 0
          state.MPDplaying = false
        case "play":
          state.MPDplaying = true
          state.count = 1
        default:
          log.Printf("[main] MPD status 'state' is neither 'play', 'pause', nor 'stop': %s!", status["state"])
          state.MPDplaying = false
          state.count = 0
      }
    }
    client.Close()
  }

  log.Printf(
      "[main] Starting block count=%d, lastSongZI=%d; lastSongID=%d, baseLimit=%d, blockLimit=%d",
      state.count,
      state.lastSongZI,
      state.lastSongID,
      state.baseLimit,
      state.blockLimit,
  )
  setRandom(false, "startup")

  os.Remove(socketPath)
  go startIPC(socketPath)

  if stateEnabled {
    state.mu.Lock()
    deriveStateLocked(state.lastSongZI, state.lastSongID, state.lastSongURI, state.baseLimit)
    state.mu.Unlock()
  }

  // ------------------------------------------------------------------
  // Start daemon WS listener for remote clients
  // ------------------------------------------------------------------
   if WSport <= 0 {
     WSport = defWSport
   }
   startWS(WSport)

  // ------------------------------------------------------------------
  // Start daemon TCP listener for remote clients
  // ------------------------------------------------------------------
  log.Printf("[main] About to listen on %s:%d", listenIP, listenPort)

  addr := fmt.Sprintf("%s:%d", listenIP, listenPort)
  ln, err := net.Listen("tcp", addr)
  if err != nil {
    log.Fatalf("[main] Failed to listen on %s: %v", addr, err)
  }
  log.Printf("[main] Listening for clients on %s", addr)

  // Spawn a goroutine to process idleEvents channel
  go eventProcessor()

  // Spawn a goroutine to accept clients
  go acceptClients(ln)

  pulseChan := make(chan PulseV1, 1)

  ctx, cancel := context.WithCancel(context.Background())
  defer cancel()

  go pulseWatcher(ctx, pulseChan)

  go func() {
    for p := range pulseChan {
      pulseMu.Lock()
      PulseData = p
      pulseMu.Unlock()
    }
  }()

  if p, err := refreshPulse(); err == nil {
    log.Printf("[main] pulse initial: %+v", p)
    pulseMu.Lock()
    PulseData = p
    pulseMu.Unlock()
  } else {
    log.Printf("[main] pulse initial ERROR: %v", err)
  }

  go daemonSupervisor()

  initShutdownHandler()

  select {
  case <-shutdown:
    log.Println("[main] Shutdown requested")
    exitCode := 0

    if err := os.Remove(socketPath); err != nil {
      log.Printf("[main] Failed to remove socket: %v\n", err)
      exitCode = 1
    } else {
      log.Printf("[main] Socket removed")
    }

    if stateEnabled {
      if err := os.Remove(statePath); err != nil {
        log.Printf("[main] Failed to remove state file: %v\n", err)
        exitCode = 1
      } else {
        log.Printf("[main] State file removed")
      }
    }

    log.Println("[main] Cleanup steps completed, exiting")
    os.Exit(exitCode)
  }
} // func main()
// End of mpdgolinger


