package main

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// wcagContrast returns the WCAG 2.x contrast ratio between two hex colors.
func wcagContrast(fg, bg string) float64 {
	const minRGB = 0.04045 / 12.92
	rel := func(hex string) float64 {
		var r, g, b int
		fmt.Sscanf(strings.TrimPrefix(hex, "#"), "%02x%02x%02x", &r, &g, &b)
		lin := func(v int) float64 {
			c := float64(v) / 255
			if c <= minRGB {
				return c / 12.92
			}
			return math.Pow((c+0.055)/1.055, 2.4)
		}
		return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
	}
	l1, l2 := rel(fg), rel(bg)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// TestThemeTextDimContrast guards against dim text fading into the theme
// background: footers and descriptions must stay readable (WCAG AA for the
// small sizes this TUI uses).
func TestThemeTextDimContrast(t *testing.T) {
	for _, name := range themeNames {
		th := themes[name]
		ratio := wcagContrast(string(th.TextDim), string(th.Bg))
		if ratio < 4.5 {
			t.Errorf("theme %q TextDim %s on Bg %s = %.2f:1 (want >= 4.5)", name, th.TextDim, th.Bg, ratio)
		}
	}
}

// TestReadableOnPicksLegibleForeground checks the selected-row helper flips
// to dark text on light accents.
func TestReadableOnPicksLegibleForeground(t *testing.T) {
	if got := readableOn(lipgloss.Color("#cba6f7")); got != "#0b0b10" { // catppuccin accent is light
		t.Fatalf("readableOn(light accent) = %s, want dark", got)
	}
	if got := readableOn(lipgloss.Color("#7c3aed")); got != "#ffffff" { // purple accent is dark
		t.Fatalf("readableOn(dark accent) = %s, want white", got)
	}
}
