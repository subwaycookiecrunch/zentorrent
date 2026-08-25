package metadata

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	c, err := OpenCatalog(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func seedMovies(t *testing.T, c *Catalog) {
	t.Helper()
	movies := []Movie{
		{ID: 1, Title: "Interstellar", OriginalTitle: "Interstellar",
			IMDbID: "tt0816692", Year: 2014, Popularity: 90,
			OriginalLanguage: "en", Genres: "Science Fiction,Adventure"},
		{ID: 2, Title: "Baahubali: The Beginning", OriginalTitle: "బాహుబలి",
			Aliases: "Bahubali, Baahubali Part 1", IMDbID: "tt4849438",
			Year: 2015, Popularity: 60, OriginalLanguage: "te",
			Genres: "Action,Drama"},
		{ID: 3, Title: "Drishyam", Aliases: "Drishyam 2015, दृश्यम",
			IMDbID: "tt4430212", Year: 2015, Popularity: 40,
			OriginalLanguage: "hi"},
		{ID: 4, Title: "Kabhi Khushi Kabhie Gham",
			Aliases: "K3G, Sometimes Happiness Sometimes Sorrow",
			IMDbID:  "tt0286657", Year: 2001, Popularity: 30,
			OriginalLanguage: "hi"},
	}
	if err := c.UpsertMovieBatch(context.Background(), movies); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestUpsertAndSearchByAlias(t *testing.T) {
	c := testCatalog(t)
	seedMovies(t, c)
	ctx := context.Background()

	// Romanized alias resolves the Telugu original.
	rows, err := c.Search(ctx, "bahubali", 10)
	if err != nil || len(rows) == 0 {
		t.Fatalf("bahubali search: %v rows=%d", err, len(rows))
	}
	if rows[0].ID != 2 {
		t.Errorf("want Baahubali id=2, got %d (%s)", rows[0].ID, rows[0].Title)
	}

	// Abbreviation alias (3 runes, trigram minimum).
	rows, err = c.Search(ctx, "k3g", 10)
	if err != nil || len(rows) == 0 {
		t.Fatalf("k3g search: %v rows=%d", err, len(rows))
	}
	if rows[0].ID != 4 {
		t.Errorf("want K3G id=4, got %d", rows[0].ID)
	}
}

func TestFuzzyResolution(t *testing.T) {
	c := testCatalog(t)
	seedMovies(t, c)

	best, err := c.BestMatch(context.Background(), "interstelar", 0.45)
	if err != nil || best == nil {
		t.Fatalf("interstelar: %v best=%v", err, best)
	}
	if best.Movie.Title != "Interstellar" || best.Score < 0.45 {
		t.Errorf("resolved %q score %.2f", best.Movie.Title, best.Score)
	}

	best, err = c.BestMatch(context.Background(), "drishiam", 0.45)
	if err != nil || best == nil {
		t.Fatalf("drishiam: %v best=%v", err, best)
	}
	if best.Movie.Title != "Drishyam" {
		t.Errorf("resolved %q, want Drishyam", best.Movie.Title)
	}
}

func TestNormalizeQueryDiacritics(t *testing.T) {
	if got := NormalizeQuery("Amélie, Poulain!"); got != "amelie poulain" {
		t.Errorf("normalize = %q", got)
	}
	if got := NormalizeQuery("  Léon:  The   Professional "); got != "leon the professional" {
		t.Errorf("normalize = %q", got)
	}
}

func TestStripNoiseWords(t *testing.T) {
	if got := StripNoiseWords("interstelar 1080p x264 bluray"); got != "interstelar" {
		t.Errorf("strip = %q", got)
	}
	if got := StripNoiseWords("drishiam 2"); got != "drishiam 2" {
		t.Errorf("numeric sequel must survive: %q", got)
	}
}

func TestIngestDailyIDDump(t *testing.T) {
	c := testCatalog(t)

	good := func(s string) []byte { b, _ := json.Marshal(s); return b }
	var buf bytes.Buffer
	buf.WriteByte('[')
	writeEntry := func(raw string, first bool) {
		if !first {
			buf.WriteByte(',')
		}
		buf.WriteString(raw)
	}
	writeEntry(`{"id":100,"title":"Inglourious Basterds","original_title":"Inglourious Basterds","popularity":55.5,"vote_average":8.2,"adult":false}`, true)
	writeEntry(`{"id":101,"title":"Adult Thing","original_title":"x","adult":true}`, false)
	writeEntry(`{"id":0,"title":"no id","adult":false}`, false)
	writeEntry(`{"id":102,"title":"","adult":false}`, false)
	_ = good
	writeEntry(`{"id":103,"titl`, false) // malformed/truncated: skipped silently

	n, err := c.IngestDailyIDDump(context.Background(), &buf)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if n != 1 {
		t.Fatalf("ingested %d rows, want 1 (adult/id-less/empty/malformed skipped)", n)
	}

	m, err := c.ByTMDBID(context.Background(), 100)
	if err != nil || m == nil || m.Title != "Inglourious Basterds" {
		t.Fatalf("by id: %v %+v", err, m)
	}
	if m.Popularity != 55.5 || m.VoteAverage != 8.2 {
		t.Errorf("floats not persisted: %+v", m)
	}

	// Gzip wrapper path.
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(`[{"id":200,"title":"Gzipped Movie","adult":false}]`))
	zw.Close()
	n, err = c.IngestGzipIDDump(context.Background(), &gz)
	if err != nil || n != 1 {
		t.Fatalf("gzip ingest: n=%d err=%v", n, err)
	}
}

func TestDetailsBackfillState(t *testing.T) {
	c := testCatalog(t)
	seedMovies(t, c)
	ctx := context.Background()

	pending, err := c.PendingDetailIDs(ctx, 10)
	if err != nil || len(pending) != 4 {
		t.Fatalf("pending: %v %d", err, len(pending))
	}
	// Popularity ordering: Interstellar first.
	if pending[0] != 1 {
		t.Errorf("most popular first, got %d", pending[0])
	}

	// Simulate a details fetch for id=1.
	m, _ := c.ByTMDBID(ctx, 1)
	m.IMDbID = "tt0816692"
	m.Year = 2014
	m.Aliases = "Interstelar misspelling bait"
	if err := c.UpdateDetails(ctx, *m); err != nil {
		t.Fatalf("update: %v", err)
	}

	pending, _ = c.PendingDetailIDs(ctx, 10)
	if len(pending) != 3 || slicesContains(pending, 1) {
		t.Errorf("id 1 should have left the pending queue: %v", pending)
	}

	got, _ := c.ByIMDbID(ctx, "tt0816692")
	if got == nil || got.ID != 1 {
		t.Errorf("imdb lookup failed: %+v", got)
	}
}

func slicesContains(xs []int64, v int64) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func TestUpsertPreservesAliasesOnReingest(t *testing.T) {
	c := testCatalog(t)
	ctx := context.Background()
	_ = c.UpsertMovieBatch(ctx, []Movie{{ID: 9, Title: "Old Title", Popularity: 5}})
	_ = c.UpdateDetails(ctx, Movie{ID: 9, Title: "Old Title", Aliases: "Kept Alias, रखा हुआ", Year: 1999})

	// A later export pass (no aliases) must not wipe enriched fields.
	if err := c.UpsertMovieBatch(ctx, []Movie{{ID: 9, Title: "Old Title", Popularity: 7}}); err != nil {
		t.Fatal(err)
	}
	m, _ := c.ByTMDBID(ctx, 9)
	if len(m.AliasList()) != 2 || m.Year != 1999 {
		t.Errorf("enrichment lost: %+v", m)
	}
	if m.Popularity != 7 {
		t.Errorf("popularity should update: %v", m.Popularity)
	}
}

func TestBigramSimilarity(t *testing.T) {
	cases := []struct {
		a, b string
		min  float64
	}{
		{"interstelar", "interstellar", 0.85},
		{"drishiam", "drishyam", 0.7},
		{"k3g", "k3g", 1.0},
		{"banana", "interstellar", 0.0},
	}
	for _, c := range cases {
		got := BigramSimilarity(c.a, c.b)
		if got < c.min {
			t.Errorf("sim(%q,%q) = %.2f, want >= %.2f", c.a, c.b, got, c.min)
		}
	}
	if BigramSimilarity("Amélie", "amelie") != 1 {
		t.Error("diacritics must normalize to identical strings")
	}
}

func TestSuggest(t *testing.T) {
	c := testCatalog(t)
	seedMovies(t, c)
	ctx := context.Background()

	got, err := c.Suggest(ctx, "drish", 5)
	if err != nil || len(got) == 0 {
		t.Fatalf("drish: %v %d", err, len(got))
	}
	if got[0].Title != "Drishyam" {
		t.Errorf("top suggestion = %q", got[0].Title)
	}

	got, err = c.Suggest(ctx, "bah", 5)
	if err != nil || len(got) == 0 {
		t.Fatalf("bah (alias prefix): %v %d", err, len(got))
	}
	if got[0].TMDBID != 2 {
		t.Errorf("alias prefix should hit Baahubali, got %q", got[0].Title)
	}

	if got, _ := c.Suggest(ctx, "", 5); len(got) != 0 {
		t.Errorf("empty prefix must return nothing")
	}
}

func TestIngestLDJSONExport(t *testing.T) {
	c := testCatalog(t)
	// Real TMDB daily dumps are newline-delimited objects, NOT an array.
	// Mirrors real dumps: most rows lack a plain "title" entirely.
	body := `{"adult":false,"id":11,"original_title":"Interstellar","popularity":90.1,"video":false,"vote_average":8.4}
{"adult":false,"id":12,"title":"Baahubali","original_title":"బాహుబలి","popularity":60.0,"video":false,"vote_average":8.0}
{"adult":true,"id":13,"original_title":"skip me"}
`

	n, err := c.IngestDailyIDDump(context.Background(), strings.NewReader(body))
	if err != nil {
		t.Fatalf("ldjson ingest: %v", err)
	}
	if n != 2 {
		t.Fatalf("ingested %d, want 2", n)
	}
	m, _ := c.ByTMDBID(context.Background(), 12)
	if m == nil || !strings.Contains(m.Title, "Baahubali") {
		t.Errorf("row 12 missing: %+v", m)
	}
}

func TestPhoneticMatching(t *testing.T) {
	if !PhoneticsMatch("drishiam", "Drishyam") {
		t.Error("drishiam should phonetically match Drishyam")
	}
	if !PhoneticsMatch("bahubali", "Baahubali") {
		t.Error("vowel-length variants should match")
	}
	if !PhoneticsMatch("interstelar", "Interstellar") {
		t.Error("transposition should match")
	}
	if PhoneticsMatch("banana", "Interstellar") {
		t.Error("unrelated titles must not match")
	}
}

func TestFuzzyDistance(t *testing.T) {
	if d := DamerauLevenshtein("interstelar", "interstellar"); d != 1 {
		t.Errorf("transposition distance = %d, want 1", d)
	}
	if s := FuzzyScore("identical", "identical"); s != 1 {
		t.Errorf("identical score = %.2f", s)
	}
}

func TestSearchPhoneticStage(t *testing.T) {
	c := testCatalog(t)
	seedMovies(t, c)

	// A misspelling with NO shared 4-grams fragments still resolves through
	// the phonetic stage.
	rows, err := c.Search(context.Background(), "baahubali", 10)
	if err != nil || len(rows) == 0 {
		t.Fatalf("baahubali: %v %d", err, len(rows))
	}
	if rows[0].ID != 2 {
		t.Errorf("expected Baahubali id=2, got %d (%s)", rows[0].ID, rows[0].Title)
	}
}
