package main

import (
	"github.com/fhs/gompd/v2/mpd"
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"net/url"
)
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

    // --- pauseTimer ---
    state.mu.Lock()
    dbg("[c2j] state.pauseTimer.Endtime: %s", state.pauseTimer.EndTime)
    dst.PauseTimer = TimerV1{
      Active:    state.pauseTimer.Active,
      Duration:  state.pauseTimer.Duration,
      EndTime:   state.pauseTimer.EndTime,
      Remaining: int(math.Max(0, time.Until(state.pauseTimer.EndTime).Seconds())),
    }

    // --- scrobbler ---
    dst.Scrobbler  = state.scrobbler

    state.mu.Unlock()
    pulseMu.RLock()
    dst.PulseData = PulseData
    pulseMu.RUnlock()
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

  if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
    m := make(map[string]string)
      m["title"] = target
      m["artist"] = "Streaming Artist"
      m["album"] = "Streaming Album"
//      m.Artist = audioMap["artist"]
//      m.Album = audioMap["album"]
//      m.Year = audioMap["year"]
//      m.Duration, _ = strconv.ParseFloat(audioMap["duration"], 64)
//      m.Time = atoi(audioMap["time"])
//      m.Disc = atoi(audioMap["disc"])
//      m.Track = atoi(audioMap["track"])
//      m.MBAlbumID = audioMap["musicbrainz_albumid"]
//      m.MBReleaseTrackID = audioMap["musicbrainz_releasetrackid"]
//      m.MBArtistID = audioMap["musicbrainz_artistid"]

    return m, []string{"stream URL"}, nil
  }

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
    ds := deriveStateLocked(state.lastSongZI, state.lastSongID, state.lastSongURI, limit)
    state.mu.Unlock()
    dbg("DBG xy.active=%v\n", xy.active)

    m["writetime"] = ds.WriteTime
    m["lingersong"] = strconv.Itoa(ds.SongZI + 1)
    m["lingersongid"] = strconv.Itoa(ds.SongID)
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
  // instead of returning an error, return empty song map
  return map[string]string{}, notes, nil


  if song != nil {
    return song, notes, nil
  }

  // --- Nothing found ---
  // instead of returning an error, return empty song map
  return map[string]string{}, notes, nil

} // func MPDtags()

