package main

import (
	"github.com/fhs/gompd/v2/mpd"
	"bytes"
	"image"
	"log"
	"os/exec"
	"path"
	"path/filepath"
	_ "image/jpeg"
	_ "image/png"
	_ "golang.org/x/image/webp"
)
func getBestAlbumArt(c *mpd.Client, uri string) (*AlbumArtResult, error) {
	var (
		imgAA     []byte
		imgRP     []byte
		cfgAA     image.Config
		cfgRP     image.Config
		errAA     error
		errRP     error
		result    AlbumArtResult
    gBAAdebug bool
	)

  if debug { gBAAdebug = true }

	// --- ReadPicture ---
  result.HasEmbed = false
	imgRP, errRP = c.ReadPicture(uri)
	if errRP != nil || len(imgRP) == 0 {
		dbg("[gBAA] ReadPicture failed for %q", uri)

		if err := c.PlaylistAdd(noEmbedList, uri); err != nil {
			log.Printf("[gBAA] failed adding %q to %s: %v",
				uri, noEmbedList, err)
		}
	} else {
		cfgRP, _, errRP = image.DecodeConfig(bytes.NewReader(imgRP))
		if errRP != nil {
			dbg("[gBAA] ReadPicture decode failed for %q: %v",
				uri, errRP)
		} else {
			dbg("[gBAA] ReadPicture succeeded for %q (%dx%d, %d bytes)",
				uri, cfgRP.Width, cfgRP.Height, len(imgRP))

			result.HasEmbed = true

			if cfgRP.Width <= 500 || cfgRP.Height <= 500 {
				if err := c.PlaylistAdd(smallRPlist, uri); err != nil {
					log.Printf("[gBAA] failed adding %q to %s: %v",
						uri, smallRPlist, err)
				}
			}
		}
	}

	// --- AlbumArt ---
  result.HasAlbumArt = false
	imgAA, errAA = c.AlbumArt(uri)
	if errAA != nil || len(imgAA) == 0 {
		dbg("[gBAA] AlbumArt failed for %q", uri)

		if err := c.PlaylistAdd(noAAlist, uri); err != nil {
			log.Printf("[gBAA] failed adding %q to %s: %v",
				uri, noAAlist, err)
		}
	} else {
		cfgAA, _, errAA = image.DecodeConfig(bytes.NewReader(imgAA))
		if errAA != nil {
			log.Printf("[gBAA] AlbumArt decode failed for %q: %v",
				uri, errAA)
		} else {
      if gBAAdebug {
  			log.Printf("[gBAA] AlbumArt succeeded for %q (%dx%d, %d bytes)",
	  			uri, cfgAA.Width, cfgAA.Height, len(imgAA))
      }

			result.HasAlbumArt = true

			if cfgAA.Width <= 500 || cfgAA.Height <= 500 {
				if err := c.PlaylistAdd(smallAAlist, uri); err != nil {
					log.Printf("[gBAA] failed adding %q to %s: %v",
						uri, smallAAlist, err)
				}
			}
		}
	}

	// --- total failure ---
	if errAA != nil && errRP != nil {
		log.Printf("[gBAA] no album art available for %q", uri)

		if err := c.PlaylistAdd(noCoverList, uri); err != nil {
			log.Printf("[gBAA] failed adding %q to %s: %v",
				uri, noCoverList, err)
		}

		// mediainfo dump
    absURI := ""
    if ! filepath.IsAbs(uri) {
      absURI = filepath.Join(musicLibPrefix, uri)
    } else {
      absURI = uri
    }
		cmd := exec.Command("mediainfo", absURI)
		out, _ := cmd.CombinedOutput()
		dbg("[gBAA] mediainfo output:\n%s", out)

		// directory listing
    absDir := ""
		dir := path.Dir(uri)
    if ! filepath.IsAbs(dir) {
      absDir = filepath.Join(musicLibPrefix, dir)
    } else {
      absDir = dir
    }
		cmd = exec.Command("ls", "-l", absDir)
		out, _ = cmd.CombinedOutput()
		dbg("[gBAA] dir listing:\n%s", out)

		return nil, nil
	}

	// --- choose best image ---
	rpScore := cfgRP.Width + cfgRP.Height
	aaScore := cfgAA.Width + cfgAA.Height

	if errAA == nil && aaScore > rpScore {
		result.Img = imgAA
		result.Width = cfgAA.Width
		result.Height = cfgAA.Height
		result.Source = "AlbumArt"
	} else if errRP == nil {
		result.Img = imgRP
		result.Width = cfgRP.Width
		result.Height = cfgRP.Height
		result.Source = "ReadPicture"
	} else if errAA == nil {
		result.Img = imgAA
		result.Width = cfgAA.Width
		result.Height = cfgAA.Height
		result.Source = "AlbumArt"
	}

	// --- mime detection ---
	result.MimeType = "image/jpeg"

	if len(result.Img) > 3 {
    // PNG
		if result.Img[0] == 0x89 &&
			result.Img[1] == 0x50 &&
			result.Img[2] == 0x4E &&
			result.Img[3] == 0x47 {

			result.MimeType = "image/png"
		}
    // WEBP (RIFF....WEBP)
    if string(result.Img[0:4]) == "RIFF" &&
      string(result.Img[8:12]) == "WEBP" {

      result.MimeType = "image/webp"
    }
	}

  result.Size = len(result.Img)
  result.Hash = fnv1a(result.Img)

	dbg("[gBAA] selected %s for %q (result.hash=%d; W×H=%dx%d; %d bytes)",
		result.Source,
		uri,
    result.Hash,
		result.Width,
		result.Height,
		len(result.Img),
	)

	return &result, nil
} // func getBestAlbumArt()

func getAlbumArtCached(c *mpd.Client, file string) ([]byte, error) {
  // Pull album art directly from MPD
  return c.AlbumArt(file)
}


