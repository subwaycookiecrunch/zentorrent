# zentorrent

a fast, minimal CLI torrent streaming client. Instantly streams torrents and magnet links to VLC. Also includes a Chrome extension for capturing magnet links directly from your browser. 

## Installation

### Windows (Winget)
```powershell
winget install subwaycookiecrunch.zt
```

### macOS (Homebrew)
```sh
brew tap subwaycookiecrunch/zt
brew install zt
```

### Linux / Binaries
Grab the latest pre-compiled binary from the [Releases page](https://github.com/subwaycookiecrunch/zentorrent/releases).

### Build from source
```sh
git clone https://github.com/subwaycookiecrunch/zentorrent
cd zentorrent
go build -o zt .
```

## Usage

**1. Stream directly**
```sh
zt "magnet:?xt=urn:btih:..."
```

**2. Interactive Source Browser**
Browse and search popular trackers (Nyaa, YTS, 1337x, etc.) directly in the terminal:
```sh
zt sources
```

**3. Background Extension mode**
Start the background server:
```sh
zt
```
Then, install the Chrome Extension located in the `zt-extension/` directory via `chrome://extensions` using the "Load Unpacked" feature. Clicking on any magnet link in your browser will automatically send it to the `zt` server and launch VLC.

## How it works under the hood
- Downloads piece metadata and aggressively prioritizes the first 5% of the file chunks to enable instant stream playback.
- Binds a localhost HTTP server to serve the actively buffering piece stream.
- Hooks directly into your local VLC installation without buffering the entire file to disk first.
