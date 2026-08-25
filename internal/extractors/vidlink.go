package extractors

import (
	"context"
	"regexp"

	"github.com/subwaycookiecrunch/zentorrent/internal/debrid"
)

// VidLink resolves vidlink.pro embeds.
type VidLink struct{ baseExtractor }

func NewVidLink() *VidLink {
	return &VidLink{baseExtractor{name: "VidLink", base: "https://vidlink.pro"}}
}

var vidlinkM3u8Re = regexp.MustCompile(`(https?://[^"'\s]+\.m3u8[^"'\s]*)`)

func (v *VidLink) crack(ctx context.Context, embedURL string) (string, error) {
	page, err := fetchPage(ctx, embedURL)
	if err != nil {
		return "", err
	}
	if m := vidlinkM3u8Re.FindStringSubmatch(page); m != nil {
		return strings_unescape(m[1]), nil
	}
	return "", ErrNoStream
}

// Resolve implements debrid.Provider.
func (v *VidLink) Resolve(ctx context.Context, item debrid.MediaItem) (*debrid.StreamSource, error) {
	return v.resolveBestEffort(ctx, item, v.crack)
}
