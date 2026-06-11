# ZenTorrent

A terminal client for streaming torrents directly to your media player (MPV or VLC). 

Instead of waiting for a download to finish, ZenTorrent runs a local HTTP server, prioritizes the first few pieces of the torrent, and pipes it straight to your player. You start watching in seconds.

## Features

- **Fast Streaming**: Aggressive piece prioritization to get video playing instantly.
- **Concurrent Search**: Scrapes YTS, 1337x, TPB, Nyaa, EZTV, and SubsPlease simultaneously.
- **Smart Playlists**: Zero-buffering transitions. It automatically pre-buffers the next queue item in the background when the current stream hits 80%.
- **ZenParty**: Synchronized watch parties with friends. Runs over public `ntfy.sh` channels using MPV's local IPC socket (no registration or hosting required).
- **ZenScript**: Automation scripts (`.zs` files) to queue up searches and streams.
- **Offline Search**: Passive DHT crawler that indexes metainfo into a local SQLite database.
- **Extras**: Auto-subtitles (OpenSubtitles), Discord/Slack webhooks, and themes (Gruvbox, Nord, Catppuccin, Dracula, etc.).

## Install

Requires **MPV** (preferred) or **VLC** to be installed on your system.

```bash
# macOS / Linux one-liner
curl -sSL https://raw.githubusercontent.com/subwaycookiecrunch/zentorrent/main/install.sh | bash

# Or build from source
go install github.com/subwaycookiecrunch/zentorrent@latest
```

Pre-built binaries are available on the [releases page](https://github.com/subwaycookiecrunch/zentorrent/releases).

## Usage

Just run `zentorrent` to launch the interactive TUI. 

Alternatively, use commands directly:

```
zentorrent search "query"     Search and stream a result
zentorrent stream <magnet>    Stream a magnet link
zentorrent history            Show recent streams
zentorrent config             Print active configuration
zentorrent status             Check background server status
zentorrent run script.zs      Run a playlist script
```

### ZenScript Example

Create a `.zs` file to queue up streams:
```
watch "inception" quality:1080p
watch "breaking bad" S01E01
watch "one piece" source:nyaa quality:1080p
```

## How it works

ZenTorrent mounts the torrent as a seekable HTTP stream. It connects to MPV over a UNIX socket (`/tmp/zt_mpv.sock`) to track playback position in real-time. The Go backend uses `anacrolix/torrent` and dynamically shifts piece priority: blocks ahead of the playhead get high priority, while blocks you've already watched are deprioritized. 

If your speed drops below a threshold, the arbiter system hot-swaps to a fallback source or lower resolution without interrupting the player.

## License

MIT
