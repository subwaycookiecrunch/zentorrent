package extractors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/subwaycookiecrunch/zentorrent/internal/debrid"
)

func TestEmbedURLConstruction(t *testing.T) {
	v := NewVidSrc()
	got := v.EmbedURL(debrid.MediaItem{TMDBID: 157336})
	if got != "https://vidsrc.to/embed/movie/157336" {
		t.Errorf("movie embed = %q", got)
	}
	tv := v.EmbedURL(debrid.MediaItem{TMDBID: 1399, Season: 2, Episode: 5})
	if tv != "https://vidsrc.to/embed/tv/1399/2/5" {
		t.Errorf("tv embed = %q", tv)
	}
	l := NewVidLink()
	if l.EmbedURL(debrid.MediaItem{TMDBID: 1}) != "https://vidlink.pro/movie/1" {
		t.Error("vidlink base wrong")
	}
	a := NewAutoEmbed()
	if a.EmbedURL(debrid.MediaItem{TMDBID: 1}) != "https://autoembed.cc/embed/movie/1" {
		t.Error("autoembed base wrong")
	}
}

func TestExtractorCracksM3u8(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<script>var file="https://cdn.example.com/master.m3u8?tok=1";</script>`))
	}))
	defer srv.Close()

	v := NewVidSrc()
	v.base = srv.URL // deterministic test target

	src, err := v.Resolve(context.Background(), debrid.MediaItem{TMDBID: 42, Title: "Test"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.Contains(src.URL, "master.m3u8") || src.Type != debrid.StreamHLS {
		t.Errorf("bad source: %+v", src)
	}
}

func TestExtractorDegradesToEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>blocked by captcha</html>"))
	}))
	defer srv.Close()

	a := NewAutoEmbed()
	a.base = srv.URL + "/embed"

	src, err := a.Resolve(context.Background(), debrid.MediaItem{TMDBID: 7, Title: "Blocked"})
	if err != nil {
		t.Fatalf("should degrade to embed URL, got %v", err)
	}
	if src.Quality != "embed" || !strings.Contains(src.URL, "/movie/7") {
		t.Errorf("degraded source wrong: %+v", src)
	}
}

func TestExtractorNeedsIdentity(t *testing.T) {
	v := NewVidLink()
	if _, err := v.Resolve(context.Background(), debrid.MediaItem{Title: "no ids"}); err == nil {
		t.Fatal("expected ErrNoStream without TMDB/IMDb identity")
	}
}
