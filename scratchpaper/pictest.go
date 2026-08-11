package main

import (
  "flag"
  "bytes"
  "context"
  "encoding/json"
  "fmt"
  "os"
  "time"
  "github.com/coder/websocket"
)

func main() {
  // parse flags
  suppressLogs := flag.Bool("l", false, "suppress loglines")
  flag.Parse()

  conn, _, err := websocket.Dial(context.Background(), "ws://localhost:8008/ws", nil)
  if err != nil {
    panic(err)
  }
  defer conn.Close(websocket.StatusNormalClosure, "done")

  // Subscribe
  msg := []byte(`{"system":"websocket","cmd":"subscribe"}`)
  conn.Write(context.Background(), websocket.MessageText, msg)

  // Increase read limit to 4 MiB for testing album art
  conn.SetReadLimit(4 * 1024 * 1024)

  for {
    typ, data, err := conn.Read(context.Background())
    if err != nil {
      fmt.Println("read error:", err)
      break
    }

    switch typ {
    case websocket.MessageText:
      // optionally suppress loglines
      if *suppressLogs && bytes.HasPrefix(data, []byte(`{"timestamps"`)) {
        continue
      }
      var pretty bytes.Buffer
      if err := json.Indent(&pretty, data, "", "  "); err != nil {
        fmt.Println("TEXT:", string(data)) // fallback if invalid JSON
      } else {
        fmt.Println("TEXT:", pretty.String())
      }
    case websocket.MessageBinary:
      ts := time.Now().Format("2006-01-02 15:04:05")
      filename := ts + "-albumart.jpg"
      os.WriteFile(filename, data, 0644)
      fmt.Printf("[%s] Saved album art to %s", ts, filename)
    }
  }
}

