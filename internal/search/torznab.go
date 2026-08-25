package search

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// Endpoint is a single Torznab-compatible indexer (or a Prowlarr/Jackett
// proxy exposing one).
type Endpoint struct {
	Name    string // human label, also used as TorrentCandidate.Source
	BaseURL string // e.g. https://prowlarr.example.com/1
	APIKey  string
}

// QueryParams describes one logical search dispatched to every endpoint.
type QueryParams struct {
	Query      string
	Year       int
	IMDbID     string // "tt0000000"; sent as Torznab's imdbid= without prefix
	Categories []int  // empty = indexer default
	Limit      int    // per-endpoint result cap; 0 = server default
}

// TorznabClient fans a single QueryParams out to all configured endpoints
// concurrently and merges results.
type TorznabClient struct {
	endpoints []Endpoint
	hc        *http.Client
}

func NewTorznabClient(eps []Endpoint) *TorznabClient {
	return &TorznabClient{
		endpoints: append([]Endpoint(nil), eps...),
		hc: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Endpoints returns the configured endpoints (copy).
func (t *TorznabClient) Endpoints() []Endpoint {
	return append([]Endpoint(nil), t.endpoints...)
}

// EndpointResult carries per-endpoint outcome so callers can surface
// partial failures as warnings instead of dropping everything.
type EndpointResult struct {
	Endpoint string
	Results  []TorrentCandidate
	Err      error
}

// Search queries every endpoint concurrently and returns merged, per-endpoint
// deconflicted candidates. It never fails because one endpoint did: errors
// are reported through the returned EndpointResults. The overall ctx bounds
// the whole fan-out; each endpoint additionally gets perEndpointTimeout.
func (t *TorznabClient) Search(ctx context.Context, p QueryParams, perEndpointTimeout time.Duration) ([]EndpointResult, error) {
	if len(t.endpoints) == 0 {
		return nil, nil
	}
	if perEndpointTimeout <= 0 {
		perEndpointTimeout = 15 * time.Second
	}

	results := make([]EndpointResult, len(t.endpoints))
	var mu sync.Mutex // guards individual slot writes only

	g, gctx := errgroup.WithContext(ctx)
	for i, ep := range t.endpoints {
		i, ep := i, ep
		g.Go(func() error {
			cctx, cancel := context.WithTimeout(gctx, perEndpointTimeout)
			defer cancel()

			cands, err := t.queryOne(cctx, ep, p)
			mu.Lock()
			results[i] = EndpointResult{Endpoint: ep.Name, Results: cands, Err: err}
			mu.Unlock()
			return nil // never abort siblings on one endpoint's failure
		})
	}
	_ = g.Wait() // closure always returns nil
	return results, ctx.Err()
}

func (t *TorznabClient) queryOne(ctx context.Context, ep Endpoint, p QueryParams) ([]TorrentCandidate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildQueryURL(ep, p), nil)
	if err != nil {
		return nil, fmt.Errorf("torznab %s: %w", ep.Name, err)
	}
	req.Header.Set("Accept", "application/xml, application/json")
	req.Header.Set("User-Agent", "ZenTorrent/4.0")

	resp, err := t.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("torznab %s: %w", ep.Name, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("torznab %s: read body: %w", ep.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("torznab %s: HTTP %d: %.120s", ep.Name, resp.StatusCode, string(body))
	}

	trimmed := strings.TrimLeft(string(body), " \t\r\n")
	var (
		cands []TorrentCandidate
		perr  error
	)
	if strings.HasPrefix(trimmed, "{") {
		cands, perr = parseTorznabJSON(body, ep.Name)
	} else {
		cands, perr = ParseTorznabXML(body, ep.Name)
	}
	if perr != nil {
		return nil, fmt.Errorf("torznab %s: %w", ep.Name, perr)
	}
	if p.Limit > 0 && len(cands) > p.Limit {
		cands = cands[:p.Limit]
	}
	return cands, nil
}

// buildQueryURL assembles the Torznab endpoint URL:
//
//	{base}/api?t={type}&apikey={key}&q=...&year=...&imdbid=...&cat=...
func buildQueryURL(ep Endpoint, p QueryParams) string {
	base := strings.TrimRight(ep.BaseURL, "/")
	// Accept both bare indexer roots and full API URLs in config.
	api := base
	if !strings.HasSuffix(base, "/api") {
		api += "/api"
	}

	q := url.Values{}
	q.Set("t", "movie")
	q.Set("apikey", ep.APIKey)
	if imdb := normalizeIMDbID(p.IMDbID); imdb != "" {
		// IMDb-scoped searches are authoritative; q stays empty per spec.
		q.Set("imdbid", strings.TrimPrefix(imdb, "tt"))
	} else if p.Query != "" {
		q.Set("q", p.Query)
		if p.Year > 0 {
			q.Set("year", strconv.Itoa(p.Year))
		}
	}
	if len(p.Categories) > 0 {
		parts := make([]string, len(p.Categories))
		for i, c := range p.Categories {
			parts[i] = strconv.Itoa(c)
		}
		q.Set("cat", strings.Join(parts, ","))
	}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	return api + "?" + q.Encode()
}

func normalizeIMDbID(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return ""
	}
	if !strings.HasPrefix(id, "tt") {
		id = "tt" + id
	}
	if _, err := strconv.Atoi(strings.TrimPrefix(id, "tt")); err != nil {
		return ""
	}
	return id
}

// ---- XML wire format -------------------------------------------------------

type torznabRSS struct {
	XMLName xml.Name       `xml:"rss"`
	Channel torznabChannel `xml:"channel"`
}

type torznabChannel struct {
	Items []torznabItem `xml:"item"`
	Error *torznabError `xml:"error"`
}

type torznabError struct {
	Code        int    `xml:"code,attr"`
	Description string `xml:",chardata"`
}

type torznabAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type torznabEnclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
}

