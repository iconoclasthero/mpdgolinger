# mpdgolinger

**mpdgolinger** is a Go-based daemon for managing MPD (Music Player Daemon) playback with block limits, idle supervision, and IPC commands.

## Background

My collection has roughly 80,000 songs an 7,000 artists and my current playlist contains ca. 73,000. I generally add music by the directory so—in theory—I should have a playlist that consists of albums by artist, roughly alphabetically. In pure random mode, mpd should play just one song by an artist (save for over-represented artists in the collection). 

Classic rock stations have long had gimmicks like "Rock Blocks," "Double Shots," "Twofer Tuesdays," "Workfoce blocks," etc., where multiple songs from the same artist or around a common theme were played consecutively. That's essentially what this does. Set your limit=_n_ and mpd playback will linger on the playlist for _n_ songs before advancing on random to another block. This is compatible with consume and repeat playback modes. If you're really digging say a particular Dead bootleg and want to hear more than _n_ songs for a block, you can temporarily override the limit for a block.

## Features

- Persistent block counting and limiting
- IPC commands (status, pause, resume, limit, block[limit], next, skip, quit)
- Idle supervision for MPD events
- Optional state file for persistent tracking
- Daemon mode with --daemon
- Supports TCP or UNIX socket connection to MPD

## Installation

$ go build -o mpdgolinger mpdgolinger.go

Optional: For static builds:

$ CGO_ENABLED=0 go build -a -ldflags '-extldflags "-static"' -o mpdgolinger mpdgolinger.go

## Usage

### Daemon Mode

$ ./mpdgolinger --daemon --limit 3 --mpdhost localhost --mpdport 6600

Optional flags:

  --mpdsocket <path>  : MPD server socket, (e.g., `/run/mpd/socket`)
- --mpdhost <host>    : MPD server host (default localhost)
- --mpdport <port>    : MPD server TCP port (default 6600)
- --state <path>      : Optional path for persistent state file
- --version           : Prints version and mpd protocol/binary versions
- --help              : Prints help
- ~~--socket <path>     : IPC socket path (required in daemon mode)~~
- ~~--listen <host>     : IPC listen address~~
- ~~--listenport <port> : IPC listen port~~

### Client Mode

Run commands against the running daemon:

```
$ ./mpdgolinger status    # prints one-line status message; serve as ping
$ ./mpdgolinger pause     # pauses linger function; mpd playback unchanged
$ ./mpdgolinger resume    # resumes linger function; mpd playback unchanged
$ ./mpdgolinger limit 5   # resets the ongoing limit to e.g., 5
$ ./mpdgolinger block 3   # sets a limit of e.g., 3 to current block only
$ ./mpdgolinger next      # skips to the next song and block
$ ./mpdgolinger skip      # skips to the next song within block
$ ./mpdgolinger quit      # exits daemon
```

## Development

- Go 1.21+
- Uses github.com/fhs/gompd/v2/mpd for MPD communication.

## Notes

- IPC socket must exist and be writable by clients.
- The IPC socket is presently hard-coded to `/var/lib/mpd/mpdlinger/mpdlinger.sock`
- Daemon supervises both MPD idle events and the IPC socket, reconnecting if necessary.

## Statefile

  To create an optional state file that can be parsed/sourced for other uses, enable the `--state <path>` when launching the daemon.  The format of the state file is:
```
writetime=2025-12-20T10:35:57.330310379-05:00
lingersongid=139234  # to verify sync with other MPD clients
lingerpause=0
lingercount=1
lingerbase=4
lingerlimit=4
lingerblocklmt=5     # 0 if blocklimit is not set
lingerpid=4042418
```

