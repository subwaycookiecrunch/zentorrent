package main

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

// Adversarial verification harness: reproduces the exact interleaving claimed by
// the finding — user highlights a row during progressive search, a later
// searchPartialMsg re-sorts/rebuilds the table and resets the cursor, then Enter
// commits a different torrent.
func TestLoadingCommitWrongRow(t *testing.T) {
	columns := []table.Column{
		{Title: "Title", Width: 36},
		{Title: "Quality", Width: 7},
		{Title: "Size", Width: 8},
		{Title: "Seeds", Width: 6},
		{Title: "Health", Width: 8},
		{Title: "Category", Width: 11},
	}
	tbl := table.New(table.WithColumns(columns), table.WithFocused(true), table.WithHeight(18))
	s := table.DefaultStyles()
	tbl.SetStyles(s)

	m := searchModel{
		query:          "ubuntu",
		table:          tbl,
		loading:        true,
		results:        nil,
		sourcesTotal:   9,
		sourcesDone:    0,
		sourcesRunning: map[string]bool{},
		tabs:           []string{"All", "Movies", "TV Shows", "Anime"},
	}

	step := func(msg tea.Msg) {
		nm, _ := m.Update(msg)
		m = nm.(searchModel)
	}

	// t=1s: yts returns 4 results.
	step(searchPartialMsg{source: "yts", results: []Result{
		{Title: "Ubuntu Movie A 1080p", Seeders: 300, Category: "Movies", Magnet: "magnet:?xt=urn:btih:AAAAAA"},
		{Title: "Ubuntu Movie B 720p", Seeders: 200, Category: "Movies", Magnet: "magnet:?xt=urn:btih:BBBBBB"},
		{Title: "Ubuntu Movie C 1080p", Seeders: 100, Category: "Movies", Magnet: "magnet:?xt=urn:btih:CCCCCC"},
		{Title: "Ubuntu Movie D 480p", Seeders: 50, Category: "Movies", Magnet: "magnet:?xt=urn:btih:DDDDDD"},
	}})
	if len(m.filteredResults) != 4 {
		t.Fatalf("expected 4 rows after first partial, got %d", len(m.filteredResults))
	}

	// User navigates down 3 rows to highlight "Ubuntu Movie C 1080p".
	for i := 0; i < 3; i++ {
		step(tea.KeyMsg{Type: tea.KeyDown})
	}
	highlightedIdx := m.table.Cursor()
	highlighted := m.filteredResults[highlightedIdx]
	t.Logf("USER HIGHLIGHTED row %d: %q (%d seeds)", highlightedIdx, highlighted.Title, highlighted.Seeders)
	if highlighted.Title != "Ubuntu Movie D 480p" {
		t.Fatalf("setup error: expected to highlight Movie D at row 3, got %q", highlighted.Title)
	}

	// t=2s: another source lands; a hot new torrent re-sorts to row 0 and the
	// rebuild resets the cursor to 0. Loading is still true (7 sources pending).
	step(searchPartialMsg{source: "tpb", results: []Result{
		{Title: "Ubuntu TOTALLY-DIFFERENT Torrent 2160p", Seeders: 9999, Category: "Movies", Magnet: "magnet:?xt=urn:btih:FFFFFF"},
	}})
	t.Logf("after partial: loading=%v cursor=%d row@cursor=%q", m.loading, m.table.Cursor(), m.filteredResults[m.table.Cursor()].Title)

	// User presses Enter (queued behind the partial).
	step(tea.KeyMsg{Type: tea.KeyEnter})

	if m.selected == nil {
		t.Fatal("nothing was selected")
	}
	t.Logf("COMMITTED via enter: %q (%d seeds)", m.selected.Title, m.selected.Seeders)

	if m.selected.Magnet != highlighted.Magnet {
		t.Errorf("RACE CONFIRMED: user highlighted %q but enter committed %q",
			highlighted.Title, m.selected.Title)
	}
	fmt.Printf("highlighted=%s committed=%s\n", highlighted.Title, m.selected.Title)
}
