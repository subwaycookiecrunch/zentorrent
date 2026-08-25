package search

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/subwaycookiecrunch/zentorrent/internal/metadata"
)

// ErrNoMatch is returned when a query cannot be resolved to a catalog movie.
// Callers may degrade to treating the raw text as a title instead.
var ErrNoMatch = errors.New("no confident metadata match")

// Scraper is the plug-in point for fallback HTML/API sources beyond Torznab
// and DHT.
type Scraper interface {
	Name() string
	Search(ctx context.Context, movie *ResolvedMovie, rawQuery string) ([]TorrentCandidate, error)
}

// AggregatorConfig tunes discovery behaviour.
type AggregatorConfig struct {
	// MinResolveScore is the catalog confidence floor (0..1).
	MinResolveScore float64
	// ResultLimit caps ranked output.
	ResultLimit int
	// PerEndpointTimeout bounds each Torznab endpoint.
	PerEndpointTimeout time.Duration
}

func (c AggregatorConfig) withDefaults() AggregatorConfig {
	if c.MinResolveScore <= 0 {
		c.MinResolveScore = 0.45
	}
	if c.ResultLimit <= 0 {
		c.ResultLimit = 40
	}
	if c.PerEndpointTimeout <= 0 {
		c.PerEndpointTimeout = 15 * time.Second
	}
	return c
}

// DiscoverOptions carry user intent extracted from the query box.
type DiscoverOptions struct {
	Resolution string   // "", "720p", "1080p", "2160p"...
	Languages  []string // e.g. ["hindi"] requested explicitly
	MinSeeders int      // hard floor applied after ranking
	Limit      int
	Year       int // optional release-year filter hint passed to indexers
}

// DiscoveryResult is the full outcome of one user query.
type DiscoveryResult struct {
	Movie    *ResolvedMovie    // canonical identity (may be nil on weak match)
	Ranked   []RankedCandidate // sorted best-first
	Warnings []string          // per-source failures worth surfacing
}

// RankedCandidate is a scored torrent plus human-readable reasons.
type RankedCandidate struct {
	TorrentCandidate
	Score   float64
	Reasons []string
}

// Aggregator bridges the metadata catalog with every torrent source.
type Aggregator struct {
	cat      *metadata.Catalog
	tmdb     *metadata.Client
	torznab  *TorznabClient
	dht      *DHTIndexer
	scrapers []Scraper
	cfg      AggregatorConfig
}

func NewAggregator(
	cat *metadata.Catalog,
	tmdb *metadata.Client,
	torznab *TorznabClient,
	dht *DHTIndexer,
	scrapers []Scraper,
	cfg AggregatorConfig,
) *Aggregator {
	return &Aggregator{
		cat:      cat,
		tmdb:     tmdb,
		torznab:  torznab,
		dht:      dht,
		scrapers: scrapers,
		cfg:      cfg.withDefaults(),
	}
}

// Suggest exposes the catalog's type-ahead completions (web + TUI share it).
func (a *Aggregator) Suggest(ctx context.Context, prefix string, limit int) ([]metadata.Suggestion, error) {
	return a.cat.Suggest(ctx, prefix, limit)
}

// ResolveQuery maps free text ("drishiam 2", "interstelar 1080p") to the
// canonical movie identity using the local trigram catalog. Direct IMDb IDs
// ("tt3778644") short-circuit to an exact lookup.
func (a *Aggregator) ResolveQuery(ctx context.Context, raw string) (*ResolvedMovie, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrNoMatch
	}

	if id := normalizeIMDbID(extractIMDbID(raw)); id != "" {
		if m, err := a.cat.ByIMDbID(ctx, id); err == nil && m != nil {
			return movieToResolved(*m, 1.0), nil
		}
		// Unknown IMDb ID still gives indexers something authoritative.
		return &ResolvedMovie{IMDbID: id, Title: metadata.StripNoiseWords(raw), Score: 0.5}, nil
	}

	best, err := a.cat.BestMatch(ctx, raw, a.cfg.MinResolveScore)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", raw, err)
	}
	if best == nil {
		return nil, fmt.Errorf("%w: %q", ErrNoMatch, raw)
	}
	return movieToResolved(best.Movie, best.Score), nil
}

func extractIMDbID(s string) string {
	m := regexp.MustCompile(`(?i)\btt\d{7,8}\b`).FindString(s)
	return strings.ToLower(m)
}

