package search

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// knownHash is the v1 infohash used across fixtures.
const knownHash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b"

func mustBase32(t testing.TB, hexHash string) string {
	t.Helper()
	raw, err := hex.DecodeString(hexHash)
	if err != nil {
		t.Fatalf("fixture hash: %v", err)
	}
	return strings.ToLower(base32.StdEncoding.EncodeToString(raw))
}

// altHash / its base32 form exercise the btih base32 branch.
var (
	altHashRaw = []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13}
	altHashHex = fmt.Sprintf("%x", altHashRaw)
)

const sampleTorznabXMLFmt = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
  <channel>
    <title>Prowlarr</title>
    <item>
      <title>Interstellar.2014.1080p.BluRay.x264-AMIABLE</title>
      <guid isPermaLink="true">https://idx.example.com/details/123</guid>
      <link>https://idx.example.com/download/123</link>
      <pubDate>Tue, 15 Nov 2022 10:45:30 +0000</pubDate>
      <category>2040</category>
      <category>2000</category>
      <enclosure url="magnet:?xt=urn:btih:%s&amp;dn=interstellar" length="1468006400" type="application/x-bittorrent"/>
      <newznab:attr name="size" value="1468006400"/>
      <newznab:attr name="seeders" value="87"/>
      <newznab:attr name="peers" value="120"/>
      <newznab:attr name="infohash" value="%s"/>
      <newznab:attr name="imdb" value="tt0816692"/>
      <newznab:attr name="grabs" value="512"/>
    </item>
    <item>
      <title>Bahubali.The.Beginning.2015.2160p.WEB-DL.Hindi.x265-TOMMY</title>
      <pubDate>bogus-date</pubDate>
      <enclosure url="magnet:?xt=urn:btih:%s&amp;dn=bahubali"/>
    </item>
  </channel>
</rss>`

func sampleTorznabXML(t testing.TB) string {
	return fmt.Sprintf(sampleTorznabXMLFmt,
		strings.ToUpper(knownHash), knownHash,
		strings.ToUpper(mustBase32(t, altHashHex)))
}

func TestParseTorznabXML(t *testing.T) {
	cands, err := ParseTorznabXML([]byte(sampleTorznabXML(t)), "prowlarr")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(cands))
	}

	c := cands[0]
	if c.Title != "Interstellar.2014.1080p.BluRay.x264-AMIABLE" {
		t.Errorf("title = %q", c.Title)
	}
	if c.InfoHash != knownHash {
		t.Errorf("infohash = %q", c.InfoHash)
	}
	if c.Seeders != 87 || c.Leechers != 120 {
		t.Errorf("seeders=%d leechers=%d (peers attr maps to leechers)", c.Seeders, c.Leechers)
	}
	if c.SizeBytes != 1468006400 || c.Grabs != 512 {
		t.Errorf("size=%d grabs=%d", c.SizeBytes, c.Grabs)
	}
	if c.IMDbID != "tt0816692" {
		t.Errorf("imdb = %q", c.IMDbID)
	}
	if !strings.HasPrefix(c.Magnet, "magnet:?xt=urn:btih:") {
		t.Errorf("magnet missing: %q", c.Magnet)
	}
	if c.DownloadURL == "" {
		t.Errorf("download url should fall back to <link>")
	}
	want := time.Date(2022, 11, 15, 10, 45, 30, 0, time.UTC)
	if !c.PublishedAt.Equal(want) {
		t.Errorf("published = %v, want %v", c.PublishedAt, want)
	}
	if len(c.Categories) != 2 || c.Categories[0] != 2040 {
		t.Errorf("categories = %v", c.Categories)
	}

	// Second item exercises base32 infohash decoding and bogus pubDate.
	b := cands[1]
	if b.InfoHash != altHashHex {
		t.Errorf("base32 decode failed: %q want %q", b.InfoHash, altHashHex)
	}
	if !b.PublishedAt.IsZero() {
		t.Errorf("bogus date should yield zero time, got %v", b.PublishedAt)
	}
	if b.Seeders != 0 {
		t.Errorf("missing attrs must default to zero, got %d", b.Seeders)
	}
}

func TestParseTorznabXMLError(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss><channel><error code="910"><description>API key invalid</description></error></channel></rss>`)
	_, err := ParseTorznabXML(body, "x")
	if err == nil || !strings.Contains(err.Error(), "910") {
		t.Fatalf("expected indexer error, got %v", err)
	}
}

