package search

import (
	"context"
	"strings"
	"testing"
)

func TestDiscoverStreamEventOrdering(t *testing.T) {
	torznabCl := torznabClientFixture(t,
		"Bahubali.The.Beginning.2015.1080p.BluRay.Dual.Audio.x264-TOMMY", knownHashAgg, 64)

	cat := testCatalogForAgg(t)
	agg := NewAggregator(cat, nil,
		torznabCl,
		nil,
		[]Scraper{&stubScraper{name: "stub", cands: []TorrentCandidate{
			{InfoHash: knownHashAgg, Title: "dup", Seeders: 3, Source: "stub"},
		}}},
		AggregatorConfig{})

	var (
		sawResolved   bool
		batches       int
		final         *DiscoveryResult
		resolvedFirst bool
	)
	for ev := range agg.DiscoverStream(context.Background(), "bahubali 2015", DiscoverOptions{}) {
		switch ev.Type {
		case EventResolved:
			if !sawResolved {
				resolvedFirst = true
			}
			sawResolved = true
			if ev.Movie == nil || !strings.Contains(ev.Movie.Title, "Baahubali") {
				t.Errorf("resolved movie wrong: %+v", ev.Movie)
			}
		case EventBatch:
			batches++
		case EventFinal:
			final = ev.Result
		}
	}

	if !sawResolved || !resolvedFirst {
		t.Fatal("resolved event missing or not first")
	}
	if batches < 2 {
		t.Errorf("expected >=2 batch events (torznab + scraper), got %d", batches)
	}
	if final == nil {
		t.Fatal("final event missing")
	}
	if final.Movie == nil || final.Movie.TMDBID != 2 {
		t.Errorf("final identity wrong: %+v", final.Movie)
	}
	if len(final.Ranked) == 0 {
		t.Fatal("no ranked results")
	}
	if !strings.Contains(final.Ranked[0].Source, "+") {
		t.Errorf("cross-source dedup expected on top row, sources=%q", final.Ranked[0].Source)
	}
}

func TestDiscoverStreamDegradeStillFinalizes(t *testing.T) {
	agg := NewAggregator(testCatalogForAgg(t), nil, NewTorznabClient(nil), nil, nil, AggregatorConfig{})
	var sawFinal bool
	var lastErr error
	for ev := range agg.DiscoverStream(context.Background(), "", DiscoverOptions{}) {
		if ev.Type == EventFinal {
			sawFinal = true
			lastErr = ev.Err
		}
	}
	if !sawFinal {
		t.Fatal("channel closed without a final event")
	}
	if lastErr == nil {
		t.Error("empty query should surface an error on the final event")
	}
}

func TestAudioTags(t *testing.T) {
	got := AudioTags("Bahubali.2015.1080p.WEB-DL.Dual.Audio.Hindi-DD5.1.x264-TOMMY")
	if len(got) == 0 || got[0] != "Dual-Audio" {
		t.Fatalf("dual audio tag missing: %v", got)
	}
	foundHindi := false
	for _, g := range got {
		if strings.EqualFold(g, "hindi") {
			foundHindi = true
		}
	}
	if !foundHindi {
		t.Errorf("hindi tag missing: %v", got)
	}
	if tags := AudioTags("Interstellar.2014.1080p.BluRay.x264-AMIABLE"); len(tags) != 0 {
		t.Errorf("clean release should have no audio tags, got %v", tags)
	}
}
