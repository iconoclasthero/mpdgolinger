package main

import (
	"github.com/fhs/gompd/v2/mpd"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
)
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


func getLogHash() string               { return "" }
func getLogJSON() interface{}          { return nil }
func logStateChange(src string, songID int, count, limit int, transition bool) {
  log.Printf(
    "[lSC] STATE CHANGE: [%s]: songID=%d count=%d/limit=%d transition=%v",
    src, songID, count, limit, transition,
  )
} // func logStateChange(src, songID string, count, limit int, transition bool)


// logCurrentSong logs the current song’s file for debugging
func logCurrentSong(c *mpd.Client, prefix string) {
  if c == nil {
    log.Printf("[lCS] %s: currentsong skipped (nil client)", prefix)
    return
  }

  song, err := c.CurrentSong()
  if err != nil {
    log.Printf("[lCS] %s: currentsong error: %v", prefix, err)
    return
  }

  file := song["file"]
  if file == "" {
    log.Printf("[lCS] %s: currentsong: <no file field>", prefix)
    return
  }

  log.Printf("[lCS] %s: currentsong file=%q", prefix, file)
} // func logCurrentSong(c *mpd.Client, prefix string)


// daemonSupervisor ensures the daemon’s main loops, reconnects, and IPC listener
