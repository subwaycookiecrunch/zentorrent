package extractors

import (
	"context"
	"regexp"

	"github.com/subwaycookiecrunch/zentorrent/internal/debrid"
)

// VidSrc resolves vidsrc.to embeds.
type VidSrc struct{ baseExtractor }

func NewVidSrc() *VidSrc {
	return &VidSrc{baseExtractor{name: "VidSrc", base: "https://vidsrc.to/embed"}}
}

var (
	vidsrcSourcesRe = regexp.MustCompile(`"source"\s*:\s*"([^"]+)"`)
	vidsrcFileRe    = regexp.MustCompile(`(?i)(?:file|source)\s*[:=]\s*["'](https?://[^"'\s]+)["']`)
	vidsrcM3u8Re    = regexp.MustCompile(`(?i)["'](https?://[^"'\s]+\.(?:m3u8|mp4)[^"'\s]*)["']`)
)

// crack attempts the two-hop player chain (embed page → sources JSON).
func (v *VidSrc) crack(ctx context.Context, embedURL string) (string, error) {
	page, err := fetchPage(ctx, embedURL)
	if err != nil {
		return "", err
	}
	if m := vidsrcFileRe.FindStringSubmatch(page); m != nil {
		return strings_unescape(m[1]), nil
	}
	if m := vidsrcM3u8Re.FindStringSubmatch(page); m != nil {
		return strings_unescape(m[1]), nil
	}
	if m := vidsrcSourcesRe.FindStringSubmatch(page); m != nil {
		return strings_unescape(m[1]), nil
	}
	return "", ErrNoStream
}

// Resolve implements debrid.Provider.
func (v *VidSrc) Resolve(ctx context.Context, item debrid.MediaItem) (*debrid.StreamSource, error) {
	return v.resolveBestEffort(ctx, item, v.crack)
}
