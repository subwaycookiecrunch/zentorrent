package main

import (
	"testing"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func newSearchTestTable() table.Model {
	// Column layout must match rebuildTable's 6-cell rows exactly: bubbles'
	// renderRow indexes m.cols[i] for every row cell, so a short column list
	// panics.
	t := table.New(
		table.WithColumns([]table.Column{
			{Title: "Title", Width: 36},
			{Title: "Quality", Width: 7},
			{Title: "Size", Width: 8},
			{Title: "Seeds", Width: 6},
			{Title: "Health", Width: 8},
			{Title: "Category", Width: 11},
		}),
		table.WithFocused(true),
		table.WithHeight(18),
	)
	s := table.DefaultStyles()
	t.SetStyles(s)
	return t
}

// Scratch verifier: pressing ctrl+c while the search delay prompt (inputMode)
// is focused must quit the program with forceExit set. os.Exit inside Update
// used to run here, skipping bubbletea's terminal restore and leaving the
// shell in raw mode; the handler now defers the hard exit to StartSearchTUI,
// after Run returns and the terminal is sane again.
func TestSearchInputModeCtrlCQuitsSafely(t *testing.T) {
	ti := textinput.New() // zero-value Model panics in Focus(); New() sets up the cursor
	m := searchModel{
		query:     "query",
		table:     newSearchTestTable(),
		tabs:      []string{"All", "Movies", "TV Shows", "Anime"},
		textInput: ti,
	}
	m.loading = false
	m.results = []Result{{Title: "Some Movie", Magnet: "magnet:?xt=1", Seeders: 5, Category: "Movies"}}
	m.rebuildTable()

	// Reach the prompt the way the user does: 't' on a result.
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	m2 := nm.(searchModel)
	if !m2.inputMode {
		t.Fatalf("expected inputMode=true after 't' on a result")
	}

	before := m2.textInput.Value()
	selBefore := m2.selected // 't' pre-selects the highlighted row by design

	// Now press ctrl+c while the prompt has focus.
	nm3, cmd3 := m2.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m3 := nm3.(searchModel)

	if !m3.quitting {
		t.Fatalf("ctrl+c in inputMode must set quitting")
	}
	if !m3.forceExit {
		t.Fatalf("ctrl+c in inputMode must arm forceExit so ExitApp runs post-restore")
	}
	if m3.selected != selBefore {
		t.Fatalf("ctrl+c changed the selection")
	}
	if m3.textInput.Value() != before {
		t.Fatalf("ctrl+c mutated the input value")
	}
	var out []tea.Msg
	execAll(t, cmd3, &out)
	quitSeen := false
	for _, msg := range out {
		if _, ok := msg.(tea.QuitMsg); ok {
			quitSeen = true
		}
	}
	if !quitSeen {
		t.Fatalf("ctrl+c produced no QuitMsg")
	}
}
