package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)


type Config struct {
	Player        string    `toml:"player"`         // "mpv", "vlc", or "auto"
	StreamPort    int       `toml:"stream_port"`    // port for video streaming (default 8888)
	ExtPort       int       `toml:"extension_port"` // port for chrome extension (default 9999)
	MaxPeers      int       `toml:"max_peers"`      // max peer connections per torrent
	Notifications bool      `toml:"notifications"`  // enable desktop notifications
	CacheSizeMB   int       `toml:"cache_size_mb"`  // max temp cache size in MB
	DownloadDir   string    `toml:"download_dir"`   // directory to save downloaded torrents
	Subtitles     SubConfig `toml:"subtitles"`
}


type SubConfig struct {
	Language  string `toml:"language"`   // ISO 639-1 language code
	APIKey    string `toml:"api_key"`    // OpenSubtitles API key
	AutoFetch bool   `toml:"auto_fetch"` // automatically search for subtitles
}


func DefaultConfig() Config {
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
	}
}


func configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "zentorrent")
}


func configPath() string {
	return filepath.Join(configDir(), "config.toml")
}


func historyPath() string {
	return filepath.Join(configDir(), "history.json")
}


func LoadConfig() Config {
	conf := DefaultConfig()

	path := configPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_ = SaveConfig(conf)
		return conf
	}

	_, err := toml.DecodeFile(path, &conf)
	if err != nil {
		fmt.Printf("⚠ config error: %v (using defaults)\n", err)
		return DefaultConfig()
	}
	return conf
}


func SaveConfig(conf Config) error {
	dir := configDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	f, err := os.Create(configPath())
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintln(f, "# ZenTorrent v2 Configuration")
	fmt.Fprintln(f, "# Player options: auto, mpv, vlc")
	fmt.Fprintln(f, "")

	encoder := toml.NewEncoder(f)
	return encoder.Encode(conf)
}


func PrintConfig(conf Config) {
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
	fmt.Println("├─────────────────────────────────────┤")
	fmt.Printf("│  Subtitle Lang: %-20s│\n", conf.Subtitles.Language)
	fmt.Printf("│  Auto Fetch:    %-20v│\n", conf.Subtitles.AutoFetch)
	apiStatus := "not set"
	if conf.Subtitles.APIKey != "" {
		apiStatus = "configured ✓"
	}
	fmt.Printf("│  API Key:       %-20s│\n", apiStatus)
	fmt.Println("└─────────────────────────────────────┘")
	fmt.Printf("\n  Config file: %s\n", configPath())
}
