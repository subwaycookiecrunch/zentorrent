package web

import (
	"context"
	"encoding/json"
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
	called := false
	s := &Server{Status: func() any { called = true; return map[string]string{"status": "streaming"} }}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn, rw, err := dialWS(t, srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	frame := readServerFrame(t, rw)
	if !strings.Contains(string(frame), "streaming") {
		t.Errorf("frame payload: %q", frame)
	}
	if !called {
		t.Error("status source never invoked")
	}
}