func movieToResolved(m metadata.Movie, score float64) *ResolvedMovie {
	r := &ResolvedMovie{
		TMDBID:        m.ID,
		IMDbID:        m.IMDbID,
		Title:         m.Title,
		OriginalTitle: m.OriginalTitle,
		Year:          m.Year,
		Aliases:       m.AliasList(),
		PosterPath:    m.PosterPath,
		Genres:        splitCSV(m.Genres),
		Language:      m.OriginalLanguage,
		Score:         score,
	}
	if r.Title == "" {
		r.Title = m.OriginalTitle
	}
	return r
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Discover runs the full pipeline: resolve -> parallel multi-source search ->
// dedupe -> filter -> rank.
func (a *Aggregator) Discover(ctx context.Context, raw string, opts DiscoverOptions) (*DiscoveryResult, error) {
	return a.discover(ctx, raw, opts, nil, nil)
}

// StreamEventType labels DiscoverStream events.
type StreamEventType int

const (
	// EventResolved fires once, as soon as the catalog identity is known —
	// before any network source answers.
	EventResolved StreamEventType = iota
	// EventBatch carries raw candidates from one source as it completes.
	EventBatch
	// EventFinal is last: the deduped, filtered, ranked result (Err set on
	// hard failure).
	EventFinal
)

// StreamEvent is one incremental discovery update.
type StreamEvent struct {
	Type   StreamEventType
	Movie  *ResolvedMovie
	Batch  []TorrentCandidate
	Result *DiscoveryResult
	Err    error
}

// DiscoverStream is the streaming form of Discover: the resolved identity
// arrives first, candidate batches stream in per-source as they complete,
// and a single EventFinal closes the channel with the ranked result.
func (a *Aggregator) DiscoverStream(ctx context.Context, raw string, opts DiscoverOptions) <-chan StreamEvent {
	ch := make(chan StreamEvent, 64)
	go func() {
		defer close(ch)
		send := func(e StreamEvent) {
			select {
			case ch <- e:
			case <-ctx.Done():
			}
		}

		var resolved *ResolvedMovie
		res, err := a.discover(ctx, raw, opts, func(m *ResolvedMovie) {
			resolved = m
			send(StreamEvent{Type: EventResolved, Movie: m})
		}, func(cs []TorrentCandidate) {
			send(StreamEvent{Type: EventBatch, Batch: cs})
		})
		if err != nil && res == nil {
			send(StreamEvent{Type: EventFinal, Err: err})
			return
		}
		// Degrade path resolves without callback when the catalog missed;
		// still surface the fallback identity so UIs can show it early.
		if resolved == nil && res != nil {
			send(StreamEvent{Type: EventResolved, Movie: res.Movie})
		}
		send(StreamEvent{Type: EventFinal, Result: res})
	}()
	return ch
}

func (a *Aggregator) discover(
	ctx context.Context,
	raw string,
	opts DiscoverOptions,
	onResolved func(*ResolvedMovie),
	onBatch func([]TorrentCandidate),
) (*DiscoveryResult, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty query")
	}
	opts.Limit = coalesce(opts.Limit, a.cfg.ResultLimit)

	res, err := a.ResolveQuery(ctx, raw)
	result := &DiscoveryResult{}
	if err != nil {
		// Degrade gracefully: search indexers with the raw text as-is.
		res = &ResolvedMovie{Title: metadata.StripNoiseWords(raw)}
		result.Warnings = append(result.Warnings, err.Error())
	}
	result.Movie = res
	if onResolved != nil {
		onResolved(res)
	}

	if opts.Resolution == "" {
		opts.Resolution = ParseResolution(raw)
	}
	if opts.Year == 0 {
		opts.Year = res.Year
	}

	var (
		mu    sync.Mutex
		cands []TorrentCandidate
		warn  = func(msg string) {
			mu.Lock()
			result.Warnings = append(result.Warnings, msg)
			mu.Unlock()
		}
		addAll = func(cs []TorrentCandidate) {
			mu.Lock()
			cands = append(cands, cs...)
			cb := onBatch
			mu.Unlock()
			if cb != nil && len(cs) > 0 {
				cb(cs)
			}
		}
	)

	g, gctx := errgroup.WithContext(ctx)

	// --- Torznab fan-out -----------------------------------------------
	terms := res.SearchTerms()
	if len(terms) > 3 {
		// Official title, original title, and the strongest alias (the one
		// users actually typed — "Bahubali" for Baahubali).
		terms = terms[:3]
	}

	if a.torznab != nil {
		g.Go(func() error {
			var plans []QueryParams
			if res.IMDbID != "" {
				plans = append(plans, QueryParams{
					IMDbID:     res.IMDbID,
					Categories: discoverCategories(opts),
				})
			}
			for _, t := range terms {
				plans = append(plans, QueryParams{
					Query:      t,
					Year:       opts.Year,
					Categories: discoverCategories(opts),
				})
			}
			if len(plans) == 0 {
				return nil
			}

			inner, innerCtx := errgroup.WithContext(gctx)
			for _, p := range plans {
				p := p
				inner.Go(func() error {
					results, err := a.torznab.Search(innerCtx, p, a.cfg.PerEndpointTimeout)
					for _, er := range results {
						if er.Err != nil {
							warn(fmt.Sprintf("torznab/%s: %v", er.Endpoint, er.Err))
							continue
						}
						addAll(er.Results)
					}
					if err != nil && ctx.Err() == nil {
						warn("torznab: " + err.Error())
					}
					return nil
				})
			}
			return inner.Wait() // critical: block until all plans land
		})
	}

	// --- Local DHT index --------------------------------------------------
	if a.dht != nil {
		g.Go(func() error {
			for _, t := range append([]string{res.Title}, terms...) {
				if t == "" {
					continue
				}
				cs, err := a.dht.Search(gctx, t, 30)
				if err != nil {
					warn("dht: " + err.Error())
					continue
				}
				addAll(cs)
			}
			return nil
		})
	}

	// --- Fallback scrapers -------------------------------------------------
	for i := range a.scrapers {
		s := a.scrapers[i]
		g.Go(func() error {
			cctx, cancel := context.WithTimeout(gctx, a.cfg.PerEndpointTimeout)
			defer cancel()
			cs, err := s.Search(cctx, res, raw)
			if err != nil {
				warn(s.Name() + ": " + err.Error())
				return nil
			}
			addAll(cs)
			return nil
		})
	}

	_ = g.Wait() // workers never fail; they warn

	deduped := DedupeCandidates(cands)
	kept := FilterJunk(deduped)
	if len(deduped) != len(kept) {
		log.Printf("aggregator: filtered %d junk candidates for %q", len(deduped)-len(kept), raw)
	}
	ranked := RankCandidates(kept, res, opts)

	if opts.MinSeeders > 0 {
		filtered := ranked[:0]
		for _, r := range ranked {
			if r.Seeders >= opts.MinSeeders {
				filtered = append(filtered, r)
			}
		}
		ranked = filtered
	}
	if len(ranked) > opts.Limit {
		ranked = ranked[:opts.Limit]
	}
	result.Ranked = ranked
	return result, nil
}

