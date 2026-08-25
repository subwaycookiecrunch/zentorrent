package metadata

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// Adversarial verification of the reported sparse-upsert data loss.
const advDump = `[
 {"adult":false,"id":100,"title":"Foo","original_title":"Foo","popularity":50,"vote_average":6},
 {"adult":false,"id":200,"title":"Bar","original_title":"Barr","popularity":40,"vote_average":5}
]`

func advIngest(t *testing.T, c *Catalog) {
	t.Helper()
	if n, err := c.IngestDailyIDDump(context.Background(), strings.NewReader(advDump)); err != nil {
		t.Fatalf("ingest: %v", err)
	} else if n != 2 {
		t.Fatalf("ingest n=%d", n)
	}
}

func TestAdvSparseUpsertWipesIMDbID(t *testing.T) {
	c := testCatalog(t)
	ctx := context.Background()

	advIngest(t, c) // day 1: sparse export rows

	// Details backfill stores the IMDb identity (as FetchMovieDetails would).
	md := &MovieDetails{}
	md.Movie = Movie{ID: 100, IMDbID: "tt1234567", Title: "Foo", OriginalTitle: "Foo",
		Year: 2020, Popularity: 55, VoteAverage: 6.2, PosterPath: "/p.jpg",
		Genres: "Drama", OriginalLanguage: "en", Overview: "plot", Aliases: "The Foo"}
	if err := c.UpdateDetails(ctx, md.Movie); err != nil {
		t.Fatalf("UpdateDetails: %v", err)
	}

	if m, err := c.ByIMDbID(ctx, "tt1234567"); err != nil || m == nil {
		t.Fatalf("pre-refresh ByIMDbID: m=%v err=%v", m, err)
	}

	advIngest(t, c) // day 2: daily export refresh upserts sparse rows again

	got, err := c.ByTMDBID(ctx, 100)
	if err != nil || got == nil {
		t.Fatalf("post-refresh ByTMDBID: %v %v", got, err)
	}
	t.Logf("post-refresh row: imdb=%q title=%q year=%d aliases=%q genres=%q lang=%q poster=%q overview=%q",
		got.IMDbID, got.Title, got.Year, got.Aliases, got.Genres, got.OriginalLanguage, got.PosterPath, got.Overview)

	// REGRESSION: a sparse daily-export refresh must NOT destroy the
	// backfilled IMDb identity.
	if m, _ := c.ByIMDbID(ctx, "tt1234567"); m == nil {
		t.Errorf("sparse refresh wiped imdb_id — ByIMDbID now misses")
	}
	if got.IMDbID != "tt1234567" {
		t.Errorf("imdb_id should survive sparse refresh, got %q", got.IMDbID)
	}
	if !got.DetailsFetched {
		t.Errorf("details_fetched flag lost across refresh (MAX guard broken)")
	}
	if got.Year != 2020 || got.Aliases != "The Foo" || got.Genres != "Drama" {
		t.Errorf("guarded columns unexpectedly lost: year=%d aliases=%q genres=%q", got.Year, got.Aliases, got.Genres)
	}
}

func TestAdvRetirementBlanksTitle(t *testing.T) {
	c := testCatalog(t)
	ctx := context.Background()

	advIngest(t, c)
	n, err := c.Count(ctx)
	if err != nil || n != 2 {
		t.Fatalf("count pre: %d %v", n, err)
	}

	// Permanent-failure retirement path from tmdb.go:198.
	if err := c.UpdateDetails(ctx, Movie{ID: 200}); err != nil {
		t.Fatalf("retire UpdateDetails: %v", err)
	}

	got, err := c.ByTMDBID(ctx, 200)
	if err != nil || got == nil {
		t.Fatalf("post-retire ByTMDBID: %v %v", got, err)
	}
	t.Logf("post-retire row: imdb=%q title=%q original=%q popularity=%v fetched=%v",
		got.IMDbID, got.Title, got.OriginalTitle, got.Popularity, got.DetailsFetched)
	// REGRESSION: 404-retirement marks fetched but must not blank the row.
	if !got.DetailsFetched {
		t.Errorf("retirement should mark details_fetched so the queue drains")
	}
	if got.Title == "" || got.OriginalTitle == "" {
		t.Errorf("retirement wiped titles: %q / %q", got.Title, got.OriginalTitle)
	}
	n, err = c.Count(ctx)
	if err != nil || n != 2 {
		t.Errorf("count post: %d %v (husk still counted)", n, err)
	}

	best, err := c.BestMatch(ctx, "bar", 0.0)
	t.Logf("BestMatch(bar) after retire: %+v err=%v", best, err)
	if best != nil && best.Score > 0 {
		t.Logf("husk still searchable-ish")
	}
}

var _ = bytes.MinRead
