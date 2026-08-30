// Package web serves ZenTorrent's embedded dashboard: a single-binary
// HTML5 player (Hls.js for HLS tiers, progressive MP4 for direct links)
// with a REST API and live WebSocket status, mounted on the same port as
// the local VOD stream server.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/subwaycookiecrunch/zentorrent/internal/debrid"
	"github.com/subwaycookiecrunch/zentorrent/internal/engine"
	"github.com/subwaycookiecrunch/zentorrent/internal/metadata"
	"github.com/subwaycookiecrunch/zentorrent/internal/search"
)

//go:embed dist/index.html
var distFS embed.FS

// Discovery is the slice of the aggregator the web UI needs.
type Discovery interface {
	Suggest(ctx context.Context, prefix string, limit int) ([]metadata.Suggestion, error)
	DiscoverStream(ctx context.Context, raw string, opts search.DiscoverOptions) <-chan search.StreamEvent
}

// TierResolver turns one torrent candidate into an ordered list of playable
// sources across every tier (debrid cache first, then P2P, then HLS).
type TierResolver interface {
	ResolveTiers(ctx context.Context, item debrid.MediaItem, magnet string, bestSeeders int) []debrid.StreamSource
}

// StatusSource supplies the live playback snapshot broadcast over /ws.
type StatusSource func() any

// Server is the embedded web dashboard.
type Server struct {
	Discovery  Discovery
	Catalog    *metadata.Catalog
	Tiers      TierResolver
	Status     StatusSource
	PlayHook   func(src debrid.StreamSource) // invoked on POST /api/play
	searchLock sync.Mutex                    // one discovery stream at a time
	lastResult atomic.Value                  // cached *search.DiscoveryResult
}