type torznabItem struct {
	Title     string            `xml:"title"`
	GUID      string            `xml:"guid"`
	Link      string            `xml:"link"`
	PubDate   string            `xml:"pubDate"`
	Size      int64             `xml:"size"`
	Category  []int             `xml:"category"`
	Enclosure *torznabEnclosure `xml:"enclosure"`
	// Namespaced newznab attrs; some proxies omit the xmlns declaration so we
	// also capture un-prefixed attr elements and merge.
	Attrs     []torznabAttr `xml:"http://www.newznab.com/DTD/2010/feeds/attributes/ attr"`
	PlainAttr []torznabAttr `xml:"attr"`
}

// ParseTorznabXML parses a Newznab/Torznab RSS response into unified
// candidates. Exported for reuse and testing.
func ParseTorznabXML(body []byte, source string) ([]TorrentCandidate, error) {
	var feed torznabRSS
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("xml decode: %w", err)
	}
	if feed.Channel.Error != nil && len(feed.Channel.Items) == 0 {
		return nil, fmt.Errorf("indexer error %d: %s",
			feed.Channel.Error.Code, strings.TrimSpace(feed.Channel.Error.Description))
	}

	out := make([]TorrentCandidate, 0, len(feed.Channel.Items))
	for _, it := range feed.Channel.Items {
		out = append(out, candidateFromItem(it, source))
	}
	return out, nil
}

