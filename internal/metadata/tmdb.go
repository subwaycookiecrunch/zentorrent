package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

const (
	tmdbBaseURL    = "https://api.themoviedb.org/3"
	tmdbExportBase = "https://files.tmdb.org/p/exports"
	defaultRPS     = 8 // stay far under TMDB's ceiling; bursts absorbed by limiter
	detailsWorkers = 6
)

// Client is a rate-limited TMDB API v3 client plus daily-export downloader.
type Client struct {
	apiKey   string
	baseURL  string
	hc       *http.Client // bounded: JSON API calls
	exportHC *http.Client // unbounded: multi-hundred-MB streaming downloads
	limiter  *rate.Limiter
}

func NewClient(apiKey string, rps float64) *Client {
	if rps <= 0 {
		rps = defaultRPS
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: tmdbBaseURL,
		hc: &http.Client{
			Timeout: 20 * time.Second,
		},
		// No blanket timeout here: the daily dump is tens of MB streamed
		// through gzip+JSON decoding straight into SQLite; the caller's
		// context governs the deadline instead.
		exportHC: &http.Client{},
		limiter:  rate.NewLimiter(rate.Limit(rps), int(rps)+2),
	}
}

// MovieDetails is the rich payload for one movie.
type MovieDetails struct {
	Movie             Movie
	AlternativeTitles []string
	Runtime           int
}

// FetchMovieDetails pulls /movie/{id} with alternative_titles appended and
// folds everything into a catalog-ready Movie.
func (c *Client) FetchMovieDetails(ctx context.Context, id int64) (*MovieDetails, error) {
	var raw struct {
		Title            string `json:"title"`
		OriginalTitle    string `json:"original_title"`
		ReleaseDate      string `json:"release_date"`
		IMDbID           string `json:"imdb_id"`
		Popularity       float64
		VoteAverage      float64 `json:"vote_average"`
		PosterPath       string  `json:"poster_path"`
		OriginalLanguage string  `json:"original_language"`
		Overview         string  `json:"overview"`
		Runtime          int     `json:"runtime"`
		Genres           []struct {
			Name string `json:"name"`
		} `json:"genres"`
		AlternativeTitles struct {
			Titles []struct {
				Country string `json:"iso_3166_1"`
				Title   string `json:"title"`
			} `json:"titles"`
		} `json:"alternative_titles"`
	}

	if err := c.apiGet(ctx,
		fmt.Sprintf("/movie/%d?append_to_response=alternative_titles", id),
		&raw); err != nil {
		return nil, err
	}

	md := &MovieDetails{Runtime: raw.Runtime}
	m := Movie{
		ID:               id,
		IMDbID:           strings.TrimSpace(raw.IMDbID),
		Title:            strings.TrimSpace(raw.Title),
		OriginalTitle:    strings.TrimSpace(raw.OriginalTitle),
		Popularity:       raw.Popularity,
		VoteAverage:      raw.VoteAverage,
		PosterPath:       raw.PosterPath,
		OriginalLanguage: raw.OriginalLanguage,
		Overview:         strings.TrimSpace(raw.Overview),
	}
	if t, err := time.Parse("2006-01-02", raw.ReleaseDate); err == nil {
		m.Year = t.Year()
	}
	genres := make([]string, 0, len(raw.Genres))
	for _, g := range raw.Genres {
		if g.Name != "" {
			genres = append(genres, g.Name)
		}
	}
	m.Genres = strings.Join(genres, ",")

	// Aliases: localized alternative titles (deduped, capped) — this is what
	// makes romanized searches ("Bahubali") and international titles resolve.
	seen := map[string]bool{}
	var aliases []string
	addAlias := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || len(s) > 120 {
			return
		}
		k := strings.ToLower(s)
		if seen[k] || strings.EqualFold(s, m.Title) || strings.EqualFold(s, m.OriginalTitle) {
			return
		}
		seen[k] = true
		aliases = append(aliases, s)
	}
	for _, t := range raw.AlternativeTitles.Titles {
		addAlias(t.Title)
		if len(aliases) >= 24 {
			break
		}
	}
	m.Aliases = strings.Join(aliases, ", ")

	md.Movie = m
	md.AlternativeTitles = aliases
	return md, nil
}

