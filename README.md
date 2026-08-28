# ZenTorrent v4

Terminal and web-based torrent streaming client with multi-source playback, watch party synchronization, and integrated audio controls.

## Streaming Tiers

| Tier | Source | Notes |
|------|--------|-------|
| 1 · Debrid | Real-Debrid, TorBox | instant cached direct links (set API keys in config) |
| 2 · P2P | warm anacrolix client | head+tail piece priority, MPV IPC seek-aware scheduler |
| 3 · HLS | VidSrc / VidLink / AutoEmbed | auto-fallback when seeders < `hls_seed_floor` |

## Layout

```
cmd/zentorrent/      entrypoint + TUI + lifecycle wiring
internal/config/     TOML config (debrid keys, ports, providers)
internal/metadata/   TMDB client, SQLite FTS5 trigram catalog,
                     phonetic (consonant-skeleton) + Damerau-Levenshtein fuzzy matching
internal/debrid/     Real-Debrid & TorBox instant-cache resolvers
internal/extractors/ HLS embed providers keyed by TMDB/IMDb ID
internal/search/     Torznab client, BEP-51/09 DHT crawler, tier-ranked aggregator
internal/engine/     shared warm torrent client + VOD seek-window buffer
internal/streamer/   MPV (IPC socket) / VLC launcher
internal/web/        embedded dashboard (go:embed) + REST + WebSocket status
```

## Quick start

```bash
go build -o zentorrent ./cmd/zentorrent
./zentorrent                 # TUI menu (type-ahead search built into 🔍 Search)
./zentorrent sync            # pull the TMDB daily dump (~1.2M titles)
./zentorrent serve           # web dashboard → http://localhost:8888
./zentorrent search "drishiam 2"
```

## Config (`~/.config/zentorrent/config.toml`)

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

Search ranking prefers **Debrid cached > seeders > quality > health**;
misspellings ("drishiam", "interstelar", romanized Hindi) resolve via trigram +
phonetic + fuzzy stages against the local catalog.
