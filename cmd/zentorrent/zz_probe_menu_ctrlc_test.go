package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Probe: ctrl+c while the main-menu input box is focused must quit cleanly
// (raw mode means ^C never raises SIGINT; the handler defers the hard exit
// to StartMainMenu's normal shutdown path).
func TestMenuInputModeCtrlCQuitsCleanly(t *testing.T) {
	for _, mode := range []string{"search", "stream", "download"} {
		m := newMenuModel()
		m.inputMode = mode
		m.textInput.SetValue("some query")

		nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		m2 := nm.(menuModel)

		if !m2.quitting || m2.action != actionQuit {
			t.Errorf("%s: ctrl+c should arm quitting+actionQuit, got quitting=%v action=%v", mode, m2.quitting, m2.action)
		}
		if m2.textInput.Value() != "some query" {
			t.Errorf("%s: ctrl+c mutated value to %q", mode, m2.textInput.Value())
		}
		if cmd == nil {
			t.Fatalf("%s: ctrl+c returned no cmd", mode)
		}
		quitSeen := false
		for _, msg := range execCmd(t, cmd) {
			if _, ok := msg.(tea.QuitMsg); ok {
				quitSeen = true
			}
		}
		if !quitSeen {
			t.Errorf("%s: ctrl+c produced no QuitMsg", mode)
		}
	}

	// Control: same model NOT in input mode must quit on ctrl+c.
	m3 := newMenuModel()
	nm4, _ := m3.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m4 := nm4.(menuModel)
	if !m4.quitting || m4.action != actionQuit {
		t.Errorf("control: menu-mode ctrl+c should quit, got quitting=%v action=%v", m4.quitting, m4.action)
	}

	// Control 2: esc exits the box without quitting (documented escape hatch).
	m5 := newMenuModel()
	m5.inputMode = "search"
	nm6, _ := m5.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m6 := nm6.(menuModel)
	if m6.inputMode != "" {
		t.Errorf("esc did not exit input mode")
	}
}

func execCmd(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	var out []tea.Msg
	for cmd != nil {
		msg := cmd()
		out = append(out, msg)
		if b, ok := msg.(tea.BatchMsg); ok {
			cmd = nil
			for _, c := range b {
				out = append(out, execCmd(t, c)...)
			}
			break
		}
		return out
	}
	return out
}
