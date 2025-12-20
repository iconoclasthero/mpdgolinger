# mpdgolinger

**mpdgolinger** is a Go-based daemon for managing MPD (Music Player Daemon) playback with block limits, idle supervision, and IPC commands.

## Features

- Persistent block counting and limiting
- IPC commands (status, pause, resume, limit, block, next, skip, quit)
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

  --mpdsocket <path>: MPD server socket, (e.g., `/run/mpd/socket`)
- --mpdhost <host>  : MPD server host (default localhost)
- --mpdport <port>  : MPD server TCP port (default 6600)
- --state <path>    : Optional path for persistent state file
~~- --socket <path>   : IPC socket path (required in daemon mode)~~

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
- Daemon supervises both MPD idle events and the IPC socket, reconnecting if necessary.
