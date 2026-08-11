package main

import (
  "encoding/json"
  "bufio"
  "fmt"
  "log"
  "net"
  "os"
  "strings"
  "strconv"
  "net/url"
  "time"
  "regexp"
  "sync"
)

/* ---------------------------------------------------- */
/*          all of this is inside mpdgolinger           */
/* ---------------------------------------------------- */

type LogLine          struct {
     Timestamp        string // exactly as in mpd.log
     Action           string // played / skipped / ignored
     Path             string // decoded filesystem path
     Notes            string
}

type      AudioV1     struct {
     Title            string   `json:"title"`
     Artist           string   `json:"artist"`
     AlbumArtist      string   `json:"albumartist"`
     Album            string   `json:"album"`
     Year             string   `json:"year"`
     Duration         float64  `json:"duration"`
     Time             int      `json:"time"`
     Disc             int      `json:"disc"`
     Track            int      `json:"track"`
     MBAlbumID        string   `json:"musicbrainz_albumid"`
     MBTrackID        string   `json:"musicbrainz_trackid"`
     MBReleaseTrackID string   `json:"musicbrainz_releasetrackid"`
     MBArtistID       string   `json:"musicbrainz_artistid"`
     MBAlbumArtistID  string   `json:"musicbrainz_albumartistid"`
     MBReleaseGrpID   string   `json:"musicbrainz_releasegroupid"`
     File             string   `json:"file"`
     SongPosition     int      `json:"song_position"`
     SongID           int      `json:"songID"`
}

type     PlayerV1     struct {
     State            string   `json:"state"`
     Volume           int      `json:"volume"`
     Elapsed          float64  `json:"elapsed"`
     Duration         float64  `json:"duration"`
     Percent          float64  `json:"percent"`
     Random           bool     `json:"random"`
     Consume          bool     `json:"consume"`
     Repeat           bool     `json:"repeat"`
     Single           bool     `json:"single"`
     SongPosition     int      `json:"song_position"`
     SongID           int      `json:"songID"`
     SongLength       int      `json:"song_length"`
}

type    LingerV1      struct {
     Song             int      `json:"song"`
     SongID           int      `json:"songid"`
     Count            int      `json:"count"`
     BaseLimit        int      `json:"baselimit"`
     Limit            int      `json:"limit"`
     BlockLimit       int      `json:"blocklimit"`
     Paused           bool     `json:"paused"`
     LingerXY         bool     `json:"lingerxy"`
     LingerX          int      `json:"lingerx"`
     LingerY          int      `json:"lingery"`
}

type    TimestampV1   struct {
     Epoch            int64    `json:"epoch"`
     Log              string   `json:"log"`
     Display          string   `json:"display"`
}

type    LogEntryV1    struct {
     Timestamps    TimestampV1 `json:"timestamps"`
     Action           string   `json:"action"`
     Notes            string   `json:"notes,omitempty"`
     File             string   `json:"file"`
     URL              string   `json:"url"`
     Audio            AudioV1  `json:"audio"`
}

type     StatusV1     struct {
     Player           PlayerV1 `json:"player"`
     Current          AudioV1  `json:"current"`
     Next             AudioV1  `json:"next"`
     Linger           LingerV1 `json:"linger"`
}

type Condition struct {
  Field string
  Op    string
  Value string
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
) // const

var debug = true

func dbg(f string, a ...any) {
  if debug {
    fmt.Fprintf(os.Stderr, "DEBUG: "+f, a...)
  }
} // func dbg()

func atoi(s string) int {
  i, _ := strconv.Atoi(s)
  return i
} // func atoi()

func formatDisplayTime(iso string) string {
    t, err := time.Parse("2006-01-02T15:04:05", iso)
    if err != nil {
        return iso
    }
    return t.Format("Jan 02 3:04 pm")
} // func formatDisplayTime

func isoLocalEpoch(iso string) int64 {
    // Parse ISO 8601 string
    t, err := time.Parse("2006-01-02T15:04:05", iso)
    if err != nil {
        return 0
    }
    // Convert to local epoch
    return t.Unix()
} // func isoLocalEpoch

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

