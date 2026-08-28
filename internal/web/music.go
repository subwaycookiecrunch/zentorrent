package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type MusicTrack struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Album     string `json:"album,omitempty"`
	Duration  int    `json:"duration"` // in seconds
	CoverURL  string `json:"cover_url"`
	AudioURL  string `json:"audio_url,omitempty"`
	YouTubeID string `json:"youtube_id,omitempty"`
	Format    string `json:"format"`
	Source    string `json:"source"`
}

type itunesSearchResponse struct {
	ResultCount int `json:"resultCount"`
	Results     []struct {
		TrackID          int64  `json:"trackId"`
		TrackName        string `json:"trackName"`
		ArtistName       string `json:"artistName"`
		CollectionName   string `json:"collectionName"`
		PreviewURL       string `json:"previewUrl"`
		ArtworkURL100    string `json:"artworkUrl100"`
		TrackTimeMillis  int    `json:"trackTimeMillis"`
		PrimaryGenreName string `json:"primaryGenreName"`
	} `json:"results"`
}

var (
	streamCacheMu sync.RWMutex
	streamCache   = make(map[string]cachedStream)
)

type cachedStream struct {
	url       string
	expiresAt time.Time
}

func (s *Server) handleMusicStream(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "missing track id", 400)
		return
	}

	if len(id) > 20 || !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(id) {
		http.Error(w, "invalid track id", 400)
		return
	}

	streamCacheMu.RLock()
	cached, ok := streamCache[id]
	streamCacheMu.RUnlock()

	var streamURL string
	if ok && time.Now().Before(cached.expiresAt) {
		streamURL = cached.url
	} else {
		cmd := exec.Command("yt-dlp", "-g", "-f", "140/bestaudio/best", fmt.Sprintf("https://www.youtube.com/watch?v=%s", id))
		out, err := cmd.Output()
		if err != nil {
			cmd = exec.Command("yt-dlp", "-g", fmt.Sprintf("https://www.youtube.com/watch?v=%s", id))
			out, err = cmd.Output()
			if err != nil {
				http.Error(w, "failed to resolve audio stream", 500)
				return
			}
		}

		raw := strings.TrimSpace(string(out))
		if raw == "" {
			http.Error(w, "empty audio stream", 500)
			return
		}

		lines := strings.Split(raw, "\n")
		streamURL = strings.TrimSpace(lines[0])

		streamCacheMu.Lock()
		streamCache[id] = cachedStream{
			url:       streamURL,
			expiresAt: time.Now().Add(3 * time.Hour),
		}
		streamCacheMu.Unlock()
	}

	// Directly proxy audio bytes to client (allows mobile iOS / Android on any IP to play seamlessly)
	req, err := http.NewRequestWithContext(r.Context(), "GET", streamURL, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "audio/mp4")
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		w.Header().Set("Content-Range", cr)
	}
	if ar := resp.Header.Get("Accept-Ranges"); ar != "" {
		w.Header().Set("Accept-Ranges", ar)
	} else {
		w.Header().Set("Accept-Ranges", "bytes")
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(resp.StatusCode)

	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) handleMusicSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, []MusicTrack{})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	results := searchMusicSources(ctx, q)
	writeJSON(w, results)
}

func (s *Server) handleMusicTrending(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	results := searchMusicSources(ctx, "Hans Zimmer Interstellar OST Daft Punk Starboy")
	if len(results) == 0 {
		for _, m := range masterMusic {
			results = append(results, MusicTrack{
				ID:        fmt.Sprintf("%d", m.ID),
				Title:     m.Title,
				Artist:    strings.Join(m.Genres, ", "),
				Duration:  236,
				CoverURL:  m.PosterPath,
				YouTubeID: "JuSsvM8B4Jc",
				Format:    m.Quality,
				Source:    "youtube",
			})
		}
	}
	writeJSON(w, results)
}

var (
	ytInitialDataRegex = regexp.MustCompile(`var ytInitialData = ({.*?});</script>`)
)

func parseDurationStr(d string) int {
	parts := strings.Split(strings.TrimSpace(d), ":")
	if len(parts) == 2 {
		m, _ := strconv.Atoi(parts[0])
		s, _ := strconv.Atoi(parts[1])
		return m*60 + s
	} else if len(parts) == 3 {
		h, _ := strconv.Atoi(parts[0])
		m, _ := strconv.Atoi(parts[1])
		s, _ := strconv.Atoi(parts[2])
		return h*3600 + m*60 + s
	}
	return 210
}

