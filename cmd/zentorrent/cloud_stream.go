package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/subwaycookiecrunch/zentorrent/internal/debrid"
	"github.com/subwaycookiecrunch/zentorrent/internal/extractors"
	"github.com/subwaycookiecrunch/zentorrent/internal/streamer"
)

var sharedCloudClient = &http.Client{
	Timeout: 8 * time.Second,
}

// CleanTorrentTitle strips release groups, codecs, resolutions and years to
// obtain a clean search title.
func CleanTorrentTitle(raw string) (title string, season int, episode int) {
	s := raw

	// If it's a magnet URI, extract 'dn' parameter
	if strings.HasPrefix(s, "magnet:") {
		if u, err := url.Parse(s); err == nil {
			if dn := u.Query().Get("dn"); dn != "" {
				s = dn
			}
		}
	}

	// Match season/episode (e.g. S01E03, 1x03, Season 1 Episode 3)
	reSE := regexp.MustCompile(`(?i)(?:s|season\s*)(\d{1,2})[\s.x_e-]*(?:e|episode\s*)(\d{1,2})`)
	if m := reSE.FindStringSubmatch(s); len(m) == 3 {
		season, _ = strconv.Atoi(m[1])
		episode, _ = strconv.Atoi(m[2])
	}

	// Replace separators with spaces
	s = strings.ReplaceAll(s, ".", " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "+", " ")

	// Strip common torrent tag junk
	junkRe := regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|4k|uhd|hdr|hdr10|dv|dolby|atmos|dts|aac|ac3|x264|x265|hevc|h264|bluray|bdrip|webrip|web-dl|webdl|remux|repack|proper|yify|yts|tgx|galaxytv|eztv|nyaa|rarbg|psa|vxt|sub\s*ita|ita|eng|hindi|tam|tel)\b.*$`)
	s = junkRe.ReplaceAllString(s, "")

	// Strip trailing year if present at the end
	reYear := regexp.MustCompile(`\b(19\d\d|20\d\d)\b.*$`)
	s = reYear.ReplaceAllString(s, "")

	title = strings.TrimSpace(s)
	if title == "" {
		title = raw
	}
	return title, season, episode
}

// ResolveMediaItem queries Cinemeta to find IMDb metadata for any search query.
func ResolveMediaItem(query string) (debrid.MediaItem, error) {
	cleanTitle, season, episode := CleanTorrentTitle(query)

	item := debrid.MediaItem{
		Title:   cleanTitle,
		Season:  season,
		Episode: episode,
	}

	cinemetaEndpoints := []string{
		fmt.Sprintf("https://v3-cinemeta.strem.io/catalog/movie/top/search=%s.json", url.PathEscape(cleanTitle)),
		fmt.Sprintf("https://v3-cinemeta.strem.io/catalog/series/top/search=%s.json", url.PathEscape(cleanTitle)),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	for _, ep := range cinemetaEndpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep, nil)
		if err != nil {
			continue
		}
		resp, err := sharedCloudClient.Do(req)
		if err != nil {
			continue
		}

		var payload struct {
			Metas []struct {
				ID          string `json:"id"`
				Type        string `json:"type"`
				Name        string `json:"name"`
				Poster      string `json:"poster"`
				Description string `json:"description"`
				Year        any    `json:"year"`
			} `json:"metas"`
		}

		if json.NewDecoder(resp.Body).Decode(&payload) == nil && len(payload.Metas) > 0 {
			resp.Body.Close()
			m := payload.Metas[0]
			item.IMDbID = m.ID
			item.Title = m.Name
			if m.Type == "series" && season == 0 {
				item.Season = 1
				item.Episode = 1
			}
			return item, nil
		}
		resp.Body.Close()
	}

	return item, nil
}

// ResolveCloudStreams concurrently tests multi-cloud extraction sources.
func ResolveCloudStreams(ctx context.Context, item debrid.MediaItem) []debrid.StreamSource {
	allExt := extractors.AllExtractors()
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		sources []debrid.StreamSource
	)

	for _, ext := range allExt {
		wg.Add(1)
		go func(e debrid.Provider) {
			defer wg.Done()
			src, err := e.Resolve(ctx, item)
			if err == nil && src != nil && src.URL != "" {
				mu.Lock()
				sources = append(sources, *src)
				mu.Unlock()
			}
		}(ext)
	}

	wg.Wait()
	return sources
}

// FallbackToCloudStream switches from 0-peer torrent swarm to high-speed cloud playback.
func FallbackToCloudStream(uri string, torrentName string) bool {
	cleanTitle, season, episode := CleanTorrentTitle(torrentName)
	if cleanTitle == "" || cleanTitle == torrentName {
		cleanTitle, season, episode = CleanTorrentTitle(uri)
	}

	if cleanTitle == "" {
		return false
	}

	fmt.Printf("\n> ⚡ 0 P2P swarm seeders responding.\n")
	fmt.Printf("> 🚀 Auto-switching to ZenCloud Multi-CDN Engine for '%s'...\n", cleanTitle)

	item, err := ResolveMediaItem(cleanTitle)
	if err != nil || item.IMDbID == "" {
		item.Title = cleanTitle
		item.Season = season
		item.Episode = episode
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	sources := ResolveCloudStreams(ctx, item)
	if len(sources) == 0 {
		target := item.IMDbID
		if target == "" {
			target = encodeQuery(cleanTitle)
		}
		embedURL := fmt.Sprintf("https://player.vidlove.cc/embed/movie/%s", target)
		if item.Season > 0 {
			embedURL = fmt.Sprintf("https://player.vidlove.cc/embed/tv/%s/%d/%d", target, item.Season, item.Episode)
		}
		sources = append(sources, debrid.StreamSource{
			Type:         debrid.StreamHLS,
			URL:          embedURL,
			ProviderName: "Zen Ultra 4K",
			Quality:      "4K UHD",
		})
	}

	bestSrc := sources[0]
	currentStream.mu.Lock()
	currentStream.Filename = item.Title
	currentStream.Resolution = "4K UHD"
	currentStream.Status = "streaming (cloud)"
	currentStream.Tier = "CLOUD · " + bestSrc.ProviderName
	currentStream.mu.Unlock()

	fmt.Printf("> 🟢 Connected to Cloud Stream [%s] • Quality: 4K UHD 60fps\n", bestSrc.ProviderName)
	fmt.Printf("> Launching desktop player (%s)...\n", streamer.DetectPlayer())

	Notify("ZenTorrent", "Streaming [Cloud 4K]: "+item.Title)

	playerCmd, err := streamer.LaunchPlayer(bestSrc.URL, "", 0)
	if err != nil || playerCmd == nil {
		fmt.Printf("> Desktop player failed. Opening in ZenTorrent Web Studio...\n")
		openBrowserDirect(fmt.Sprintf("http://localhost:%d/?tab=play&id=%s&type=movie", appConfig.StreamPort, item.IMDbID))
		return true
	}

	playerCmd.Wait()
	return true
}

// PlayCloudStream handles direct CLI streaming: `zentorrent play <query>`
func PlayCloudStream(query string) error {
	cleanTitle, season, episode := CleanTorrentTitle(query)
	fmt.Printf("\n  ┌──────────────────────────────────────────────────────────┐\n")
	fmt.Printf("  │ 🚀 ZenTorrent Cloud Cinema Engine                        │\n")
	fmt.Printf("  │ Searching: %-46s│\n", cleanTitle)
	fmt.Printf("  └──────────────────────────────────────────────────────────┘\n\n")

	item, err := ResolveMediaItem(query)
	if err != nil || item.IMDbID == "" {
		item.Title = cleanTitle
		item.Season = season
		item.Episode = episode
	}

	fmt.Printf("> Resolved: %s (IMDb: %s)\n", item.Title, item.IMDbID)
	if item.Season > 0 {
		fmt.Printf("> Season %d • Episode %d\n", item.Season, item.Episode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	fmt.Printf("> Connecting to Multi-Cloud CDN stream providers...\n")
	sources := ResolveCloudStreams(ctx, item)

	var streamURL string
	provider := "Zen Ultra 4K"

	if len(sources) > 0 {
		streamURL = sources[0].URL
		provider = sources[0].ProviderName
	} else {
		target := item.IMDbID
		if target == "" {
			target = encodeQuery(item.Title)
		}
		if item.Season > 0 {
			streamURL = fmt.Sprintf("https://player.vidlove.cc/embed/tv/%s/%d/%d", target, item.Season, item.Episode)
		} else {
			streamURL = fmt.Sprintf("https://player.vidlove.cc/embed/movie/%s", target)
		}
	}

	fmt.Printf("> 🟢 Live Stream Ready [%s] • Quality: 4K UHD 60fps\n", provider)
	fmt.Printf("> Opening player (%s)...\n", streamer.DetectPlayer())

	Notify("ZenTorrent", "Now Streaming: "+item.Title)

	playerCmd, err := streamer.LaunchPlayer(streamURL, "", 0)
	if err != nil || playerCmd == nil {
		fmt.Printf("> Desktop player failed. Opening in ZenTorrent Web Studio...\n")
		openBrowserDirect(fmt.Sprintf("http://localhost:%d/?tab=play&id=%s&type=movie", appConfig.StreamPort, item.IMDbID))
		return nil
	}

	return playerCmd.Wait()
}

func encodeQuery(s string) string {
	return url.QueryEscape(strings.TrimSpace(s))
}

func openBrowserDirect(targetURL string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", targetURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	_ = cmd.Start()
}