/* ---------------------------------------------------- */
var mpdSock string
var mpdPassword = "seven314"

/* ============================================================
   SEND COMMAND + PARSE RESPONSE
============================================================ */

////func mpdSearch(conn net.Conn, cmd string, conds []Condition) ([]map[string]string, error) {
////
////  /* --- formerlyquoteSearchArg            --- */
////  quoteSearchArg := func (conds []Condition) string {
////
////    /* --- formerly escapeMPDFilterValue --- */
////    escapeMPDFilterValue := func(s string) string {
////      var b strings.Builder
////      b.Grow(len(s) * 2)
////
////      for i := 0; i < len(s); i++ {
////        c := s[i]
////        switch c {
////        case '\\', '"', '\'':
////          b.WriteByte('\\')
////        }
////        b.WriteByte(c)
////      }
////      return b.String()
////    }
////
////    /* --- formerly quoteMPDArg --- */
////    quoteMPDArg := func(s string) string {
////      var b strings.Builder
////      b.Grow(len(s)*2 + 2)
////
////      b.WriteByte('"')
////      for i := 0; i < len(s); i++ {
////        c := s[i]
////        if c == '"' || c == '\\' {
////          b.WriteByte('\\')
////        }
////        b.WriteByte(c)
////      }
////      b.WriteByte('"')
////
////      return b.String()
////    }
////
////    /* --- builder logic --- */
////    var parts []string
////
////    for _, c := range conds {
////      escaped := escapeMPDFilterValue(c.Value)
////      part := fmt.Sprintf("(%s %s \"%s\")",
////        c.Field,
////        c.Op,
////        escaped,
////      )
////      parts = append(parts, part)
////    }
////
////    filter := "(" + strings.Join(parts, " AND ") + ")"
////
////    return quoteMPDArg(filter)
////  }
////
////
////  searchArg := quoteSearchArg(conds)
////  searchString := cmd + " " + searchArg + "\n"
////
////  dbg("Sending command: %s", searchString)
////
////  if _, err := conn.Write([]byte(searchString)); err != nil {
////    return nil, err
////  }
////
////  reader := bufio.NewReader(conn)
////
////  var results []map[string]string
////  var current map[string]string
////
////  for {
////    line, err := reader.ReadString('\n')
////    if err != nil {
////      return nil, err
////    }
////
////    line = strings.TrimSpace(line)
////
////    if line == "OK" {
////      break
////    }
////    if strings.HasPrefix(line, "ACK") {
////      return nil, fmt.Errorf("MPD error: %s", line)
////    }
////    if line == "" {
////      continue
////    }
////
////    if strings.HasPrefix(line, "file: ") {
////      if current != nil {
////        results = append(results, current)
////      }
////      current = make(map[string]string)
////    }
////
////    parts := strings.SplitN(line, ": ", 2)
////    if len(parts) == 2 {
////      if current == nil {
////        current = make(map[string]string)
////      }
////      current[strings.ToLower(parts[0])] = parts[1]
////    }
////  }
////
////  if current != nil {
////    results = append(results, current)
////  }
////
////  return results, nil
////} // func mpdSearch
//
//
//func mpdSearch(conn net.Conn, cmd string, conds []Condition) ([]map[string]string, error) {
//
//  /* --- formerly quoteSearchArg --- */
//  quoteSearchArg := func(conds []Condition) string {
//
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

var mpdMapPool = sync.Pool{
  New: func() any {
    return make(map[string]string, 16)
  },
}

