package search

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/subwaycookiecrunch/zentorrent/internal/metadata"
)

func testCatalogForAgg(t *testing.T) *metadata.Catalog {
	t.Helper()
	c, err := metadata.OpenCatalog(":memory:")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	movies := []metadata.Movie{
		{ID: 1, Title: "Interstellar", IMDbID: "tt0816692", Year: 2014,
			Popularity: 90, OriginalLanguage: "en"},
		{ID: 2, Title: "Baahubali: The Beginning", Aliases: "Bahubali",
			IMDbID: "tt4849438", Year: 2015, Popularity: 60, OriginalLanguage: "te"},
		{ID: 3, Title: "Drishyam", IMDbID: "tt4430212", Year: 2015,
			Popularity: 40, OriginalLanguage: "hi"},
	}
	if err := c.UpsertMovieBatch(context.Background(), movies); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return c
}

func TestResolveQueryFuzzy(t *testing.T) {
	agg := NewAggregator(testCatalogForAgg(t), nil, NewTorznabClient(nil), nil, nil,
		AggregatorConfig{MinResolveScore: 0.45})

	for q, want := range map[string]string{
		"interstelar 1080p": "Interstellar",
		"drishiam":          "Drishyam",
		"bahubali":          "Baahubali: The Beginning",
	} {
		m, err := agg.ResolveQuery(context.Background(), q)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if m.Title != want {
			t.Errorf("%q resolved to %q, want %q", q, m.Title, want)
		}
	}

	// Direct IMDb ID short-circuit.
	m, err := agg.ResolveQuery(context.Background(), "watch tt0816692 please")
	if err != nil || m.IMDbID != "tt0816692" || m.Title != "Interstellar" {
		t.Errorf("imdb shortcut: %+v err=%v", m, err)
	}

	// Garbage must fail with ErrNoMatch, not panic.
	if _, err := agg.ResolveQuery(context.Background(), "zzqqxx totally unknown 12345"); err == nil {
		t.Error("expected ErrNoMatch for garbage query")
	} else if !strings.Contains(err.Error(), "no confident") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestDedupeCandidates(t *testing.T) {
	in := []TorrentCandidate{
		{InfoHash: "aa", Title: "Same Movie 1080p", SizeBytes: 100, Seeders: 10, Source: "prowlarr", Magnet: "magnet:?xt=urn:btih:aa"},
		{InfoHash: "AA", Title: "Same Movie 1080p", SizeBytes: 100, Seeders: 42, Source: "dht"},
		{InfoHash: "bb", Title: "Other Movie 720p", SizeBytes: 200, Seeders: 5, Source: "dht"},
		// No infohash anywhere: falls back to title+size fingerprint.
		{Title: "No Hash Flick", SizeBytes: 300 << 20, Seeders: 7, Source: "scraperA"},
		{Title: "No.Hash.Flick", SizeBytes: 300 << 20, Seeders: 9, Source: "scraperB"},
	}

	out := DedupeCandidates(in)
	if len(out) != 3 {
		t.Fatalf("want 3 after dedup, got %d: %+v", len(out), out)
	}

	first := out[0]
	if first.Seeders != 42 {
		t.Errorf("merged seeders should take max, got %d", first.Seeders)
	}
	if first.InfoHash != "aa" {
		t.Errorf("infohash should normalize lowercase: %q", first.InfoHash)
	}
	if !strings.Contains(first.Source, "prowlarr") || !strings.Contains(first.Source, "dht") {
		t.Errorf("sources should merge: %q", first.Source)
	}
	if first.Magnet == "" {
		t.Errorf("magnet from either side must survive")
	}

	noHash := out[2]
	if noHash.Seeders != 9 || !strings.Contains(noHash.Source, "scraperA+scraperB") &&
		!strings.Contains(noHash.Source, "scraperB+scraperA") {
		t.Errorf("hashless merge wrong: %+v", noHash)
	}
}

func TestFilterJunk(t *testing.T) {
	in := []TorrentCandidate{
		{Title: "Good.Movie.1080p.BluRay-GROUP"},
		{Title: "Movie.1080p.PASSWORD.PROTECTED"}, // keyword junk
		{Passworded: true, Title: "Looks.Fine.But.Flagged"},
		{Title: "setup.exe installer scam"},
		{Title: "CAM.Print.Watch.Early"}, // kept: penalized in rank instead
	}
	out := FilterJunk(in)
	if len(out) != 2 {
		t.Fatalf("want good+cam survivors (2), got %d: %+v", len(out), out)
	}
	if ParseResolution(out[1].Title) != "" && strings.Contains(strings.ToLower(out[1].Title), "password") {
		t.Error("junk leaked through")
	}
}

func candidatesForRank() []TorrentCandidate {
	return []TorrentCandidate{
		{InfoHash: "hi-res-low-seed", Title: "Interstellar.2014.2160p.BluRay.x265-GECKOS", Seeders: 12},
		{InfoHash: "low-res-high-seed", Title: "Interstellar.2014.480p.WEBRip.x264-RARBG", Seeders: 500},
		{InfoHash: "match-lang", Title: "Baahubali.2015.1080p.AMZN.WebRip.Dual.Audio.Hindi-DDP5.1.x264-TOMMY", Seeders: 30},
		{InfoHash: "cam", Title: "Bahubali.2015.Hindi.HDTS.x264-NOGROUP", Seeders: 200},
		{InfoHash: "dead", Title: "Interstellar.2014.1080p.BluRay.x264-YIFY", Seeders: 0},
	}
}

func TestRankCandidatesResolutionIntent(t *testing.T) {
	ranked := RankCandidates(candidatesForRank(), &ResolvedMovie{Language: "en"},
		DiscoverOptions{Resolution: "2160p"})

	var hiRes, lowRes int
	for i, r := range ranked {
		switch r.InfoHash {
		case "hi-res-low-seed":
			hiRes = i
		case "low-res-high-seed":
			lowRes = i
		}
	}
	if hiRes > lowRes {
		t.Errorf("requested 2160p should outrank a 480p even with fewer seeders:\n%+v",
			rankedRankedTitles(ranked))
	}
	for _, r := range ranked {
		if r.InfoHash == "hi-res-low-seed" {
			foundRes := false
			for _, reason := range r.Reasons {
				if strings.Contains(reason, "matches requested") {
					foundRes = true
				}
			}
			if !foundRes {
				t.Errorf("missing resolution reason: %v", r.Reasons)
			}
		}
	}
}

func rankedRankedTitles(rs []RankedCandidate) string {
	var sb strings.Builder
	for i, r := range rs {
		fmt.Fprintf(&sb, "%d:%s(s=%d,sc=%.0f) ", i, r.Title, r.Seeders, r.Score)
	}
	return sb.String()
}

func TestRankCandidatesLanguageBoost(t *testing.T) {
	ranked := RankCandidates(candidatesForRank(),
		&ResolvedMovie{Title: "Baahubali: The Beginning", Language: "te"},
		DiscoverOptions{})

	langIdx, camIdx := -1, -1
	for i, r := range ranked {
		switch r.InfoHash {
		case "match-lang":
			langIdx = i
		case "cam":
			camIdx = i
		}
	}
	if langIdx < 0 || camIdx < 0 {
		t.Fatalf("candidates lost: %s", rankedRankedTitles(ranked))
	}
	if langIdx > camIdx {
		t.Errorf("dual-audio hindi release should beat a 200-seeder cam:\n%s",
			rankedRankedTitles(ranked))
	}
}

type stubScraper struct {
	name    string
	cands   []TorrentCandidate
	wantRaw string
	check   func(movie *ResolvedMovie, raw string) error
}

func (s *stubScraper) Name() string { return s.name }
func (s *stubScraper) Search(ctx context.Context, movie *ResolvedMovie, raw string) ([]TorrentCandidate, error) {
	if s.check != nil {
		if err := s.check(movie, raw); err != nil {
			return nil, err
		}
	}
	return s.cands, nil
}

func torznabClientFixture(t *testing.T, title, hash string, seeders int) *TorznabClient {
	t.Helper()
	xml := fmt.Sprintf(`<?xml version="1.0"?>
<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item>
<title>%s</title>
<newznab:attr name="infohash" value="%s"/>
<newznab:attr name="seeders" value="%d"/>
<enclosure url="magnet:?xt=urn:btih:%s&amp;dn=x"/>
</item></channel></rss>`, title, hash, seeders, hash)
	return NewTorznabClientWithHTTP([]Endpoint{{Name: "testidx", BaseURL: "https://idx.test/api", APIKey: "k"}}, &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(xml)),
				Header:     make(http.Header),
			}, nil
		}),
	})
}

