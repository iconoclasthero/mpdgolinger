package main


import (
//  "flag"
   flag "github.com/spf13/pflag"
  "github.com/fhs/gompd/v2/mpd"
  "github.com/coder/websocket"
  "github.com/mitchellh/mapstructure"
  "context"
  "regexp"
  "bufio"
  "bytes"
  "fmt"
  "log"
  "net"
  "net/http"
  "io"
  "os"
  "os/signal"
  "os/exec"
  "syscall"
  "path"
  "path/filepath"
  "strconv"
  "strings"
  "sync"
  "time"
  "sync/atomic"
  "math"
  "math/rand"
  "encoding/json"
  "encoding/base64"
  "net/url"
  "sort"
)

//const version = "0.03.0"

// State holds daemon state
type       State      struct {
     mu               sync.Mutex
     paused           bool   // mpdlinger paused?
     MPDplaying       bool   // mpd playing?
     count            int    // current position in block
     blockLimit       int    // temporary override (later)
     transition       bool   // true between last-song-start and next-song-start
     lastSongID       string
     lastSongZI       int
     baseLimit        int
     blockOn          bool
     consume          bool
     lastAlbumKey     string
     timer            TimerV1
} // type State struct

type   derivedState   struct {
     WriteTime        string
     SongZI           int
     SongID           string
     Playing          int          // mpd state
     Paused           int          // mpdlingerstate
     Count            int
     BaseLimit        int
     Limit            int
     BlockLimit       int
     PID              int
     LingerXY         int
     LingerX          int
     LingerY          int
} // type derivedState struct

type      xyState     struct {
     active           bool
     start            int // songpos X
     end              int // songpos Y
}

type      mpdEnv      struct {
     mpdHost          string
     mpdPort          int
     mpdSocket        string
     mpdPass          string
     mpdLog           string
}

type    configFile    struct {
     path             string
     data             string
     exists           bool
} // type configFile struct

type     Condition    struct {
     Field            string
     Op               string
     Value            string
     Unary            bool
     Not              string
}

type LogLine          struct {
     Timestamp        string // exactly as in mpd.log
     Action           string // played / skipped / ignored
     Path             string // decoded filesystem path
     Notes            string
}

type      TimerV1     struct {
     Active           bool      `json:"active"`    // true if running
     Duration         int       `json:"duration"`  // original set duration in seconds
     EndTime          time.Time `json:"-"`         // internal only
     Remaining        int       `json:"remaining"` // computed for StatusV1
}

type      AudioV1     struct {
     Title            string    `json:"title"`
     Artist           string    `json:"artist"`
     AlbumArtist      string    `json:"albumartist"`
     Album            string    `json:"album"`
     Year             string    `json:"year"`
     Duration         float64   `json:"duration"`
     Time             int       `json:"time"`
     Disc             int       `json:"disc"`
     Track            int       `json:"track"`
     MBAlbumID        string    `json:"musicbrainz_albumid"`
     MBTrackID        string    `json:"musicbrainz_trackid"`
     MBReleaseTrackID string    `json:"musicbrainz_releasetrackid"`
     MBArtistID       string    `json:"musicbrainz_artistid"`
     MBAlbumArtistID  string    `json:"musicbrainz_albumartistid"`
     MBReleaseGrpID   string    `json:"musicbrainz_releasegroupid"`
     File             string    `json:"file"`
     SongPosition     int       `json:"song_position"`
     SongID           int       `json:"songID"`
}

type     PlayerV1     struct {
     State            string    `json:"state"`
     Volume           int       `json:"volume"`
     Elapsed          float64   `json:"elapsed"`
     Duration         float64   `json:"duration"`
     Percent          float64   `json:"percent"`
     Random           bool      `json:"random"`
     Consume          bool      `json:"consume"`
     Repeat           bool      `json:"repeat"`
     Single           bool      `json:"single"`
     SongPosition     int       `json:"song_position"`
     SongID           int       `json:"songID"`
     SongLength       int       `json:"song_length"`
     PlaylistRev      int       `json:"playlist_rev"`
}

type    LingerV1      struct {
     Song             int       `json:"song"`
     SongID           int       `json:"songid"`
     Count            int       `json:"count"`
     BaseLimit        int       `json:"baselimit"`
     Limit            int       `json:"limit"`
     BlockLimit       int       `json:"blocklimit"`
     Paused           bool      `json:"paused"`
     LingerXY         bool      `json:"lingerxy"`
     LingerX          int       `json:"lingerx"`
     LingerY					int			  `json:"lingery"`
}

type    TimestampV1   struct {
     Epoch            int64     `json:"epoch"`
     Log              string    `json:"log"`
     Display          string    `json:"display"`
}

type    LogEntryV1    struct {
     Timestamps     TimestampV1 `json:"timestamps"`
     Action           string    `json:"action"`
     Notes            string    `json:"notes,omitempty"`
     File             string    `json:"file"`
     URL              string    `json:"url"`
     Audio            AudioV1   `json:"audio"`
}

type     StatusV1     struct {
     Player           PlayerV1  `json:"player"`
     Current          AudioV1   `json:"current"`
     Next             AudioV1   `json:"next"`
     Linger           LingerV1  `json:"linger"`
     Timer            TimerV1   `json:"timer"`
}


const (
  defaultSkippedList = ".mpdskip"
  defaultMPDhost     = "localhost"
  defaultMPDport     = 6600
  defaultMPDsocket   = "/run/mpd/socket"
  defaultMPDpath     = "/usr/bin/mpd"
  defaultState       = "/var/lib/mpd/mpdlinger/mpdgolinger.state"
  defaultListenIP    = "0.0.0.0"
  defaultListenPort  = 6599
  defaultSocketPath  = "/var/lib/mpd/mpdlinger/mpdgolinger.sock"
  defaultMPDlog      = "/var/log/mpd/mpd.log"
  defaultDaemonIP    = "localhost"
  defaultDaemonPort  = 6559
  defaultIgnoredList = ".mpdignore"
  defaultMPDpassword = ""
) // const