// DailyIDDumpURL builds the export URL for a date (US MM_DD_YYYY format).
func DailyIDDumpURL(day time.Time) string {
	return fmt.Sprintf("%s/movie_ids_%s.json.gz",
		tmdbExportBase, day.Format("01_02_2006"))
}

// StreamDailyExport downloads and decompresses the daily ID dump for day and
// streams it into the catalog. Memory stays flat across millions of rows.
// Uses the unbounded export client: a blanket http timeout here would kill
// the connection mid-download (body read counts toward it).
func (c *Client) StreamDailyExport(ctx context.Context, cat *Catalog, day time.Time) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, DailyIDDumpURL(day), nil)
	if err != nil {
		return 0, fmt.Errorf("metadata: export request: %w", err)
	}
	resp, err := c.exportHC.Do(req)
	if err != nil {
		return 0, fmt.Errorf("metadata: export download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		// Exports publish for yesterday; today's file 403s until then.
		return 0, fmt.Errorf("metadata: export not available yet (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("metadata: export HTTP %d", resp.StatusCode)
	}
	n, err := cat.IngestGzipIDDump(ctx, resp.Body)
	if err != nil {
		return n, fmt.Errorf("metadata: ingest after %d rows: %w", n, err)
	}
	return n, nil
}

// BackfillDetails enriches the most popular rows lacking details. It runs
// until ctx is cancelled, the pending queue is empty, or limit rows have been
// processed (limit<=0 = unbounded).
func (c *Client) BackfillDetails(ctx context.Context, cat *Catalog, limit int) error {
	done := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if limit > 0 && done >= limit {
			return nil
		}

		const page = detailsWorkers * 10
		ids, err := cat.PendingDetailIDs(ctx, page)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}

		g, gctx := errgroup.WithContext(ctx)
		sem := make(chan struct{}, detailsWorkers)
		for _, id := range ids {
			id := id
			g.Go(func() error {
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-gctx.Done():
					return gctx.Err()
				}

				md, err := c.FetchMovieDetails(gctx, id)
				if err != nil {
					if isPermanentTMDBError(err) {
						// Retire the row (UpdateDetails marks it fetched);
						// guarded upserts keep any existing fields intact.
						if uerr := cat.UpdateDetails(gctx, Movie{ID: id}); uerr != nil {
							log.Printf("tmdb backfill: retire id=%d: %v", id, uerr)
						}
						return nil
					}
					return nil // transient: leave in queue for next pass
				}
				if uerr := cat.UpdateDetails(gctx, md.Movie); uerr != nil {
					// One row's persistence failure must not kill the pass;
					// it stays pending and is retried on the next page.
					log.Printf("tmdb backfill: persist id=%d: %v", id, uerr)
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		done += len(ids)
	}
}

func isPermanentTMDBError(err error) bool {
	return errors.Is(err, ErrTMDBNotFound)
}

// ErrTMDBNotFound marks permanent lookup failures so backfill can retire
// the row instead of retrying forever.
var ErrTMDBNotFound = errors.New("tmdb resource not found")

func (c *Client) apiGet(ctx context.Context, pathWithQuery string, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("metadata: tmdb rate limit: %w", err)
	}
	sep := "?"
	if strings.Contains(pathWithQuery, "?") {
		sep = "&"
	}
	u := c.baseURL + pathWithQuery + sep + "api_key=" + c.apiKey

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ZenTorrent/4.0")

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("metadata: tmdb get %s: %w", pathWithQuery, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("metadata: tmdb read: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: /movie/%s", ErrTMDBNotFound,
			strings.SplitN(strings.TrimPrefix(strings.SplitN(pathWithQuery, "?", 2)[0], "/movie/"), "/", 2)[0])
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("metadata: tmdb HTTP %d: %.120s", resp.StatusCode, string(body))
	}
	return json.Unmarshal(body, out)
}