// Handler returns the full HTTP mux for the dashboard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/home", s.handleHome)
	mux.HandleFunc("/api/details", s.handleDetails)
	mux.HandleFunc("/api/genre", s.handleGenreCatalog)
	mux.HandleFunc("/api/tv-episodes", s.handleTVEpisodes)
	mux.HandleFunc("/api/suggest", s.handleSuggest)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/results", s.handleResults)
	mux.HandleFunc("/api/torrents", s.handleLiveTorrents)
	mux.HandleFunc("/api/party/create", s.handlePartyCreate)
	mux.HandleFunc("/api/party/join", s.handlePartyJoin)
	mux.HandleFunc("/api/party/sync", s.handlePartySync)
	mux.HandleFunc("/api/party/action", s.handlePartyAction)
	mux.HandleFunc("/api/trackers/boost", s.handleTrackersBoost)
	mux.HandleFunc("/api/downloads", s.handleDownloads)
	mux.HandleFunc("/api/tiers", s.handleTiers)
	mux.HandleFunc("/api/play", s.handlePlay)
	mux.HandleFunc("/api/music/search", s.handleMusicSearch)
	mux.HandleFunc("/api/music/trending", s.handleMusicTrending)
	mux.HandleFunc("/api/music/stream", s.handleMusicStream)
	mux.HandleFunc("/manifest.json", s.handleManifest)
	mux.HandleFunc("/sw.js", s.handleServiceWorker)
	mux.HandleFunc("/app-icon.svg", s.handleAppIcon)
	mux.HandleFunc("/ws", s.handleWS)

	return mux
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	body, err := distFS.ReadFile("dist/index.html")
	if err != nil {
		http.Error(w, "dashboard missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, []any{})
		return
	}

	type card struct {
		ID           int64   `json:"id"`
		IMDbID       string  `json:"imdb_id,omitempty"`
		Title        string  `json:"title"`
		Year         int     `json:"year"`
		MediaType    string  `json:"media_type"`
		VoteAverage  float64 `json:"vote_average"`
		Genres       string  `json:"genres,omitempty"`
		Overview     string  `json:"overview,omitempty"`
		PosterPath   string  `json:"poster_path,omitempty"`
		BackdropPath string  `json:"backdrop_path,omitempty"`
	}

	out := make([]card, 0)
	seen := make(map[string]bool)
	var mu sync.Mutex
	lq := strings.ToLower(q)

	addCard := func(c card) {
		mu.Lock()
		defer mu.Unlock()
		key := strings.ToLower(c.Title)
		if seen[key] || c.Title == "" {
			return
		}
		seen[key] = true
		out = append(out, c)
	}

	// 1. Check in curated master collections first
	for _, m := range append(append(append(masterSpotlight, masterMovies...), masterSeries...), masterAnime...) {
		key := strings.ToLower(m.Title)
		if strings.Contains(key, lq) || (m.IMDbID != "" && strings.Contains(strings.ToLower(m.IMDbID), lq)) {
			addCard(card{
				ID:           m.ID,
				IMDbID:       m.IMDbID,
				Title:        m.Title,
				Year:         m.Year,
				MediaType:    m.MediaType,
				VoteAverage:  m.VoteAverage,
				Genres:       strings.Join(m.Genres, ", "),
				Overview:     m.Overview,
				PosterPath:   m.PosterPath,
				BackdropPath: m.BackdropPath,
			})
		}
	}

	// 2. Query Cinemeta search API for instant rich posters and real IMDb metadata
	cinemetaEndpoints := []string{
		fmt.Sprintf("https://v3-cinemeta.strem.io/catalog/movie/top/search=%s.json", url.PathEscape(q)),
		fmt.Sprintf("https://v3-cinemeta.strem.io/catalog/series/top/search=%s.json", url.PathEscape(q)),
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for _, ep := range cinemetaEndpoints {
		wg.Add(1)
		go func(endpoint string) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if err != nil {
				return
			}
			resp, err := sharedClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var payload struct {
				Metas []struct {
					ID          string   `json:"id"`
					Type        string   `json:"type"`
					Name        string   `json:"name"`
					Poster      string   `json:"poster"`
					Background  string   `json:"background"`
					Year        any      `json:"year"`
					ImdbRating  string   `json:"imdbRating"`
					Genres      []string `json:"genres"`
					Description string   `json:"description"`
				} `json:"metas"`
			}
			if json.NewDecoder(resp.Body).Decode(&payload) == nil {
				for _, it := range payload.Metas {
					if it.Name == "" || it.ID == "" {
						continue
					}
					year := 2024
					switch v := it.Year.(type) {
					case float64:
						year = int(v)
					case string:
						if y, err := strconv.Atoi(strings.Split(v, "–")[0]); err == nil {
							year = y
						}
					}

					rating, _ := strconv.ParseFloat(it.ImdbRating, 64)
					if rating <= 0 {
						rating = 8.0
					}

					mType := it.Type
					if mType == "series" {
						mType = "tv"
					}

					poster := it.Poster
					if poster == "" {
						poster = fmt.Sprintf("https://images.metahub.space/poster/medium/%s/img", it.ID)
					}

					addCard(card{
						IMDbID:       it.ID,
						Title:        it.Name,
						Year:         year,
						MediaType:    mType,
						VoteAverage:  rating,
						Genres:       strings.Join(it.Genres, ", "),
						Overview:     it.Description,
						PosterPath:   poster,
						BackdropPath: it.Background,
					})
				}
			}
		}(ep)
	}

	wg.Wait()

	// 3. Query local discovery suggestions if available
	if s.Discovery != nil {
		if items, err := s.Discovery.Suggest(ctx, q, 10); err == nil {
			for _, it := range items {
				poster := it.PosterPath
				if poster == "" && it.IMDbID != "" {
					poster = fmt.Sprintf("https://images.metahub.space/poster/medium/%s/img", it.IMDbID)
				}
				addCard(card{
					ID:           it.TMDBID,
					IMDbID:       it.IMDbID,
					Title:        it.Title,
					Year:         it.Year,
					MediaType:    it.MediaType,
					VoteAverage:  it.VoteAverage,
					Genres:       it.Genres,
					Overview:     it.Overview,
					PosterPath:   poster,
					BackdropPath: it.BackdropPath,
				})
			}
		}
	}

	if len(out) > 20 {
		out = out[:20]
	}

	writeJSON(w, out)
}

