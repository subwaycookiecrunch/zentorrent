// Package metadata implements ZenTorrent's global movie identity layer: a
// TMDB-backed local catalog with trigram fuzzy search over millions of
// titles, aliases, and localized names.
package metadata

import (
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (FTS5 enabled)
)

// Movie is one canonical catalog row.
type Movie struct {
	ID               int64  // TMDB id
	IMDbID           string // "tt0000000" or ""
	Title            string
	OriginalTitle    string
	Aliases          string // raw stored form: comma/newline separated localized & romanized names
	Year             int
	Popularity       float64
	VoteAverage      float64
	PosterPath       string
	Genres           string // comma separated genre names
	OriginalLanguage string
	Overview         string
	DetailsFetched   bool
}

// AliasList splits the stored alias blob into individual aliases.
func (m Movie) AliasList() []string {
	return SplitAliases(m.Aliases)
}

// SplitAliases splits a stored alias blob on commas and newlines.
func SplitAliases(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// ScoredMovie pairs a catalog hit with its resolver confidence (0..1).
type ScoredMovie struct {
	Movie Movie
	Score float64
}

// Catalog is the local SQLite-backed movie index.
type Catalog struct {
	db *sql.DB
}

// catalogSchemaStmts are executed individually: trigger bodies contain
// semicolons and must not be naively split.
var catalogSchemaStmts = []string{
	`CREATE TABLE IF NOT EXISTS movies (
		id                INTEGER PRIMARY KEY,
		imdb_id           TEXT NOT NULL DEFAULT '',
		title             TEXT NOT NULL DEFAULT '',
		original_title    TEXT NOT NULL DEFAULT '',
		aliases           TEXT NOT NULL DEFAULT '',
		year              INTEGER NOT NULL DEFAULT 0,
		popularity        REAL NOT NULL DEFAULT 0,
		vote_average      REAL NOT NULL DEFAULT 0,
		poster_path       TEXT NOT NULL DEFAULT '',
		genres            TEXT NOT NULL DEFAULT '',
		original_language TEXT NOT NULL DEFAULT '',
		overview          TEXT NOT NULL DEFAULT '',
		searchable        TEXT NOT NULL DEFAULT '',
		details_fetched   INTEGER NOT NULL DEFAULT 0
	)`,
	`ALTER TABLE movies ADD COLUMN phonetic TEXT NOT NULL DEFAULT ''`,
	`CREATE INDEX IF NOT EXISTS idx_movies_imdb ON movies(imdb_id)`,
	`CREATE INDEX IF NOT EXISTS idx_movies_details_pending ON movies(details_fetched, popularity DESC)`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS movies_fts USING fts5(
		searchable, content='movies', content_rowid='id', tokenize='trigram'
	)`,
	`CREATE TRIGGER IF NOT EXISTS movies_ai AFTER INSERT ON movies BEGIN
		INSERT INTO movies_fts(rowid, searchable) VALUES (new.id, new.searchable);
	END`,
	`CREATE TRIGGER IF NOT EXISTS movies_ad AFTER DELETE ON movies BEGIN
		INSERT INTO movies_fts(movies_fts, rowid, searchable) VALUES('delete', old.id, old.searchable);
	END`,
	`CREATE TRIGGER IF NOT EXISTS movies_au AFTER UPDATE OF
		title, original_title, aliases, year, popularity, vote_average,
		poster_path, genres, original_language, overview, searchable, details_fetched, imdb_id
	ON movies BEGIN
		INSERT INTO movies_fts(movies_fts, rowid, searchable) VALUES('delete', old.id, old.searchable);
		INSERT INTO movies_fts(rowid, searchable) VALUES (new.id, new.searchable);
	END`,
}

// OpenCatalog opens (creating if necessary) the catalog database. path may be
// "" or ":memory:" for an in-memory instance. A single connection is used to
// avoid SQLITE_BUSY churn; throughput comes from batched transactions instead.
func OpenCatalog(path string) (*Catalog, error) {
	var dsn string
	switch path {
	case "", ":memory:":
		// Unique shared-cache name so multiple in-memory catalogs can coexist.
		dsn = fmt.Sprintf("file:zentorrent_catalog_%p?mode=memory&cache=shared", new(Catalog))
	default:
		dsn = "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("metadata: open catalog: %w", err)
	}
	db.SetMaxOpenConns(1)

	_, _ = db.Exec("PRAGMA synchronous = NORMAL;")
	_, _ = db.Exec("PRAGMA cache_size = -64000;")
	_, _ = db.Exec("PRAGMA temp_store = MEMORY;")

	for _, stmt := range catalogSchemaStmts {
		if _, err := db.Exec(stmt); err != nil {
			// The phonetic ALTER is idempotent-by-intent: duplicate column on
			// upgraded databases is fine, anything else is fatal.
			if strings.Contains(err.Error(), "duplicate column name") {
				continue
			}
			db.Close()
			return nil, fmt.Errorf("metadata: schema: %w", err)
		}
	}
	return &Catalog{db: db}, nil
}

// Close releases the underlying handle.
func (c *Catalog) Close() error { return c.db.Close() }

// Count returns the number of indexed movies.
func (c *Catalog) Count(ctx context.Context) (int64, error) {
	var n int64
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM movies`).Scan(&n)
	return n, err
}

