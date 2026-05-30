# zentorrent

a blazing fast, minimalist terminal torrent client and streamer. 

zentorrent drops bloated web UIs and heavy electron apps in favor of a gorgeous, keyboard-driven terminal interface. paste a magnet link or `.torrent` file, and it aggressively prioritizes piece metadata to instantly stream video to your native player (VLC or MPV) without waiting for the download to finish. 

or just use it as a standard client and download straight to disk.

## features
- **instant streaming**: prioritizes the first 5% of video chunks to launch VLC/MPV immediately.
- **download mode**: download entire torrents to disk with a clean animated progress dashboard.
- **interactive search**: query popular trackers directly from your terminal.
- **bookmarks & history**: save things for later or pick up where you left off.
- **keyboard-driven**: built with charmbracelet's bubbletea for a buttery smooth TUI.
- **subtitles**: auto-fetches subtitles in the background.

## installation

### the easiest way (mac & linux)
```bash
curl -sSL https://raw.githubusercontent.com/subwaycookiecrunch/zentorrent/main/install.sh | bash
```

### windows & pre-compiled binaries
grab the latest `.exe` (windows) or binary (mac/linux) from the [Releases page](https://github.com/subwaycookiecrunch/zentorrent/releases). put it somewhere in your `PATH` and you're good to go.

### build from source
if you have Go installed, this will compile and place the binary in your `GOPATH`:
```bash
go install github.com/subwaycookiecrunch/zentorrent@latest
```

## usage

just type `zentorrent` in your terminal to launch the interactive menu.

from there you can:
- **Search** to find something to watch
- **Stream** to paste a magnet link or `/path/to/file.torrent`
- **Download** to save files directly to your machine
- **Config** to configure your preferred player, subtitles, and download directories

## under the hood
- runs a local HTTP server that serves the actively buffering piece stream.
- binds directly to `anacrolix/torrent` for lightning-fast peer discovery and DHT routing.
- reprioritizes torrent pieces dynamically based on playback position so the stream never stutters.
