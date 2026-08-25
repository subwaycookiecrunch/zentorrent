// Package search provides ZenTorrent's discovery layer: a Torznab indexer
// client, a decentralized DHT metainfo indexer, and an aggregator that
// resolves user queries to canonical movie identities and ranks torrent
// candidates across all configured sources.
//
// This file defines the shared vocabulary used by every component.
package search

import (
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TorrentCandidate is a single torrent offer surfaced by any source
// (Torznab indexer, DHT index, scraper). Sources populate as many fields as
// they can; the aggregator normalizes and ranks them uniformly.
type TorrentCandidate struct {
	// InfoHash is the 40-character lowercase hex v1 infohash. May be empty
	// when a source only exposes a download URL.
	InfoHash string
	Title    string
	Magnet   string
	// DownloadURL is a direct .torrent (or provider redirect) URL when known.
	DownloadURL string
	SizeBytes   int64
	Seeders     int
	Leechers    int
	Grabs       int
	// Source identifies where the candidate came from, e.g. "prowlarr",
	// "dht", or a scraper name. Dedup merges multiple sources together.
	Source      string
	PublishedAt time.Time
	IMDbID      string // normalized "tt0000000" form, empty when unknown
	Categories  []int
	// Passworded is true when a source explicitly marked the torrent as
	// requiring a password. Such candidates are dropped before ranking.
	Passworded bool
}

// Key returns the deduplication identity for the candidate. Infohash is
// authoritative; when unavailable we fall back to a title+size fingerprint.
func (c TorrentCandidate) Key() string {
	if c.InfoHash != "" {
		return "ih:" + strings.ToLower(c.InfoHash)
	}
	if c.Magnet != "" {
		if ih := InfoHashFromMagnet(c.Magnet); ih != "" {
			return "ih:" + ih
		}
	}
	return fmt.Sprintf("ts:%s|%d", normalizeKeyPart(c.Title), c.SizeBytes/(1<<20))
}

// HasLocation reports whether the candidate can actually be started: it
// carries an infohash (magnet can be synthesized) or a usable URL.
func (c TorrentCandidate) HasLocation() bool {
	return c.InfoHash != "" || c.Magnet != "" || c.DownloadURL != ""
}

func normalizeKeyPart(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// InfoHashFromMagnet extracts the hex infohash from a magnet URI, decoding
// base32 xt values (common on older indexers) into hex.
func InfoHashFromMagnet(magnet string) string {
	u, err := url.Parse(magnet)
	if err != nil {
		return ""
	}
	for _, xt := range u.Query()["xt"] {
		const prefix = "urn:btih:"
		if !strings.HasPrefix(strings.ToLower(xt), prefix) {
			continue
		}
		v := xt[len(prefix):]
		switch len(v) {
		case 40:
			if _, err := hex.DecodeString(strings.ToLower(v)); err == nil {
				return strings.ToLower(v)
			}
		case 32:
			if raw, err := base32.StdEncoding.DecodeString(strings.ToUpper(v)); err == nil && len(raw) == 20 {
				return hex.EncodeToString(raw)
			}
		}
	}
	return ""
}

// SynthesizeMagnet returns a magnet URI for the candidate, preferring an
// embedded magnet and otherwise constructing one from the infohash.
func (c TorrentCandidate) SynthesizeMagnet() string {
	if c.Magnet != "" {
		return c.Magnet
	}
	if c.InfoHash == "" {
		return ""
	}
	display := c.Title
	if display == "" {
		display = c.InfoHash
	}
	m := fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", c.InfoHash, url.QueryEscape(display))
	if c.Source != "" {
		tr := trackerForSource(c.Source)
		if tr != "" {
			m += "&tr=" + url.QueryEscape(tr)
		}
	}
	return m
}

func trackerForSource(source string) string {
	// DHT-discovered torrents have no guaranteed trackers; give them the
	// public set so the engine client can find peers quickly.
	return "udp://tracker.opentrackr.org:1337/announce"
}

// ResolvedMovie is the canonical identity a raw user query was resolved to.
type ResolvedMovie struct {
	TMDBID        int64
	IMDbID        string // "tt0000000"
	Title         string
	OriginalTitle string
	Year          int
	Aliases       []string
	PosterPath    string
	Overview      string
	Genres        []string
	Language      string // ISO 639-1, e.g. "hi" for Hindi cinema
	// Score is the resolver's confidence in the match, 0..1.
	Score float64
}

// SearchTerms returns the strings downstream indexers should be queried
// with: the official title, the original title, and strong aliases.
func (m *ResolvedMovie) SearchTerms() []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		k := strings.ToLower(s)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, s)
	}
	add(m.Title)
	add(m.OriginalTitle)
	for _, a := range m.Aliases {
		add(a)
	}
	return out
}

// Torznab category constants (subset relevant to video discovery).
const (
	CatMoviesHD      = 2040
	CatMoviesUHD     = 2045
	CatMoviesForeign = 2010
	CatTV            = 5000
	CatAnime         = 5070
)
