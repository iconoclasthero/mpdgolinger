# mpdgolinger

**mpdgolinger** is a Go-based daemon for managing MPD (Music Player Daemon) playback with block limits, idle supervision, and IPC commands.

## Background

My collection has roughly 80,000 songs an 7,000 artists and my current playlist contains ca. 73,000. I generally add music by the directory so—in theory—I should have a playlist that consists of albums by artist, roughly alphabetically. In pure random mode, mpd should play just one song by an artist (save for over-represented artists in the collection). 

Classic rock stations have long had gimmicks like "Rock Blocks," "Double Shots," "Twofer Tuesdays," "Workfoce blocks," etc., where multiple songs from the same artist or around a common theme were played consecutively. That's essentially what this does. Set your limit=_n_ (default _n_=4) and mpd playback will _linger_ on the playlist for _n_ songs before advancing on random to another block. This is compatible with consume and repeat mpd playback modes. And if you're really digging, say, a particular Dead bootleg and want to hear more than _n_ songs for a block, you can temporarily override the limit for a block. Given the law of low numbers (Benford's Law), there's a decent chance the random will land on 4 consecutive songs by the same artist (at least until comnsume—if enabled—has swiss-cheesed your playlist).

## Features

- Persistent block counting and limiting
- IPC commands (status, pause, resume, limit, block[limit], next, skip, quit)
- Idle supervision for MPD events
- Optional state file for persistent tracking
- Daemon mode with --daemon
- Supports TCP or UNIX socket connection to MPD

## Installation

Basic build:

```sh
go build -o mpdgolinger mpdgolinger.go
```

Optional static build:

```sh
CGO_ENABLED=0 go build -a -ldflags '-extldflags "-static"' -o mpdgolinger mpdgolinger.go
```

Optional strip builds (to remove debug symbols for a smaller bianry):

```sh
strip mpdgolinger
```

## Usage

### Daemon Mode

```sh
./mpdgolinger --daemon --limit=3 --mpdhost=localhost --mpdport=6600   # to specify via cli flags
./mpdgolinger --daemon --config=./mpdgolinger.conf                    # to use a config file
```

An example systemd unit file is included for reference.

Optional flags/config file options:

```
  --config=<path>     : Path to config file; values overridden by flags  
  --daemon            : Launch mpdgolinger in daemon mode                [daemon only]
  --mpdsocket=<path>  : MPD server socket, (e.g., `/run/mpd/socket`)     [daemon only]
  --mpdhost=<host>    : MPD server host (default localhost)              [daemon only]
  --mpdport=<port>    : MPD server TCP port (default 6600)               [daemon only]
  --mpdpass=<pass>    : MPD server password                              [daemon only]
  --listenip=<host>   : IPC listen address                               [daemon only]
  --listenport=<port> : IPC listen port                                  [daemon only]
  --socket=<path>     : IPC socket path
  --state=<path>      : Optional path for persistent state file
  --daemonip=<host>   : Connect to daemon listening at address           [client only]
  --daemonport=<port> : Connect to daemon listening on port              [client only]
  --execpost=<path>   : File/commandto execute after client              [client only]
  --version           : Prints mpdgolinger binary version
  --help              : Prints help
```
Config file syntax is `key=value` pair.

### Client Mode

Run commands against the running daemon:

```sh
mpdgolinger status            # prints status message, format below; serves as ping
mpdgolinger pause             # pauses linger function; mpd playback unchanged
mpdgolinger resume            # resumes linger function; mpd playback restarted if paused
mpdgolinger toggle            # toggles linger play/pause; resumes mpd playback if paused
mpdgolinger limit <n>         # changes the ongoing limit to <n>; zero resets below
mpdgolinger limit             # resets the ongoing limit to default/startup limit
mpdgolinger block[limit] <n>  # sets a limit of <n> to current block only
mpdgolinger block[limit]      # turns off the block limit override (as does 0)
mpdgolinger next              # skips to the next song and block
mpdgolinger skip              # skips to the next song within block (i.e., mpc next)
mpdgolinger count <n>         # sets the count to <n>
mpdgolinger verbose <on|off>  # turns daemon verbose logging on or off
mpdgolinger version           # prints client/daemon and mpd protocol/binary versions
~~mpdgolinger xy <x> <y|+n>     # turns XY Mode on with bounds <x> <y> or increment <+n>~~
~~mpdgolinger xyoff             # turns XY Mode off~~
mpdgolinger mpc               # outputs mpd state, current/next songs, linger status
mpdgolinger quit              # exits daemon
```

## Development

- Go 1.21+
- Uses github.com/fhs/gompd/v2/mpd for MPD communication.

## Notes

- IPC socket must exist and be writable by clients.
- The IPC socket defaults to `/var/lib/mpd/mpdlinger/mpdlinger.sock`
- Daemon supervises both MPD idle events and the IPC socket, reconnecting if necessary.

## Statefile

To create an optional state file that can be parsed/sourced for other uses, enable the `--state=<path>` when launching the daemon. The format of the state file is the same as `status`:

```txt
writetime=2025-12-20T10:35:57.330310379-05:00
lingersongid=139234  # to verify sync with other MPD clients
lingerpause=0
lingercount=1
lingerbase=4
lingerlimit=4
lingerblocklmt=5     # 0 if blocklimit is not set
lingerpid=4042418
```