var (
  xy xyState
  version = "dev"
  // Core daemon state
  state = &State{
  baseLimit: defaultLimit,
  }

  env mpdEnv

  defaultLimit = 4

  shutdown = make(chan struct{})
  shutdownOnce sync.Once

  // State file management
  statePath string
  stateEnabled bool

  // MPD connection parameteres
  mpdHost     string = ""
  mpdPort     int    = 0
  mpdPass     string = ""
  mpdSocket   string = ""
  mpdLog      string = ""
  mpdPath     string = ""
  mpdPassword string = defaultMPDpassword

   mpdMapPool = sync.Pool{
    New: func() any {
      return make(map[string]string, 32)
    },
  }

  skippedList string = ""  // no flag implemented
  ignoredList string = defaultIgnoredList


  // IPC socket
  socketPath = defaultSocketPath

  // TCP listener for TCP client connections
  listenIP   string
  listenPort int

  defWSport int = 8008


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


type       wsCtx      struct {
     conn             *websocket.Conn
     mu               sync.Mutex
     conns            map[*websocket.Conn]struct{}
     watcherRunning   bool // <- add this
     writeMu          sync.Mutex
}

type      Request     struct {
     System string          `json:"system"`
     Cmd    string          `json:"cmd"`
     Args   json.RawMessage `json:"args"`
}

type     IdleEvent    struct {
     Subsystem        string
     SongID           string
     Status           map[string]string
     PlaylistRev      int
}

var idleEvents = make(chan IdleEvent, 32)

var wsGlobal = &wsCtx{
  conns: make(map[*websocket.Conn]struct{}),
}





/* ---------------- mpdtags -------------- */
var (
  defaultLibPrefix  = "/library/music"
  musicLibPrefix    = defaultLibPrefix
  debug     bool    = false // manual toggle
  tryParsed bool    = true
  rawTags   bool
)

var (
  reAlbumDir  = regexp.MustCompile(`([^/]+) -- ([^/]+) \(\d{4}\)$`)
  reAlbumFile = regexp.MustCompile(`^([^/]+) -- \d{2}-\d{2} - (.+)\.[^.]+$`)
  reFlatFile  = regexp.MustCompile(`^\d{2}-\d{2} - ([^/]+) -- (.+)\.[^.]+$`)
)

//
//func testMPDtags() {
//  debug = true
//
//  // connect to MPD (socket only, ignore host/port)
//  c, err := dialMPD()
//  if err != nil {
//    fmt.Printf("MPD connect failed: %v\n", err)
//    return
//  }
//  defer c.Close()
//
//  // call MPDtags
////  tags, notes, err := MPDtags(c, "status", "status")
//    tags, notes, err := MPDtags(c, "Grateful Dead/Grateful Dead -- Without a Net (1990)/Grateful Dead -- 01-01 - Feel Like a Stranger.flac", "played")
//  if err != nil {
//    fmt.Printf("MPDtags error: %v\n", err)
//  }
//
//  fmt.Println("=== TAGS ===")
//  for k, v := range tags {
//    fmt.Printf("%s=%s\n", k, v)
//  }
//
//  fmt.Println("=== NOTES ===")
//  for _, n := range notes {
//    fmt.Println(n)
//  }
//
//  // ---- NEW PART ----
//
//  js, err := convert2json(tags)
//  if err != nil {
//    fmt.Printf("convert2json error: %v\n", err)
//    return
//  }
//
//  b, err := json.MarshalIndent(js, "", "  ")
//  if err != nil {
//    fmt.Printf("json marshal error: %v\n", err)
//    return
//  }
//
//  fmt.Println("=== JSON ===")
//  fmt.Println(string(b))
//  fmt.Println("=== END TEST ===")
//} // func testMPDtags


func audioFromRaw(raw map[string]string, p string) AudioV1 {
  var src string
//  var dur float64
  yearRE := regexp.MustCompile(`\d{4}`)
  if raw[p+"originaldate"] != "" {
    src = raw["originaldate"]
  } else {
    src = raw["date"]
  }

  year := yearRE.FindString(src)
  if year == "" {
    year = src
  }

  dur, _ := strconv.ParseFloat(raw[p+"duration"], 64)

  return AudioV1{
    Title:              raw[p+"title"],
    Artist:             raw[p+"artist"],
    AlbumArtist:        raw[p+"albumartist"],
    Album:              raw[p+"album"],
    Year:               year,
//    Duration:           raw[p+"duration"],
    Duration:           dur,
    Time:               atoi(raw[p+"time"]),
    Disc:               atoi(raw[p+"disc"]),
    Track:              atoi(raw[p+"track"]),
    MBAlbumID:          raw[p+"musicbrainz_albumid"],
    MBTrackID:          raw[p+"musicbrainz_trackid"],
    MBReleaseTrackID:   raw[p+"musicbrainz_releasetrackid"],
    MBArtistID:         raw[p+"musicbrainz_artistid"],
    MBAlbumArtistID:    raw[p+"musicbrainz_albumartistid"],
    MBReleaseGrpID:     raw[p+"musicbrainz_releasegroupid"],
    File:               raw[p+"file"],
    SongPosition:       atoi(raw[p+"pos"]) + 1,
    SongID:             atoi(raw[p+"id"]),
  }
} // func audioFromRaw()


func convert2json(raw map[string]string, out interface{}, extra ...interface{}) error {
  switch dst := out.(type) {

  // ---------------- StatusV1 ----------------
  case *StatusV1:
    // --- player ---
    dst.Player.State        = raw["state"]
    dst.Player.Volume       = atoi(raw["volume"])
    dst.Player.SongID       = atoi(raw["songid"])
    songZI                 := atoi(raw["song"])
    dst.Player.SongPosition = songZI + 1
    dst.Player.SongLength   = atoi(raw["playlistlength"])
    dst.Player.PlaylistRev  = atoi(raw["playlist"])
    elapsed, _             := strconv.ParseFloat(raw["elapsed"], 64)
    duration, _            := strconv.ParseFloat(raw["duration"], 64)
    dst.Player.Elapsed      = elapsed
    dst.Player.Duration     = duration
    if duration > 0 {
      dst.Player.Percent    = (elapsed * 100) / duration
    }
    dst.Player.Random       = raw["random"] == "1"
    dst.Player.Consume      = raw["consume"] == "1"
    dst.Player.Repeat       = raw["repeat"] == "1"
    dst.Player.Single       = raw["single"] == "1"

    // --- current song ---
    dst.Current             = audioFromRaw(raw, "")

    // --- next song ---
    dst.Next                = audioFromRaw(raw, "next_")

    // --- linger ---
    dst.Linger.Song         = atoi(raw["lingersong"])
    dst.Linger.SongID       = atoi(raw["lingersongid"])
    dst.Linger.Count        = atoi(raw["lingercount"])
    dst.Linger.BaseLimit    = atoi(raw["lingerbase"])
    dst.Linger.Limit        = atoi(raw["lingerlimit"])
    dst.Linger.BlockLimit   = atoi(raw["lingerblocklmt"])
    dst.Linger.Paused       = raw["lingerpause"] == "1"
    dst.Linger.LingerXY     = raw["lingerxy"] == "1"
    dst.Linger.LingerX      = atoi(raw["lingerx"])
    dst.Linger.LingerY      = atoi(raw["lingery"])

    // --- timer ---
    state.mu.Lock()
    dst.Timer = TimerV1{
      Active:    state.timer.Active,
      Duration:  state.timer.Duration,
      Remaining: int(math.Max(0, time.Until(state.timer.EndTime).Seconds())),
    }
    state.mu.Unlock()

    return nil

  // ---------------- LogEntryV1 ----------------
  case *LogEntryV1:
    if len(extra) < 4 {
      return fmt.Errorf("convert2json: not enough extra arguments for LogEntryV1")
    }
    tsRaw, ok1             := extra[0].(string)
    action, ok2            := extra[1].(string)
    notesSlice, ok3        := extra[2].([]string)
    filePath, ok4          := extra[3].(string)
    if !ok1 || !ok2 || !ok3 || !ok4 {
      return fmt.Errorf("convert2json: invalid extra argument types for LogEntryV1")
    }

    // --- timestamps ---
    epoch                  := isoLocalEpoch(tsRaw)
    dst.Timestamps          = TimestampV1{
      Epoch:                  epoch,
      Log:                    tsRaw,
      Display:                formatDisplayTime(tsRaw),
    }

    // --- action/notes/file/url ---
    dst.Action              = action
    dst.Notes               = strings.Join(notesSlice, "; ")
    dst.File                = filePath
    dst.URL                 = url.PathEscape(filePath)

    // --- audio ---
    dst.Audio               = audioFromRaw(raw, "")
    dst.Audio.Duration, _   = strconv.ParseFloat(raw["duration"], 64)
    dst.Audio.Time          = atoi(raw["time"])

    return nil

  case *AudioV1:
    *dst = audioFromRaw(raw, "")
    return nil

  default:
    return fmt.Errorf("convert2json: unsupported output type %T", out)
  }
} // func convert2json


func mpdPlaylist(albumKey string) ([]AudioV1, error) {

	// ---- connect ----
	mpdSocket = os.Getenv("MPD_SOCK")
	if mpdSocket == "" {
		mpdSocket = "/run/mpd/socket"
	}

	conn, err := net.Dial("unix", mpdSocket)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	// consume greeting
	if _, err := reader.ReadString('\n'); err != nil {
		return nil, err
	}

	// ---- local quote closure ----
	quote := func(s string) string {
		s = strings.ReplaceAll(s, `"`, `\"`)
		return `"` + s + `"`
	}

	uuidRe := regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)
	pathRe := regexp.MustCompile(`^(.+/)+[^/]+\.[a-zA-Z0-9]{3,4}$`)
  aaRe := regexp.MustCompile(`^.+ -- .+$`)

	var cmd string

  log.Printf("[mpdPlaylist] albumKey=%s", albumKey)

	switch {

	// MUSICBRAINZ_ALBUMID
	case uuidRe.MatchString(albumKey):
		cmd = fmt.Sprintf(
			"playlistsearch MUSICBRAINZ_ALBUMID %s\n",
			quote(albumKey),
		)

	// file starts_with
	case pathRe.MatchString(albumKey):
		albumKey = filepath.Dir(albumKey)
		cmd = fmt.Sprintf(
			"playlistsearch \"(base \\\"%s\\\")\"\n",
			albumKey,
		)

  // match AlbumArtist -- Album
  case aaRe.MatchString(albumKey):
    parts := strings.SplitN(albumKey, " -- ", 2)
    if len(parts) != 2 {
      return nil, fmt.Errorf("cannot parse albumKey: %q", albumKey)
    }

    albumArtist := strings.TrimSpace(parts[0])
    album := strings.TrimSpace(parts[1])
    cmd = fmt.Sprintf(
      "playlistsearch \"((albumArtist == \\\"%s\\\") AND (album == \\\"%s\\\"))\"\n",
      albumArtist,
      album,
    )

  case strings.HasPrefix(albumKey, "search"):
    dbg("case strings.HasPrefix(%s, \"search\")", albumKey)
    searchExp := strings.TrimSpace(strings.TrimPrefix(albumKey, "search "))
    dbg("searchExp = \"%s\"", searchExp)
    part := strings.SplitN(searchExp, " ", 3)

    part[0] = strings.TrimSpace(part[0])
    part[1] = strings.TrimSpace(part[1])
    part[2] = strings.TrimSpace(part[2])
    cmd = fmt.Sprintf("playlistsearch \"(%s %s \\\"%s\\\")\"\n", part[0], part[1], part[2])

	// raw expression
	default:
		cmd = fmt.Sprintf(
			"playlistsearch \"(any == \\\"%s\\\")\"\n", albumKey)
	}

  log.Printf("[mpdPlaylist] cmd=%s", cmd)

	// send
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return nil, err
	}

	var results []AudioV1
	var current map[string]string

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}

		line = strings.TrimSpace(line)

		if line == "OK" {
			break
		}
		if strings.HasPrefix(line, "ACK") {
			return nil, fmt.Errorf("MPD error: %s", line)
		}
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "file: ") {
			if current != nil {
				var a AudioV1
				if err := convert2json(current, &a); err == nil {
					results = append(results, a)
				}
			}
			current = make(map[string]string)
		}

		parts := strings.SplitN(line, ": ", 2)
		if len(parts) == 2 {
			if current == nil {
				current = make(map[string]string)
			}
			current[strings.ToLower(parts[0])] = parts[1]
		}
	}

	if current != nil {
		var a AudioV1
		if err := convert2json(current, &a); err == nil {
			results = append(results, a)
		}
	}

	return results, nil
} // func mpdPlaylist


/* ---------------- utils ---------------- */

func isoLocalEpoch(iso string) int64 {
    // Parse ISO 8601 string
    t, err := time.Parse("2006-01-02T15:04:05", iso)
    if err != nil {
        return 0
    }
    // Convert to local epoch
    return t.Unix()
} // func isoLocalEpoch

func formatDisplayTime(iso string) string {
    t, err := time.Parse("2006-01-02T15:04:05", iso)
    if err != nil {
        return iso
    }
    return t.Format("Jan 02 3:04 pm")
} // func formatDisplayTime

func atoi(s string) int {
  i, _ := strconv.Atoi(s)
  return i
} // func atoi()

func dbg(f string, a ...any) {
  if debug {
    fmt.Fprintf(os.Stderr, "DEBUG: "+f+"\n", a...)
  }
} // func dbg()

func emitError(msg string) {
  fmt.Printf("error=%q\n", msg)
} // func emitError()


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
} // func printSong()


//
//func mpdSearch(conn net.Conn, cmd string, conds []Condition) ([]map[string]string, error) {
//  /* --- formerly quoteSearchArg --- */
//  quoteSearchArg := func(conds []Condition) string {
//    /* --- formerly escapeMPDFilterValue --- */
//    escapeMPDFilterValue := func(s string) string {
//      var b strings.Builder
//      b.Grow(len(s) * 2)
//
//      for i := 0; i < len(s); i++ {
//        c := s[i]
//        switch c {
//        case '\\', '"', '\'':
//          b.WriteByte('\\')
//        }
//        b.WriteByte(c)
//      }
//      return b.String()
//    }
//
//    /* --- formerly quoteMPDArg --- */
//    quoteMPDArg := func(s string) string {
//      var b strings.Builder
//      b.Grow(len(s)*2 + 2)
//
//      b.WriteByte('"')
//      for i := 0; i < len(s); i++ {
//        c := s[i]
//        if c == '"' || c == '\\' {
//          b.WriteByte('\\')
//        }
//        b.WriteByte(c)
//      }
//      b.WriteByte('"')
//
//      return b.String()
//    }
//
//    /* --- builder logic --- */
//    parts := make([]string, 0, len(conds))
//
//    for _, c := range conds {
//      escaped := escapeMPDFilterValue(c.Value)
//
//      part := fmt.Sprintf(
//        "(%s %s \"%s\")",
//        c.Field,
//        c.Op,
//        escaped,
//      )
//
//      parts = append(parts, part)
//    }
//
//    filter := "(" + strings.Join(parts, " AND ") + ")"
//
//    return quoteMPDArg(filter)
//  }
//
//  searchArg := quoteSearchArg(conds)
//  searchString := cmd + " " + searchArg + "\n"
//
//  dbg("Sending command: %s", searchString)
//
//  if _, err := conn.Write([]byte(searchString)); err != nil {
//    return nil, err
//  }
//
//  reader := bufio.NewReader(conn)
//
//  results := make([]map[string]string, 0, 64)
//  var current map[string]string
//
//  for {
//    line, err := reader.ReadString('\n')
//    if err != nil {
//      return nil, err
//    }
//
//    line = strings.TrimSpace(line)
//
//    if line == "OK" {
//      break
//    }
//
//    if strings.HasPrefix(line, "ACK") {
//      return nil, fmt.Errorf("MPD error: %s", line)
//    }
//
//    if line == "" {
//      continue
//    }
//
//    /* --- record boundary --- */
//    if strings.HasPrefix(line, "file: ") {
//
//      if current != nil {
//        results = append(results, current)
//      }
//
//      current = make(map[string]string, 16)
//      current["file"] = line[6:] // faster than TrimPrefix
//
//      continue
//    }
//
//    /* --- key/value parsing without SplitN allocation --- */
//    idx := strings.Index(line, ": ")
//    if idx < 0 {
//      continue
//    }
//
//    if current == nil {
//      // ignore metadata before first file
//      continue
//    }
//
//    key := strings.ToLower(line[:idx])
//    val := line[idx+2:]
//
//    current[key] = val
//  }
//
//  if current != nil {
//    results = append(results, current)
//  }
//
//  return results, nil
//} // func mpdSearch