func searchableText(m Movie) string {
	parts := make([]string, 0, 3)
	parts = append(parts, m.Title)
	if m.OriginalTitle != "" && !strings.EqualFold(m.OriginalTitle, m.Title) {
		parts = append(parts, m.OriginalTitle)
	}
	if m.Aliases != "" {
		parts = append(parts, m.Aliases)
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}

// UpsertMovieBatch inserts or updates movies in a single transaction. Rows
// are matched on TMDB id.
func (c *Catalog) UpsertMovieBatch(ctx context.Context, movies []Movie) error {
	if len(movies) == 0 {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("metadata: upsert begin: %w", err)
	}
	defer tx.Rollback()

	const q = `INSERT INTO movies
		(id, imdb_id, title, original_title, aliases, year, popularity,
		 vote_average, poster_path, genres, original_language, overview,
		 searchable, details_fetched, phonetic)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			imdb_id=CASE WHEN excluded.imdb_id != '' THEN excluded.imdb_id ELSE movies.imdb_id END,
			title=CASE WHEN excluded.title != '' THEN excluded.title ELSE movies.title END,
			original_title=CASE WHEN excluded.original_title != '' THEN excluded.original_title ELSE movies.original_title END,
			aliases=CASE WHEN excluded.aliases != '' THEN excluded.aliases ELSE movies.aliases END,
			year=CASE WHEN excluded.year != 0 THEN excluded.year ELSE movies.year END,
			popularity=excluded.popularity,
			vote_average=excluded.vote_average,
			poster_path=CASE WHEN excluded.poster_path != '' THEN excluded.poster_path ELSE movies.poster_path END,
			genres=CASE WHEN excluded.genres != '' THEN excluded.genres ELSE movies.genres END,
			original_language=CASE WHEN excluded.original_language != '' THEN excluded.original_language ELSE movies.original_language END,
			overview=CASE WHEN excluded.overview != '' THEN excluded.overview ELSE movies.overview END,
			searchable=lower(coalesce(
				CASE WHEN excluded.title != '' THEN excluded.title ELSE movies.title END,'') || char(10) ||
				coalesce(CASE WHEN excluded.original_title != '' THEN excluded.original_title ELSE movies.original_title END,'') || char(10) ||
				coalesce(CASE WHEN excluded.aliases != '' THEN excluded.aliases ELSE movies.aliases END,'')),
			phonetic=CASE WHEN excluded.phonetic != '' THEN excluded.phonetic ELSE movies.phonetic END,
			details_fetched=MAX(movies.details_fetched, excluded.details_fetched)`

	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return fmt.Errorf("metadata: upsert prepare: %w", err)
	}
	defer stmt.Close()

	for i := range movies {
		m := movies[i]
		if _, err := stmt.ExecContext(ctx,
			m.ID, m.IMDbID, m.Title, m.OriginalTitle, m.Aliases, m.Year,
			m.Popularity, m.VoteAverage, m.PosterPath, m.Genres,
			m.OriginalLanguage, m.Overview, searchableText(m), m.DetailsFetched,
			phoneticText(m),
		); err != nil {
			return fmt.Errorf("metadata: upsert id=%d: %w", m.ID, err)
		}
	}

	return tx.Commit()
}

// ---- Ingest -----------------------------------------------------------------

