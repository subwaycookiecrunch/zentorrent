package search

import (
	"strings"
	"testing"
)

func TestParseSizeBytes(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"1.5 GB", int64(15 * 1024 * 1024 * 1024 / 10)},
		{"700 MB", 700 * 1024 * 1024},
		{"500 KB", 500 * 1024},
		{"1024 B", 1024},
		{"2.5G", int64(25 * 1024 * 1024 * 1024 / 10)},
		{"", 0},
	}
	for _, tc := range tests {
		got := parseSizeBytes(tc.in)
		if got != tc.want {
			t.Errorf("parseSizeBytes(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseTGxHTML(t *testing.T) {
	sampleHTML := `<div class="tgxtablerow">
		<a class="txlight" href="/torrent/123/Movie-Title">Stree 2 (2024) Hindi 1080p Web-DL Dual-Audio</a>
		<a href="magnet:?xt=urn:btih:ABCDEF1234567890ABCDEF1234567890ABCDEF12&dn=Stree+2"></a>
		<span class="badge badge-secondary">2.1 GB</span>
		<font color="green"><b>142</b></font>
	</div>`

	cands, err := parseTGxHTML(strings.NewReader(sampleHTML))
	if err != nil {
		t.Fatalf("parseTGxHTML err: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(cands))
	}
	c := cands[0]
	if !strings.Contains(c.Title, "Stree 2") {
		t.Errorf("title = %q", c.Title)
	}
	if c.InfoHash != "abcdef1234567890abcdef1234567890abcdef12" {
		t.Errorf("infohash = %q", c.InfoHash)
	}
	if c.Seeders != 142 {
		t.Errorf("seeders = %d, want 142", c.Seeders)
	}
	if c.SizeBytes <= 0 {
		t.Errorf("sizeBytes = %d", c.SizeBytes)
	}
}

func TestDefaultScrapersCount(t *testing.T) {
	scrapers := DefaultScrapers()
	if len(scrapers) < 8 {
		t.Errorf("expected at least 8 scrapers, got %d", len(scrapers))
	}
}
