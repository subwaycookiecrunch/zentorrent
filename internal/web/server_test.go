package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/subwaycookiecrunch/zentorrent/internal/debrid"
	"github.com/subwaycookiecrunch/zentorrent/internal/metadata"
	"github.com/subwaycookiecrunch/zentorrent/internal/search"
)

const testHashWeb = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"

type stubDiscovery struct{}

func (stubDiscovery) Suggest(ctx context.Context, prefix string, limit int) ([]metadata.Suggestion, error) {
	return []metadata.Suggestion{{TMDBID: 3, Title: "Drishyam", Year: 2015}}, nil
}

func (stubDiscovery) DiscoverStream(ctx context.Context, raw string, opts search.DiscoverOptions) <-chan search.StreamEvent {
	ch := make(chan search.StreamEvent, 1)
	ch <- search.StreamEvent{Type: search.EventFinal, Result: &search.DiscoveryResult{}}
	close(ch)
	return ch
}

type fakeTiers struct{}

func (fakeTiers) ResolveTiers(ctx context.Context, item debrid.MediaItem, magnet string, seeders int) []debrid.StreamSource {
	return []debrid.StreamSource{
		{Type: debrid.StreamDebrid, URL: "https://dl/x", ProviderName: "Real-Debrid"},
		{Type: debrid.StreamTorrent, Magnet: magnet},
	}
}

func TestDashboardServed(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "ZenTorrent") {
		t.Fatalf("dashboard missing: %d", rec.Code)
	}
}

func TestSuggestEndpoint(t *testing.T) {
	s := &Server{Discovery: stubDiscovery{}}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/suggest?q=dri", nil))
	if !strings.Contains(rec.Body.String(), "Drishyam") {
		t.Errorf("suggestions missing: %s", rec.Body.String())
	}
}

func TestTiersEndpoint(t *testing.T) {
	s := &Server{Tiers: fakeTiers{}}
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"title":"X","magnet":"magnet:?xt=urn:btih:` + testHashWeb + `","best_seeders":3}`)
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/tiers", body))
	var out []debrid.StreamSource
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Type != debrid.StreamDebrid || out[1].Type != debrid.StreamTorrent {
		t.Errorf("tier order wrong: %+v", out)
	}
}

func TestWebSocketHandshakeAndPush(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	called := false
	s := &Server{Status: func() any { called = true; return map[string]string{"status": "streaming"} }}
	snap := s.Status()
	payload, _ := json.Marshal(snap)

	go func() {
		_ = writeFrame(serverConn, payload)
	}()

	rw := bufio.NewReadWriter(bufio.NewReader(clientConn), bufio.NewWriter(clientConn))
	frame := readServerFrame(t, rw)
	if !strings.Contains(string(frame), "streaming") {
		t.Errorf("frame payload: %q", frame)
	}
	if !called {
		t.Error("status source never invoked")
	}
}

func TestHomeEndpoint(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/home", nil))
	if rec.Code != 200 {
		t.Fatalf("home code %d", rec.Code)
	}
	var res HomeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal home: %v", err)
	}
	if len(res.Spotlight) == 0 {
		t.Error("spotlight items missing in home response")
	}
}

func TestDetailsEndpoint(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/details?imdb=tt0816692&type=movie", nil))
	if rec.Code != 200 {
		t.Fatalf("details code %d", rec.Code)
	}
	var det MediaDetails
	if err := json.Unmarshal(rec.Body.Bytes(), &det); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if len(det.StreamingLinks) == 0 {
		t.Error("streaming links missing in details response")
	}
}

func TestTVEpisodesEndpoint(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/tv-episodes?imdb=tt0903747&season=1", nil))
	if rec.Code != 200 {
		t.Fatalf("tv-episodes code %d", rec.Code)
	}
	var eps []EpisodeInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &eps); err != nil {
		t.Fatalf("unmarshal episodes: %v", err)
	}
	if len(eps) == 0 {
		t.Error("episodes missing in tv-episodes response")
	}
}