func runXYStep(c *mpd.Client) error {
  st, err := c.Status()
  if err != nil {
      return err
  }
  songZI, _ := strconv.Atoi(st["song"])
  if xy.end <= xy.start+1 {
    log.Printf("[rXYS] completed: x=%d y=%d", xy.start, xy.end)
    xy.active = false
    return nil
  }

  if songZI != xy.start {
    log.Printf("[rXYS] song %d not %d, disabling XY", songZI, xy.start)
    xy.active = false
    _ = mpdDo(func(c *mpd.Client) error {
      if err := c.Consume(state.consume); err != nil {
        log.Printf("[rXYS] failed to restore consume=%t: %v", state.consume, err)
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
    log.Printf("[rXYS] pick: r=%d → %d", r, xy.start+1)
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
func runIdleLoop(w *mpd.Watcher) error {
  var rILdebug  bool

  log.Println("[rIL] MPD connection established, entering idle loop")

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
      log.SetFlags(log.LstdFlags | log.Lmicroseconds)
      // keep this very explicit so we can trace EOF vs broken pipe
      log.Printf("[rIL] Watcher error: %v", err)
      log.SetFlags(log.LstdFlags)
      if errors.Is(err, io.EOF) {
        log.Printf("[rIL] Watcher got EOF, exiting watcher")
        return
      }
      time.Sleep(5 * time.Second)
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
      log.Println("[rIL] received shutdown")
      return nil

    case <-ticker.C:
      state.mu.Lock()

      expired := state.pauseTimer.Active &&
                 time.Now().After(state.pauseTimer.EndTime)

      if !expired {
        state.mu.Unlock()
        continue
      }

      log.Printf("[rIL] pauseTimer expired")

      shouldPause := state.MPDplaying

      // clear pauseTimer state FIRST (under lock)
      state.pauseTimer.Active = false
      state.pauseTimer.Duration = 0
      state.pauseTimer.EndTime = time.Time{}

      state.mu.Unlock()

      // THEN do MPD work
      if shouldPause {
        err := mpdDo(func(c *mpd.Client) error {
          return c.Pause(true)
        }, "pauseTimer-expire-pause")

        if err != nil {
          log.Printf("[rIL] pauseTimer pause failed: %v", err)
        }
      }

      continue

    case subsystem, ok := <-w.Event:
      if !ok {
        return fmt.Errorf("watcher closed")
      }

      if rILdebug { log.Printf("[rIL] Idle event subsystem=%s", subsystem) }

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
        if rILdebug { logCurrentSong(c, "[rIL] idle event (pre-status)") }

        // Fetch current player status.
        //
        // If this fails, the connection is already bad and the
        // supervisor must reconnect.
        //
        current, err := c.CurrentSong()
        if err != nil {
          return err
        }

        status, err := c.Status()
        if err != nil {
          // return so outer code treats this as a status error and reconnects
          return err
        }

        songID, _      := strconv.Atoi(status["songid"])
        prevSongID     := state.lastSongID
        songZI, _      := strconv.Atoi(status["song"])  // Zero-Indezed song playlist position
        plRev, _       := strconv.Atoi(status["playlist"])
        songURI        := current["file"]
        mbid           := current["MUSICBRAINZ_ALBUMID"]
        album          := current["Album"]
        albumKey       := status["songid"]
        albumArtist    := current["AlbumArtist"]
        if albumArtist == "" {
          albumArtist  =  "<unavailable>"
        }

        switch {
          case mbid != "":
            albumKey = mbid

          case songURI != "":
            if idx := strings.LastIndex(songURI, "/"); idx > 0 {
              albumKey = songURI[:idx]
            } else {
              albumKey = songURI
            }

          case albumArtist != "" && albumArtist != "<unavailable>" && album != "":
            albumKey = albumArtist + " -- " + album

          default:
            albumKey = "[songID=" + status["songid"] + "] " +albumArtist + " -- " + album
        }

        dbg("[rIL] songURI = %s", songURI)
        dbg("[rIL] albumKey=%s", albumKey)

ev := IdleEvent{
    Subsystem:   subsystem,
    SongID:      songID,
    PrevSongID:  prevSongID,
    SongURI:     songURI,
    Status:      status,
    PlaylistRev: plRev,
    AlbumKey:    albumKey,
}

log.Printf("[rIL] EMIT event rev=%d evptr=%p songID=%d", ev.PlaylistRev, &ev, ev.SongID)
idleEvents <- ev

/*        idleEvents <- IdleEvent{
          Subsystem:   subsystem,
          SongID:      songID,
          SongURI:     songURI,
          Status:      status,
          PlaylistRev: plRev,
          AlbumKey:    albumKey,
        }
*/

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
            state.prevSongID = state.lastSongID
            state.prevSongURI = state.lastSongURI

            state.lastSongID   = songID
            log.Printf("[rIL-6617] ==== setting state.lastSongID = songID: %d", state.lastSongID)
            state.lastSongURI  = songURI
            state.lastAlbumKey = albumKey
            state.count++ // keep counting while paused (matches bash behavior)
            log.Printf("[rIL] Paused: song advanced, count=%d (limit=%d)", state.count, limit)
            log.Printf("[rIL] Paused: state.lastSongURI=%s", state.lastSongURI)
          } else {
            log.Printf("[rIL] Paused: idle event, song unchanged")
          }
          deriveStateLocked(songZI, songID, songURI, limit)  // <--- write after increment to update state count while paused
        }

        // If songID did not change, this idle event was caused by:
        //   • seek
        //   • pause/resume
        //   • repeat/random toggles
        //   • other non-track-boundary events
        //
        if songID == state.lastSongID {
          // nothing changed; keep trace so we can see frequent idle hits
          dbg("[rIL] Idle event received but songID unchanged: %d", songID)
//          state.mu.Unlock()
          // Log currentsong again so we can verify the *file*
          // really stayed the same across the idle break.
          logCurrentSong(c, "[rIL] idle event (songID unchanged)")
          return nil
        }

        // From here on, MPD has started a *new song*.
        // Update the FSM accordingly.
        //
        prevCount := state.count
        prevTransition := state.transition
        if songID != state.lastSongID {
          state.prevSongID   = state.lastSongID
          state.prevSongURI  = state.lastSongURI
          state.prevAlbumKey = state.lastAlbumKey
        }
        state.lastSongID     = songID
        log.Printf("[rIL-6656] ==== setting state.lastSongID = songID: %d", state.lastSongID)
        state.lastSongURI    = songURI
        state.lastAlbumKey   = albumKey

        // ---------- INSERTED XY HANDLER ----------
        if xy.active {
          // addresses https://github.com/iconoclasthero/mpdgolinger/issues/19
          plLen, err := strconv.Atoi(status["playlistlength"])
          if err != nil {
            log.Printf("[rIL] [XY] invalid playlistlength: %v", err)
            return nil
          }
          max := plLen - 1
          if xy.end > max {
            log.Printf(
              "[rIL] [XY] clamping Y from %d to %d (playlist shrank)",
              xy.end, max,
            )
            xy.end = max
          }
          if songZI != xy.start {
            state.transition = true
            xy.active = false

            if err := c.Consume(state.consume); err != nil {
              log.Printf("[rIL] [XY] failed to restore consume=%t: %v", state.consume, err)
            }

            log.Printf("[rIL] song %d not %d, disabling XY", songZI, xy.start)
            log.Printf("[rIL] state.transtion: %t; xy.active: %t", state.transition, xy.active)
            return nil
          }
          log.Printf("[rIL] Calling runXYStep, playlist postion: %d (ZI)", songZI)
          err = runXYStep(c)
          if err != nil {
            log.Printf("[rIL] runXYStep failed: %v", err)
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
            log.Printf("[rIL] random(false) failed: %v", err)
            // Do NOT abort: random failure should not desync FSM
          }

          state.blockLimit = 0
          state.blockOn = false
          state.count = 1
          state.transition = false
          log.Printf("[rIL] Transition: random off, count reset to 1")

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
            log.Printf("[rIL] random(true) failed: %v", err)
          }

          state.transition = true
          log.Printf("[rIL] Last song in block reached: random on, transition true")

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
        deriveStateLocked(songZI, songID, songURI, limit)

        // Log the song again *after* state changes and random toggles.
        // This lets us confirm whether MPD advanced tracks as expected.
        //
        if rILdebug { logCurrentSong(c, "[rIL] idle event (post-state-change)") }

        _ = prevCount
        _ = prevTransition
//        state.mu.Unlock()

        return nil

      }, "idleLoop-event")

      if err != nil {
        // Make the error explicit and return so daemonSupervisor can reconnect
        log.Printf("[rIL] MPD status fetch failed: %v — will attempt reconnect", err)

        // Best-effort logging of currentsong during failure path.
        _ = mpdDo(func(c *mpd.Client) error {
          logCurrentSong(c, "[rIL] idle error path")
          return nil
        }, "idle-error-log")

        log.Println("[rIL] MPD connection closed")
        return fmt.Errorf("status error: %w", err)
      }
    }
  }

  // If we exit the loop, the watcher closed.
  return fmt.Errorf("watcher closed")
} // func runIdleLoop()


// startIPC starts a UNIX socket server to accept client commands
