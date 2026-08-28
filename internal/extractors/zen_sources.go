package extractors

import (
	"context"
	"regexp"

	"github.com/subwaycookiecrunch/zentorrent/internal/debrid"
)

// VidLove resolves player.vidlove.cc embeds (Zen Ultra 4K).
type VidLove struct{ baseExtractor }

func NewVidLove() *VidLove {
	return &VidLove{baseExtractor{name: "Zen Ultra", base: "https://player.vidlove.cc/embed"}}
}

func (v *VidLove) crack(ctx context.Context, embedURL string) (string, error) {
	page, err := fetchPage(ctx, embedURL)
	if err != nil {
		return "", err
	}
	return extractPackedM3U8(page)
}

func (v *VidLove) Resolve(ctx context.Context, item debrid.MediaItem) (*debrid.StreamSource, error) {
	return v.resolveBestEffort(ctx, item, v.crack)
}

// ZenLive resolves player.cinezo.live embeds (Zen Live Direct).
type ZenLive struct{ baseExtractor }

func NewZenLive() *ZenLive {
	return &ZenLive{baseExtractor{name: "Zen Live", base: "https://player.cinezo.live/embed"}}
}

func (c *ZenLive) crack(ctx context.Context, embedURL string) (string, error) {
	page, err := fetchPage(ctx, embedURL)
	if err != nil {
		return "", err
	}
	return extractPackedM3U8(page)
}

func (c *ZenLive) Resolve(ctx context.Context, item debrid.MediaItem) (*debrid.StreamSource, error) {
	return c.resolveBestEffort(ctx, item, c.crack)
}

// VidFast resolves vidfast.vc embeds (Zen Nitro).
type VidFast struct{ baseExtractor }

func NewVidFast() *VidFast {
	return &VidFast{baseExtractor{name: "Zen Nitro", base: "https://vidfast.vc"}}
}

func (v *VidFast) crack(ctx context.Context, embedURL string) (string, error) {
	page, err := fetchPage(ctx, embedURL)
	if err != nil {
		return "", err
	}
	return extractPackedM3U8(page)
}

func (v *VidFast) Resolve(ctx context.Context, item debrid.MediaItem) (*debrid.StreamSource, error) {
	return v.resolveBestEffort(ctx, item, v.crack)
}

// VidBolt resolves vidbolt.xyz embeds (Zen Direct).
type VidBolt struct{ baseExtractor }

func NewVidBolt() *VidBolt {
	return &VidBolt{baseExtractor{name: "Zen Direct", base: "https://vidbolt.xyz"}}
}

func (v *VidBolt) Resolve(ctx context.Context, item debrid.MediaItem) (*debrid.StreamSource, error) {
	return v.resolveBestEffort(ctx, item, func(ctx context.Context, embedURL string) (string, error) {
		return "", ErrNoStream
	})
}

// Helper to extract packed / eval m3u8 sources from HTML
func extractPackedM3U8(html string) (string, error) {
	reM3U8 := regexp.MustCompile(`["'](https?://[^"']+\.m3u8[^"']*)["']`)
	if m := reM3U8.FindStringSubmatch(html); len(m) > 1 {
		return strings_unescape(m[1]), nil
	}

	reSource := regexp.MustCompile(`sources:\s*\[\s*\{\s*file:\s*["']([^"']+)["']`)
	if m := reSource.FindStringSubmatch(html); len(m) > 1 {
		return strings_unescape(m[1]), nil
	}

	reHLS := regexp.MustCompile(`(?:file|url|source|stream)\s*:\s*["'](https?://[^"']+(?:master|index|playlist)\.m3u8[^"']*)["']`)
	if m := reHLS.FindStringSubmatch(html); len(m) > 1 {
		return strings_unescape(m[1]), nil
	}

	return "", ErrNoStream
}

// AllExtractors returns all active Tier-3 embed/streaming providers.
func AllExtractors() []debrid.Provider {
	return []debrid.Provider{
		NewVidSrc(),
		NewVidLink(),
		NewAutoEmbed(),
		NewVidLove(),
		NewVidFast(),
		NewZenLive(),
		NewVidBolt(),
	}
}
