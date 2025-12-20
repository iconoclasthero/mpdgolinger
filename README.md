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

$ ./mpdgolinger --daemon --limit 3 --mpdhost localhost --mpdport 6600 --socket /path/to/socket

Optional flags:

- --mpdhost <host>: MPD server host (default localhost)
- --mpdport <port>: MPD server TCP port (default 6600)
- --socket <path>: IPC socket path (required in daemon mode)
- --state <path>: Optional path for persistent state file

### Client Mode

Run commands against the running daemon:

$ ./mpdgolinger status
$ ./mpdgolinger pause
$ ./mpdgolinger resume
$ ./mpdgolinger limit 5
$ ./mpdgolinger block 3
$ ./mpdgolinger next
$ ./mpdgolinger skip
$ ./mpdgolinger quit

## Development

- Go 1.21+
- Uses github.com/fhs/gompd/v2/mpd for MPD communication.

## Notes

- IPC socket must exist and be writable by clients.
- Daemon supervises both MPD idle events and the IPC socket, reconnecting if necessary.