func mpdSearch(conn net.Conn, cmd string, conds []Condition) ([]map[string]string, error) {

  /* --- helper: build quoted MPD search arg --- */
  quoteSearchArg := func(conds []Condition) string {

    escape := func(s string) string {
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

    quote := func(s string) string {
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
      parts = append(parts, fmt.Sprintf("(%s %s \"%s\")", c.Field, c.Op, escape(c.Value)))
    }

    return quote("(" + strings.Join(parts, " AND ") + ")")
  }

  searchString := cmd + " " + quoteSearchArg(conds) + "\n"
  if _, err := conn.Write([]byte(searchString)); err != nil {
    return nil, err
  }

  reader := bufio.NewReader(conn)
  results := make([]map[string]string, 0, 64)
  var current map[string]string

  for {
    line, err := reader.ReadString('\n')
    if err != nil {
      return nil, err
    }
    line = strings.TrimRight(line, "\r\n")

    switch {
    case line == "OK":
      break

    case strings.HasPrefix(line, "ACK"):
      return nil, fmt.Errorf("MPD error: %s", line)

    case line == "":
      continue

    case strings.HasPrefix(line, "file: "):
      if current != nil {
        results = append(results, current)
      }
      current = mpdMapPool.Get().(map[string]string)
      for k := range current {
        delete(current, k)
      }
      current["file"] = line[6:]
      continue

    default:
      if current == nil {
        continue
      }
      idx := strings.Index(line, ": ")
      if idx < 0 {
        continue
      }
      key := strings.ToLower(line[:idx])
      val := line[idx+2:]
      current[key] = val
    }
  }

  if current != nil {
    results = append(results, current)
  }

  // return map objects to pool if needed
  // (optional: only do this if you know results won’t escape immediately)
  // for i := range results {
  //   mpdMapPool.Put(results[i])
  // }

  return results, nil
} // func mpdSearch


func directDialMPD() (net.Conn, error) {

  mpdSock = os.Getenv("MPD_SOCK")
  if mpdSock == "" {
    mpdSock = defaultMPDsocket
  }

  conn, err := net.Dial("unix", mpdSock)
  if err != nil {
    return nil, err
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


/* ============================================================
   MAIN
============================================================ */

func main() {

  /* -- won't be needed in mpdgolinger case statement: -- */
  var js map[string]interface{}
  js = map[string]interface{}{}
  /* ---------------------------------------------------- */

  conn, err :=  directDialMPD()

  defer conn.Close()

  if len(os.Args) < 2 {
    log.Fatal("missing JSON input")
  }

  input := os.Args[1]

  var payload map[string]interface{}

  if err := json.Unmarshal([]byte(input), &payload); err != nil {
    log.Fatal(err)
  }

  // this is exactly what the case will see
  cmd, _ := payload["cmd"].(string)
  argsIface := payload["args"]

  if argsIface == nil {
    log.Fatal("missing args")
  }

  args, ok := argsIface.(map[string]interface{})
  if !ok {
    log.Fatal("invalid args")
  }

  var conditions []Condition

  raw, ok := args["conds"].([]interface{})
  if !ok {
    log.Fatal("conds missing or invalid")
  }


  for _, item := range raw {

    m, ok := item.(map[string]interface{})
    if !ok {
      continue
    }

    f, _ := m["field"].(string)
    o, _ := m["op"].(string)
    v, _ := m["value"].(string)

    if f == "" || o == "" || v == "" {
      continue
    }

    conditions = append(conditions, Condition{f, o, v})
  }

  if len(conditions) == 0 {
    log.Fatal("no conditions supplied")
  }

  log.Println("cmd:", cmd)
  log.Println("conds:", conditions)

  results, err := mpdSearch(conn, cmd, conditions)
  if err != nil {
    log.Fatal(err)
  }

  dbg("Found %d entries\n", len(results))

  for i, r := range results {
    dbg("[%d]\n", i)
    for k, v := range r {
      dbg("  %s: %s\n", k, v)
    }
    log.Println()
  }

  var resp []AudioV1

  for _, song := range results {

    for k, v := range song {
      lk := strings.ToLower(k)
      if lk != k {
        delete(song, k)
        song[lk] = v
      }
    }

    var a AudioV1
    if err := convert2json(song, &a); err == nil {
      resp = append(resp, a)
    }
  }

  js["response"] = resp

  out, _ := json.Marshal(js)

  fmt.Printf("\n%s\n", string(out))

}




