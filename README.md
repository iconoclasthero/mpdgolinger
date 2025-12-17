# mpdgo-linger

Experimental MPD idle-based block randomizer written in Go.

Goal:
- Observe MPD idle(player) events
- Track song blocks
- Toggle random on last song, off on transition
- Debug idle edge cases (seek, pause, skip)

Status:
- Active debugging
- Heavy logging enabled intentionally

See mpdgolinger.go for core logic.