// handleSearch kicks a streaming discovery run; results land in
// /api/results once complete (the TUI-style streaming is collapsed into a
// single poll for browser simplicity).
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || s.Discovery == nil {
		http.Error(w, "missing q", http.StatusBadRequest)
		return
	}
	if !s.searchLock.TryLock() {
		http.Error(w, "search already running", http.StatusTooManyRequests)
		return
	}
	// Drop the previous search's results up front so /api/results reports
	// ready:false instead of serving the old movie's rows as the new query runs.
	s.lastResult.Store((*search.DiscoveryResult)(nil))
	go func() {
		defer s.searchLock.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		for ev := range s.Discovery.DiscoverStream(ctx, q, search.DiscoverOptions{}) {
			if ev.Type == search.EventFinal && ev.Result != nil {
				s.lastResult.Store(ev.Result)
			}
		}
	}()
	writeJSON(w, map[string]bool{"started": true})
}

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	v := s.lastResult.Load()
	res, ok := v.(*search.DiscoveryResult)
	if !ok || res == nil {
		writeJSON(w, map[string]any{"ready": false})
		return
	}
	type row struct {
		Title    string `json:"title"`
		Magnet   string `json:"magnet"`
		InfoHash string `json:"info_hash"`
		Size     int64  `json:"size"`
		Seeders  int    `json:"seeders"`
		Source   string `json:"source"`
		Quality  string `json:"quality"`
	}
	rows := make([]row, 0, len(res.Ranked))
	for _, rc := range res.Ranked {
		rows = append(rows, row{
			Title:    rc.Title,
			Magnet:   rc.SynthesizeMagnet(),
			InfoHash: rc.InfoHash,
			Size:     rc.SizeBytes,
			Seeders:  rc.Seeders,
			Source:   rc.Source,
			Quality:  search.ParseResolution(rc.Title),
		})
	}
	movie := map[string]any{}
	if res.Movie != nil {
		movie["title"] = res.Movie.Title
		movie["year"] = res.Movie.Year
		movie["tmdb"] = res.Movie.TMDBID
		movie["imdb"] = res.Movie.IMDbID
		movie["poster"] = res.Movie.PosterPath
	}
	writeJSON(w, map[string]any{"ready": true, "movie": movie, "results": rows})
}

// handleTiers resolves all three tiers for one candidate.
func (s *Server) handleTiers(w http.ResponseWriter, r *http.Request) {
	if s.Tiers == nil {
		writeJSON(w, []any{})
		return
	}
	var req struct {
		Title       string `json:"title"`
		Magnet      string `json:"magnet"`
		InfoHash    string `json:"info_hash"`
		TMDBID      int64  `json:"tmdb"`
		IMDbID      string `json:"imdb"`
		BestSeeders int    `json:"best_seeders"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	srcs := s.Tiers.ResolveTiers(ctx, debrid.MediaItem{
		Title: req.Title, TMDBID: req.TMDBID, IMDbID: req.IMDbID,
		Magnet: req.Magnet, InfoHash: req.InfoHash,
	}, req.Magnet, req.BestSeeders)
	writeJSON(w, srcs)
}

// handlePlay notifies the desktop app to take over a chosen source
// (launches MPV/VLC locally); browsers play sources directly via <video>.
func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	var src debrid.StreamSource
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("[Web] play request: %s via %s", src.Title, src.ProviderName)
	if s.PlayHook != nil {
		go s.PlayHook(src)
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleTrackersBoost(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"active":   true,
		"trackers": engine.GetTrackersCount(),
		"dht":      480,
	})
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write([]byte(`{
  "name": "ZenTorrent Cinema",
  "short_name": "ZenTorrent",
  "start_url": "/",
  "display": "standalone",
  "background_color": "#060608",
  "theme_color": "#060608",
  "orientation": "any",
  "icons": [
    {
      "src": "/app-icon.svg",
      "sizes": "192x192 512x512",
      "type": "image/svg+xml",
      "purpose": "any maskable"
    }
  ]
}`))
}

func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(`const CACHE_NAME = 'zentorrent-v4';
self.addEventListener('install', (e) => { self.skipWaiting(); });
self.addEventListener('activate', (e) => { e.waitUntil(clients.claim()); });
self.addEventListener('fetch', (e) => {
  if (e.request.method !== 'GET') return;
  e.respondWith(fetch(e.request).catch(() => caches.match(e.request)));
});`))
}

func (s *Server) handleAppIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512">
  <rect width="512" height="512" rx="128" fill="#060608"/>
  <rect x="24" y="24" width="464" height="464" rx="104" fill="none" stroke="rgba(255,255,255,0.08)" stroke-width="8"/>
  <path d="M128 144 H384 L180 368 H384" stroke="url(#grad)" stroke-width="54" stroke-linecap="round" stroke-linejoin="round" fill="none"/>
  <defs>
    <linearGradient id="grad" x1="128" y1="144" x2="384" y2="368" gradientUnits="userSpaceOnUse">
      <stop offset="0%" stop-color="#a855f7"/>
      <stop offset="100%" stop-color="#06b6d4"/>
    </linearGradient>
  </defs>
</svg>`))
}
