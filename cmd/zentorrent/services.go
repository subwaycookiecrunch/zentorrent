package main

// services.go wires the v4 discovery stack (metadata catalog, TMDB sync,
// DHT crawler, Torznab endpoints, aggregator) into the app lifecycle with
// graceful shutdown.

import (
	"context"
	"fmt"
	"golang.org/x/sync/errgroup"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/subwaycookiecrunch/zentorrent/internal/config"
	"github.com/subwaycookiecrunch/zentorrent/internal/debrid"
	"github.com/subwaycookiecrunch/zentorrent/internal/extractors"
	"github.com/subwaycookiecrunch/zentorrent/internal/metadata"
	"github.com/subwaycookiecrunch/zentorrent/internal/search"
	"github.com/subwaycookiecrunch/zentorrent/internal/web"
)

// tierResolver implements web.TierResolver across all configured tiers.
type tierResolver struct {
	providers  []debrid.Provider
	extractors []debrid.Provider
	cfg        config.Config
}

func (tr *tierResolver) ResolveTiers(ctx context.Context, item debrid.MediaItem,
	magnet string, bestSeeders int) []debrid.StreamSource {

	var out []debrid.StreamSource

	// Tier 1 — debrid caches, checked in parallel with a hard 6s budget.
	if len(tr.providers) > 0 {
		var mu sync.Mutex
		g, gctx := errgroup.WithContext(ctx)
		pctx, cancel := context.WithTimeout(gctx, 6*time.Second)
		defer cancel()
		for _, p := range tr.providers {
			p := p
			g.Go(func() error {
				it := item
				if it.Magnet == "" {
					it.Magnet = magnet
				}
				src, err := p.Resolve(pctx, it)
				if err != nil {
					return nil // not cached / unauthorized / slow: skip silently
				}
				mu.Lock()
				out = append(out, *src)
				mu.Unlock()
				return nil
			})
		}
		_ = g.Wait()
	}

	// Tier 2 — local P2P via the warm engine (always available).
	if magnet != "" || item.InfoHash != "" {
		ih := strings.ToLower(item.InfoHash)
		out = append(out, debrid.StreamSource{
			Type:         debrid.StreamTorrent,
			Magnet:       magnet,
			InfoHash:     ih,
			Title:        item.Title,
			ProviderName: "P2P",
		})
	}

	// Tier 3 — HLS embeds when seeds are thin and identity is known.
	if tr.cfg.EnableHLSFallback && (item.TMDBID != 0 || item.IMDbID != "") &&
		(bestSeeders < tr.cfg.HLSSeedFloor) {
		var mu sync.Mutex
		g, gctx := errgroup.WithContext(ctx)
		hctx, cancel := context.WithTimeout(gctx, 8*time.Second)
		defer cancel()
		for _, e := range tr.extractors {
			e := e
			g.Go(func() error {
				src, err := e.Resolve(hctx, item)
				if err != nil || src == nil {
					return nil
				}
				mu.Lock()
				out = append(out, *src)
				mu.Unlock()
				return nil
			})
		}
		_ = g.Wait()
	}
	return out
}

// Services holds the long-lived v4 handles shared across TUI views.
type Services struct {
	cfg       config.Config
	Catalog   *metadata.Catalog
	TMDB      *metadata.Client
	Discovery *search.Aggregator
	DHT       *search.DHTIndexer
	Web       *web.Server
	Tiers     web.TierResolver

	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
}

var services *Services

// Discovery exposes the process-wide aggregator; nil when startup failed.
func Discovery() *search.Aggregator {
	if services != nil {
		return services.Discovery
	}
	return nil
}

// CatalogHandle exposes the catalog (used by the smart-search suggester).
func CatalogHandle() *metadata.Catalog {
	if services != nil {
		return services.Catalog
	}
	return nil
}