func TestDiscoverEndToEnd(t *testing.T) {
	torznabCl := torznabClientFixture(t,
		"Bahubali.The.Beginning.2015.1080p.BluRay.x264-TOMMY", knownHashAgg, 77)

	cat := testCatalogForAgg(t)
	agg := NewAggregator(cat, nil,
		torznabCl,
		nil,
		[]Scraper{&stubScraper{
			name:  "stub",
			cands: []TorrentCandidate{{InfoHash: knownHashAgg, Title: "dup-from-scraper", Seeders: 5, Source: "stub"}},
		}},
		AggregatorConfig{})

	res, err := agg.Discover(context.Background(), "bahubali 2015", DiscoverOptions{})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if res.Movie == nil || res.Movie.TMDBID != 2 {
		t.Fatalf("resolve failed: %+v warnings=%v", res.Movie, res.Warnings)
	}

	if len(res.Ranked) == 0 {
		t.Fatal("no results")
	}
	top := res.Ranked[0]
	if top.InfoHash != knownHashAgg {
		t.Errorf("top result = %q", top.InfoHash)
	}
	if !strings.Contains(top.Source, "testidx") || !strings.Contains(top.Source, "stub") {
		t.Errorf("cross-source dedup expected on top hit, sources=%q", top.Source)
	}
	if top.Seeders != 77 {
		t.Errorf("seeders should survive merge: %d", top.Seeders)
	}
	if top.SynthesizeMagnet() == "" {
		t.Error("must expose a playable magnet")
	}
}

func TestDiscoverDegradesOnUnknownQuery(t *testing.T) {
	var gotRaw string
	scraper := &stubScraper{name: "probe", check: func(_ *ResolvedMovie, raw string) error {
		gotRaw = raw
		return nil
	}}

	agg := NewAggregator(testCatalogForAgg(t), nil, NewTorznabClient(nil), nil,
		[]Scraper{scraper}, AggregatorConfig{})

	res, err := agg.Discover(context.Background(), "zzqqxx unresolvable 480p", DiscoverOptions{})
	if err != nil {
		t.Fatalf("degraded discover should not hard-fail: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Error("warning about failed resolution expected")
	}
	if res.Movie == nil || !strings.Contains(res.Movie.Title, "zzqqxx unresolvable") {
		t.Errorf("fallback title wrong: %+v", res.Movie)
	}
	if !strings.Contains(gotRaw, "zzqqxx") {
		t.Errorf("scrapers should receive raw query, got %q", gotRaw)
	}
}

const knownHashAgg = "640b2f4e1a0c78e0b65cf36ac1d4f3aa5e9b8c7d"
