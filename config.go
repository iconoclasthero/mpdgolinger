package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)
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
