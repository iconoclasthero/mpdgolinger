
var (
  defaultLog       = "/var/log/mpd/mpd.log"
  defaultLibPrefix = "/library/music"
  debug     bool   = false // manual toggle
  tryParsed bool
  rawTags   bool
)

var (
  reAlbumDir  = regexp.MustCompile(`([^/]+) -- ([^/]+) \(\d{4}\)$`)
  reAlbumFile = regexp.MustCompile(`^([^/]+) -- \d{2}-\d{2} - (.+)\.[^.]+$`)
  reFlatFile  = regexp.MustCompile(`^\d{2}-\d{2} - ([^/]+) -- (.+)\.[^.]+$`)
)


/* ---------------- utils ---------------- */

func dbg(f string, a ...any) {
  if debug {
    fmt.Fprintf(os.Stderr, "DEBUG: "+f+"\n", a...)
  }
} // cnuf dbg

func emitError(msg string) {
  fmt.Printf("error=%q\n", msg)
} // cnuf emitError


func printSong(song map[string]string) {
  for k, v := range song {
    outk := k
    if !rawTags {
      outk = strings.ToLower(k)
      if outk == "last-modified" {
        outk = "lastmodified"
      }
    }
    fmt.Printf("%s=%q\n", outk, v)
  }
} // cnuf printSong

/* ---------------- mpd connect ---------------- */

func parseMPDHostEnv() (host, socket, pass string) {
  v := os.Getenv("MPD_HOST")
  if v == "" {
    return
  }
  if at := strings.IndexByte(v, '@'); at >= 0 {
    pass = v[:at]
    v = v[at+1:]
  }
  if strings.Contains(v, "/") {
    socket = v
  } else {
    host = v
  }
  return
} // cnuf parseMPDHostEnv()

func defaultMPDSocket() string {
  if v := os.Getenv("MPD_SOCKET"); v != "" {
    return v
  }
  if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
    p := xdg + "/mpd/socket"
    if _, err := os.Stat(p); err == nil {
      return p
    }
  }
  if _, err := os.Stat("/run/mpd/socket"); err == nil {
    return "/run/mpd/socket"
  }
  if home := os.Getenv("HOME"); home != "" {
    p := home + "/.mpd/socket"
    if _, err := os.Stat(p); err == nil {
      return p
    }
  }
  return ""
} // cnuf defaultMPDSocket


/* ---------------- tryparsed ---------------- */

func tryParsedLookup(c *mpd.Client, path string) (map[string]string, error) {
  dir  := filepath.Base(filepath.Dir(path))
  base := filepath.Base(path)

  var album  string
  var artist string
  var track  string

  // Case 1: album directory + album-style filename
  if dm := reAlbumDir.FindStringSubmatch(dir); dm != nil {
    album = dm[2] // album only; artist is ignored here by design

    if fm := reAlbumFile.FindStringSubmatch(base); fm != nil {
      artist = fm[1]
      track  = fm[2]
    }
  }

  // Case 2: flat filename (no album info available)
  if artist == "" || track == "" {
    if fm := reFlatFile.FindStringSubmatch(base); fm != nil {
      artist = fm[1]
      track  = fm[2]
      album  = ""
    }
  }

  if artist == "" || track == "" {
    return nil, fmt.Errorf("filename not parseable")
  }

  dbg("tryparsed album=%q artist=%q track=%q", album, artist, track)

  var res []mpd.Attrs
  var err error

  // Prefer album-qualified search when available
  if album != "" {
    res, err = c.Search(
      "album",  album,
      "artist", artist,
      "title",  track,
    )
    if err == nil && len(res) > 0 {
      return map[string]string(res[0]), fmt.Errorf("recovered via tryparsed")
    }
  }

  // Fallback: artist + title only
  res, err = c.Search(
    "artist", artist,
    "title",  track,
  )
  if err != nil || len(res) == 0 {
    return nil, fmt.Errorf("tryparsed search failed")
  }

  return map[string]string(res[0]), fmt.Errorf("recovered via tryparsed")
}


/* ---------------- main ---------------- */

