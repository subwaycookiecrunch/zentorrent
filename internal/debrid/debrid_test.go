package debrid

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testHash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b"

func TestRealDebridCacheHit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer testkey" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "instantAvailability"):
			w.Write([]byte(`{"` + testHash + `":{"rd":[{"1":{}}]}}`))
		case strings.Contains(r.URL.Path, "addMagnet"):
			w.Write([]byte(`{"id":"t123"}`))
		case strings.Contains(r.URL.Path, "torrents/info/t123"):
			w.Write([]byte(`{"id":"t123","status":"downloaded","links":["https://rd/link/1"],
				"files":[{"id":0,"path":"/data/Movie.1080p.mkv","bytes":3200000000}]}`))
		case strings.Contains(r.URL.Path, "unrestrict"):
			w.Write([]byte(`{"download":"https://rd.dl/abc","filename":"Movie.1080p.mkv","filesize":3200000000}`))
		case strings.Contains(r.URL.Path, "delete"):
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	rd := NewRealDebrid("testkey")
	rd.base = srv.URL

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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"` + testHash + `":{"rd":[]}}`)) // empty = uncached
	}))
	defer srv.Close()

	rd := NewRealDebrid("k")
	rd.base = srv.URL
	_, err := rd.Resolve(context.Background(), MediaItem{Magnet: "magnet:?xt=urn:btih:" + testHash})
	if err != ErrNotCached {
		t.Fatalf("want ErrNotCached, got %v", err)
	}
}

func TestTorBoxResolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "mylist") && r.URL.Query().Get("cached") == "true":
			w.Write([]byte(`{"success":true,"data":[{"id":7,"hash":"` + strings.ToUpper(testHash) +
				`","name":"Show.S01","cached":true,"download_state":"cached",
				  "files":[{"file_id":1,"short_name":"ep1.mkv","size":2000000000}]}]}`))
		case strings.Contains(r.URL.Path, "requestdl"):
			w.Write([]byte(`{"success":true,"data":[{"filename":"ep1.mkv","size":2000000000,"link":"https://tb.dl/x"}]}`))
		default:
			w.Write([]byte(`{"success":false,"error":"unexpected"}`))
		}
	}))
	defer srv.Close()

	tb := NewTorBox("k")
	tb.base = srv.URL

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