func discoverCategories(opts DiscoverOptions) []int {
	return []int{CatMoviesHD, CatMoviesUHD, CatMoviesForeign, CatAnime}
}

func coalesce(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

// ---- dedup ------------------------------------------------------------------

// DedupeCandidates merges candidates sharing an identity (infohash first,
// title+size fingerprint otherwise). Merged entries keep the richest fields:
// max seeders, combined source labels, and any location info present.
func DedupeCandidates(in []TorrentCandidate) []TorrentCandidate {
	index := make(map[string]int, len(in))
	order := make([]string, 0, len(in))
	merged := make([]TorrentCandidate, 0, len(in))

	for _, c := range in {
		key := c.Key()
		if i, ok := index[key]; ok {
			merged[i] = mergeTwo(merged[i], c)
			continue
		}
		index[key] = len(merged)
		order = append(order, key)
		merged = append(merged, copyCandidate(c))
	}
	return merged
}

func mergeTwo(a, b TorrentCandidate) TorrentCandidate {
	out := a
	if b.Seeders > out.Seeders {
		out.Seeders = b.Seeders
	}
	if b.Leechers > out.Leechers {
		out.Leechers = b.Leechers
	}
	if b.Grabs > out.Grabs {
		out.Grabs = b.Grabs
	}
	if b.SizeBytes > out.SizeBytes {
		out.SizeBytes = b.SizeBytes
	}
	if out.InfoHash == "" {
		out.InfoHash = b.InfoHash
	}
	if out.Magnet == "" {
		out.Magnet = b.Magnet
	}
	if out.DownloadURL == "" {
		out.DownloadURL = b.DownloadURL
	}
	if out.Title == "" {
		out.Title = b.Title
	}
	if out.PublishedAt.IsZero() || (!b.PublishedAt.IsZero() && b.PublishedAt.After(out.PublishedAt)) {
		out.PublishedAt = b.PublishedAt
	}
	if out.IMDbID == "" {
		out.IMDbID = b.IMDbID
	}
	if b.Passworded {
		out.Passworded = true // pessimistic merge
	}
	if !slices.Contains(out.Categories, first(b.Categories)) && len(b.Categories) > 0 {
		out.Categories = append(out.Categories, b.Categories...)
	}
	if !strings.Contains(out.Source, b.Source) {
		out.Source = out.Source + "+" + b.Source
	}
	return out
}

func first(s []int) int {
	if len(s) == 0 {
		return 0
	}
	return s[0]
}

func copyCandidate(c TorrentCandidate) TorrentCandidate {
	c.Categories = append([]int(nil), c.Categories...)
	return c
}

// ---- junk filtering -----------------------------------------------------------

var junkPatterns = regexp.MustCompile(`(?i)` + strings.Join([]string{
	`password`, `passwort`, `contrase[ñn]a`, `senha`,
	`\bexe\b`, `setup(\.exe)?\b`, `install(\.exe)?\b`,
	`join\s+t\.me`, `@\w+\.com`, `\bfake\b`, `virus`, `ransom`,
}, "|"))

// FilterJunk drops password-protected and obvious scam uploads. Low-quality
// sources (CAM/TS) survive here but take a heavy ranking penalty — hiding
// them entirely would break rare-title searches.
func FilterJunk(in []TorrentCandidate) []TorrentCandidate {
	out := in[:0]
	for _, c := range in {
		if c.Passworded {
			continue
		}
		if junkPatterns.MatchString(c.Title) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// ---- ranking ---------------------------------------------------------------

var resolutionRe = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|576p|480p)\b`)
var camRe = regexp.MustCompile(`(?i)\b(cam|hdts|hdcam|ts|telesync|dvdscr|screener)\b`)
var dualAudioRe = regexp.MustCompile(`(?i)dual[._\s-]?audio`)

// verifiedGroups is a conservative whitelist of release groups with decent
// encode reputations. Presence earns a ranking bonus only.
var verifiedGroups = map[string]bool{
	"yify": true, "ytsmx": true, "yts": true, "rarbg": true, "evo": true,
	"fgt": true, "amiable": true, "geckos": true, "ctrlhd": true,
	"ntb": true, "don": true, "framestor": true, "bhdstudio": true,
	"smurf": true, "tommy": true, "hone": true, "flax": true,
	"playweb": true, "nikt0n": true, "chdbits": true, "wik": true,
}

var langTagMap = map[string][]string{
	"hi": {"hindi", "bollywood", "esubs"},
	"ta": {"tamil", "kollywood"},
	"te": {"telugu", "tollywood"},
	"ml": {"malayalam"},
	"kn": {"kannada"},
	"bn": {"bengali", "bangla"},
	"pa": {"punjabi", "panjabi"},
	"mr": {"marathi"},
	"ja": {"japanese", "jap subs"},
	"ko": {"korean"},
	"zh": {"chinese", "mandarin", "cantonese"},
	"es": {"spanish", "latino"},
	"fr": {"french"},
	"de": {"german"},
	"it": {"italian"},
	"ru": {"russian"},
	"pt": {"portuguese", "brazilian"},
}

var resTier = map[string]int{"480p": 1, "576p": 2, "720p": 3, "1080p": 4, "2160p": 5}

// ParseResolution extracts the highest resolution token from a title/query.
func ParseResolution(s string) string {
	found := ""
	for _, m := range resolutionRe.FindAllString(strings.ToLower(s), -1) {
		if resTier[m] > resTier[found] {
			found = m
		}
	}
	return found
}

var audioTagOrder = []string{
	"dual audio", "hindi", "tamil", "telugu", "malayalam", "kannada",
	"bengali", "bangla", "punjabi", "panjabi", "marathi", "japanese",
	"korean", "chinese", "mandarin", "cantonese", "spanish", "french",
	"german", "italian", "russian", "portuguese", "multi",
}

// AudioTags extracts human-readable audio/language tags from a release
// title, in display order, capped at three.
func AudioTags(title string) []string {
	t := strings.ToLower(title)
	var out []string
	for _, tag := range audioTagOrder {
		if tag == "dual audio" {
			if dualAudioRe.MatchString(t) {
				out = append(out, "Dual-Audio")
			}
			continue
		}
		if strings.Contains(t, tag) {
			out = append(out, strings.ToUpper(tag[:1])+tag[1:])
		}
		if len(out) >= 3 {
			break
		}
	}
	return out
}

// RankCandidates scores and sorts candidates best-first.
// buildLangWordRe compiles a word-boundary matcher for a short language
// code so "hi" cannot fire inside unrelated words.
func buildLangWordRe(code string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^a-z])` + regexp.QuoteMeta(code) + `(?:$|[^a-z])`)
}