func mpdSearch(conn net.Conn, cmd string, conds []Condition) ([]map[string]string, error) {
  /* --- formerly quoteSearchArg --- */
  quoteSearchArg := func(conds []Condition) string {
    /* --- formerly escapeMPDFilterValue --- */
    escapeMPDFilterValue := func(s string) string {
      var b strings.Builder
      b.Grow(len(s) * 2)

      for i := 0; i < len(s); i++ {
        c := s[i]
        switch c {
        case '\\', '"', '\'':
          b.WriteByte('\\')
        }
        b.WriteByte(c)
      }
      return b.String()
    }

    /* --- formerly quoteMPDArg --- */
    quoteMPDArg := func(s string) string {
      var b strings.Builder
      b.Grow(len(s)*2 + 2)

      b.WriteByte('"')
      for i := 0; i < len(s); i++ {
        c := s[i]
        if c == '"' || c == '\\' {
          b.WriteByte('\\')
        }
        b.WriteByte(c)
      }
      b.WriteByte('"')
      return b.String()
    }

    parts := make([]string, 0, len(conds))

    for _, c := range conds {
      escaped := escapeMPDFilterValue(c.Value)

//      part := fmt.Sprintf(
//        "(%s %s \"%s\")",
//        c.Field,
//        c.Op,
//        escaped,
//      )
//      parts = append(parts, part)
      parts = append(parts, fmt.Sprintf(`%s(%s %s "%s")`, c.Not, c.Field, c.Op, escaped))  // c.Not = "" || "!"
    }
    filter := "(" + strings.Join(parts, " AND ") + ")"
    return quoteMPDArg(filter)
  }

  searchArg := quoteSearchArg(conds)
  searchString := cmd + " " + searchArg + "\n"

  dbg("Sending command: %s", searchString)

  if _, err := conn.Write([]byte(searchString)); err != nil {
    return nil, err
  }

  reader := bufio.NewReader(conn)

  /* --- prevent infinite hang --- */
  conn.SetReadDeadline(time.Now().Add(10 * time.Second))
  defer conn.SetReadDeadline(time.Time{})

  results := make([]map[string]string, 0, 64)
  var current map[string]string

  for {
    lineBytes, err := reader.ReadSlice('\n')
    if err != nil {
      return nil, err
    }
    lineBytes = bytes.TrimSpace(lineBytes)
    line := string(lineBytes)
    if line == "OK" {
      break
    }
    if strings.HasPrefix(line, "ACK") {
      return nil, fmt.Errorf("MPD error: %s", line)
    }
    if line == "" {
      continue
    }

    /* --- record boundary --- */
    if strings.HasPrefix(line, "file: ") {
      if current != nil {
        results = append(results, current)
      }

      /* --- reuse map from pool --- */
      current = mpdMapPool.Get().(map[string]string)

      /* clear previous contents */
      for k := range current {
        delete(current, k)
      }
      current["file"] = line[6:]
      continue
    }

//    idx := strings.Index(line, ": ")
//    if idx < 0 {
//      continue
//    }
    idx := strings.IndexByte(line, ':')
    if idx < 0 || idx+1 >= len(line) || line[idx+1] != ' ' {
        continue
    }

    if current == nil {
      continue
    }
    key := strings.ToLower(line[:idx])
    val := line[idx+2:]

    current[key] = val
  }

  if current != nil {
    results = append(results, current)
  }
  return results, nil
} // func mpdSearch


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
} // func tryParsedLookup


func MPDtags(c *mpd.Client, target string, action string) (map[string]string, []string, error) {
  var notes []string
  dbg("mpdtags target=%q action=%q\n", target, action)

  // --- Status fetch ---
  if target == "status" {
    m := make(map[string]string)
    err := mpdDo(func(c *mpd.Client) error {
      st, err := c.Status()
      if err != nil {
        return err
      }
      for k, v := range st {
        m[strings.ToLower(k)] = v
      }

      cs, err := c.CurrentSong()
      if err != nil {
        return err
      }
      for k, v := range cs {
        m[strings.ToLower(k)] = v
      }

      // Next song info
      if nidStr, ok := st["nextsong"]; ok {
        if nextID, err := strconv.Atoi(nidStr); err == nil {
          ne, err := c.PlaylistInfo(nextID, -1)
          if err == nil && len(ne) > 0 {
            for k, v := range ne[0] {
              m["next_"+strings.ToLower(k)] = v
            }
          }
        }
      }
      return nil
    }, "Status, CurrentSong, NextSong")

    if err != nil {
      notes = append(notes, fmt.Sprintf("ERR fetching status: %v", err))
    }

    // --- Linger info ---
    state.mu.Lock()
    limit := state.baseLimit
    if state.blockOn && state.blockLimit > 0 {
      limit = state.blockLimit
    }
    ds := deriveStateLocked(state.lastSongZI, state.lastSongID, limit)
    state.mu.Unlock()
    dbg("DBG xy.active=%v\n", xy.active)

    m["writetime"] = ds.WriteTime
    m["lingersong"] = strconv.Itoa(ds.SongZI + 1)
    m["lingersongid"] = ds.SongID
    m["lingerpause"] = strconv.Itoa(ds.Paused)
    m["lingercount"] = strconv.Itoa(ds.Count)
    m["lingerbase"] = strconv.Itoa(ds.BaseLimit)
    m["lingerlimit"] = strconv.Itoa(ds.Limit)
    m["lingerblocklmt"] = strconv.Itoa(ds.BlockLimit)
    m["lingerpid"] = strconv.Itoa(ds.PID)

    if xy.active {
      m["lingerxy"] = strconv.Itoa(ds.LingerXY)
      m["lingerx"] = strconv.Itoa(ds.LingerX + 1)
      m["lingery"] = strconv.Itoa(ds.LingerY + 1)
    }

    return m, notes, nil
  }

  // --- Current song ---
  if target == "current" {
    s, err := c.CurrentSong()
    if err != nil || s == nil {
      return nil, nil, fmt.Errorf("no current song")
    }
    return map[string]string(s), nil, nil
  }

  // --- Next song ---
  if target == "next" {
    st, err := c.Status()
    if err != nil {
      return nil, nil, err
    }

    i, ok := st["nextsong"]
    if !ok {
      return nil, nil, fmt.Errorf("no next song")
    }

    idx, err := strconv.Atoi(i)
    if err != nil {
      return nil, nil, err
    }

    s, err := c.PlaylistInfo(idx, -1)
    if err != nil || len(s) == 0 || s[0]["file"] == "" {
      return nil, nil, fmt.Errorf("no next song")
    }

    return map[string]string(s[0]), nil, nil
  }

  // --- Path-based song lookup ---
  path := target
  dbg("DBG mpdtags path=%q\n", path)

  var song map[string]string
  var err error

  // --- 1. Relative path lookup ---
  songs, err := c.ListAllInfo(path)

  dbg("MPDTAGS REL: path=%q err=%v songs_len=%d", path, err, len(songs))

  if err == nil && len(songs) > 0 && songs[0]["file"] != "" {
    notes = append(notes, "Found via relative path lookup.")
    m := make(map[string]string)
    for k, v := range songs[0] {
      m[strings.ToLower(k)] = v
    }
    return m, notes, nil

  }

  // --- 2. Absolute path lookup ---
  absPath := "/library/music/" + path
  songs, err = c.ListInfo(absPath)
  if err != nil || len(songs) == 0 || songs[0]["file"] == "" {
    // Absolute path failed
    if action == "played" || action == "skipped" {
      // --- 3. Parsed lookup fallback ---
      ps, perr := tryParsedLookup(c, path)
      if ps != nil {
        if perr != nil {
          notes = append(notes, perr.Error())
        } else {
          notes = append(notes, "Found via parsed lookup.")
        }

        m := make(map[string]string)
        for k, v := range ps {
          m[strings.ToLower(k)] = v
        }
        return m, notes, nil

      } else {
        notes = append(notes, "File not found")
      }
    } else if action == "ignored" {
      notes = append(notes, "File not found")
    }
  } else {
    // Absolute path succeeded
    notes = append(notes, "Found via absolute path lookup.")

    m := make(map[string]string)
    for k, v := range songs[0] {
      m[strings.ToLower(k)] = v
    }
    return m, notes, nil

  }

  // --- fallback if nothing worked ---
//  return nil, notes, fmt.Errorf("file not found")
  // instead of returning an error, return empty song map
  return map[string]string{}, notes, nil


  if song != nil {
    return song, notes, nil
  }

  // --- Nothing found ---
//  return nil, notes, fmt.Errorf("file not found for %q", path)
  // instead of returning an error, return empty song map
  return map[string]string{}, notes, nil

} // func MPDtags()



