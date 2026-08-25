package main

// smart_playback.go — tier-aware playback entry points shared by the TUI,
// the web dashboard and the extension server.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/subwaycookiecrunch/zentorrent/internal/debrid"
	"github.com/subwaycookiecrunch/zentorrent/internal/streamer"
)

// desktopPlaySource is the web dashboard's /api/play hook: takes over any
// resolved tier on the desktop player.
func desktopPlaySource(src debrid.StreamSource) {
	switch src.Type {
	case debrid.StreamTorrent:
		if src.Magnet != "" {
			playTorrentDesktop(src.Magnet)
		}
	case debrid.StreamHLS:
		fallthrough
	default:
		playerCmd, err := streamer.LaunchPlayer(src.URL, "", 0)
		if err != nil {
			fmt.Printf("> [Web] player failed for %s: %v\n", src.ProviderName, err)
			return
		}
		if playerCmd != nil {
			go playerCmd.Wait()
		}
	}
}

// playTorrentDesktop starts the warm P2P engine and launches the player as
// soon as the VOD endpoint answers.
func playTorrentDesktop(magnet string) {
	go func() {
		defer func() { recover() }()
		go streamTorrent(magnet, nil, nil)

		streamURL := fmt.Sprintf("http://localhost:%d/stream", appConfig.StreamPort)
		for i := 0; i < 150; i++ { // up to 30s for metadata+first pieces
			if streamer.CheckStream(streamURL) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		playerCmd, _ := streamer.LaunchPlayer(streamURL, "", 0)
		if playerCmd != nil {
			playerCmd.Wait()
		}
	}()
}

// maybeDebridShortcut tries Tier 1 before committing to the torrent engine.
// Returns true when playback was handed to a debrid provider.
func maybeDebridShortcut(title, magnet, infoHash string, tmdbID int64) bool {
	if services == nil || services.Tiers == nil || len(tierProviders()) == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	srcs := services.Tiers.ResolveTiers(ctx, debrid.MediaItem{
		Title: title, TMDBID: tmdbID, Magnet: magnet, InfoHash: infoHash,
	}, magnet, 1<<30) // seeders unknown pre-search: skip HLS floor logic

	for _, src := range srcs {
		if src.Type == debrid.StreamDebrid {
			currentStream.mu.Lock()
			currentStream.Tier = "DEBRID · " + src.ProviderName
			currentStream.mu.Unlock()
			fmt.Printf("> ⚡ [%s] cached — playing direct link (%s)\n",
				strings.ToUpper(src.ProviderName), src.Quality)
			playerCmd, err := streamer.LaunchPlayer(src.URL, "", 0)
			if err == nil && playerCmd != nil {
				go playerCmd.Wait()
				return true
			}
		}
	}
	return false
}

func tierProviders() []debrid.Provider {
	if tr, ok := services.Tiers.(*tierResolver); ok {
		return tr.providers
	}
	return nil
}
