package main

import (
    "fmt"
    "log"

    "github.com/fhs/gompd/v2/mpd"
)

func main() {
    client, err := mpd.Dial("tcp", "localhost:6600")
    if err != nil {
        log.Fatalf("Failed to connect to MPD: %v", err)
    }
    defer client.Close()

    mbid := "223140a9-b13a-4a9a-b3c2-f718e0bcfb0"

    // Just build the command object
    cmd := client.Command("playlistsearch", fmt.Sprintf("(MUSICBRAINZ_ALBUMID == '%s')", mbid))

    // Print the raw command object
    fmt.Printf("Command object: %+v\n", cmd)
}
