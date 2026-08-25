// Package config owns ZenTorrent's TOML configuration, paths, and defaults.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// TorznabConfig is one indexer endpoint in config.toml:
//
//	[[torznab_endpoints]]
//	  name    = "prowlarr"
//	  url     = "http://localhost:9696/1"
//	  api_key = "..."
//	  categories = [2040, 2045, 2010]
type TorznabConfig struct {
	Name       string `toml:"name"`
	URL        string `toml:"url"`
	APIKey     string `toml:"api_key"`
	Categories []int  `toml:"categories"`
}

type SubConfig struct {
	Language  string `toml:"language"`
	APIKey    string `toml:"api_key"`
	AutoFetch bool   `toml:"auto_fetch"`
}

type Config struct {
	Player        string    `toml:"player"`
	StreamPort    int       `toml:"stream_port"`
	ExtPort       int       `toml:"extension_port"`
	MaxPeers      int       `toml:"max_peers"`
	Notifications bool      `toml:"notifications"`
	CacheSizeMB   int       `toml:"cache_size_mb"`
	DownloadDir   string    `toml:"download_dir"`
	Subtitles     SubConfig `toml:"subtitles"`
	Webhooks      []string  `toml:"webhooks"`
	Theme         string    `toml:"theme"`

	TMDBApiKey         string          `toml:"tmdb_api_key"`
	TorznabEndpoints   []TorznabConfig `toml:"torznab_endpoints"`
	EnableDHTCrawler   bool            `toml:"enable_dht_crawler"`
	AutoSyncDailyDumps bool            `toml:"auto_sync_daily_dumps"`

	// Tier 1 debrid providers.
	RealDebridAPIKey string `toml:"real_debrid_api_key"`
	TorBoxAPIKey     string `toml:"torbox_api_key"`
	// Tier 3 web-embed fallback when torrent seeds are thin.
	EnableHLSFallback bool `toml:"enable_hls_fallback"`
	HLSSeedFloor      int  `toml:"hls_seed_floor"`
}

// FallbackTMDBKey stands in when neither config nor env provide a key. The
// daily ID-dump ingest needs no key at all; only detail backfill does, and
// it degrades to log lines until a real key is supplied.
const FallbackTMDBKey = "REPLACE_WITH_TMDB_V3_KEY"

// ResolveTMDBKey applies precedence: explicit value > TMDB_API_KEY env >
// built-in fallback.
func ResolveTMDBKey(configured string) string {
	if v := strings.TrimSpace(configured); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("TMDB_API_KEY")); v != "" {
		return v
	}
	return FallbackTMDBKey
}

func Default() Config {
	return Config{
		Player:        "auto",
		StreamPort:    8888,
		ExtPort:       9999,
		MaxPeers:      50,
		Notifications: true,
		CacheSizeMB:   512,
		DownloadDir:   filepath.Join(os.Getenv("HOME"), "Downloads", "ZenTorrent"),
		Subtitles: SubConfig{
			Language:  "en",
			AutoFetch: true,
		},
		Webhooks:           []string{},
		Theme:              "purple",
		EnableDHTCrawler:   true,
		AutoSyncDailyDumps: true,
		EnableHLSFallback:  true,
		HLSSeedFloor:       20,
	}
}

func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "zentorrent")
}

func Path() string { return filepath.Join(Dir(), "config.toml") }

func HistoryPath() string { return filepath.Join(Dir(), "history.json") }

func Load() Config {
	conf := Default()

	path := Path()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_ = Save(conf)
		return conf
	}

	if _, err := toml.DecodeFile(path, &conf); err != nil {
		fmt.Printf("⚠ config error: %v (using defaults)\n", err)
		return Default()
	}
	return conf
}

func Save(conf Config) error {
	if err := os.MkdirAll(Dir(), 0755); err != nil {
		return err
	}

	f, err := os.Create(Path())
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintln(f, "# ZenTorrent Configuration")
	fmt.Fprintln(f, "# Player options: auto, mpv, vlc, terminal")
	fmt.Fprintln(f, "")

	return toml.NewEncoder(f).Encode(conf)
}

func Print(conf Config) {
	fmt.Println("┌─────────────────────────────────────┐")
	fmt.Println("│  ⚙  ZenTorrent Configuration        │")
	fmt.Println("├─────────────────────────────────────┤")
	fmt.Printf("│  Player:        %-20s│\n", conf.Player)
	fmt.Printf("│  Stream Port:   %-20d│\n", conf.StreamPort)
	fmt.Printf("│  Extension Port:%-20d│\n", conf.ExtPort)
	fmt.Printf("│  Max Peers:     %-20d│\n", conf.MaxPeers)
	fmt.Printf("│  Notifications: %-20v│\n", conf.Notifications)

	dir := conf.DownloadDir
	if len(dir) > 20 {
		dir = dir[:17] + "..."
	}
	fmt.Printf("│  Download Dir:  %-20s│\n", dir)
	fmt.Printf("│  Cache Size:    %-17s MB│\n", fmt.Sprintf("%d", conf.CacheSizeMB))
	tmdbState := "fallback"
	if ResolveTMDBKey(conf.TMDBApiKey) != FallbackTMDBKey {
		tmdbState = "configured ✓"
	}
	rdState := "not set"
	if strings.TrimSpace(conf.RealDebridAPIKey) != "" {
		rdState = "configured ✓"
	}
	tbState := "not set"
	if strings.TrimSpace(conf.TorBoxAPIKey) != "" {
		tbState = "configured ✓"
	}
	fmt.Printf("│  TMDB Key:      %-20s│\n", tmdbState)
	fmt.Printf("│  RealDebrid:    %-20s│\n", rdState)
	fmt.Printf("│  TorBox:        %-20s│\n", tbState)
	fmt.Printf("│  Torznab Feeds: %-20d│\n", len(conf.TorznabEndpoints))
	fmt.Printf("│  DHT Crawler:   %-20v│\n", conf.EnableDHTCrawler)
	fmt.Printf("│  Daily Sync:    %-20v│\n", conf.AutoSyncDailyDumps)
	fmt.Printf("│  HLS Fallback:  %-20v│\n", conf.EnableHLSFallback)
	fmt.Println("├─────────────────────────────────────┤")
	fmt.Printf("│  Subtitle Lang: %-20s│\n", conf.Subtitles.Language)
	fmt.Printf("│  Auto Fetch:    %-20v│\n", conf.Subtitles.AutoFetch)
	apiStatus := "not set"
	if conf.Subtitles.APIKey != "" {
		apiStatus = "configured ✓"
	}
	fmt.Printf("│  API Key:       %-20s│\n", apiStatus)
	fmt.Println("└─────────────────────────────────────┘")
	fmt.Printf("\n  Config file: %s\n", Path())
}
