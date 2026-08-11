package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
)
func refreshPulse() (PulseV1, error) {
  type sink struct {
    Name   string `json:"name"`
    State  string `json:"state"`
    Mute   bool   `json:"mute"`
    Volume map[string]struct {
      Percent string `json:"value_percent"`
    } `json:"volume"`
  }

  serverFlag := fmt.Sprintf("--server=%s:%d", pulseServer, pulsePort)

  defaultNameBytes, err := exec.Command(
    pulsePath,
    serverFlag,
    "get-default-sink",
  ).Output()

//  cmd := exec.Command("/usr/bin/pactl", "--server=127.0.0.1:4713", "--format=json", "list", "sinks")
  defaultName := strings.TrimSpace(string(defaultNameBytes))

  cmd := exec.Command(pulsePath, serverFlag, "--format=json", "list", "sinks")

  out, err := cmd.CombinedOutput()
  if err != nil {
    return PulseV1{}, fmt.Errorf("pulse failed: %v | %s", err, string(out))
  }

  var sinks []sink
  if err := json.Unmarshal(out, &sinks); err != nil {
    return PulseV1{}, err
  }

  var s *sink
  log.Printf("[rP] sink defaultName: %s", defaultName)
  for i := range sinks {
    if sinks[i].Name == defaultName {
      s = &sinks[i]
      break
    }
  }
  if s == nil && len(sinks) > 0 {
    s = &sinks[0]
  }
  if s == nil {
    return PulseV1{}, fmt.Errorf("no sinks found")
  }

  pd := PulseV1{
    SinkName: s.Name,
    Mute:     s.Mute,
    Channels: make(map[string]int),
    PulsePath: pulsePath,
    PulseServer: pulseServer,
    PulsePort: pulsePort,
  }

  total := 0
  count := 0

  for ch, v := range s.Volume {
    p := strings.TrimSuffix(v.Percent, "%")
    val, err := strconv.Atoi(strings.TrimSpace(p))
    if err != nil {
      continue
    }
    pd.Channels[ch] = val
    total += val
    count++
  }

  if count > 0 {
    pd.Volume = total / count
  }

  if len(pd.Channels) == 2 {
    vals := make([]int, 0, 2)
    for _, v := range pd.Channels {
      vals = append(vals, v)
    }
    pd.VolString = fmt.Sprintf("Volume: %d%% – %d%%", vals[0], vals[1])
  } else {
    pd.VolString = fmt.Sprintf("Volume: %d%%", pd.Volume)
  }

  return pd, nil
} // func refreshPulse


func pulseWatcher(ctx context.Context, out chan<- PulseV1) {
  serverFlag := fmt.Sprintf("--server=%s:%d", pulseServer, pulsePort)
  cmd := exec.CommandContext(ctx, pulsePath, serverFlag, "subscribe")
  stdout, err := cmd.StdoutPipe()
  if err != nil {
    return
  }

  if err := cmd.Start(); err != nil {
    return
  }

  scanner := bufio.NewScanner(stdout)

  var lastpd PulseV1
  for scanner.Scan() {
    line := scanner.Text()
    // only care about sink events
    if !strings.Contains(line, "sink") {
      continue
    } else if strings.Contains(line, "sink-input") {
      continue
    } else {
      dbg("[pW] line := scanner.Text %s", line)
    }

    pd, err := refreshPulse()
    dbg("[pW] pd, err := refreshPulse() %q", pd)
    if err == nil && !reflect.DeepEqual(pd, lastpd) {
      out <- pd
      idleEvents <- IdleEvent{
        Subsystem: "pulseaudio",
        PulseData:  pd,
      }
    }
    lastpd = pd
  }
} // func pulseWatcher

