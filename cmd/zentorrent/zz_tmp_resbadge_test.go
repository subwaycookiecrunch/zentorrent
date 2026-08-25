package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// Temporary verification for review finding: empty Resolution renders a
// literal dangling '[]' badge (ui.go:166-171) while waiting for metadata.
func TestTmpEmptyResolutionBadge(t *testing.T) {
	res := "" // StreamState zero value, stream.go:40

	titleStyle := lipgloss.NewStyle().Foreground(colorTextPri).Bold(true)
	resBadge := lipgloss.NewStyle().
		Foreground(colorCyan).
		Bold(true).
		Render(fmt.Sprintf("[%s]", res))

	line := fmt.Sprintf("  %s  %s\n", titleStyle.Render("Waiting for metadata..."), resBadge)
	stripped := stripANSI(line)
	t.Logf("visible line: %q", stripped)
	if strings.Contains(stripped, "[]") {
		t.Logf("CONFIRMED: dangling '[]' badge rendered next to title")
	} else {
		t.Errorf("no dangling badge found")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
