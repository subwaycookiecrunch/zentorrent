// Package debrid implements ZenTorrent's Tier-1 streaming sources: premium
// debrid clouds (Real-Debrid, TorBox) that turn a magnet into an instant
// HTTP direct link when the swarm is already cached on their servers.
package debrid

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// StreamType discriminates playback tiers.
type StreamType string

const (
	StreamTorrent StreamType = "torrent"
	StreamDebrid  StreamType = "debrid"
	StreamHLS     StreamType = "hls"
)

// SubtitleTrack is one external subtitle sidecar for players that support it.
type SubtitleTrack struct {
	Lang string `json:"lang"`
	URL  string `json:"url"`
}

// StreamSource is a resolved, playable source of any tier.
type StreamSource struct {
	Type         StreamType        `json:"type"`
	URL          string            `json:"url"` // direct HTTP URL or .m3u8 manifest
	Title        string            `json:"title"`
	Quality      string            `json:"quality"` // 4K / 1080p / 720p …
	ProviderName string            `json:"provider"`
	InfoHash     string            `json:"info_hash,omitempty"` // torrent tier
	Magnet       string            `json:"magnet,omitempty"`
	Subtitles    []SubtitleTrack   `json:"subtitles,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"` // referer/origin if required
}

// MediaItem is the identity a provider resolves from: either a magnet
// (torrent-tier hand-off) or a canonical movie (HLS tier).
type MediaItem struct {
	Title    string
	IMDbID   string // "tt…"
	TMDBID   int64
	Year     int
	Magnet   string
	InfoHash string
	Quality  string
	Season   int // >0 switches extractors to TV mode
	Episode  int
}

// Provider resolves media into playable stream sources.
type Provider interface {
	Name() string
	// Resolve returns exactly one playable source or ErrNoStream /
	// ErrNotCached when this provider cannot serve the item quickly.
	Resolve(ctx context.Context, item MediaItem) (*StreamSource, error)
}

// ErrNoStream marks "provider cannot serve this item" without implying a
// network failure — callers skip to the next tier.
var ErrNoStream = errors.New("debrid: no stream available")

// ErrNotCached marks "the torrent exists but is not cached on this debrid";
// distinct from ErrNoStream because instant-cache checks are cheap and worth
// retrying across candidates.
var ErrNotCached = errors.New("debrid: not cached")

// ErrUnauthorized wraps missing/invalid API keys.
var ErrUnauthorized = errors.New("debrid: unauthorized")

// CommonClient carries the knobs both providers share.
type CommonClient struct {
	APIKey  string
	hc      *http.Client
	limiter *rate.Limiter
}

func newCommon(apiKey string) CommonClient {
	return CommonClient{
		APIKey: strings.TrimSpace(apiKey),
		hc: &http.Client{
			Timeout: 30 * time.Second,
		},
		// Both APIs throttle hard; 2 rps with small burst is polite.
		limiter: rate.NewLimiter(rate.Limit(2), 3),
	}
}
