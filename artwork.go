package main

import (
  "encoding/json"
  "fmt"
  "hash/fnv"
  "image"
  _ "image/gif"
  _ "image/jpeg"
  _ "image/png"
  "io"
  "os"
  "path/filepath"
  "strings"

  "github.com/dhowden/tag"
  _ "golang.org/x/image/webp"
  "log"
)

var musicDirPrefix = "/library/music"

type FSCandidate struct {
  Path       string `json:"path"`
  MimeType   string `json:"mimeType"`
  Width      int    `json:"width"`
  Height     int    `json:"height"`
  Size       int64  `json:"size"`
  Hash       uint64 `json:"hash,string"`
  IsAA       bool   `json:"isAA"`
  IsAnimated bool   `json:"isAnimated"`
  Source     string `json:"source"`
}

type RPCandidate struct {
  Path     string   `json:"path"`
  MimeType string   `json:"mimeType"`
  Width    int      `json:"width"`
  Height   int      `json:"height"`
  Size     int64    `json:"size"`
  Hash     uint64   `json:"hash,string"`
  Tracks   []string `json:"tracks"`
  Source   string   `json:"source"`
}

type ArtworkResponse struct {
  AlbumPath string        `json:"albumPath"`
  FS        []FSCandidate `json:"fs"`
  RP        []RPCandidate `json:"rp"`
}

//func fnvHash(b []byte) string {
//  h := fnv.New64a()
//  h.Write(b)
//  return fmt.Sprintf("%x", h.Sum(nil))
//}

func fnv1a(data []byte) uint64 {
  h := fnv.New64a()
  _, _ = h.Write(data)
  return h.Sum64()
}

func imageMeta(path string) (string, int, int, int64, uint64, error) {
  f, err := os.Open(path)
  if err != nil {
    return "", 0, 0, 0, 0, err
  }
  defer f.Close()

  cfg, format, err := image.DecodeConfig(f)
  if err != nil {
    return "", 0, 0, 0, 0, err
  }

  _, err = f.Seek(0, 0)
  if err != nil {
    return "", 0, 0, 0, 0, err
  }

  data, err := io.ReadAll(f)
  if err != nil {
    return "", 0, 0, 0, 0, err
  }

  info, _ := os.Stat(path)

  return "image/" + format,
    cfg.Width,
    cfg.Height,
    info.Size(),
    fnv1a(data),
    nil
}

func main() {
//  albumDir := os.Args[1]
  type Req struct {
    Path string `json:"path"`
  }

  var req Req
  if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
    log.Fatal(err)
  }

  if !filepath.IsAbs(req.Path) {
    req.Path = filepath.Join(musicDirPrefix, req.Path)
  }

  albumDir := req.Path



  var fsEntries []FSCandidate
  rpMap := map[uint64]*RPCandidate{}

  aaPriority := []string{
    "cover.png",
    "cover.jpg",
    "cover.jxl",
    "cover.webp",
  }

  aaFile := ""

  for _, name := range aaPriority {
    p := filepath.Join(albumDir, name)
    if _, err := os.Stat(p); err == nil {
      aaFile = p
      break
    }
  }

  filepath.Walk(albumDir, func(path string, info os.FileInfo, err error) error {
    if err != nil || info.IsDir() {
      return nil
    }

    ext := strings.ToLower(filepath.Ext(path))

    switch ext {

    case ".jpg", ".jpeg", ".png", ".gif", ".webp":
      mime, w, h, size, hash, err := imageMeta(path)
      if err != nil {
        return nil
      }

      fsEntries = append(fsEntries, FSCandidate{
        Path:       path,
        MimeType:   mime,
        Width:      w,
        Height:     h,
        Size:       size,
        Hash:       hash,
        IsAA:       path == aaFile,
        IsAnimated: ext == ".gif" || ext == ".webp",
        Source:     "fs",
      })

    case ".flac", ".mp3", ".m4a":
      f, err := os.Open(path)
      if err != nil {
        return nil
      }
      defer f.Close()

      meta, err := tag.ReadFrom(f)
      if err != nil {
        return nil
      }

      pic := meta.Picture()
      if pic == nil {
        return nil
      }

      cfg, _, err := image.DecodeConfig(strings.NewReader(string(pic.Data)))
      if err != nil {
        return nil
      }

      hash := fnv1a(pic.Data)

      if existing, ok := rpMap[hash]; ok {
        existing.Tracks = append(existing.Tracks, path)
        return nil
      }

      rpMap[hash] = &RPCandidate{
        Path:     path,
        MimeType: pic.MIMEType,
        Width:    cfg.Width,
        Height:   cfg.Height,
        Size:     int64(len(pic.Data)),
        Hash:     hash,
        Tracks:   []string{path},
        Source:   "rp",
      }
    }

    return nil
  })

  var rpEntries []RPCandidate
  for _, v := range rpMap {
    rpEntries = append(rpEntries, *v)
  }

  resp := ArtworkResponse{
    AlbumPath: albumDir,
    FS:        fsEntries,
    RP:        rpEntries,
  }

  out, _ := json.MarshalIndent(resp, "", "  ")
  fmt.Println(string(out))
}