type dailyExportEntry struct {
	Adult         bool    `json:"adult"`
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Popularity    float64 `json:"popularity"`
	VoteAverage   float64 `json:"vote_average"`
}

// IngestDailyIDDump streams a TMDB daily ID dump (the decompressed body of
// movie_ids_MM_DD_YYYY.json.gz) into the catalog. Both published formats are
// accepted: newline-delimited JSON objects (the real export layout) and a
// plain JSON array. Memory stays flat regardless of dump size because
// records are decoded one at a time and committed in fixed-size
// transactions. Adult titles are skipped.
func (c *Catalog) IngestDailyIDDump(ctx context.Context, r io.Reader) (int64, error) {
	br := bufio.NewReader(io.LimitReader(r, 8<<30))

	// Detect the container by the first meaningful byte.
	for {
		b, err := br.Peek(1)
		if err != nil {
			return 0, fmt.Errorf("metadata: export: empty stream: %w", err)
		}
		if b[0] == ' ' || b[0] == '\t' || b[0] == '\r' || b[0] == '\n' {
			br.ReadByte()
			continue
		}
		break
	}
	first, _ := br.Peek(1)

	const batchSize = 2000
	batch := make([]Movie, 0, batchSize)
	var total int64

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := c.UpsertMovieBatch(ctx, batch); err != nil {
			return err
		}
		total += int64(len(batch))
		batch = batch[:0]
		return nil
	}

	accept := func(e dailyExportEntry) bool {
		// Many movie_ids dump rows carry ONLY original_title — fall back to
		// it so the catalog doesn't silently drop most of the export.
		title := strings.TrimSpace(e.Title)
		if title == "" {
			title = strings.TrimSpace(e.OriginalTitle)
		}
		if e.Adult || e.ID == 0 || title == "" {
			return false
		}
		batch = append(batch, Movie{
			ID:            e.ID,
			Title:         title,
			OriginalTitle: strings.TrimSpace(e.OriginalTitle),
			Popularity:    e.Popularity,
			VoteAverage:   e.VoteAverage,
		})
		return len(batch) >= batchSize
	}

	dec := json.NewDecoder(br)
	if len(first) > 0 && first[0] == '[' {
		// Array form.
		tok, err := dec.Token()
		if err != nil {
			return 0, fmt.Errorf("metadata: export: expect '[': %w", err)
		}
		if d, ok := tok.(json.Delim); !ok || d != '[' {
			return 0, errors.New("metadata: export: expected JSON array")
		}
		for dec.More() {
			if ctx.Err() != nil {
				return total, ctx.Err()
			}
			var e dailyExportEntry
			if err := dec.Decode(&e); err != nil {
				// A failed Decode leaves the stream unpositioned — retrying
				// would spin forever. Committed batches survive; stop clean.
				break
			}
			if accept(e) {
				if err := flush(); err != nil {
					return total, err
				}
			}
		}
	} else {
		// Newline-delimited objects (actual TMDB export format).
		for {
			if ctx.Err() != nil {
				return total, ctx.Err()
			}
			var e dailyExportEntry
			if err := dec.Decode(&e); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				// Syntax errors unposition the decoder; keep what we have.
				break
			}
			if accept(e) {
				if err := flush(); err != nil {
					return total, err
				}
			}
		}
	}

	if err := flush(); err != nil {
		return total, err
	}
	return total, nil
}

// IngestGzipIDDump convenience wrapper for .json.gz bodies.
func (c *Catalog) IngestGzipIDDump(ctx context.Context, r io.Reader) (int64, error) {
	zr, err := gzip.NewReader(io.LimitReader(r, 2<<30))
	if err != nil {
		return 0, fmt.Errorf("metadata: gzip: %w", err)
	}
	defer zr.Close()
	return c.IngestDailyIDDump(ctx, zr)
}

// ---- Details backfill ---------------------------------------------------------

