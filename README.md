# mpdgolinger

**mpdgolinger** is a Go-based daemon for managing MPD (Music Player Daemon) playback with block limits, idle supervision, and IPC commands.

## Background

My collection has roughly 80,000 songs and 7,000 artists and my current playlist numbers ca. 73,000. I generally add music by the directory so—in theory—I should have a playlist that consists of albums by artist, roughly alphabetically. In pure random mode, mpd should play just one song in a row by an artist (save for over-represented artists in the collection). 

Classic rock stations have long had gimmicks like "Rock Blocks," "Double Shots," "Twofer Tuesdays," "Workfoce blocks," etc., where multiple songs from the same artist or around a common theme are played consecutively. That's essentially what this does. Set your limit=_n_ (default _n_=4) and mpd playback will _linger_ on the playlist for _n_ songs before advancing on random to another block. This is compatible with consume and repeat mpd playback modes. And if you're really digging, say, a particular Dead bootleg and want to hear more than _n_ songs for a block, you can temporarily override the limit for a block. Given the law of low numbers (Benford's Law), there's a decent chance the random will land on 4 consecutive songs by the same artist (at least until comnsume—if enabled—has swiss-cheesed your playlist).

lin·​ger [ˈliŋ-gər] verb
To  stay in a place longer than necessary, often due to enjoyment or reluctance to leave, or for something to remain or persist, like a scent or feeling, even as its strength fades; it implies slowness, delay, and persistence

## Features

- Persistent block counting and limiting
- IPC commands (status, pause, resume, limit, block[limit], next, skip, quit)
- Idle supervision for MPD events
- Optional state file for persistent tracking
- Daemon mode with --daemon
- Supports TCP or UNIX socket connection to MPD
- XY playback mode

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
Examples:
```sh
mpdgolinger --daemon --limit=3 --mpdhost=localhost --mpdport=6600  # specify via cli flags
mpdgolinger --daemon --config=./mpdgolinger.conf                   # specify via config file
```

An example systemd unit file is included for reference.

### Flags/.conf file options

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
  --limit=<n>         : Sets baselimit to n                              [daemon only]
  --log=<path>        : Daemon logging path (default is stdout/fd1)      [daemon only]
  --state=<path>      : Optional path for persistent state file          [daemon only]
  --socket=<path>     : IPC socket path
  --config=<path>     : Path for config file defaults
  --daemonip=<host>   : Connect to daemon listening at address           [client only]
  --daemonport=<port> : Connect to daemon listening on port              [client only]
  --execpost=<path>   : File/commandto execute after client              [client only]
  --verbose           : Verbose output
  --version           : Prints mpdgolinger binary version
  --help              : Prints help
```
Config file syntax is `key=value` pair; use double quotes on the RHS as necessary.

#### Daemon .conf-only options:
```
skiplist=<MPDplaylist name>  # record skipped songs in this playlist; `.m3u` added by MPD
mpdpath=</absolute/path/mpd> # defaults to `/usr/bin/mpd`; 'none' disables verstion check
```

### Client Mode

Run commands against the running daemon:

```sh
mpdgolinger status            # prints status message, format below; serves as ping
mpdgolinger pause             # pauses linger function; mpd playback unchanged
mpdgolinger resume            # resumes linger function; mpd playback restarted if paused
mpdgolinger toggle            # toggles linger play/pause; resumes mpd playback if paused
mpdgolinger limit <n>         # changes the base limit to <n>; zero resets below
mpdgolinger limit             # resets the base limit to the base limit at startup
mpdgolinger block[limit] <n>  # sets a limit of <n> to current block only
mpdgolinger block[limit]      # turns off the block limit override (as does n=0)
mpdgolinger next              # skips to the next song and block; mpd playback restarted
mpdgolinger skip              # skips to the next song within block (i.e., mpc next)
mpdgolinger count <n>         # sets the count to <n>
mpdgolinger verbose <on|off>  # turns the running daemon verbose logging on or off
mpdgolinger state <path>      # enables/changes state file path daemon writes to
mpdgolinger version           # prints client/daemon, mpd protocol & /usr/bin/mpd versions
mpdgolinger mpc               # outputs mpd state, current/next songs, linger status
mpdgolinger quit|exit         # exits daemon
```

### XY Playback Mode

XY Mode allows a randomized playback of a specified subset of a playlist **in consume mode only.** The initial bounds (X & Y) are specified via the client (the Y bound may be specified by an increment). During playback the playlist remains at position X and a random song between X+1 and Y is moved to X+1.  When X is finished and consumed, Y is decremented by one and the cycle repeats until Y=X; XY mode is disabled (via `mpdgolinger xyoff`); or a change causes the current song to change from X.

```sh
mpdgolinger xy <x> <y|+n>     # turns XY Mode on with bounds <x> <y> or increment <+n>
mpdgolinger xyoff             # turns XY Mode off
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

```sh
writetime=2025-12-20T07:35:57.330310379-05:00
lingersong=41333     # 1-indexed song position to verify sync with other MPD clients
lingersongid=139234  # mpd songid to verify sync with other MPD clients
lingerpause=0
lingercount=1
lingerbase=4
lingerlimit=4
lingerblocklmt=5     # 0 if blocklimit is not set
lingerpid=4042418
```
In XY Mode, the following additional fields will be diplayed:
```
lingerxy=true
lingerx=624
lingery=644
```
