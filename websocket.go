package main

import (
	"github.com/coder/websocket"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)
func cid(c *websocket.Conn) string {
  connIDMu.Lock()
  id := connID[c]
  connIDMu.Unlock()
  return id
}

var wsWatcherCount int64

/*
func wsHandler(w http.ResponseWriter, r *http.Request) {

  conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
    InsecureSkipVerify: true,
    CompressionMode: websocket.CompressionContextTakeover, // or websocket.CompressionContextNone
//    CompressionThreshold: 1024,
  })

  log.Printf("[wsH] ACCEPT pid=%d conn=%p remote=%s", os.Getpid(), conn, r.RemoteAddr)
  log.Printf("[wsH] UPGRADE hdrs=%+v", r.Header)
  log.Printf("[wsH] conn=%p remote=%s xff=%v", conn, r.RemoteAddr, r.Header.Get("X-Forwarded-For"))

  dbg("[wsH] WS extension request: %s", r.Header.Get("Sec-WebSocket-Extensions"))

  conn.SetReadLimit(512 * 1024) // 512 KB max frame size

  if err != nil {
    log.Printf("[wsH] accept failed: %v", err)
    return
  }

  ctx := &wsCtx{
    conn:  conn,
//    conns: globalWSCtx.conns, // share map for all watchers
  }

  // add connection to global set
  ctx.mu.Lock()
  ctx.conns[conn] = struct{}{}
  ctx.mu.Unlock()
  log.Printf("[wsH] connection added")

  // remove connection on exit
  defer func() {
    ctx.mu.Lock()
    delete(ctx.conns, conn)
    ctx.mu.Unlock()
    conn.Close(websocket.StatusNormalClosure, "done")
    log.Printf("[wsH] connection removed")
  }()

  // start wsWatcher for this connection
  log.Printf("[wsH] NEW conn ptr=%p remote=%v", conn, r.RemoteAddr)
  go wsWatcher(ctx)
  atomic.AddInt64(&wsWatcherCount, 1)
  log.Printf("[wsW] watcher count=%d START", atomic.LoadInt64(&wsWatcherCount))
  defer atomic.AddInt64(&wsWatcherCount, -1)

  // helper write func: always use this for safe sends
  write := func(c *websocket.Conn, typ websocket.MessageType, payload []byte) bool {
    wctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    err := c.Write(wctx, typ, payload)
    cancel()
    if err != nil {
      log.Printf("[wsH] write failed, removing conn: %v", err)
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
      log.Printf("[wsH] read error: %v", err)
      break
    }

    dbg("[wsH] incoming raw size: %d bytes", len(msgBytes))

    msg := string(msgBytes)

    dbg("[wsH] incoming decompressed size: %d bytes", len(msg))

    // detect JSON messages
    var js map[string]interface{}
    if err := json.Unmarshal(msgBytes, &js); err == nil {
      log.Printf("[wsH] received JSON: %s", msg)
      // log raw incoming message for debugging
      dbg("[wsH] incoming: msg=%s", string(msg))

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
*/


func wsHandler(w http.ResponseWriter, r *http.Request) {

  conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
    InsecureSkipVerify: true,
    CompressionMode: websocket.CompressionContextTakeover,
  })
  if err != nil {
    log.Printf("[wsH] accept failed: %v", err)
    return
  }

  log.Printf("[wsH] connected %p", conn)

  // register connection
  wsMu.Lock()
  wsConns[conn] = struct{}{}
  wsMu.Unlock()

globalWSHub.mu.Lock()
globalWSHub.conns[conn] = struct{}{}
globalWSHub.mu.Unlock()

  // assign connIDs
  id := fmt.Sprintf("ws-%d", nextID.Add(1))

  connIDMu.Lock()
  connID[conn] = id
  connIDMu.Unlock()

  // cleanup on exit
  defer func() {
    wsMu.Lock()
    delete(wsConns, conn)
    wsMu.Unlock()

    conn.Close(websocket.StatusNormalClosure, "bye")
    log.Printf("[wsH] disconnected %p", conn)
  }()

  conn.SetReadLimit(512 * 1024)

  // helper write
  write := func(c *websocket.Conn, typ websocket.MessageType, payload []byte) bool {
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()

    err := c.Write(ctx, typ, payload)
    if err != nil {
      log.Printf("[wsH] write failed: %v", err)

      wsMu.Lock()
      delete(wsConns, c)
      wsMu.Unlock()

      c.Close(websocket.StatusNormalClosure, "")
      return false
    }
    return true
  }

  // read loop
  for {
    _, msgBytes, err := conn.Read(r.Context())
    if err != nil {
      log.Printf("[wsH] read error: %v", err)
      break
    }

    msg := string(msgBytes)

    // JSON path
    var js map[string]any
    if json.Unmarshal(msgBytes, &js) == nil {

      var req Request
      _ = json.Unmarshal(msgBytes, &req)

      responses := verbProcessorJSON(js, req, conn)

      for _, line := range responses {
        if line != "" {
          write(conn, websocket.MessageText, []byte(line))
        }
      }
      continue
    }

    // text path
    responses := verbProcessor(msg)
    for _, line := range responses {
      if line != "" {
        write(conn, websocket.MessageText, []byte(line))
      }
    }
  }
} // func wsHandler