// PendingDetailIDs returns TMDB ids whose rich details have not been fetched,
// most popular first.
func (c *Catalog) PendingDetailIDs(ctx context.Context, limit int) ([]int64, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := c.db.QueryContext(ctx,
		`SELECT id FROM movies WHERE details_fetched = 0 ORDER BY popularity DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("metadata: pending ids: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

// UpdateDetails persists rich details (and marks the row fetched).
func (c *Catalog) UpdateDetails(ctx context.Context, m Movie) error {
	m.DetailsFetched = true
	return c.UpsertMovieBatch(ctx, []Movie{m})
}

// ---- Lookup -------------------------------------------------------------------

func scanMovie(scan func(dest ...any) error) (Movie, error) {
	var (
		m      Movie
		imdb   sql.NullString
		detail int
	)
	err := scan(&m.ID, &imdb, &m.Title, &m.OriginalTitle, &m.Aliases, &m.Year,
		&m.Popularity, &m.VoteAverage, &m.PosterPath, &m.Genres,
		&m.OriginalLanguage, &m.Overview, &detail)
	if err != nil {
		return Movie{}, err
	}
	m.IMDbID = imdb.String
	m.DetailsFetched = detail != 0
	return m, nil
}

const movieCols = `id, imdb_id, title, original_title, aliases, year, popularity,
	vote_average, poster_path, genres, original_language, overview, details_fetched`

// ByTMDBID fetches one movie by TMDB id.
func (c *Catalog) ByTMDBID(ctx context.Context, id int64) (*Movie, error) {
	row := c.db.QueryRowContext(ctx, `SELECT `+movieCols+` FROM movies WHERE id = ?`, id)
	m, err := scanMovie(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("metadata: by id: %w", err)
	}
	return &m, nil
}

// ByIMDbID fetches one movie by IMDb identifier ("tt0000000").
func (c *Catalog) ByIMDbID(ctx context.Context, imdb string) (*Movie, error) {
	row := c.db.QueryRowContext(ctx, `SELECT `+movieCols+` FROM movies WHERE imdb_id = ?`, imdb)
	m, err := scanMovie(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("metadata: by imdb: %w", err)
	}
	return &m, nil
}

// Search performs trigram fuzzy search over titles, original titles, and
// aliases. Queries shorter than three runes (trigram minimum) fall back to a
// substring LIKE scan so abbreviations like "k3g" still resolve.
func (c *Catalog) Search(ctx context.Context, query string, limit int) ([]Movie, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 25
	}

	norm := NormalizeQuery(query)
	if norm == "" {
		return nil, nil
	}

	// 1. Whole-query phrase match (best precision with trigram).
	rows, err := c.ftsQuery(ctx, ftsPhrase(norm), "", limit)
	if err == nil && len(rows) > 0 {
		return rows, nil
	}

	// 2. Individual terms ANDed together.
	terms := strings.Fields(norm)
	if len(terms) > 1 {
		rows, err = c.ftsQuery(ctx, ftsAND(terms), "", limit)
		if err == nil && len(rows) > 0 {
			return rows, nil
		}
	}

	// 3. Longest term alone.
	if best := longestTerm(terms); best != "" && best != norm {
		rows, err = c.ftsQuery(ctx, ftsPhrase(best), "", limit)
		if err == nil && len(rows) > 0 {
			return rows, nil
		}
	}

	// 4. Fragment probe: trigram MATCH is substring-exact, so typos like
	// "interstelar" slip through it. Overlapping 4-gram LIKE fragments give
	// real fuzzy recall; BestMatch re-scores whatever surfaces here.
	if rows := c.fragmentProbe(ctx, norm); len(rows) > 0 {
		return rows, nil
	}

	// 5. Phonetic stage: Double Metaphone catches misspellings sharing no
	// n-grams with the real title ("drishiam" vs "Drishyam" extremes).
	if prows, err := c.SearchPhonetic(ctx, norm, limit); err == nil && len(prows) > 0 {
		return prows, nil
	}

	// 6. Sub-3-rune abbreviation: plain LIKE over the searchable column.
	if runeLen(norm) < 3 {
		return c.likeSearch(ctx, norm, 25)
	}
	return nil, nil
}

// fragmentProbe ORs together a handful of 4-character slices of the query.
// Any catalog row sharing even one fragment becomes a scoring candidate.
func (c *Catalog) fragmentProbe(ctx context.Context, norm string) []Movie {
	const fragLen = 4
	rs := []rune(norm)
	if runeLen(norm) < fragLen {
		return nil
	}
	frags := make([]string, 0, 6)
	step := (len(rs) - fragLen) / 5 // spread across the string
	if step < 1 {
		step = 1
	}
	for i := 0; i+fragLen <= len(rs) && len(frags) < 6; i += step {
		frag := string(rs[i : i+fragLen])
		if !slices.Contains(frags, frag) {
			frags = append(frags, frag)
		}
	}
	if len(frags) == 0 {
		return nil
	}

	clauses := make([]string, len(frags))
	args := make([]any, len(frags))
	for i, f := range frags {
		clauses[i] = "searchable LIKE ? ESCAPE '\\'"
		args[i] = likeWrap(f)
	}
	args = append(args, 300)

	q := `SELECT ` + movieCols + ` FROM movies WHERE (` +
		strings.Join(clauses, " OR ") + `) ORDER BY popularity DESC LIMIT ?`
	rows, err := c.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Movie
	for rows.Next() {
		m, err := scanMovie(rows.Scan)
		if err == nil {
			out = append(out, m)
		}
	}
	return out
}

func (c *Catalog) ftsQuery(ctx context.Context, match, extraWhere string, limit int) ([]Movie, error) {
	q := `SELECT ` + movieCols + ` FROM movies_fts f JOIN movies m ON m.id = f.rowid
		WHERE movies_fts MATCH ? ` + extraWhere + `
		ORDER BY rank LIMIT ?`
	rows, err := c.db.QueryContext(ctx, q, match, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Movie
	for rows.Next() {
		m, err := scanMovie(rows.Scan)
		if err == nil {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

// phoneticText derives the phonetic lookup key(s) for a row from every
// spelling it carries (title, original title, aliases).
func phoneticText(m Movie) string {
	parts := []string{m.Title, m.OriginalTitle}
	parts = append(parts, m.AliasList()...)
	var b strings.Builder
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		k := PhoneticKey(p)
		if k != "" && !seen[k] {
			seen[k] = true
			b.WriteString(k)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// BackfillPhonetics computes phonetic keys for rows ingested before the
// column existed. Returns rows updated; runs until exhausted or maxBatches.
func (c *Catalog) BackfillPhonetics(ctx context.Context, maxBatches int) (int64, error) {
	var total int64
	for i := 0; i < maxBatches; i++ {
		rows, err := c.db.QueryContext(ctx,
			`SELECT id, title, original_title, aliases FROM movies
			 WHERE phonetic = '' LIMIT 5000`)
		if err != nil {
			return total, err
		}
		type kv struct {
			id int64
			k  string
		}
		var batch []kv
		for rows.Next() {
			var id int64
			var title, orig, aliases string
			if rows.Scan(&id, &title, &orig, &aliases) == nil {
				batch = append(batch, kv{id, phoneticText(Movie{
					Title: title, OriginalTitle: orig, Aliases: aliases,
				})})
			}
		}
		rows.Close()
		if len(batch) == 0 {
			return total, nil
		}

		tx, err := c.db.BeginTx(ctx, nil)
		if err != nil {
			return total, err
		}
		for _, e := range batch {
			if e.k == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE movies SET phonetic = ? WHERE id = ? AND phonetic = ''`,
				e.k, e.id); err == nil {
				total++
			}
		}
		if err := tx.Commit(); err != nil {
			return total, err
		}
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
	return total, nil
}

