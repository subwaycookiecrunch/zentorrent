package extractors

import (
	"context"
	"io"
	"net/http"
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
	vl := NewVidLove()
	if vl.EmbedURL(debrid.MediaItem{TMDBID: 1288445}) != "https://player.vidlove.cc/embed/movie/1288445" {
		t.Error("vidlove movie URL wrong")
	}
	cz := NewZenLive()
	if cz.EmbedURL(debrid.MediaItem{TMDBID: 1288445, Season: 1, Episode: 2}) != "https://player.cinezo.live/embed/tv/1288445/1/2" {
		t.Error("zenlive tv URL wrong")
	}
	vf := NewVidFast()
	if vf.EmbedURL(debrid.MediaItem{TMDBID: 100}) != "https://vidfast.vc/movie/100" {
		t.Error("vidfast URL wrong")
	}
	all := AllExtractors()
	if len(all) < 5 {
		t.Errorf("expected at least 5 extractors, got %d", len(all))
	}
}

type mockRoundTripper func(req *http.Request) (*http.Response, error)

func (m mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return m(req) }

func TestExtractorCracksM3u8(t *testing.T) {
	oldClient := httpClient
	httpClient = &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`<script>var file="https://cdn.example.com/master.m3u8?tok=1";</script>`)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	defer func() { httpClient = oldClient }()

	v := NewVidSrc()
	src, err := v.Resolve(context.Background(), debrid.MediaItem{TMDBID: 42, Title: "Test"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.Contains(src.URL, "master.m3u8") || src.Type != debrid.StreamHLS {
		t.Errorf("bad source: %+v", src)
	}
}

func TestExtractorDegradesToEmbed(t *testing.T) {
	oldClient := httpClient
	httpClient = &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`<html>blocked by captcha</html>`)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	defer func() { httpClient = oldClient }()

	a := NewAutoEmbed()
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