func main() {
  var (
    host      string
    socket    string
    port      int

    current   bool
    next      bool
    last      bool
    lastLog   string
    useSocket bool
  )

	out := os.Args[:1] // keep argv[0]

	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]

		switch {
		case arg == "--verbose":
			debug = true
			dbg("Turning verbose debug mode on")

		case arg == "--last":
			last = true
			lastLog = defaultLog
			dbg("last=true log=%q", lastLog)

		case strings.HasPrefix(arg, "--last="):
			last = true
			lastLog = strings.TrimPrefix(arg, "--last=")
			dbg("last=true log=%q", lastLog)

		case arg == "--socket":
			useSocket = true
			socket = "" // default socket
			dbg("useSocket=true (bare --socket)")

		case strings.HasPrefix(arg, "--socket="):
			useSocket = true
			socket = strings.TrimPrefix(arg, "--socket=")
			dbg("useSocket=true socket=%q", socket)

		default:
			out = append(out, arg) // keep unhandled args
		}
	}

	os.Args = out


  flag.StringVar(&host, "host", "", "MPD host")
  flag.IntVar(&port, "port", 6600, "MPD port")
  flag.BoolVar(&current, "current", false, "current song")
  flag.BoolVar(&next, "next", false, "next song")
  flag.BoolVar(&tryParsed, "tryparsed", false, "try filename parsing fallback")
  flag.Parse()

  // ---------------------- socket/host selection ----------------------
  dbg("CLI socket=%q, env MPD_HOST=%q", socket, os.Getenv("MPD_HOST"))

//  hostFromEnv, socketFromEnv, passFromEnv := parseMPDHostEnv()
  hostFromEnv, socketFromEnv, _ := parseMPDHostEnv()

  if socket == "" { // CLI didn't override
    if socketFromEnv != "" {
      socket = socketFromEnv
      dbg("using socket from MPD_HOST env: %q", socket)
    } else {
      socket = defaultMPDSocket()
      dbg("using default socket: %q", socket)
    }
  } else {
    dbg("using CLI socket: %q", socket)
  }

  if host == "" {
    host = hostFromEnv
    if host != "" {
      dbg("using host from MPD_HOST env: %q", host)
    }
  }

  // Now dialMPD just uses whatever host/port/socket we have
  dbg("dialMPD(%q, %d, %q, %v)", host, port, socket, useSocket)
  c, err := dialMPD(host, port, socket, useSocket)
  if err != nil {
    emitError(err.Error())
    os.Exit(1)
  }
  defer c.Close()

  /* ---------- path mode ---------- */

  if !current && !next && !last {
    args := flag.Args()
    if len(args) == 0 {
      return
    }

    path := args[0]

    // Determine if path is absolute
    if filepath.IsAbs(path) {
      // Try absolute path first (requires socket)
      dbg("if filepath.IsABS(path) ListAllInfo called with path=%q", path)
      songs, err := c.ListInfo(path)
      if err != nil || len(songs) == 0 || songs[0]["file"] == "" {
        emitError("absolute path not resolvable, ensure socket access")
        os.Exit(1)
      }
      printSong(map[string]string(songs[0]))
      return
    }

    // Otherwise relative path
    dbg("Otherwise relative path ListAllInfo called with path=%q", path)
    songs, err := c.ListAllInfo(path)
    if err != nil || len(songs) == 0 || songs[0]["file"] == "" {
      // Optional: commented fallback to prepend defaultLibPrefix
      // abs := defaultLibPrefix + "/" + path
      // songs, err = c.ListAllInfo(abs)

      if tryParsed {
        ps, perr := tryParsedLookup(c, path)
        if ps != nil {
          emitError(perr.Error())
          printSong(ps)
          return
        }
      }
      emitError("file not found")
      os.Exit(1)
    }

    printSong(map[string]string(songs[0]))

    return
  }

  /* ---------- --current ---------- */

  if current {
    s, err := c.CurrentSong()
    if err != nil || s == nil {
      emitError("no current song")
      return
    }
    printSong(s)
    return
  }

  /* ---------- --next ---------- */

  if next {
    st, err := c.Status()
    if err != nil {
      emitError(err.Error())
      return
    }
    i, _ := strconv.Atoi(st["nextsong"])
    s, err := c.PlaylistInfo(i, -1)
    if err != nil || len(s) == 0 {
      emitError("no next song")
      return
    }
    printSong(map[string]string(s[0]))
    return
  }

  /* ---------- --last ---------- */

  if last {
    dbg("in `if last` lastLog = %s", lastLog)
    _, _, path, err := findLastPlayed(lastLog)
    if err != nil {
      emitError(err.Error())
      os.Exit(1)
    }

    dbg("if last relative ListAllInfo called with path=%s", path)
    s, err := c.ListAllInfo(path)
    if err == nil && len(s) > 0 && s[0]["file"] != "" {
      printSong(map[string]string(s[0]))
      return
    }

    abs := defaultLibPrefix + "/" + path
    dbg("if last relative ListAllInfo called with path=%s", path)
    s, err = c.ListInfo(abs)
    if err == nil && len(s) > 0 && s[0]["file"] != "" {
      printSong(map[string]string(s[0]))
      return
    }

    if tryParsed {
      ps, perr := tryParsedLookup(c, path)
      if ps != nil {
        emitError(perr.Error())
        printSong(ps)
        return
      }
    }

    emitError("unable to resolve last track")
    os.Exit(1)
  }
} // cnuf main()
