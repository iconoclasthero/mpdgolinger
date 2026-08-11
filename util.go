package main

import (
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)
func fnv1a(data []byte) uint64 {
  h := fnv.New64a()
  _, _ = h.Write(data)
  return h.Sum64()
} // func fnv1a

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


func truncateJSON(v any) any {
  switch val := v.(type) {
  case []byte:
    return "msg is binary data"

  case float64:
    // Truncate decimals (e.g., 3.14159 -> 3)
    return math.Trunc(val)

  case string:
    // Truncate long string values (e.g., limit to 10 characters)
    runes := []rune(val)
    if len(runes) > 11 {
      return string(runes[:11]) + "..."
    }
    return val

  case map[string]any:
    // Recursively process JSON objects
    newMap := make(map[string]any, len(val))
    for k, v := range val {
      // Skip the key entirely if it matches "response"
      if k == "response" {
        continue
      }
      newMap[k] = truncateJSON(v)
    }
    return newMap

  case []any:
    // Recursively process JSON arrays
    newSlice := make([]any, len(val))
    for i, v := range val {
      newSlice[i] = truncateJSON(v)
    }
    return newSlice

  default:
    // Return booleans, nulls, and other types as-is
    return val
  }
} // func truncateJSON

// verbProcessorJSON handles JSON commands from the WebSocket and returns
// JSON-formatted responses. Does NOT send over the WebSocket — wsHandler does that.
//func verbProcessorJSON(js map[string]interface{}, req Request, ctx *wsCtx) []string {
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
func btoi(b bool) int {
  if b {
    return 1
  }
  return 0
} // func btoi(b bool)


// requestShutdown closes the shutdown channel exactly once