const sampleTorznabJSON = `{
  "results": [
    {
      "Title": "Drishyam.2015.1080p.WEB-DL.Hindi.x264-EVO",
      "InfoHash": "9F86D081884C7D659A2FEAA0C55AD015A3BF4F1B",
      "MagnetUri": "magnet:?xt=urn:btih:9F86D081884C7D659A2FEAA0C55AD015A3BF4F1B&dn=drishyam",
      "Size": 3200000000,
      "Seeders": 40,
      "Peers": 52,
      "PublishDate": "2023-05-01T00:00:00Z",
      "ImdbId": 1543513,
      "Categories": [2040]
    }
  ]
}`

func TestParseTorznabJSON(t *testing.T) {
	cands, err := parseTorznabJSON([]byte(sampleTorznabJSON), "jackett")
	if err != nil {
		t.Fatalf("parse json: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("want 1, got %d", len(cands))
	}
	c := cands[0]
	if c.Title != "Drishyam.2015.1080p.WEB-DL.Hindi.x264-EVO" || c.IMDbID != "tt1543513" {
		t.Errorf("title/imdb mismatch: %+v", c)
	}
	if c.Leechers != 12 {
		t.Errorf("leechers should derive Peers-Seeders=12, got %d", c.Leechers)
	}
	if c.InfoHash != "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b" {
		t.Errorf("infohash case: %q", c.InfoHash)
	}
}

func TestBuildQueryURL(t *testing.T) {
	ep := Endpoint{Name: "test", BaseURL: "http://x.test/api/", APIKey: "k"}
	u := buildQueryURL(ep, QueryParams{Query: "Blade Runner 2049", Year: 2017,
		Categories: []int{CatMoviesHD, CatMoviesUHD}, Limit: 50})
	for _, want := range []string{
		"http://x.test/api?", // trailing /api in base is not doubled
		"t=movie",
		"q=Blade+Runner+2049", "year=2017",
		"cat=2040%2C2045", "limit=50", "apikey=k",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("url %q missing %q", u, want)
		}
	}

	u2 := buildQueryURL(ep, QueryParams{IMDbID: "tt1853728"})
	if !strings.Contains(u2, "imdbid=1853728") || strings.Contains(u2, "q=") {
		t.Errorf("imdbid search malformed: %q", u2)
	}
}

type mockRoundTripper func(req *http.Request) (*http.Response, error)

func (m mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return m(req) }

func TestSearchConcurrentAcrossEndpoints(t *testing.T) {
	client := NewTorznabClientWithHTTP([]Endpoint{
		{Name: "slow", BaseURL: "https://slow.test/api", APIKey: "k"},
		{Name: "fast", BaseURL: "https://fast.test/api", APIKey: "k"},
	}, &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Host, "slow") {
				select {
				case <-time.After(400 * time.Millisecond):
				case <-req.Context().Done():
					return nil, req.Context().Err()
				}
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(sampleTorznabXML(t))),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(strings.ReplaceAll(sampleTorznabXML(t), "AMIABLE", "GECKOS"))),
				Header:     make(http.Header),
			}, nil
		}),
	})

	start := time.Now()
	results, err := client.Search(context.Background(),
		QueryParams{IMDbID: "tt0816692"}, 80*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("fan-out not concurrent (took %v)", elapsed)
	}
	byName := map[string]EndpointResult{}
	for _, r := range results {
		byName[r.Endpoint] = r
	}
	if got := byName["fast"]; got.Err != nil || len(got.Results) != 2 {
		t.Errorf("fast endpoint bad: err=%v n=%d", got.Err, len(got.Results))
	}
	if got := byName["slow"]; got.Err == nil {
		t.Errorf("slow endpoint should have timed out")
	}
}

func TestInfoHashFromMagnet(t *testing.T) {
	hexMag := "magnet:?xt=urn:btih:" +
		"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b"
	if got := InfoHashFromMagnet(hexMag); got != "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b" {
		t.Errorf("hex magnet: %q", got)
	}
	b32Mag := "magnet:?xt=urn:btih:" + mustBase32(t, altHashHex)
	if got := InfoHashFromMagnet(b32Mag); got != altHashHex {
		t.Errorf("base32 magnet: %q want %q", got, altHashHex)
	}
	if got := InfoHashFromMagnet("not-a-magnet"); got != "" {
		t.Errorf("garbage should be empty, got %q", got)
	}
}
