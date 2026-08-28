# ZenTorrent v4

[![Website](https://img.shields.io/badge/Official_Website-zentorrent.vercel.app-d5a85b?style=for-the-badge&logo=vercel)](https://zentorrent.vercel.app/)
[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=for-the-badge&logo=go)](https://go.dev/)
[![Platform](https://img.shields.io/badge/Platform-macOS_%7C_Windows_%7C_Linux-7c3aed?style=for-the-badge)](https://zentorrent.vercel.app/)

> **Official Website & Downloads:** [zentorrent.vercel.app](https://zentorrent.vercel.app/)

Terminal and web-based cinema & music streaming client with multi-source playback, YouTube & lossless music audio search, watch party synchronization, and integrated audio DSP controls.

---

## 🚀 Download & Installation

Visit **[zentorrent.vercel.app](https://zentorrent.vercel.app/)** to get pre-built binaries or install via your terminal:

### 🍎 macOS
```bash
brew tap subwaycookiecrunch/tap
brew install zentorrent
```
*Or download the standalone Apple Silicon / Intel binary from [zentorrent.vercel.app](https://zentorrent.vercel.app/).*

### 🪟 Windows
Download the standalone executable `zentorrent.exe` directly from [zentorrent.vercel.app](https://zentorrent.vercel.app/) or run via PowerShell:
```powershell
irm https://zentorrent.vercel.app/install.ps1 | iex
```

### 🐧 Linux
```bash
curl -fsSL https://zentorrent.vercel.app/install.sh | sh
```

---

## ⚡ Streaming Tiers

| Tier | Source | Notes |
|------|--------|-------|
| 1 · Debrid | Real-Debrid, TorBox | instant cached direct links (set API keys in config) |
| 2 · P2P | warm anacrolix client | head+tail piece priority, MPV IPC seek-aware scheduler |
| 3 · HLS | VidSrc / VidLink / AutoEmbed | auto-fallback when seeders < `hls_seed_floor` |
| 4 · Music | ZenPlayer Audio Engine | YouTube & lossless streaming with real-time DSP |

---

## 📁 Repository Layout

```
cmd/zentorrent/      entrypoint + TUI + lifecycle wiring + zenplayer
internal/config/     TOML config (debrid keys, ports, providers)
internal/metadata/   TMDB client, SQLite FTS5 trigram catalog,
                     phonetic (consonant-skeleton) + Damerau-Levenshtein fuzzy matching
internal/debrid/     Real-Debrid & TorBox instant-cache resolvers
internal/extractors/ HLS embed providers keyed by TMDB/IMDb ID
internal/search/     Torznab client, BEP-51/09 DHT crawler, tier-ranked aggregator
internal/engine/     shared warm torrent client + VOD seek-window buffer
internal/streamer/   MPV (IPC socket) / VLC launcher
internal/web/        embedded dashboard (go:embed) + REST + WebSocket + ZenPlayer
```

---

## 🛠️ Quick Start

```bash
# Build from source
go build -o zentorrent ./cmd/zentorrent

# Launch TUI Menu
./zentorrent

# Sync TMDB Daily Dump (~1.2M titles)
./zentorrent sync

# Start ZenTorrent Studio Web Player & ZenPlayer Music
./zentorrent serve           # → http://localhost:8888

# Search and stream directly
./zentorrent search "interstellar"
```

---

## ⚙️ Configuration (`~/.config/zentorrent/config.toml`)

```toml
tmdb_api_key = "..."            # or TMDB_API_KEY env (dumps work without it)
real_debrid_api_key = "..."
torbox_api_key = "..."

[[torznab_endpoints]]
name = "prowlarr"; url = "http://localhost:9696/1"; api_key = "..."

enable_dht_crawler   = true
auto_sync_daily_dumps = true
enable_hls_fallback  = true
hls_seed_floor       = 20
```

Search ranking prefers **Debrid cached > seeders > quality > health**; misspellings resolve via trigram + phonetic + fuzzy stages against the local catalog.

---

🌐 **Website:** [https://zentorrent.vercel.app](https://zentorrent.vercel.app/)