func RankCandidates(cands []TorrentCandidate, movie *ResolvedMovie, opts DiscoverOptions) []RankedCandidate {
	desiredRes := strings.ToLower(strings.TrimSpace(opts.Resolution))
	if _, known := resTier[desiredRes]; desiredRes != "" && !known {
		desiredRes = "" // unrecognized tier: fall back to best-available bonus
	}
	wantLangs := make([]string, 0, 2)
	if movie != nil && movie.Language != "" {
		wantLangs = append(wantLangs, movie.Language)
		wantLangs = append(wantLangs, langTagMap[movie.Language]...)
	}
	wantLangs = append(wantLangs, lowerAll(opts.Languages)...)

	out := make([]RankedCandidate, 0, len(cands))
	for _, c := range cands {
		rc := RankedCandidate{TorrentCandidate: c}
		titleLower := strings.ToLower(c.Title)

		// Seeders dominate: log2 scale so 10 vs 100 matters more than 10k vs 11k.
		score := log2(float64(c.Seeders)+1) * 12
		if score > 60 {
			score = 60
		}
		switch {
		case c.Seeders >= 50:
			rc.Reasons = append(rc.Reasons, fmt.Sprintf("healthy swarm (%d seeders)", c.Seeders))
		case c.Seeders == 0:
			score -= 25
			rc.Reasons = append(rc.Reasons, "no seeders right now")
		}

		// Resolution match against intent.
		gotRes := ParseResolution(c.Title)
		switch {
		case desiredRes == "" && gotRes != "":
			score += float64([]int{4, 5, 10, 15, 18}[resTier[gotRes]-1])
			rc.Reasons = append(rc.Reasons, gotRes)
		case desiredRes != "" && gotRes == desiredRes:
			score += 25
			rc.Reasons = append(rc.Reasons, "matches requested "+desiredRes)
		case desiredRes != "" && gotRes != "":
			delta := resTier[gotRes] - resTier[desiredRes]
			if delta < 0 {
				delta = -delta
			}
			score -= float64(8 + 6*delta)
			rc.Reasons = append(rc.Reasons, fmt.Sprintf("resolution %s ≠ %s", gotRes, desiredRes))
		}

		// Verified release group.
		for g := range verifiedGroups {
			if strings.Contains(titleLower, "-"+g) ||
				strings.Contains(titleLower, "["+g+"]") ||
				strings.HasSuffix(titleLower, g) {
				score += 10
				rc.Reasons = append(rc.Reasons, "verified group "+strings.ToUpper(g))
				break
			}
		}

		// Audio/language affinity. Short ISO codes need word boundaries so
		// "hi" doesn't fire on "tHIs Is Sparta".
		for _, l := range wantLangs {
			if l == "" {
				continue
			}
			matched := false
			if len(l) <= 2 {
				matched = buildLangWordRe(l).MatchString(titleLower)
			} else {
				matched = strings.Contains(titleLower, l)
			}
			if matched {
				score += 14
				rc.Reasons = append(rc.Reasons, "language match "+l)
				break
			}
		}
		if dualAudioRe.MatchString(titleLower) {
			score += 8
			rc.Reasons = append(rc.Reasons, "dual audio")
		}

		// Quality-class penalties.
		if camRe.MatchString(titleLower) {
			score -= 35
			rc.Reasons = append(rc.Reasons, "cam/ts class source")
		}

		// Freshness nudge.
		if age := time.Since(c.PublishedAt); !c.PublishedAt.IsZero() && age < 45*24*time.Hour {
			score += 3
		}

		rc.Score = score
		out = append(out, rc)
	}

	slices.SortStableFunc(out, func(a, b RankedCandidate) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		}
		return strings.Compare(a.Key(), b.Key())
	})
	return out
}

func log2(f float64) float64 {
	return math.Log2(f)
}

func lowerAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToLower(strings.TrimSpace(s))
	}
	return out
}
