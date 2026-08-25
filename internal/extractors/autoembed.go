package extractors

import (
	"context"
	"regexp"

	"github.com/subwaycookiecrunch/zentorrent/internal/debrid"
)

// AutoEmbed resolves autoembed.cc embeds.
type AutoEmbed struct{ baseExtractor }

func NewAutoEmbed() *AutoEmbed {
	return &AutoEmbed{baseExtractor{name: "AutoEmbed", base: "https://autoembed.cc/embed"}}
}

var autoEmbedM3u8Re = regexp.MustCompile(`"(https?://[^"]+\.m3u8[^"]*)"`)

func (a *AutoEmbed) crack(ctx context.Context, embedURL string) (string, error) {
	page, err := fetchPage(ctx, embedURL)
	if err != nil {
		return "", err
	}
	if m := autoEmbedM3u8Re.FindStringSubmatch(page); m != nil {
		return m[1], nil
	}
	return "", ErrNoStream
}

// Resolve implements debrid.Provider.
func (a *AutoEmbed) Resolve(ctx context.Context, item debrid.MediaItem) (*debrid.StreamSource, error) {
	return a.resolveBestEffort(ctx, item, a.crack)
}
