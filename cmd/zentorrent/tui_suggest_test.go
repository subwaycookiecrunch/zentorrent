package main

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/subwaycookiecrunch/zentorrent/internal/metadata"
)

type fakeSuggester struct {
	calls int
	last  string
	items []metadata.Suggestion
}

func (f *fakeSuggester) Suggest(ctx context.Context, prefix string, limit int) ([]metadata.Suggestion, error) {
	f.calls++
	f.last = prefix
	return f.items, nil
}

// execAll flattens tea.Batch trees into leaf messages (ticks included).
func execAll(t *testing.T, cmd tea.Cmd, out *[]tea.Msg) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			execAll(t, c, out)
		}
	default:
		*out = append(*out, msg)
	}
}

func TestMenuSearchTypeAhead(t *testing.T) {
	fake := &fakeSuggester{items: []metadata.Suggestion{
		{TMDBID: 3, Title: "Drishyam", Year: 2015},
		{TMDBID: 4, Title: "Drishyam 2", Year: 2022},
	}}

	m := newMenuModel()
	m.suggester = fake
	m.inputMode = "search"
	m.textInput.Focus()

	// Typing "dr" must schedule exactly one live lookup (latest generation).
	var msgs []tea.Msg
	for _, r := range []rune{'d', 'r'} {
		nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = nm.(menuModel)
		execAll(t, cmd, &msgs)
	}
	// Feed every produced message back; stale generations drop themselves.
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		nm, cmd := m.Update(msg)
		m = nm.(menuModel)
		if cmd != nil {
			var follow []tea.Msg
			execAll(t, cmd, &follow)
			for _, fmsg := range follow {
				if fmsg == nil {
					continue
				}
				nm3, _ := m.Update(fmsg)
				m = nm3.(menuModel)
			}
		}
	}

	if fake.calls != 1 {
		t.Errorf("expected 1 debounced catalog call, got %d", fake.calls)
	}
	if fake.last != "dr" {
		t.Errorf("suggest prefix = %q, want dr", fake.last)
	}
	if len(m.sugg) != 2 {
		t.Fatalf("pills missing: %d", len(m.sugg))
	}

	// ↓ highlights the first pill and fills the box with its exact title.
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(menuModel)
	if m.sugSel != 0 || m.textInput.Value() != "Drishyam (2015)" {
		t.Fatalf("pill selection wrong: sel=%d val=%q", m.sugSel, m.textInput.Value())
	}

	// Enter commits the selected title into the normal search flow.
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(menuModel)
	if cmd == nil {
		t.Fatal("enter should quit the program")
	}
	if m.action != actionSearch || m.query != "Drishyam (2015)" {
		t.Errorf("commit mismatch: action=%v query=%q", m.action, m.query)
	}
}

func TestMenuViewVisualOutput(t *testing.T) {
	m := newMenuModel()
	m.width = 160
	m.height = 48
	m.frame = 10
	view := m.View()
	t.Logf("\n%s\n", view)
	if view == "" {
		t.Fatal("expected non-empty menu view")
	}
}

func TestMenuTypingDismissesPills(t *testing.T) {
	fake := &fakeSuggester{items: []metadata.Suggestion{{Title: "X", Year: 2000}}}
	m := newMenuModel()
	m.suggester = fake
	m.inputMode = "search"
	m.textInput.SetValue("dr")
	m.sugg = []metadata.Suggestion{{Title: "Drishyam", Year: 2015}}
	m.sugSel = -1

	// A fresh keystroke resets selection; pills refresh via new generation.
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = nm.(menuModel)
	if m.sugSel != -1 {
		t.Errorf("selection should reset on typing, got %d", m.sugSel)
	}
	if len(m.sugg) != 1 {
		t.Error("stale pills may linger until the next result arrives")
	}
}
