# ZenTorrent

Terminal and web client for streaming torrents and music, with multi-source playback, watch party synchronization, and audio controls.

Website: [https://zentorrent.vercel.app](https://zentorrent.vercel.app)

---

## Download and Installation

Binaries and web installer are available on [zentorrent.vercel.app](https://zentorrent.vercel.app).

### macOS
```bash
brew tap subwaycookiecrunch/tap
brew install zentorrent
```

### Windows
Download `zentorrent.exe` from [zentorrent.vercel.app](https://zentorrent.vercel.app) or install via PowerShell:
```powershell
irm https://zentorrent.vercel.app/install.ps1 | iex
```

### Linux
```bash
curl -fsSL https://zentorrent.vercel.app/install.sh | sh
```

---

## Streaming Tiers

| Tier | Source | Description |
|------|--------|-------------|
| 1 · Debrid | Real-Debrid, TorBox | Instant cached direct links |
| 2 · P2P | BitTorrent client | Head/tail piece prioritization with MPV seek scheduler |
| 3 · HLS | VidSrc / VidLink / AutoEmbed | Fallback when seeders < `hls_seed_floor` |
| 4 · Music | Audio Engine | YouTube and lossless stream resolver with equalizer |

---

## Project Structure

```
cmd/zentorrent/      CLI entrypoint, TUI, and ZenPlayer launcher
internal/config/     TOML configuration (debrid keys, ports, providers)
internal/metadata/   TMDB client, SQLite FTS5 catalog, phonetic/fuzzy search
internal/debrid/     Real-Debrid and TorBox resolvers
internal/extractors/ HLS stream providers keyed by TMDB/IMDb ID
internal/search/     Torznab client, DHT crawler, ranked aggregator
internal/engine/     Torrent client and VOD buffer scheduler
internal/streamer/   MPV / VLC player integration
internal/web/        Embedded web UI and REST/WebSocket API
```

---

## Quick Start

```bash
# Build from source
go build -o zentorrent ./cmd/zentorrent

# Launch TUI
./zentorrent

# Sync TMDB catalog (~1.2M records)
./zentorrent sync

# Start web interface on http://localhost:8888
./zentorrent serve

# Search and stream
./zentorrent search "interstellar"
```

---

## Configuration (`~/.config/zentorrent/config.toml`)

```toml
tmdb_api_key = "..."            # or set TMDB_API_KEY environment variable
real_debrid_api_key = "..."
torbox_api_key = "..."

[[torznab_endpoints]]
name = "prowlarr"
url = "http://localhost:9696/1"
api_key = "..."

enable_dht_crawler = true
auto_sync_daily_dumps = true
enable_hls_fallback = true
hls_seed_floor = 20
```

Search ranking prioritizes **Debrid cached > seeders > quality > health**. Queries resolve through trigram, phonetic, and Damerau-Levenshtein fuzzy matching against the local SQLite database.