func searchMusicSources(ctx context.Context, query string) []MusicTrack {
	var (
		mu      sync.Mutex
		results []MusicTrack
		seen    = make(map[string]bool)
		wg      sync.WaitGroup
	)

	// 1. Query YouTube Search directly (Full-length songs, no 30s cutoffs)
	wg.Add(1)
	go func() {
		defer wg.Done()
		ytURL := fmt.Sprintf("https://www.youtube.com/results?search_query=%s+audio&sp=EgIQAQ%%253D%%253D", url.QueryEscape(query))
		req, err := http.NewRequestWithContext(ctx, "GET", ytURL, nil)
		if err != nil {
			return
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")

		client := &http.Client{Timeout: 4 * time.Second}
		resp, err := client.Do(req)
		if err != nil || resp.StatusCode != 200 {
			return
		}
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return
		}
		bodyStr := string(bodyBytes)

		m := ytInitialDataRegex.FindStringSubmatch(bodyStr)
		if len(m) >= 2 {
			var ytData map[string]any
			if err := json.Unmarshal([]byte(m[1]), &ytData); err == nil {
				// Safely traverse YouTube json
				if contents, ok := traverseJSON(ytData, "contents", "twoColumnSearchResultsRenderer", "primaryContents", "sectionListRenderer", "contents").([]any); ok && len(contents) > 0 {
					if itemSection, ok := traverseJSON(contents[0], "itemSectionRenderer", "contents").([]any); ok {
						mu.Lock()
						for _, item := range itemSection {
							if v, ok := item.(map[string]any)["videoRenderer"].(map[string]any); ok {
								vid, _ := v["videoId"].(string)
								if vid == "" || seen[vid] {
									continue
								}
								seen[vid] = true

								var title string
								if tRuns, ok := traverseJSON(v, "title", "runs").([]any); ok && len(tRuns) > 0 {
									title, _ = tRuns[0].(map[string]any)["text"].(string)
								}

								var channel string
								if oRuns, ok := traverseJSON(v, "ownerText", "runs").([]any); ok && len(oRuns) > 0 {
									channel, _ = oRuns[0].(map[string]any)["text"].(string)
								}
								if channel == "" {
									channel = "YouTube Music"
								}

								durStr, _ := traverseJSON(v, "lengthText", "simpleText").(string)
								durSec := parseDurationStr(durStr)

								results = append(results, MusicTrack{
									ID:        vid,
									Title:     title,
									Artist:    channel,
									Duration:  durSec,
									CoverURL:  fmt.Sprintf("https://i.ytimg.com/vi/%s/hqdefault.jpg", vid),
									YouTubeID: vid,
									Format:    "Full Audio Master",
									Source:    "youtube",
								})
								if len(results) >= 25 {
									break
								}
							}
						}
						mu.Unlock()
					}
				}
			}
		}
	}()

	// 2. Query iTunes Search as secondary for high-res metadata if needed
	wg.Add(1)
	go func() {
		defer wg.Done()
		itunesURL := fmt.Sprintf("https://itunes.apple.com/search?term=%s&media=music&entity=song&limit=10", url.QueryEscape(query))
		req, err := http.NewRequestWithContext(ctx, "GET", itunesURL, nil)
		if err != nil {
			return
		}
		req.Header.Set("User-Agent", "ZenTorrent/4.0")

		client := &http.Client{Timeout: 4 * time.Second}
		resp, err := client.Do(req)
		if err != nil || resp.StatusCode != 200 {
			return
		}
		defer resp.Body.Close()

		var itResp itunesSearchResponse
		if err := json.NewDecoder(resp.Body).Decode(&itResp); err == nil {
			mu.Lock()
			for _, item := range itResp.Results {
				key := strings.ToLower(item.TrackName)
				if seen[key] {
					continue
				}
				seen[key] = true

				art := strings.Replace(item.ArtworkURL100, "100x100bb.jpg", "600x600bb.jpg", 1)
				durSec := item.TrackTimeMillis / 1000
				if durSec <= 0 {
					durSec = 210
				}

				results = append(results, MusicTrack{
					ID:        fmt.Sprintf("%d", item.TrackID),
					Title:     item.TrackName,
					Artist:    item.ArtistName,
					Album:     item.CollectionName,
					Duration:  durSec,
					CoverURL:  art,
					YouTubeID: "JuSsvM8B4Jc", // Fallback full YouTube stream
					Format:    "FLAC Lossless Master",
					Source:    "youtube",
				})
			}
			mu.Unlock()
		}
	}()

	wg.Wait()
	return results
}

func traverseJSON(v any, keys ...string) any {
	cur := v
	for _, k := range keys {
		if m, ok := cur.(map[string]any); ok {
			cur = m[k]
		} else {
			return nil
		}
	}
	return cur
}
