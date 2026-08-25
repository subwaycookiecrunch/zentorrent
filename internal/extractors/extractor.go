// Package extractors implements Tier-3 sources: web HLS embeds keyed by
// TMDB/IMDb ID. These serve as instant fallbacks when torrent seeds are
// thin. Embed endpoints are deterministic; live .m3u8 extraction is
// best-effort — providers rotate their player chains constantly, so a
// resolver that cannot crack the page degrades to serving the embed URL.
package extractors

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/subwaycookiecrunch/zentorrent/internal/debrid"
)

// ErrNoStream mirrors debrid.ErrNoStream for interface symmetry.
var ErrNoStream = errors.New("extractors: no stream available")

// httpClient shared by all extractors.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// baseExtractor carries the common fields of every embed provider.
type baseExtractor struct {
	name string
	base string // embed endpoint prefix
}

func (b baseExtractor) Name() string { return b.name }

// EmbedURL builds the public embed page for the item (movie or TV).
func (b baseExtractor) EmbedURL(item debrid.MediaItem) string {
	if item.Season > 0 {
		return fmt.Sprintf("%s/tv/%d/%d/%d", b.base, item.TMDBID, item.Season, item.Episode)
	}
	return fmt.Sprintf("%s/movie/%d", b.base, item.TMDBID)
}

// resolveBestEffort tries fetch+extract via the provided cracker; on any
// failure it falls back to the embed URL so the web UI can still open it in
// a browser (embed players are fully functional HTML pages).
func (b baseExtractor) resolveBestEffort(
	ctx context.Context,
	item debrid.MediaItem,
	crack func(ctx context.Context, embedURL string) (string, error),
) (*debrid.StreamSource, error) {
	if item.TMDBID == 0 && item.IMDbID == "" {
		return nil, ErrNoStream
	}
	embed := b.EmbedURL(item)

	if m3u8, err := crack(ctx, embed); err == nil && m3u8 != "" {
		return &debrid.StreamSource{
			Type:         debrid.StreamHLS,
			URL:          m3u8,
			Title:        item.Title,
			Quality:      "auto",
			ProviderName: b.name,
			Headers:      map[string]string{"Referer": embed},
		}, nil
	}

	// Degrade to the embed URL itself — hls.js-capable pages play fine in a
	// browser even when our extractor cannot see inside them.
	if !strings.Contains(embed, "http") {
		return nil, ErrNoStream
	}
	return &debrid.StreamSource{
		Type:         debrid.StreamHLS,
		URL:          embed,
		Title:        item.Title,
		Quality:      "embed",
		ProviderName: b.name,
		Headers:      map[string]string{"Referer": embed},
	}, nil
}

// fetchPage retrieves the embed page body with browser-ish headers.
func fetchPage(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("extractors: HTTP %d for %s", resp.StatusCode, url)
	}
	buf := make([]byte, 0, 512<<10)
	tmp := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if len(buf) >= 512<<10 || err != nil {
			break
		}
	}
	return string(buf), nil
}