func wsBroadcastMgr(msgs []wsMsg, target *websocket.Conn) {
  debugMsgs := func(msgs []wsMsg) []any {
    out := make([]any, 0, len(msgs))

    for _, m := range msgs {
      if m.typ == websocket.MessageBinary {
        out = append(out, map[string]any{
          "type": "binary",
          "len":  len(m.data),
        })
        continue
      }

      var v any
      if err := json.Unmarshal(m.data, &v); err != nil {
        out = append(out, map[string]any{
          "type": "text",
          "error": err.Error(),
          "raw":   string(m.data),
        })
        continue
      }

      out = append(out, truncateJSON(v))
    }

    return out
  }

  var b []byte
  to := "all subscribed clients"
  if target != nil { to = cid(target) }

  if debug {
    b, _ = json.MarshalIndent(debugMsgs(msgs), "", "  ")
  }

  log.Printf("[wsBM] started, broadcasting to: %s. %s\n", to, string(b))

  // --- push to subscribed connections ---
  globalWSHub.mu.Lock()

  var recipients []*websocket.Conn

  if target != nil {
    recipients = []*websocket.Conn{target}
  } else {
    for conn := range globalWSHub.conns {
      recipients = append(recipients, conn)
    }
  }

  globalWSHub.mu.Unlock()

  log.Printf("[wsBM] recepient count=%d", len(recipients))

  if debug {
    for _, conn := range recipients {
      log.Printf("[wsBM] conn: %s", cid(conn))
    }
  }

  for _, conn := range recipients {
    dead := false

    dbg("[wsBM] primary for push loop: %v", cid(conn))

    write := func(typ websocket.MessageType, payload []byte) bool {
      wctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
      defer cancel()

      err := conn.Write(wctx, typ, payload)
      if err != nil {
        log.Printf("[wsBM] write failed, removing conn: %v", err)
        conn.Close(websocket.StatusNormalClosure, "")
        dead = true
        return false
      }
      return true
    }

    log.Printf("[wsBM] broadcast start conn=%s", cid(conn))

    for _, m := range msgs {
      if !write(m.typ, m.data) {
        dead = true
        break
      } else if m.typ == 1 {
        var v any
        if err := json.Unmarshal(m.data, &v); err != nil {
          log.Printf("[wsBM] bad json: %v", err)
        } else {
          data := truncateJSON(v)
          log.Printf("[wsBM] to: %s, websocket.MessageText: %#v", cid(conn), data)
        }
      } else if m.typ == 2 {
//          var artHash uint64
//          if art != nil {
//            artHash = art.Hash
//          }
//          log.Printf("[wsBM] >>>>>>>>>>>>>>>>>>>>>>>>>>> websocket.MessageBinary len(m.data): %d; art.hash: %d; lastArtHash: %d", len(m.data), artHash, lastArtHash)
          log.Printf("[wsBM] Pushed binary data frame message to %s", cid(conn))
      }
    }

    if dead {
      globalWSHub.mu.Lock()
      delete(globalWSHub.conns, conn)
      globalWSHub.mu.Unlock()
      continue
    }

    dbg("[wsBM] broadcast complete conn=%p", cid(conn))
  }
} // func wsBroadcastMgr


// truncateJSON recursively finds and truncates floats and strings inside the JSON any value
func startWS(port int) {
  http.HandleFunc("/ws", wsHandler)

  go func() {
    addr := fmt.Sprintf(":%d", port)
    log.Printf("[sWS] WS listening on :%s (/ws)", addr)
    if err := http.ListenAndServe(addr, nil); err != nil {
      log.Fatalf("[sWS] ws server failed: %v", err)
    }
  }()
} // func startWS()


// runXYStep executes one XY shuffle step using playlist positions ("song").
// Assumes globals: xy.start, xy.end, song, verbose