func mpdLogParse(numEntries int) []LogLine {
  // mpdLogParse reads the MPD log newest-first.
  // It scans mpd.log and, if necessary and present, mpd.log.1.
  // This matches the default Linux logrotate behavior used by MPD.
  // Additional rotated or compressed logs (.2.gz, etc.) are intentionally ignored.
  var results []LogLine
  paths := []string{mpdLog}

  // check if .1 exists
  if info, err := os.Stat(mpdLog + ".1"); err == nil && !info.IsDir() {
    paths = append(paths, mpdLog+".1")
  }

  limit := numEntries

  for _, path := range paths {
    f, err := os.Open(path)
    if err != nil {
      continue
    }
    defer f.Close()

    stat, err := f.Stat()
    if err != nil {
      continue
    }

    const chunkSize = 8192
    var (
      offset = stat.Size()
      buf    []byte
    )

    for offset > 0 && len(results) < limit {
      readSize := chunkSize
      if offset < int64(readSize) {
        readSize = int(offset)
      }
      offset -= int64(readSize)

      tmp := make([]byte, readSize)
      if _, err := f.ReadAt(tmp, offset); err != nil && err != io.EOF {
        break
      }

      buf = append(tmp, buf...)

      for {
        i := bytes.LastIndexByte(buf, '\n')
        if i == -1 {
          break
        }


        line := string(buf[i+1:])
        buf = buf[:i]

        // parse line in-place
        if !strings.Contains(line, " player: ") {
          continue
        }

        parts := strings.SplitN(line, " ", 3)
        if len(parts) < 3 {
          continue
        }

        ts := parts[0]
        rest := parts[2]

        actionEnd := strings.IndexByte(rest, ' ')
        if actionEnd == -1 {
          continue
        }

        action := rest[:actionEnd]
        path := rest[actionEnd+1:]
        path = strings.Trim(path, "\"")

        // unescape MPD path
        path = strings.ReplaceAll(path, `\"`, `"`)
        path = strings.ReplaceAll(path, `\\`, `\`)

        ll := LogLine{
          Timestamp: ts,
          Action:    action,
          Path:      path,
        }

        results = append(results, ll)
        if len(results) >= limit {
          break
        }
      }
    }
    if len(results) >= limit {
      break
    }
  }

  if verbose {
    for _, r := range results {
      fmt.Printf("%s | %-7s | %s\n", r.Timestamp, r.Action, r.Path)
    }
  }

  return results

} // func mpdLogParse(numEntries int)


func getLogEntriesJSON(num int) []string {
    logs := mpdLogParse(num)
    var out []string
    for _, ll := range logs {
        j, _ := json.Marshal(ll)
        out = append(out, string(j))
    }
    return out
} // func getLogEntriesJSON


func JSONLog(numEntries int) ([]LogEntryV1, error) {
  var entries []LogEntryV1
  lines := mpdLogParse(numEntries)

  for _, ll := range lines {
    audioMap, notes, _ := MPDtags(nil, ll.Path, ll.Action) // pass nil client if MPDtags handles it internally

    audio := AudioV1{}
    if audioMap != nil {
      audio.Title = audioMap["title"]
      audio.Artist = audioMap["artist"]
      audio.Album = audioMap["album"]
      audio.Year = audioMap["year"]
      audio.Duration, _ = strconv.ParseFloat(audioMap["duration"], 64)
      audio.Time = atoi(audioMap["time"])
      audio.Disc = atoi(audioMap["disc"])
      audio.Track = atoi(audioMap["track"])
      audio.MBAlbumID = audioMap["musicbrainz_albumid"]
      audio.MBReleaseTrackID = audioMap["musicbrainz_releasetrackid"]
      audio.MBArtistID = audioMap["musicbrainz_artistid"]
    }

    entry := LogEntryV1{
  Timestamps: TimestampV1{
    Log: ll.Timestamp,
  },
      Action:    ll.Action,
      Notes:     strings.Join(notes, " "),
      File:      ll.Path,
      Audio:     audio,
    }
    entries = append(entries, entry)
  }

  return entries, nil
} // JSONLog


// global context for all WS connections
var globalWSCtx = &wsCtx{
  conns: make(map[*websocket.Conn]struct{}),
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
  conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
    InsecureSkipVerify: true,
    CompressionMode: websocket.CompressionContextTakeover, // or websocket.CompressionContextNone
//    CompressionThreshold: 1024,
  })

  log.Printf("WS extension request: %s", r.Header.Get("Sec-WebSocket-Extensions"))

  conn.SetReadLimit(512 * 1024) // 512 KB max frame size

  if err != nil {
    log.Printf("[WS] accept failed: %v", err)
    return
  }

  ctx := &wsCtx{
    conn:  conn,
    conns: globalWSCtx.conns, // share map for all watchers
  }

  // add connection to global set
  ctx.mu.Lock()
  ctx.conns[conn] = struct{}{}
  ctx.mu.Unlock()
  log.Printf("[WS] connection added")

  // remove connection on exit
  defer func() {
    ctx.mu.Lock()
    delete(ctx.conns, conn)
    ctx.mu.Unlock()
    conn.Close(websocket.StatusNormalClosure, "done")
    log.Printf("[WS] connection removed")
  }()

  // start wsWatcher for this connection
  go wsWatcher(ctx)

  // helper write func: always use this for safe sends
  write := func(c *websocket.Conn, typ websocket.MessageType, payload []byte) bool {
    wctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    err := c.Write(wctx, typ, payload)
    cancel()
    if err != nil {
      log.Printf("[WS] write failed, removing conn: %v", err)
      c.Close(websocket.StatusNormalClosure, "")
      ctx.mu.Lock()
      delete(ctx.conns, c)
      ctx.mu.Unlock()
      return false
    }
    return true
  }

  // ===== read loop keeps websocket alive =====
  for {
    _, msgBytes, err := conn.Read(r.Context())
    if err != nil {
      log.Printf("[WS] read error: %v", err)
      break
    }

log.Printf("[WS] incoming raw size: %d bytes", len(msgBytes))

    msg := string(msgBytes)

log.Printf("[WS] incoming decompressed size: %d bytes", len(msg))

    // detect JSON messages
    var js map[string]interface{}
    if err := json.Unmarshal(msgBytes, &js); err == nil {
      log.Printf("[WS] received JSON: %s", msg)
      // log raw incoming message for debugging
      dbg("[WS] incoming: msg=%s", string(msg))

      var req Request
      json.Unmarshal(msgBytes, &req)

      responses := verbProcessorJSON(js, req, ctx)

      for _, line := range responses {
        if line == "" {
          continue
        }
        write(conn, websocket.MessageText, []byte(line))
      }
      continue
    }

    // handle normal text commands
    responses := verbProcessor(msg)
    for _, line := range responses {
      if line == "" {
        continue
      }
      write(conn, websocket.MessageText, []byte(line))
    }
  }
} // func wsHandler()



func getLogHash() string               { return "" }
func getLogJSON() interface{}          { return nil }
func getAlbumArtCached(c *mpd.Client, file string) ([]byte, error) {
  // Pull album art directly from MPD
  return c.AlbumArt(file)
}


func wsWatcher(ctx *wsCtx) {
  log.Println("wsWatcher started")

// inside wsWatcher, top of for loop
select {
case <-time.After(0): // non-blocking placeholder
default:
}
if state.timer.Active {
    select {
    case idleEvents <- IdleEvent{Subsystem: "timer"}:
        // injected for wsWatcher
    default:
        // channel full, skip
    }
}

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
    log.Println("wsWatcher stopped for connection")
  }()

  // persistent state trackers
  var lastAlbumKey string
  var lastSongID string
  var lastPlaylistRev int

//  for range idleEvents {
  for ev := range idleEvents {

    var (
      msgs [][]byte  // accumulate messages to send
      data []byte
      notes []string
      img []byte
      pushArt bool
      allLogEntries [][]byte
      songChanged bool
//      status map[string]string
//      playlistChanged bool
//      playlistRev int
    )


    switch ev.Subsystem {
    case "timer":
      js := &StatusV1{}  // declare a fresh StatusV1 just for the timer case
      // build timer info exactly like in StatusV1
      state.mu.Lock()
      js.Timer = TimerV1{
        Active:    state.timer.Active,
        Duration:  state.timer.Duration,
        Remaining: int(math.Max(0, time.Until(state.timer.EndTime).Seconds())),
      }
      state.mu.Unlock()
      log.Printf("[WS TIMER CASE] %+v", js.Timer)

      // also add js to msgs
      data, err := json.Marshal(js)
      if err != nil {
        log.Printf("[WS TIMER CASE marshal] %v", err)
        break
      }
      msgs = append(msgs, data)

    case "player":
      var (
        err error
        status map[string]string
        n []string
      )
      log.Printf("[WS] idle event subsystem=%s rev=%d", ev.Subsystem, ev.PlaylistRev)
      // --- fetch normalized status ---
//      status, notes, err := MPDtags(nil, "status", "status")
      status, n, err = MPDtags(nil, "status", "status")
//      status, n, err = MPDtags(...)
      notes = n
      if err != nil {
        log.Printf("wsWatcher MPDtags error: %v", err)
        continue
      }

      js := &StatusV1{}
      if err := convert2json(status, js); err != nil {
        log.Printf("wsWatcher convert2json error: %v", err)
        continue
      }

      data, err = json.Marshal(js)
      if err != nil {
        log.Printf("wsWatcher marshal error: %v", err)
        continue
      }
msgs = append(msgs, data)
for _, note := range notes {
  msgs = append(msgs, []byte(note))
}

      // --- fetch raw MPD song fields for album identity ---
      var albumKey string
//      var img []byte
//      var pushArt bool
//      var allLogEntries [][]byte
//      var songChanged bool

      err = mpdDo(func(c *mpd.Client) error {

        song, err := c.CurrentSong()
        if err != nil {
          return err
        }

        mbalbid := song["MUSICBRAINZ_ALBUMID"]
        uri := song["file"]
        album := song["Album"]
        albumArtist := song["AlbumArtist"]
        songID := song["Id"]

        if lastSongID == "" || songID != lastSongID {
          log.Printf("[SONG] songID changed: %q → %q", lastSongID, songID)
          songChanged = true
          lastSongID = songID
        }

        switch {
        case mbalbid != "":
          albumKey = mbalbid
        case uri != "":
          if idx := strings.LastIndex(uri, "/"); idx > 0 {
            albumKey = uri[:idx]
          }
        case albumArtist != "" && album != "":
          albumKey = albumArtist + " -- " + album
        default:
          albumKey = songID
        }

        // fetch art ONLY if album changed
        log.Printf("[ART] About to test albumKey vs. lastAlbumKey: %q → %q", lastAlbumKey, albumKey)
        if albumKey == "" || albumKey != lastAlbumKey {
          log.Printf("[ART] albumKey changed: %q → %q", lastAlbumKey, albumKey)
  //        img, err = c.AlbumArt(uri)
          img, err = c.ReadPicture(uri)
          log.Printf("[ART] After ReadPicture(%s), len(img)=%d", uri, len(img))
          if err != nil || len(img) == 0 {
            log.Printf("[ART] ReadPicture(%s) failed, trying fallback.", uri)
            img, err = c.AlbumArt(uri)
            if err != nil || len(img) == 0 {
              log.Printf("[ART] AlbumArt(%s) failed, image apparently unavailable.", uri)

              // mediainfo on the file
              cmd := exec.Command("mediainfo", uri)
              out, _ := cmd.CombinedOutput()
              log.Printf("[ART] mediainfo output:\n%s", out)

              // ls of the directory containing the file
              dir := path.Dir(uri)
              cmd = exec.Command("ls", "-l", dir)
              out, _ = cmd.CombinedOutput()
              log.Printf("[ART] dir listing:\n%s", out)

              img = nil
            }
          }
          lastAlbumKey = albumKey
          state.mu.Lock()
          state.lastAlbumKey = lastAlbumKey
          state.mu.Unlock()

          pushArt = true
        } else {
          log.Printf("[ART] not pushing albumart (albumKey unchanged: %q)", albumKey)
          pushArt = false
        }

        if songChanged {
          // --- prepare log like json-log ---
          lines := mpdLogParse(24)
          for _, ll := range lines {
            tags, notes, err := MPDtags(c, ll.Path, ll.Action)
            if err != nil {
              log.Printf("[LOG] MPDtags error for %q: %v", ll.Path, err)
              continue
            }

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
              log.Printf("[LOG] convert2json failed for %q: %v", ll.Path, err)
              continue
            }

            entryData, err := json.Marshal(entry)
            if err != nil {
              log.Printf("[LOG] json.Marshal failed for %q: %v", ll.Path, err)
              continue
            }

            allLogEntries = append(allLogEntries, entryData)
//          log.Printf("[LOG] prepared log update %d bytes for file=%q", len(entryData), ll.Path)
          }
//msgs = append(msgs, allLogEntries...)
        } else {
          log.Printf("[LOG] songChanged=%v; not sending log", songChanged)
        }

        return nil
      }, "wsWatcher-albumkey")

      if err != nil {
        log.Printf("wsWatcher mpdDo error: %v", err)
        continue
      }
if songChanged && len(allLogEntries) > 0 {
  msgs = append(msgs, allLogEntries...)
}
    case "playlist":

//      if ev.Subsystem == "playlist" && ev.PlaylistRev != lastPlaylistRev {
//        playlistChanged = true
//        playlistRev = ev.PlaylistRev
//        lastPlaylistRev = ev.PlaylistRev
//      }
//
//      msg := map[string]interface{}{
//        "type": "playlist_changed",
//        "playlist_rev": ev.PlaylistRev,
//      }
//      payload, _ := json.Marshal(msg)
//      msgs = append(msgs, payload)

      if ev.PlaylistRev != lastPlaylistRev {
        lastPlaylistRev = ev.PlaylistRev

      msg := map[string]interface{}{
        "system": ev.Subsystem,
        "cmd": "changed",
        "response": map[string]interface{} {
          "playlist_rev": ev.PlaylistRev,
        },
      }
      payload, _ := json.Marshal(msg)
      msgs = append(msgs, payload)
      } else {
       continue
      }

    default:
      continue
    }



// nothing to send → skip
if len(msgs) == 0 && !pushArt {
  continue
}

    // --- push to all subscribed connections ---
    ctx.mu.Lock()

    for conn := range ctx.conns {

        ctx.writeMu.Lock()
        dead := false

        write := func(typ websocket.MessageType, payload []byte) bool {
            wctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
            defer cancel()

            err := conn.Write(wctx, typ, payload)
            if err != nil {
                log.Printf("[WS] write failed, removing conn: %v", err)
                conn.Close(websocket.StatusNormalClosure, "")
                dead = true
                return false
            }
            return true
        }

        dbg("broadcast start conn=%p", conn)

        // --- STATUS ---
//        if !write(websocket.MessageText, data) {
//            ctx.writeMu.Unlock()
//            delete(ctx.conns, conn)
//            continue
//        }

for _, m := range msgs {
  if !write(websocket.MessageText, m) {
    break
  }
}
//        if playlistChanged {
//          msg := map[string]interface{}{
//            "type": "playlist_changed",
//            "playlist_rev":  playlistRev,
//          }
//
//          payload, _ := json.Marshal(msg)
//
//          if !write(websocket.MessageText, payload) {
//            ctx.writeMu.Unlock()
//            delete(ctx.conns, conn)
//            continue
//          }
//        }


        // --- NOTES ---
//        for _, note := range notes {
//            if !write(websocket.MessageText, []byte(note)) {
//                break
//            }
//        }

        if dead {
            ctx.writeMu.Unlock()
            delete(ctx.conns, conn)
            continue
        }

        // --- ALBUM ART ---
        if pushArt {
            dbg("pushArt true for conn=%p len(img)=%d", conn, len(img))

            if len(img) > 0 {
                if !write(websocket.MessageBinary, img) {
                    ctx.writeMu.Unlock()
                    delete(ctx.conns, conn)
                    continue
                }
                dbg("album art pushed (%d bytes)", len(img))
            } else {
                // empty frame clears stale client art
                if !write(websocket.MessageBinary, []byte{}) {
                    ctx.writeMu.Unlock()
                    delete(ctx.conns, conn)
                    continue
                }
                dbg("empty album art frame pushed")
            }
        }

        // --- LOG ENTRIES ---
//        if songChanged && len(allLogEntries) > 0 {
//            dbg("pushing %d log entries", len(allLogEntries))
//            for _, entry := range allLogEntries {
//                if !write(websocket.MessageText, entry) {
//                    break
//                }
//            }
//        }

        ctx.writeMu.Unlock()

        if dead {
            delete(ctx.conns, conn)
            continue
        }

        dbg("broadcast complete conn=%p", conn)
    }

    ctx.mu.Unlock()
  }
  time.Sleep(100 * time.Millisecond)
} // func wsWatcher()

// verbProcessorJSON handles JSON commands from the WebSocket and returns
// JSON-formatted responses. Does NOT send over the WebSocket — wsHandler does that.
func verbProcessorJSON(js map[string]interface{}, req Request, ctx *wsCtx) []string {

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
  var URI, songID, playState, songZIstr, title string
  var randomState, consumeState, repeatState, singleState string
  var songZI int
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

    songID    = status["songid"]
    songZIstr = status["song"]
    playState = status["state"]
    title     = song["Title"]
    URI       = song["file"]

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
  switch system {   // mpd, player, playlist; linger; websocket; pulseaudio; search; timer //
  case "timer":
    state.mu.Lock()
    defer state.mu.Unlock()

    switch cmd {
      case "on":
        if ! state.MPDplaying {
          js["error"] = "MPD timer cannot be set when mpd is not playing"
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
        state.timer.Active = true
        state.timer.Duration = argsSeconds
        state.timer.EndTime = time.Now().Add(time.Duration(argsSeconds) * time.Second)
        js["response"] = fmt.Sprintf("Timer set to %d, update status", argsSeconds)
        out, _ := json.Marshal(js)
        return []string{string(out)}
      case "reset":
        if ! state.MPDplaying {
          js["error"] = "MPD timer cannot be set when mpd is not playing"
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }
        if argsIface != nil {
          js["notes"] = "system:timer cmd:reset & cmd:off do not take arguments"
        }
        if state.timer.Duration > 0 {
          state.timer.Active = true
          state.timer.EndTime = time.Now().Add(time.Duration(state.timer.Duration) * time.Second)
          js["response"] = fmt.Sprintf("Timer reset to %d, update status", state.timer.Duration)
        } else {
          js["response"] = "error"
          js["error"] = "No existing timer to reset"
        }
        out, _ := json.Marshal(js)
        return []string{string(out)}
      case "off":
        if argsIface != nil {
          js["notes"] = "system:timer cmd:reset & cmd:off do not take arguments"
        }
        state.timer.Duration = 0
        state.timer.Active = false
        state.timer.EndTime = time.Time{}
        state.timer.Remaining = 0
        js["response"] = "Timer turned off"
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
      case "json-log":
        log.Printf("vPJ: received json-log command")
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
          ctx.conn.Write(context.Background(), websocket.MessageText, out)
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
            err = ctx.conn.Write(context.Background(), websocket.MessageText, out)
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
          ctx.conn.Write(context.Background(), websocket.MessageText, out)
          return nil
        }

        js := map[string]any{
          "response": "log-end",
        }

        out, _ := json.Marshal(js)
        ctx.conn.Write(context.Background(), websocket.MessageText, out)

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

//          if strings.HasPrefix(line,"playlistlength:") {
//
//            parts := strings.Split(strings.TrimSpace(line),": ")
//            if len(parts) == 2 {
//
//              n,_ := strconv.Atoi(parts[1])
//
//              if found == 0 {
//                plBefore = n
//              } else {
//                plAfter = n
//              }
//
//              found++
//            }
//          }
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
      case "pause", "resume", "togglestate":
        var target bool //2083
        if cmd == "togglestate" {
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
        var newSongID string
        var newSongZI int
        if err = mpdDo(func(c *mpd.Client) error {
          status, err := c.Status()
          if err != nil {
            return err
          }
          newSongID = status["songid"]
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
          errors = append(errors, fmt.Sprintf("Song ID unchanged; newSongID: %s; lastSongID: %s", newSongID, songID))
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
        if URI != "" {
          err := mpdDo(func(c *mpd.Client) error { return c.PlaylistAdd(ignoredList, URI) }, "JSON-ignore-addIgnored")
          if err != nil {
            js["response"] = "error"
            js["error"] = err.Error()
          } else {
            js["response"] = fmt.Sprintf("Added to %s playlist: %s", ignoredList, URI)
          }
        } else {
          js["response"] = "error"
          js["error"] = "Ignore failed: MPD returned no URI to add to ignored playlist"
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
      case "albumart", "album-art":
        var imageBytes []byte
        var mimeType string

        err := mpdDo(func(c *mpd.Client) error {
          var err error
          imageBytes, err = c.AlbumArt(URI)  // Returns []byte directly
          if err != nil {
            return err
          }
          return nil
        }, "JSON-albumart")

        if err != nil {
          js["response"] = "error"
          js["error"] = fmt.Sprintf("AlbumArt failed: %v", err)
          out, _ := json.Marshal(js)
          return []string{string(out)}
        }

        // Detect MIME type
        mimeType = "image/jpeg"
        if len(imageBytes) > 3 {
          // PNG magic bytes: 89 50 4E 47
          if imageBytes[0] == 0x89 && imageBytes[1] == 0x50 &&
             imageBytes[2] == 0x4E && imageBytes[3] == 0x47 {
            mimeType = "image/png"
          }
        }

        // Base64 encode
        base64Data := base64.StdEncoding.EncodeToString(imageBytes)

        js["response"] = "ok"
        js["file"] = URI
        js["data"] = base64Data
        js["mime_type"] = mimeType

        out, _ := json.Marshal(js)
        return []string{string(out)}


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

//        for _, item := range raw {
//          m, ok := item.(map[string]interface{})
//          if !ok {
//            continue
//          }
//
//          f, _ := m["field"].(string)
//          o, _ := m["op"].(string)
//          v, _ := m["value"].(string)
//
//          if f == "" || o == "" || v == "" {
//            continue
//          }
//
//          conditions = append(conditions, Condition{f, o, v})
//        }
//
//        if len(conditions) == 0 {
//          log.Fatal("no conditions supplied")
//        }
        conditions := make([]Condition, 0, len(raw))

//        parseErr := ""
        for i, item := range raw {
          m, ok := item.(map[string]interface{})
          if !ok {
            js["response"] = "error"
            js["error"] = fmt.Sprintf("condition[%d] is not an object", i)
            out, _ := json.Marshal(js)
            return []string{string(out)}
//            continue
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
//          if u { // unary operator
//            if f != "" && v != "" && (n == "" || n == "!") {
//              if o != "" {
//                js["note"] = fmt.Sprintf("Additional operator '%s' supplied with %s unary; ignored.", o, f)
//                o = ""
//              }
//              conditions = append(conditions, Condition{f, o, v, u, n})
//            }
//          } else            if f != "" && o != "" && v != "" && (n == "" || n == "!") {
//              conditions = append(conditions, Condition{f, o, v, u, n})
//            } else {
//          parseErr = fmt.Sprintf("no conditions supplied: field='%s'; operator='%s'; value='%s'; unary='%v'; not='%s'", f, o, v, u, n)
//
//          }

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
//      case "next":
//        var respArr []string
//        log.Printf("vPJ: received linger next")
//
//        // 1. Update block state
//        state.mu.Lock()
//        state.transition = true
//        state.paused = false
//        state.mu.Unlock()
//
//        // 2. Delegate to existing sscmsc random and next handler
//        resp := verbProcessorJSON(map[string]interface{}{
//          "system":"mpd",
//             "cmd":"random",
//            "args":1,
//        }, ctx)
//        // Unmarshal into slice
//        if err := json.Unmarshal([]byte(resp[0]), &respArr); err != nil {
//          respArr = []string{"error unmarshaling response"}
//        }
//
//        resp = verbProcessorJSON(map[string]interface{}{
//          "system":"mpd",
//             "cmd":"next",
//        }, ctx)
//
//        // Unmarshal into slice
//        if err := json.Unmarshal([]byte(resp[0]), &respArr); err != nil {
//          respArr = append([]string{"error unmarshaling response"})
//        }
//
//        resp = verbProcessorJSON(map[string]interface{}{
//          "system":"mpd",
//             "cmd":"play",      // vPJ mpd/play/nil sends play -1
//        }, ctx)
//
//        // Unmarshal into slice
//        if err := json.Unmarshal([]byte(resp[0]), &respArr); err != nil {
//          respArr = append([]string{"error unmarshaling response"})
//        }
//
//
//        // Add linger-specific message
//        respArr = append(respArr, "linger block advanced")
//
//        // Marshal back
//        out, _ := json.Marshal(respArr)
//        return []string{string(out)}
      case "next":
        var respArr []string
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

          resp := verbProcessorJSON(jsreq, req, ctx)

          var tmp []string
          if err := json.Unmarshal([]byte(resp[0]), &tmp); err != nil {
            respArr = append(respArr, "error unmarshaling response")
            return
          }

          respArr = append(respArr, tmp...)
        }


        // ------------------------------------------------------------
        // Equivalent to CLI handler behaviour
        // ------------------------------------------------------------

        // turn random on
        run("random", 1)

        // advance playlist
        run("next", nil)

        // resume playback if paused (vPJ play/nil → play -1)
        run("play", nil)


        // ------------------------------------------------------------
        // Linger message
        // ------------------------------------------------------------
        respArr = append(respArr, "linger block advanced")

        out, _ := json.Marshal(respArr)
        return []string{string(out)}

      case "toggle":
        log.Printf("vPJ: received toggle command")

        state.mu.Lock()
        paused := !state.paused
        expired, limit := setPaused(paused)
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

        if !hasArg {
          out, _ := json.Marshal([]string{"ERR count requires a value"})
          return []string{string(out)}
        }

//        n, err := strconv.Atoi(fields[1])
        if err != nil || n < 0 {
          out, _ := json.Marshal([]string{"ERR invalid count"})
          return []string{string(out)}
        }

        state.mu.Lock()

        state.count = n

        limit := state.baseLimit
        if state.blockOn && state.blockLimit > 0 && state.blockLimit != state.baseLimit {
          limit = state.blockLimit
        }

        state.transition = state.count >= limit
        deriveStateLocked(state.lastSongZI, state.lastSongID, limit)

        state.mu.Unlock()

        _ = mpdDo(func(c *mpd.Client) error {
          return c.Random(state.transition)
        }, "count")

        log.Printf("STATE CHANGE: [IPC] count set=%d", n)

        out, _ := json.Marshal([]string{
          fmt.Sprintf("Count set to %d", n),
        })

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

        deriveStateLocked(state.lastSongZI, state.lastSongID, limit)

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

        deriveStateLocked(state.lastSongZI, state.lastSongID, limit)

        state.mu.Unlock()

        _ = mpdDo(func(c *mpd.Client) error {
          return c.Random(false)
        }, "IPC-blocklimit")

        log.Printf("STATE CHANGE: [IPC] block limit set=%d (effective=%d), count=%d, transition=%v",
          state.blockLimit, limit, state.count, state.transition)

        out, _ := json.Marshal([]string{
          fmt.Sprintf("Block limit set to %d", n),
        })

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
              log.Print("[XY] failed to return consume or original %v state: %v", state.consume, err)
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

  case "pulseaudio":
    switch cmd {
      case "up_volume", "down_volume", "mute_volume":
        arg := ""
        switch cmd {
        case "up_volume": arg = "+5"
        case "down_volume": arg = "-5"
        case "mute_volume": arg = "mute"
        }
        outBytes, err := exec.Command("./pulsevol", arg, "--no-volstatus", "--config="+configFlag).CombinedOutput()
//        resp := []string{string(outBytes)}
        resp := []string{strings.TrimSpace(string(outBytes))}
        if err != nil {
          resp = append(resp, fmt.Sprintf("error: %v", err))
        }
        js["response"] = resp
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
        // mark watcher as running
        if !ctx.watcherRunning {
          ctx.watcherRunning = true
          go wsWatcher(ctx)
          log.Println("[vPJ] wsWatcher started for connection %p", ctx.conn)
        }

        ctx.writeMu.Lock()
        defer ctx.writeMu.Unlock()

        // push subscribed first
        js["response"] = "subscribed"
        out, _ := json.Marshal(js)
        ctx.conn.Write(context.Background(), websocket.MessageText, out)

        // then push status, logs, and finally binary album art
        // --- immediate push on subscribe ---
        status, notes, err := MPDtags(nil, "status", "status")
        if err == nil {
          js := &StatusV1{}
          if err := convert2json(status, js); err == nil {
            data, err := json.Marshal(js)
            if err == nil {
              ctx.conn.Write(context.Background(), websocket.MessageText, data)
              log.Println("[vPJ] pushed initial confirmation to subscriber")

              // send notes if any
              for _, note := range notes {
                ctx.conn.Write(context.Background(), websocket.MessageText, []byte(note))
                log.Printf("[vPJ] pushed note on subscribe event: %q", note)
              }
            }
          }

          // --- push logs immediately on subscribe ---
          resp := verbProcessor("json-log")
          for _, entry := range resp {
            if entry != "" {
              ctx.conn.Write(context.Background(), websocket.MessageText, []byte(entry))
            }
//          log.Printf("[vPJ] pushed log entry to subscriber")
          }

//          // to push as one log frame rather than n=24 frames:
//          // filter empty entries first
//          var logs []map[string]interface{}
//          for _, entry := range resp {
//            if entry != "" {
//              var log map[string]interface{}
//              if err:= json.Unmarshal([]byte(entry), &log); err == nil {
//                logs = append(logs, log)
//              }
//            }
//          }
//
//          // marshal as one JSON array
//          if len(logs) > 0 {
//            combined, err := json.Marshal(logs)
//            if err == nil {
//              ctx.conn.Write(context.Background(), websocket.MessageText, combined)
//              log.Printf("[vPJ] pushed %d log entries in one frame", len(logs))
//            }
//          }
//          // end of log accumulation


          // --- immediate push of album art with ReadPicture fallback ---
          var img []byte
          err := mpdDo(func(c *mpd.Client) error {
            song, err := c.CurrentSong()
            if err != nil {
              log.Printf("[vPJ] Subscribe: CurrentSong error: %v", err)
              return err
            }

            uri := song["file"]
            log.Printf("[vPJ] Subscribe: attempting ReadPicture for %q", uri)
            img, err = c.ReadPicture(uri)
            if err != nil || len(img) == 0 {
                log.Printf("[vPJ] Subscribe: ReadPicture failed, falling back to AlbumArt for %q", uri)
                img, err = c.AlbumArt(uri)
                if err != nil || len(img) == 0 {
                    log.Printf("[vPJ] Subscribe: AlbumArt failed, no image available for %q", uri)
                    img = nil
                } else {
                    log.Printf("[vPJ] Subscribe: AlbumArt succeeded, %d bytes", len(img))
                }
            } else {
                log.Printf("[vPJ] Subscribe: ReadPicture succeeded, %d bytes", len(img))
            }
            return nil
          }, "wsWatcher-subscribe-art")

          if err == nil {
            if len(img) > 0 {
              ctx.conn.Write(context.Background(), websocket.MessageBinary, img)
              log.Printf("[vPJ] pushed album art (%d bytes) to subscriber", len(img))
            } else {
              ctx.conn.Write(context.Background(), websocket.MessageBinary, []byte{})
              log.Printf("[vPJ] pushed empty album art frame to subscriber")
            }
          }

        }

//        js["response"] = "subscribed"
//        out, _ := json.Marshal(js)
        return nil //[]string{string(out)}

    case "ping":
      js["response"] = "pong"
      out, _ := json.Marshal(js)
      ctx.conn.Write(context.Background(), websocket.MessageText, out)
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


  default: // of switch system
    switch cmd {
      case "ping":
        js["response"] = "pong"
        js["timestamp"] = "implement timestamp"
        out, _ := json.Marshal(js)
        ctx.conn.Write(context.Background(), websocket.MessageText, out)
        return nil

    default:
      js["response"] = "error"
      js["error"] = "invalid system"
      out, _ := json.Marshal(js)
      return []string{string(out)}
    }
  } // switch system

} // func verbProcessorJSON()


func startWS(port int) {
  http.HandleFunc("/ws", wsHandler)

  go func() {
    addr := fmt.Sprintf(":%d", port)
    log.Printf("WS listening on :%s (/ws)", addr)
    if err := http.ListenAndServe(addr, nil); err != nil {
      log.Fatalf("ws server failed: %v", err)
    }
  }()
} // func startWS()


// runXYStep executes one XY shuffle step using playlist positions ("song").
// Assumes globals: xy.start, xy.end, song, verbose
func runXYStep(c *mpd.Client) error {
  st, err := c.Status()
  if err != nil {
      return err
  }
  songZI, _ := strconv.Atoi(st["song"])
  if xy.end <= xy.start+1 {
    log.Printf("[XY] completed: x=%d y=%d", xy.start, xy.end)
    xy.active = false
    return nil
  }

  if songZI != xy.start {
    log.Printf("[rXYS] song %d not %d, disabling XY", songZI, xy.start)
    xy.active = false
    _ = mpdDo(func(c *mpd.Client) error {
      if err := c.Consume(state.consume); err != nil {
        log.Printf("[XY] failed to restore consume=%t: %v", state.consume, err)
      }
      return nil
    }, "xy-restore-consume")
    return nil
  }

  // Select random playlist position r ∈ [xy.start+1, xy.end]
  r := rand.Intn(xy.end-(xy.start+1)+1) + (xy.start + 1)

  if verbose {
    log.Printf("[rXYS] debug: x=%d y=%d song=%d r=%d (range %d..%d)", xy.start, xy.end, songZI, r, xy.start+1, xy.end)
  } else {
    log.Printf("[XY] pick: r=%d → %d", r, xy.start+1)
  }

  // Move song at r → xy.start+1 (current+1)
  if err := c.Move(r, -1, xy.start+1); err != nil {
    return fmt.Errorf("xy move failed (r=%d → %d): %w", r, xy.start+1, err)
  }

  if verbose {
    log.Printf("[rXYS] move complete: %d → %d, consume will advance", r, xy.start+1)
  }

  // Decrement upper bound
  xy.end--

  if verbose {
    log.Printf("[rXYS] advance: new y=%d (remaining=%d)", xy.end, xy.end-xy.start)
  }

  return nil
} // func runXYStep(c *mpd.Client) error


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
} // func loadConfig()


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
} // func parseConfig()


// mpdDo runs a function with a short-lived MPD client and logs errors
func mpdDo(fn func(*mpd.Client) error, ctx string) error {
  var client *mpd.Client
  var err error

  useSocket := false
  useTCP := false

  // helper: dial MPD (socket or TCP) and apply password if set
  dialMPD := func() (*mpd.Client, error) {
    var c *mpd.Client
    var e error

    if useSocket {
      c, e = mpd.Dial("unix", mpdSocket)
    } else if useTCP {
      c, e = mpd.Dial("tcp", fmt.Sprintf("%s:%d", mpdHost, mpdPort))
    } else {
      return nil, fmt.Errorf("mpdDo: no connection method selected")
    }

    if e != nil {
      return nil, e
    }

    // apply password if specified
    if mpdPass != "" {
      if err := c.Command("password " + mpdPass).OK(); err != nil {
        c.Close()
        return nil, fmt.Errorf("mpdDo: password auth failed: %v", err)
      }
    }

    return c, nil
  }

  // 1. User defined TCP + socket → prefer socket, fallback TCP
  if mpdHost != "" && mpdPort != 0 && mpdSocket != "" {
    if conn, err := net.DialTimeout("unix", mpdSocket, 500*time.Millisecond); err == nil {
      conn.Close()
//      log.Printf("[mpdDo] socket usable: %s", mpdSocket)
      dbg("[mpdDo] socket usable: %s", mpdSocket)
      useSocket = true
    } else if client, err = mpd.Dial("tcp", fmt.Sprintf("%s:%d", mpdHost, mpdPort)); err == nil {
      log.Printf("[mpdDo] tcp usable: %s:%d", mpdHost, mpdPort)
      useTCP = true
    } else {
      log.Printf("[mpdDo] neither user socket nor tcp usable")
    }
  }

  // 2. Only socket defined
  if (!useSocket || !useTCP) && mpdSocket != "" {
    if conn, err := net.DialTimeout("unix", mpdSocket, 500*time.Millisecond); err == nil {
      conn.Close()
//      log.Printf("[mpdDo] socket usable: %s", mpdSocket)
      dbg("[mpdDo] socket usable: %s", mpdSocket)
      useSocket = true
    } else {
      log.Printf("[mpdDo] socket unusable: %v", err)
    }
  }

  // 3. Only TCP defined
  if (!useSocket || !useTCP) && mpdHost != "" && mpdPort != 0 {
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

  // Final connect + auth
  client, err = dialMPD()
  if err != nil {
    return fmt.Errorf("mpdDo: connect failed: %v", err)
  }

//  // Timeout-wrapped execution
//  done := make(chan error, 1)
//  go func() {
//    done <- fn(client)
//  }()
//
//  select {
//  case err := <-done:
//    client.Close()
//    return err
//  case <-time.After(3 * time.Second):
//    client.Close()
//    return fmt.Errorf("mpdDo: timeout (%s)", ctx)
//  }
  err = fn(client)
  client.Close()
  return err
} // func mpdDo(fn func(*mpd.Client) error, ctx string) error



func parseMPDEnv() {
  // MPD_HOST parsing
  if verbose { log.Printf("[parseMPDEnv] Started.\n") }
  if v := os.Getenv("MPD_HOST"); v != "" {
    // 1. abstract socket with no password
    if strings.HasPrefix(v, "@") {
      env.mpdSocket = v
    } else if strings.Contains(v, "@@") { // 2. password@@abstract
      parts := strings.SplitN(v, "@@", 2)
      env.mpdPass = parts[0]
      env.mpdSocket = "@" + parts[1] // preserve leading @
    } else if strings.Contains(v, "@") { // 3. password@tcp or password@concrete socket
      parts := strings.SplitN(v, "@", 2)
      env.mpdPass = parts[0]
      addr := parts[1]
      if strings.Contains(addr, "/") {
        env.mpdSocket = addr
        if ! strings.HasPrefix(addr, "/") {
          log.Printf("[parseMPDEnv] env.mpdSocket assumed to be relative path: %s\n", env.mpdSocket)
        }
      } else {
        env.mpdHost = addr
      }
    } else { // 4. tcp host or concrete socket without password
      if strings.Contains(v, "/") {
        env.mpdSocket = v
        if ! strings.HasPrefix(v, "/") {
          log.Printf("[parseMPDEnv] env.mpdSocket assumed to be relative path: %s\n", v)
        }
      } else {
        env.mpdHost = v
      }
    }
  }

  // MPD_PORT environment
  if p := os.Getenv("MPD_PORT"); p != "" {
    if verbose { log.Printf("[parseMPDEnv] MPD_PORT: %s\n", p) }
    if n, err := strconv.Atoi(p); err == nil {
      env.mpdPort = n
      if verbose { log.Printf("[parseMPDEnv] env.mpdPort set to: %d\n", env.mpdPort) }

    }
  } else {
    if verbose { log.Printf("[parseMPDEnv] MPD_PORT empty\n") }
  }


  // MPD_LOG environment
  if l := os.Getenv("MPD_LOG"); l != "" {
    if verbose { log.Printf("[parseMPDEnv] MPD_LOG: %s\n", l) }

    if info, err := os.Stat(l); err == nil && !info.IsDir() {
      mpdLog = l
    }
  } else {
    if verbose { log.Printf("[parseMPDEnv] MPD_LOG empty\n") }
  }

}


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


func directDialMPD() (net.Conn, error) {
  d := net.Dialer{Timeout: 5 * time.Second}
  // Try UNIX socket first
  conn, err := d.Dial("unix", mpdSocket)
  if err != nil {
    // UNIX failed, try TCP fallback
    addr := fmt.Sprintf("%s:%d", mpdHost, mpdPort)
    conn, err = d.Dial("tcp", addr)
    if err != nil {
      return nil, fmt.Errorf("failed to connect via UNIX and TCP: %w", err)
    }
  }

  reader := bufio.NewReader(conn)
  line, err := reader.ReadString('\n')
  dbg("MPD response: %s", line)

  if err != nil {
    conn.Close()
    return nil, err
  }

  if !strings.HasPrefix(line, "OK MPD") {
    conn.Close()
    return nil, fmt.Errorf("mpd greeting invalid: %s", line)
  }

  if mpdPassword != "" {
    cmd := fmt.Sprintf("password \"%s\"\n", mpdPassword)
    if _, err := conn.Write([]byte(cmd)); err != nil {
      conn.Close()
      return nil, err
    }

    resp, err := reader.ReadString('\n')
    dbg("MPD password response: %s", resp)

    if err != nil {
      conn.Close()
      return nil, err
    }

    if !strings.HasPrefix(resp, "OK") {
      conn.Close()
      return nil, fmt.Errorf("mpd auth failed: %s", resp)
    }
  }

  return conn, nil
} // func directDialMPD



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
      w, err = mpd.NewWatcher("unix", mpdSocket, "",
        "player", "options", "mixer", "playlist", "stored_playlist", "output")
    } else {
      w, err = mpd.NewWatcher("tcp", fmt.Sprintf("%s:%d", mpdHost, mpdPort), "",
        "player", "options", "mixer", "playlist", "stored_playlist", "output")
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


// runIdleLoop [rIL()] runs the MPD idle loop, updating state on song changes
func runIdleLoop(w *mpd.Watcher) error {
  log.Println("MPD connection established, entering idle loop")

  // mpd.Watcher exposes an Error channel that reports lower-level
  // connection issues (EOF, broken pipe, protocol errors).
  //
  // IMPORTANT:
  //   These errors do *not* necessarily stop the Event channel,
  //   so we log them asynchronously for correlation only.
  //

  ticker := time.NewTicker(1 * time.Second)
  defer ticker.Stop()

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

    case <-ticker.C:
      state.mu.Lock()

      expired := state.timer.Active &&
                 time.Now().After(state.timer.EndTime)

      if !expired {
        state.mu.Unlock()
        continue
      }

      log.Printf("[timer] expired")

      shouldPause := state.MPDplaying

      // clear timer state FIRST (under lock)
      state.timer.Active = false
      state.timer.Duration = 0
      state.timer.EndTime = time.Time{}

      state.mu.Unlock()

      // THEN do MPD work
      if shouldPause {
        err := mpdDo(func(c *mpd.Client) error {
          return c.Pause(true)
        }, "timer-expire-pause")

        if err != nil {
          log.Printf("[timer] pause failed: %v", err)
        }
      }

      continue
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
        songID    := status["songid"]
        songZI, _ := strconv.Atoi(status["song"])  // Zero-Indezed song playlist position
        plRev, _  := strconv.Atoi(status["playlist"])

        idleEvents <- IdleEvent{
          Subsystem:   subsystem,
          SongID:      songID,
          Status:      status,
          PlaylistRev: plRev,
        }

        // Protect shared FSM state.
        state.mu.Lock()
        defer state.mu.Unlock()

        // Assign playing=true to state.MPDplaying if "state" == "play"
        if status["state"] == "play" {
          state.MPDplaying = true
        } else {
          state.MPDplaying = false
        }

        // Compute the current limit
        limit := state.baseLimit
        if state.blockOn && state.blockLimit > 0 {
          limit = state.blockLimit
        }

        if verbose {
          log.Printf("[rIL] limit: %d; state.transition: %t; xy.active: %t", limit, state.transition, xy.active)
        }

        // Recovery: count exceeded limit (can happen after XY abort or prior bugs)
        if state.count > limit && ! state.transition && ! xy.active {
          log.Printf(
            "[rIL] Recovery: count=%d exceeded limit=%d, forcing transition",
            state.count, limit,
          )
          state.transition = true
          return nil
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
          deriveStateLocked(songZI, songID, limit)  // <--- write after increment to update state count while paused

//          state.mu.Unlock()
//          return nil
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
//          state.mu.Unlock()
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

        // ---------- INSERTED XY HANDLER ----------
        if xy.active {
          // addresses https://github.com/iconoclasthero/mpdgolinger/issues/19
          plLen, err := strconv.Atoi(status["playlistlength"])
          if err != nil {
            log.Printf("[XY] invalid playlistlength: %v", err)
            return nil
          }
          max := plLen - 1
          if xy.end > max {
            log.Printf(
              "[XY] clamping Y from %d to %d (playlist shrank)",
              xy.end, max,
            )
            xy.end = max
          }
          if songZI != xy.start {
            state.transition = true
            xy.active = false

            if err := c.Consume(state.consume); err != nil {
              log.Printf("[XY] failed to restore consume=%t: %v", state.consume, err)
            }

            log.Printf("[rIL] song %d not %d, disabling XY", songZI, xy.start)
            log.Printf("[rIL] state.transtion: %t; xy.active: %t", state.transition, xy.active)
            return nil
          }
          log.Printf("[rIL] Calling runXYStep, playlist postion: %d (ZI)", songZI)
          err = runXYStep(c)
          if err != nil {
            log.Printf("runXYStep failed: %v", err)
          }
//          if err := mpdDo(func(c *mpd.Client) error {
//            return runXYStep(c)
//          }, "rXYS"); err != nil {
//            log.Printf("runXYStep failed: %v", err)
//          }
        }

        // ------------------------------------------

        if state.transition && ! xy.active {
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

        } else if ! xy.active && state.count == limit-1 { // <- uses computed local limit
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
          log.Printf("[rIL] Normal increment: count=%d/%d", state.count, limit)
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
        deriveStateLocked(songZI, songID, limit)

        // Log the song again *after* state changes and random toggles.
        // This lets us confirm whether MPD advanced tracks as expected.
        //
        logCurrentSong(c, "idle event (post-state-change)")

        _ = prevCount
        _ = prevTransition
//        state.mu.Unlock()

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
} // func shellQuote(s string) string 


// shellQuoteSlice quotes an entire slice of strings
func shellQuoteSlice(slice []string) []string {
  out := make([]string, len(slice))
  for i, s := range slice {
    out[i] = shellQuote(s)
  }
  return out
} // func shellQuoteSlice(slice []string) []string]


// shellQuoteKV quotes only the value part of a key=value string for shell safety
func shellQuoteKV(line string) string {
  parts := strings.SplitN(line, "=", 2)
  if len(parts) != 2 {
    // not a key=value pair, just quote the whole thing
    return shellQuote(line)
  }
  key, val := parts[0], parts[1]
  return key + "=" + shellQuote(val)
} // func shellQuoteKV(line string) string


// verbProcessor [vP()] parses and executes IPC commands from csv, returning response lines.
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
      log.Printf("IPC: received json-log command")

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
      log.Printf("IPC: received json-status command")

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
      for _, note := range notes {
        responses = append(responses, note)
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
      ds := deriveStateLocked(state.lastSongZI, state.lastSongID, limit)
      state.mu.Unlock()

      // append lines to responses slice
      responses = append(responses,
        fmt.Sprintf("writetime=%s", ds.WriteTime),
        fmt.Sprintf("lingersong=%d", ds.SongZI+1),
        fmt.Sprintf("lingersongid=%s", ds.SongID),
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
      deriveStateLocked(state.lastSongZI, state.lastSongID, state.baseLimit)
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
      deriveStateLocked(state.lastSongZI, state.lastSongID, state.baseLimit)
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
      deriveStateLocked(state.lastSongZI, state.lastSongID, limit)
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
      deriveStateLocked(state.lastSongZI, state.lastSongID, limit)
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
      deriveStateLocked(state.lastSongZI, state.lastSongID, limit)
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

      deriveStateLocked(state.lastSongZI, state.lastSongID, limit)
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
    responses = append(responses, resp)
  }


  state.mu.Lock()
  songID := state.lastSongID
  songZI := state.lastSongZI
  limit := state.baseLimit
  if state.blockOn && state.blockLimit > 0 {
    limit = state.blockLimit
  }
  deriveStateLocked(songZI, songID, limit)
  state.mu.Unlock()

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
func deriveStateLocked(songZI int, songID string, limit int) *derivedState {
  // state.mu MUST already be held
  now := time.Now().Format(time.RFC3339Nano)

  ds := &derivedState{
    WriteTime:  now,
    SongZI:     songZI,
    SongID:     songID,
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
} // func writeStateLocked(songID string, limit int)


// formatState formats a derivedState struct into key=value lines
func formatState(w io.Writer, ds *derivedState) {
  fmt.Fprintf(w, "writetime=%s\n", ds.WriteTime)
  fmt.Fprintf(w, "lingersong=%d\n", ds.SongZI+1)
  fmt.Fprintf(w, "lingersongid=%s\n", ds.SongID)
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

  ds := deriveStateLocked(state.lastSongZI, state.lastSongID, limit)
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
      log.Printf("mpdLog set to .conf mpdlog: %s\n", mpdLog)
    } else if env.mpdLog != "" {
      mpdLog = env.mpdLog
      log.Printf("mpdLog set to env.mpdLog: %s\n", env.mpdLog)
    } else if defaultMPDlog != "" {
      mpdLog = defaultMPDlog
      log.Printf("mpdLog set to defaultMPDlog: %s\n", defaultMPDlog)
    } else {
      log.Printf("ERROR: mpdLog unset!\n")
    }
  } else {
    log.Printf("mpdLog set to --mpdlog: %s\n", mpdLog)
  }


  if mpdSocket == "" {
    if v, ok := kv["mpdsocket"]; ok && v != "" {
      mpdSocket = v
      log.Printf("mpdSocket set to .conf mpdsocket: %s\n", mpdSocket)
    } else if env.mpdSocket != "" { // final fallback
      mpdSocket = env.mpdSocket
      log.Printf("mpdSocket set to env.mpdSocket: %s\n", env.mpdSocket)
    } else if defaultMPDsocket != "" {
      mpdSocket = defaultMPDsocket
    }
  } else {
    log.Printf("mpdSocket set to --mpdsocket: %s\n", mpdSocket)
  }

  if mpdHost == "" {
    if v, ok := kv["mpdhost"]; ok && v != "" {
      mpdHost = v
      log.Printf("mpdHost set to .conf mpdhost: %s\n", mpdHost)
    } else if env.mpdHost != "" { // final fallback
      mpdHost = env.mpdHost
      log.Printf("mpdHost set to env.mpdHost: %s\n", env.mpdHost)
    }
  } else {
    log.Printf("mpdHost set to --mpdhost: %s\n", mpdHost)
  }

  if mpdPort == 0 {
    if v, ok := kv["mpdport"]; ok && v != "" {
      if n, err := strconv.Atoi(v); err == nil {
        mpdPort = n
        log.Printf("mpdPort set to .conf mpdport: %d\n", mpdPort)
      }
    } else if env.mpdPort != 0 { // final fallback
      mpdPort = env.mpdPort
      log.Printf("mpdPort set to env.mpdPort: %d\n", env.mpdPort)
    }
  } else {
    log.Printf("mpdPort set to --mpdport: %d\n", mpdPort)
  }

  if mpdPass == "" {
    if v, ok := kv["mpdpass"]; ok && v != "" {
      mpdPass = v
    } else if env.mpdPass != "" { // final fallback
      mpdPass = env.mpdPass
      log.Printf("mpdPass set to env.mpdPass: %s\n", env.mpdPass)
    }
  } else {
    log.Printf("mpdPass set to --mpdpass: %s\n", mpdPass)
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
    state.lastSongZI = 0
  } else {
    status, err := client.Status()
    if err != nil {
      log.Printf("Status error at startup: %v", err)
      state.count = 0
      state.lastSongID = ""
      state.lastSongZI = 0
    } else {
      state.lastSongID = status["songid"]
      state.lastSongZI, _ = strconv.Atoi(status["song"])  // Zero-Indezed song playlist position
      switch status["state"] {                            // This doesn't look right: why is count being asigned this way?
        case "pause", "stop":                             // Especially when paused??
          state.count = 0
          state.MPDplaying = false
        case "play":
          state.MPDplaying = true
          state.count = 1
        default:
          log.Printf("MPD status 'state' is neither 'play', 'pause', nor 'stop': %s!", status["state"])
          state.MPDplaying = false
          state.count = 0
      }
    }
    client.Close()
  }

  log.Printf(
      "Starting block count=%d, lastSongZI=%d; lastSongID=%s, baseLimit=%d, blockLimit=%d",
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
    deriveStateLocked(state.lastSongZI, state.lastSongID, state.baseLimit)
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




