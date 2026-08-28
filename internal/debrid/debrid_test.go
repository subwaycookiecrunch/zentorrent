package debrid

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type mockRoundTripper func(req *http.Request) (*http.Response, error)

func (m mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return m(req) }

const testHash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b"

func TestRealDebridCacheHit(t *testing.T) {
	rd := NewRealDebridWithHTTP("testkey", &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("Authorization") != "Bearer testkey" {
				return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
			}
			switch {
			case strings.Contains(req.URL.Path, "instantAvailability"):
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"` + testHash + `":{"rd":[{"1":{}}]}}`)), Header: make(http.Header)}, nil
			case strings.Contains(req.URL.Path, "addMagnet"):
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"t123"}`)), Header: make(http.Header)}, nil
			case strings.Contains(req.URL.Path, "torrents/info/t123"):
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"t123","status":"downloaded","links":["https://rd/link/1"],
					"files":[{"id":0,"path":"/data/Movie.1080p.mkv","bytes":3200000000}]}`)), Header: make(http.Header)}, nil
			case strings.Contains(req.URL.Path, "unrestrict"):
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"download":"https://rd.dl/abc","filename":"Movie.1080p.mkv","filesize":3200000000}`)), Header: make(http.Header)}, nil
			case strings.Contains(req.URL.Path, "delete"):
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
			default:
				return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
			}
		}),
	})

	src, err := rd.Resolve(context.Background(), MediaItem{
		Magnet: "magnet:?xt=urn:btih:" + testHash,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if src.Type != StreamDebrid || src.ProviderName != "Real-Debrid" {
		t.Errorf("bad source: %+v", src)
	}
	if src.Quality != "1080p" || !strings.HasSuffix(src.Title, ".mkv") {
		t.Errorf("quality/title wrong: %+v", src)
	}
	if !strings.HasPrefix(src.URL, "https://rd.dl/") {
		t.Errorf("unrestricted URL missing: %q", src.URL)
	}
}

func TestRealDebridNotCached(t *testing.T) {
	rd := NewRealDebridWithHTTP("k", &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"` + testHash + `":{"rd":[]}}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	_, err := rd.Resolve(context.Background(), MediaItem{Magnet: "magnet:?xt=urn:btih:" + testHash})
	if err != ErrNotCached {
		t.Fatalf("want ErrNotCached, got %v", err)
	}
}

func TestTorBoxResolve(t *testing.T) {
	tb := NewTorBoxWithHTTP("k", &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Path, "mylist") && req.URL.Query().Get("cached") == "true":
				return &http.Response{
					StatusCode: 200,
					Body: io.NopCloser(strings.NewReader(`{"success":true,"data":[{"id":7,"hash":"` + strings.ToUpper(testHash) +
						`","name":"Show.S01","cached":true,"download_state":"cached",
						  "files":[{"file_id":1,"short_name":"ep1.mkv","size":2000000000}]}]}`)),
					Header: make(http.Header),
				}, nil
			case strings.Contains(req.URL.Path, "requestdl"):
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(`{"success":true,"data":[{"filename":"ep1.mkv","size":2000000000,"link":"https://tb.dl/x"}]}`)),
					Header:     make(http.Header),
				}, nil
			default:
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(`{"success":false,"error":"unexpected"}`)),
					Header:     make(http.Header),
				}, nil
			}
		}),
	})

	src, err := tb.Resolve(context.Background(), MediaItem{
		Magnet:   "magnet:?xt=urn:btih:" + testHash,
		InfoHash: testHash,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if src.ProviderName != "TorBox" || !strings.HasPrefix(src.URL, "https://tb.dl/") {
		t.Errorf("bad source: %+v", src)
	}
}

func TestQualityFromSize(t *testing.T) {
	cases := map[int64]string{
		20 << 30: "4K REMUX", 9 << 30: "4K", 4 << 30: "1080p", 600 << 20: "720p", 10 << 20: "SD",
	}
	for size, want := range cases {
		if got := QualityFromSize(size); got != want {
			t.Errorf("QualityFromSize(%d)=%s want %s", size, got, want)
		}
	}
}

func TestMissingKeyUnauthorized(t *testing.T) {
	rd := NewRealDebrid("")
	_, err := rd.InstantAvailable(context.Background(), testHash)
	if err != ErrUnauthorized {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}