func candidateFromItem(it torznabItem, source string) TorrentCandidate {
	attrs := map[string]string{}
	for _, a := range append(append([]torznabAttr{}, it.Attrs...), it.PlainAttr...) {
		attrs[strings.ToLower(a.Name)] = a.Value
	}

	c := TorrentCandidate{
		Title:       strings.TrimSpace(it.Title),
		SizeBytes:   it.Size,
		Categories:  it.Category,
		Source:      source,
		PublishedAt: parseTorznabTime(it.PubDate),
	}
	if v, ok := attrs["size"]; ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.SizeBytes = n
		}
	}
	if v, ok := attrs["seeders"]; ok {
		c.Seeders = atoiDefault(v)
	}
	if v, ok := attrs["peers"]; ok {
		c.Leechers = atoiDefault(v)
	} else if v, ok := attrs["leechers"]; ok {
		c.Leechers = atoiDefault(v)
	}
	if v, ok := attrs["grabs"]; ok {
		c.Grabs = atoiDefault(v)
	}
	if v, ok := attrs["infohash"]; ok {
		c.InfoHash = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := attrs["imdb"]; ok {
		c.IMDbID = normalizeIMDbID(v)
	} else if v, ok := attrs["imdbid"]; ok {
		c.IMDbID = normalizeIMDbID(v)
	}
	if v, ok := attrs["passworded"]; ok && (v == "1" || strings.EqualFold(v, "true")) {
		c.Passworded = true
	}

	if it.Enclosure != nil {
		u := strings.TrimSpace(it.Enclosure.URL)
		if u != "" {
			if strings.HasPrefix(u, "magnet:") {
				c.Magnet = u
				if c.InfoHash == "" {
					c.InfoHash = InfoHashFromMagnet(u)
				}
				if c.SizeBytes == 0 && it.Enclosure.Length > 0 {
					c.SizeBytes = it.Enclosure.Length
				}
			} else {
				c.DownloadURL = u
				if c.SizeBytes == 0 && it.Enclosure.Length > 0 {
					c.SizeBytes = it.Enclosure.Length
				}
			}
		}
	}
	if c.DownloadURL == "" && it.Link != "" && !strings.HasPrefix(it.Link, "magnet:") {
		c.DownloadURL = it.Link
	} else if c.Magnet == "" && strings.HasPrefix(it.Link, "magnet:") {
		c.Magnet = it.Link
	}
	return c
}

// imdbAnyToString renders a JSON-decoded IMDb identifier without the
// scientific notation fmt.Sprint would produce for large numbers.
func imdbAnyToString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case float64:
		if x == math.Trunc(x) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	default:
		return strings.TrimSpace(fmt.Sprint(x))
	}
}

func parseTorznabTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func atoiDefault(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// ---- JSON wire format (Jackett/Prowlarr-style) ------------------------------

type torznabJSONFeed struct {
	Results []torznabJSONItem `json:"results"`
}

type torznabJSONItem struct {
	Title       string `json:"Title"`
	GUID        string `json:"Guid"`
	InfoHash    string `json:"InfoHash"`
	MagnetURI   string `json:"MagnetUri"`
	DownloadURL string `json:"DownloadUrl"`
	Size        int64  `json:"Size"`
	Seeders     int    `json:"Seeders"`
	Leechers    int    `json:"Leechers"`
	Peers       int    `json:"Peers"` // some indexers report total peers here
	PublishDate string `json:"PublishDate"`
	// IMDbID arrives as a string from most indexers but as a bare number
	// from others; decode permissively.
	IMDbID     any   `json:"ImdbId"`
	Categories []int `json:"Categories"`
	Grabs      int   `json:"Grabs"`
	Passworded bool  `json:"Passworded,omitempty"`
}

func parseTorznabJSON(body []byte, source string) ([]TorrentCandidate, error) {
	var feed torznabJSONFeed
	if err := json.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	out := make([]TorrentCandidate, 0, len(feed.Results))
	for _, r := range feed.Results {
		c := TorrentCandidate{
			Title:       strings.TrimSpace(r.Title),
			InfoHash:    strings.ToLower(strings.TrimSpace(r.InfoHash)),
			Magnet:      r.MagnetURI,
			DownloadURL: r.DownloadURL,
			SizeBytes:   r.Size,
			Seeders:     r.Seeders,
			Leechers:    r.Leechers,
			Source:      source,
			Categories:  r.Categories,
			Grabs:       r.Grabs,
			Passworded:  r.Passworded,
		}
		if c.Leechers == 0 && r.Peers > r.Seeders {
			c.Leechers = r.Peers - r.Seeders
		}
		if r.PublishDate != "" {
			if t, err := time.Parse(time.RFC3339, r.PublishDate); err == nil {
				c.PublishedAt = t
			}
		}
		if id := normalizeIMDbID(imdbAnyToString(r.IMDbID)); id != "" {
			c.IMDbID = id
		}
		if c.InfoHash == "" && c.Magnet != "" {
			c.InfoHash = InfoHashFromMagnet(c.Magnet)
		}
		out = append(out, c)
	}
	return out, nil
}