// SearchPhonetic matches movies whose stored phonetic key starts with the
// query's phonetic key — last resort for heavy misspellings that share no
// character n-grams with the real title.
func (c *Catalog) SearchPhonetic(ctx context.Context, query string, limit int) ([]Movie, error) {
	qk := PhoneticKey(query)
	if qk == "" || limit <= 0 {
		return nil, nil
	}
	rows, err := c.db.QueryContext(ctx,
		`SELECT `+movieCols+` FROM movies WHERE phonetic LIKE ? || '%'
		 ORDER BY popularity DESC LIMIT ?`, qk, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Movie
	for rows.Next() {
		m, err := scanMovie(rows.Scan)
		if err == nil {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

// likeWrap builds an escaped contains-pattern from a raw term.
func likeWrap(term string) string {
	return "%" + likeEscape(term) + "%"
}

func (c *Catalog) likeSearch(ctx context.Context, term string, limit int) ([]Movie, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT `+movieCols+` FROM movies WHERE searchable LIKE ? ESCAPE '\'
		 ORDER BY popularity DESC LIMIT ?`, likeWrap(term), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Movie
	for rows.Next() {
		m, err := scanMovie(rows.Scan)
		if err == nil {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func ftsPhrase(q string) string { return `"` + strings.ReplaceAll(q, `"`, `""`) + `"` }

func ftsAND(terms []string) string {
	parts := make([]string, 0, len(terms))
	for _, t := range terms {
		if runeLen(t) >= 3 {
			parts = append(parts, ftsPhrase(t))
		}
	}
	return strings.Join(parts, " AND ")
}

func longestTerm(terms []string) string {
	best := ""
	for _, t := range terms {
		if runeLen(t) > runeLen(best) {
			best = t
		}
	}
	return best
}

func runeLen(s string) int { return len([]rune(s)) }

// ---- Resolution scoring -------------------------------------------------------

// NormalizeQuery lowercases, strips diacritics ("é" -> "e"), and collapses
// everything else to spaces so catalog text and user queries land in one
// canonical space before comparison.
func NormalizeQuery(s string) string {
	decomposed := norm.NFD.String(strings.ToLower(s))
	var b strings.Builder
	for _, r := range decomposed {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue // combining accent mark
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			if b.Len() > 0 && !strings.HasSuffix(b.String(), " ") {
				b.WriteByte(' ')
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// BigramSimilarity is the Dice coefficient over character bigrams; robust to
// typos and small transpositions ("interstelar" ~ "interstellar"). When one
// side normalizes to nothing (non-Latin scripts), raw lowercase comparison
// is used instead so Telugu/Korean titles still score against themselves.
func BigramSimilarity(a, b string) float64 {
	na, nb := NormalizeQuery(a), NormalizeQuery(b)
	if na == "" || nb == "" {
		a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
		if a == b {
			return 1
		}
		return 0
	}
	a, b = na, nb
	if a == b {
		return 1
	}
	if runeLen(a) < 2 || runeLen(b) < 2 {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	grams := make(map[string]int, len(ra))
	for i := 0; i+1 < len(ra); i++ {
		grams[string(ra[i:i+2])]++
	}
	hits := 0
	for i := 0; i+1 < len(rb); i++ {
		g := string(rb[i : i+2])
		if grams[g] > 0 {
			grams[g]--
			hits++
		}
	}
	total := (len(ra) - 1) + (len(rb) - 1)
	if total == 0 {
		return 0
	}
	return 2 * float64(hits) / float64(total)
}

// BestMatch resolves free text to the strongest catalog identity. It scores
// candidates on textual similarity plus light priors (exact hits, popularity).
// Returns nil when nothing clears minScore.
func (c *Catalog) BestMatch(ctx context.Context, query string, minScore float64) (*ScoredMovie, error) {
	cands, err := c.Search(ctx, query, 40)
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		// Try once more with quality/noise words stripped.
		if cleaned := StripNoiseWords(query); cleaned != query {
			cands, err = c.Search(ctx, cleaned, 40)
			if err != nil {
				return nil, err
			}
		}
	}

	qNorm := NormalizeQuery(query)
	var best *ScoredMovie
	for _, m := range cands {
		score := scoreAgainstQuery(qNorm, query, m)
		if best == nil || score > best.Score {
			best = &ScoredMovie{Movie: m, Score: score}
		}
	}
	if best == nil || best.Score < minScore {
		return nil, nil
	}
	return best, nil
}

func scoreAgainstQuery(qNorm, rawQuery string, m Movie) float64 {
	targets := []string{m.Title, m.OriginalTitle}
	targets = append(targets, m.AliasList()...)

	best := 0.0
	for _, t := range targets {
		if s := BigramSimilarity(qNorm, t); s > best {
			best = s
		}
	}
	// Exact / containment bonuses on the raw strings.
	qLower := strings.ToLower(strings.TrimSpace(rawQuery))
	for _, t := range targets {
		tl := strings.ToLower(t)
		if tl == qLower {
			best += 0.30
			break
		}
		if strings.Contains(tl, qLower) && runeLen(qLower) >= 3 {
			best += 0.10
			break
		}
	}
	// Light popularity prior (log-ish, capped).
	if m.Popularity > 0 {
		popBonus := m.Popularity / (m.Popularity + 100)
		best += 0.05 * popBonus
	}
	if best > 1 {
		best = 1
	}
	return best
}

// StripNoiseWords removes release-quality tokens and stray years from a
// user query before resolution ("interstelar 1080p" -> "interstelar").
var noiseWords = map[string]bool{}

func init() {
	for _, w := range []string{
		"480p", "576p", "720p", "1080p", "2160p", "1080i", "720i",
		"4k", "uhd", "hd", "cam", "ts", "tc", "dvdrip", "bdrip", "brrip",
		"webrip", "webdl", "web-dl", "web", "hdtv", "bluray", "blu-ray",
		"x264", "x265", "h264", "h265", "hevc", "aac", "aac2", "ac3",
		"dts", "truehd", "atmos", "10bit", "8bit", "hdr", "dv", "dolby",
		"dual", "audio", "esubs", "subs", "subbed", "hq", "print",
		"movie", "full", "download",
	} {
		noiseWords[w] = true
	}
}

// ---- Type-ahead suggestions ---------------------------------------------------

// Suggestion is one autocomplete entry.
type Suggestion struct {
	TMDBID       int64   `json:"id"`
	IMDbID       string  `json:"imdb_id,omitempty"`
	Title        string  `json:"title"`
	Original     string  `json:"original,omitempty"`
	Year         int     `json:"year"`
	MediaType    string  `json:"media_type"` // "movie", "tv", "anime"
	VoteAverage  float64 `json:"vote_average"`
	Popularity   float64 `json:"popularity"`
	Genres       string  `json:"genres,omitempty"`
	Language     string  `json:"lang,omitempty"`
	Overview     string  `json:"overview,omitempty"`
	PosterPath   string  `json:"poster_path,omitempty"`
	BackdropPath string  `json:"backdrop_path,omitempty"`
}

// Suggest returns instant type-ahead completions for a partial query,
// matching titles, original titles, and aliases. Safe to call every few
// keystrokes: it is a single indexed LIKE over the catalog.
func (c *Catalog) Suggest(ctx context.Context, prefix string, limit int) ([]Suggestion, error) {
	cleanPrefix := NormalizeQuery(prefix)
	cleanPrefix = strings.ReplaceAll(cleanPrefix, " ", "")
	if cleanPrefix == "" || limit <= 0 {
		return nil, nil
	}
	// Three relevance tiers: field-start ("drish"->"Drishyam"), then
	// word-start inside a title ("bah"->"Bahnhof"), then plain contains.
	// Fields are newline-separated inside searchable.
	startPattern := "%\n" + likeEscape(cleanPrefix) + "%"
	wordPattern := "% " + likeEscape(cleanPrefix) + "%"
	rows, err := c.db.QueryContext(ctx,
		`SELECT id, imdb_id, title, original_title, year, vote_average, popularity, genres, original_language, overview, poster_path FROM movies
		 WHERE searchable LIKE ? ESCAPE '\'
		 ORDER BY (searchable LIKE ? ESCAPE '\') DESC,
		          (searchable LIKE ? ESCAPE '\') DESC,
		          popularity DESC
		 LIMIT ?`,
		"%"+likeEscape(cleanPrefix)+"%", startPattern, wordPattern, limit)
	if err != nil {
		return nil, fmt.Errorf("metadata: suggest: %w", err)
	}
	defer rows.Close()

	out := make([]Suggestion, 0, limit)
	for rows.Next() {
		var (
			s      Suggestion
			imdb   sql.NullString
			poster sql.NullString
		)
		if err := rows.Scan(&s.TMDBID, &imdb, &s.Title, &s.Original, &s.Year, &s.VoteAverage, &s.Popularity, &s.Genres, &s.Language, &s.Overview, &poster); err == nil {
			s.IMDbID = imdb.String
			s.PosterPath = poster.String
			s.MediaType = "movie"
			out = append(out, s)
		}
	}
	return out, rows.Err()
}

// StripNoiseWords drops quality/release tokens and standalone years.
func StripNoiseWords(q string) string {
	fields := strings.Fields(q)
	out := make([]string, 0, len(fields))
	changed := false
	for _, f := range fields {
		lf := strings.ToLower(strings.Trim(f, ",.-_"))
		if noiseWords[lf] {
			changed = true
			continue
		}
		if y, err := strconv.Atoi(lf); err == nil && (y >= 1900 && y <= 2100) && len(fields) > 1 {
			changed = true
			continue
		}
		out = append(out, f)
	}
	if !changed || len(out) == 0 {
		return strings.Join(fields, " ")
	}
	return strings.Join(out, " ")
}