// StartServices opens the metadata store and launches background workers.
// interactive=false (read-only CLI commands like `config`/`status`) skips
// the network-touching crawler and dump sync.
func StartServices(cfg config.Config, interactive bool) (*Services, error) {
	dir := config.Dir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("services: config dir: %w", err)
	}

	cat, err := metadata.OpenCatalog(filepath.Join(dir, "catalog.db"))
	if err != nil {
		return nil, err
	}

	baseCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	s := &Services{cfg: cfg, Catalog: cat, ctx: baseCtx, cancel: func() {
		stop()
	}}
	services = s
	OnShutdown(s.Shutdown) // idempotent; safe alongside deferred calls

	endpoints := make([]search.Endpoint, 0, len(cfg.TorznabEndpoints))
	for _, e := range cfg.TorznabEndpoints {
		endpoints = append(endpoints, search.Endpoint{
			Name: e.Name, BaseURL: e.URL, APIKey: e.APIKey,
		})
	}
	torznab := search.NewTorznabClient(endpoints)
	tmdb := metadata.NewClient(config.ResolveTMDBKey(cfg.TMDBApiKey), 8)
	s.TMDB = tmdb

	s.Discovery = search.NewAggregator(cat, tmdb, torznab, nil, nil, search.AggregatorConfig{})

	// ---- multi-tier resolution stack ----
	tr := &tierResolver{cfg: cfg}
	if key := strings.TrimSpace(cfg.RealDebridAPIKey); key != "" {
		tr.providers = append(tr.providers, debrid.NewRealDebrid(key))
	}
	if key := strings.TrimSpace(cfg.TorBoxAPIKey); key != "" {
		tr.providers = append(tr.providers, debrid.NewTorBox(key))
	}
	if cfg.EnableHLSFallback {
		tr.extractors = append(tr.extractors,
			extractors.NewVidSrc(), extractors.NewVidLink(), extractors.NewAutoEmbed())
	}
	s.Tiers = tr
	s.Web = &web.Server{
		Discovery: s.Discovery,
		Catalog:   cat,
		Tiers:     tr,
		Status:    func() any { return currentStreamSnapshot() },
		PlayHook:  desktopPlaySource,
	}

	if !interactive {
		return s, nil
	}

	// Daily ID-dump ingest + detail backfill, fully in the background.
	if cfg.AutoSyncDailyDumps {
		go s.dailyDumpLoop()
	}

	// BEP-51/09 crawler writing straight into its FTS index; wired into the
	// aggregator so [DHT] results come from live-crawled metadata too.
	if cfg.EnableDHTCrawler {
		idx, err := search.NewDHTIndexer(search.DHTConfig{
			DBPath: filepath.Join(dir, "dht_index_v4.db"),
		})
		if err == nil {
			s.DHT = idx
			s.Discovery = search.NewAggregator(cat, tmdb, torznab, idx, nil, search.AggregatorConfig{})
			s.Web.Discovery = s.Discovery
			go func() {
				defer func() { recover() }()
				idx.Run(s.ctx)
			}()
		} else {
			fmt.Printf("> dht crawler unavailable: %v\n", err)
		}
	} else if legacyErr := InitDHTIndex(); legacyErr == nil {
		// Legacy zendht source keeps working when the v4 crawler is off.
		go func() {
			defer func() { recover() }()
			StartDHTIndexer()
		}()
	}

	return s, nil
}

// dailyDumpLoop checks (once per day) whether the local catalog is missing
// the latest TMDB export and ingests it if so. Non-blocking by design.
func (s *Services) dailyDumpLoop() {
	defer func() { recover() }()
	marker := filepath.Join(config.Dir(), "last_dump_sync")
	today := time.Now().Format("2006-01-02")

	if data, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(data)) == today {
		return // already synced today
	}
	s.runDumpSync(true)

	// Enrich the most popular rows so aliases keep improving over time.
	bgCtx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
	defer cancel()
	if err := s.TMDB.BackfillDetails(bgCtx, s.Catalog, 400); err != nil && bgCtx.Err() == nil {
		fmt.Printf("[Metadata] detail backfill stopped early: %v\n", err)
	}
}

// runDumpSync ingests the newest available export (today first, then
// yesterday — TMDB publishes with a lag). verbose prints progress to the
// terminal; `zentorrent sync` calls this for a user-visible forced refresh.
func (s *Services) runDumpSync(verbose bool) {
	marker := filepath.Join(config.Dir(), "last_dump_sync")
	today := time.Now().Format("2006-01-02")

	say := func(format string, args ...any) {
		if verbose {
			fmt.Printf("[Metadata] "+format+"\n", args...)
		}
	}

	day := time.Now()
	for attempt := 0; attempt < 3; attempt++ { // today, yesterday, day before
		say("syncing TMDB catalog (%s)...", day.Format("2006-01-02"))
		n, err := s.TMDB.StreamDailyExport(s.ctx, s.Catalog, day)
		if err == nil {
			total, _ := s.Catalog.Count(s.ctx)
			say("ingested %d titles — catalog now has %d movies", n, total)
			_ = os.WriteFile(marker, []byte(today), 0644)
			return
		}
		if s.ctx.Err() != nil {
			return
		}
		say("%v", err)
		day = day.AddDate(0, 0, -1)
	}
	say("all export dates unavailable — will retry next launch")
}

// ForceCatalogSync runs a catalog refresh synchronously and reports counts.
func (s *Services) ForceCatalogSync() {
	s.runDumpSync(true)
}

// Shutdown cancels background work and closes every handle exactly once.
func (s *Services) Shutdown() {
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.DHT != nil {
			_ = s.DHT.Close() // stops Run and closes its sqlite handle
		}
		if s.Catalog != nil {
			_ = s.Catalog.Close()
		}
	})
}
