package main

import (
//	"github.com/fhs/gompd/v2/mpd"
	"github.com/coder/websocket"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
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
     lastSongID       int
     lastSongZI       int
     lastSongURI      string
     prevSongID       int
     prevSongURI      string
     prevAlbumKey     string
     baseLimit        int
     blockOn          bool
     consume          bool
     lastAlbumKey     string
     pauseTimer       TimerV1
     scrobbler        ScrobblerV1
} // type State struct

type   derivedState   struct {
     WriteTime        string
     SongZI           int
     SongID           int
     SongURI          string
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
     PauseTimer       TimerV1   `json:"pause_timer"`
     PulseData        PulseV1   `json:"pulse_data"`
     Scrobbler      ScrobblerV1 `json:"scrobbler"`
}

type ScrobblerV1      struct {
     Enabled          bool      `json:"enabled"`
     Running          bool      `json:"running"`
     LastCmd          string    `json:"last_cmd"`
     LastErr          string    `json:"last_err,omitempty"`
     ExitCode         int       `json:"exit_code"`
}

type      PulseV1     struct {
     SinkName         string    `json:"sinkname"`
     Volume           int       `json:"volume"`         // average or first channel
     Mute             bool      `json:"mute"`
     Channels    map[string]int `json:"channels"`       // front-left → 23, etc
     VolString        string    `json:"volstring"`      // for debug, replace with "-"
     PulsePath        string    `json:"pulse_path"`     // for debug
     PulseServer      string    `json:"server"`         // for debug
     PulsePort        int       `json:"port"`           // for debug
}

type AlbumArtResult   struct {
     Img              []byte    `json:"-"`
     MimeType         string    `json:"mime"`
     Source           string    `json:"source"`
     Size             int       `json:"size"`
     Hash             uint64    `json:"hash,string"`
     Width            int       `json:"width,omitempty"`
     Height           int       `json:"height,omitempty"`
     HasEmbed         bool      `json:"hasEmbed"`
     HasAlbumArt      bool      `json:"hasAlbumArt"`
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
  defaultNoCoverList = ".noCoverList"
  defaultSmallRPlist = ".smallRPlist"
  defaultSmallAAlist = ".smallAAlist"
  defaultNoEmbedList = ".noEmbedList"
  defaultNoAAlist    = ".noAAlist"
  defaultFixArtList  = ".fixArtList"
  defaultMPDpassword = ""
  defaultPulseServer = "127.0.0.1"
  defaultPulsePort   = 4713
  defaultPulsePath   = "/usr/bin/pactl"
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
  noCoverList string = defaultNoCoverList
  fixArtList  string = defaultFixArtList
  smallRPlist string = defaultSmallRPlist
  smallAAlist string = defaultSmallAAlist
  noEmbedList string = defaultNoEmbedList
  noAAlist    string = defaultNoAAlist

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

  PulseData PulseV1
  pulseMu sync.RWMutex
  pulseServer = defaultPulseServer
  pulsePort = defaultPulsePort
  pulsePath = defaultPulsePath

  // global context for all WS connections
  globalWSCtx = &wsCtx{
//    conns: make(map[*websocket.Conn]struct{}),
  }

  globalWSHub = &wsHub{
    conns: make(map[*websocket.Conn]struct{}),
  }

  wsMu sync.Mutex
  wsConns = map[*websocket.Conn]struct{}{}

  connIDMu sync.Mutex
  connID   = map[*websocket.Conn]string{}
  nextID   atomic.Uint64

) // var


type       wsCtx      struct {
     conn             *websocket.Conn
//     mu               sync.Mutex
//     conns            map[*websocket.Conn]struct{}
//     watcherRunning   bool // <- add this
     writeMu          sync.Mutex
    mu      *sync.Mutex
    conns   *map[*websocket.Conn]struct{}
}

type wsHub struct {
    mu    sync.Mutex
    conns map[*websocket.Conn]struct{}
}

type      Request     struct {
     System string          `json:"system"`
     Cmd    string          `json:"cmd"`
     Args   json.RawMessage `json:"args"`
}

type     IdleEvent    struct {
     Subsystem        string
     SongID           int
     PrevSongID       int
     SongURI          string
     Status           map[string]string
     PlaylistRev      int
     AlbumKey         string
     PulseData        PulseV1
     Subscribe        bool
     Target           *websocket.Conn
}

var idleEvents = make(chan IdleEvent, 32)


/* ---------------- mpdtags -------------- */
