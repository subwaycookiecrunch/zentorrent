package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/subwaycookiecrunch/zentorrent/internal/metadata"
)

// Temporary adversarial probe: records every query the Discover Torznab
// fan-out actually sends for the romanized "bahubali" user query against a
// Baahubali row whose OriginalTitle is native script and whose alias is the
// romanized form users type.
func TestTmpProbeDiscoverQueries(t *testing.T) {
	c, err := metadata.OpenCatalog(":memory:")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	defer c.Close()

	movies := []metadata.Movie{
		{ID: 2, Title: "Baahubali: The Beginning", OriginalTitle: "బాహుబలి",
			Aliases: "Bahubali", IMDbID: "tt4849438", Year: 2015,
			Popularity: 60, OriginalLanguage: "te"},
	}
	if err := c.UpsertMovieBatch(context.Background(), movies); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		mu.Lock()
		got = append(got, q.Encode())
		mu.Unlock()
		w.Write([]byte(`<?xml version="1.0"?><rss><channel></channel></rss>`))
	}))
	defer srv.Close()

	agg := NewAggregator(c, nil,
		NewTorznabClient([]Endpoint{{Name: "rec", BaseURL: srv.URL, APIKey: "k"}}),
		nil, nil, AggregatorConfig{})

	res, err := agg.Discover(context.Background(), "bahubali 2015", DiscoverOptions{})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	mu.Lock()
	sent := append([]string(nil), got...)
	mu.Unlock()
	sort.Strings(sent)

	t.Logf("resolved movie: title=%q original=%q aliases=%v imdb=%s",
		res.Movie.Title, res.Movie.OriginalTitle, res.Movie.Aliases, res.Movie.IMDbID)
	for i, s := range sent {
		t.Logf("request %d: %s", i, s)
	}

	aliasQueried := false
	for _, s := range sent {
		if strings.Contains(strings.ToLower(s), "q=bahubali") &&
			!strings.Contains(strings.ToLower(s), "baahubali") {
			aliasQueried = true
		}
	}
	if aliasQueried {
		t.Log("ALIAS WAS QUERIED — finding would be refuted")
	} else {
		t.Log("ALIAS NEVER QUERIED — finding confirmed")
	}

	terms := res.Movie.SearchTerms()
	cut := terms
	if len(cut) > 3 {
		cut = cut[:3]
	}
	t.Logf("SearchTerms()=%q ; terms kept by Discover=%q", terms, cut)
}
