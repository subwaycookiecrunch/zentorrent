package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

func TestTruncateCellsNeverSplitsRunes(t *testing.T) {
	// Byte slicing used to tear multibyte sequences; output must stay valid
	// UTF-8 for Devanagari / CJK / Korean titles.
	for _, title := range []string{
		"फिल्म हिंदी में HD डाउनलोड करें 1080p",
		"어벤져스 엔드게임 블루레이 고화질 다운로드 1080p",
		"海賊王ワンピース 全話 高画質",
		"Dhurandhar.2025.1080p.WEBRip.x264.AAC5.1-HONESTLY",
		strings.Repeat("é", 40), // two-byte runes
	} {
		got := truncateCells(title, 20)
		if !utf8.ValidString(got) {
			t.Fatalf("truncateCells produced invalid UTF-8 for %q: %q", title, got)
		}
		if lipgloss.Width(got) > 20 {
			t.Fatalf("truncateCells exceeded width budget: %q is %d cells", got, lipgloss.Width(got))
		}
	}
}

func TestTruncateCellsShortInputUntouched(t *testing.T) {
	const s = "short title"
	if got := truncateCells(s, 33); got != s {
		t.Fatalf("got %q, want %q", got, s)
	}
}

func TestWrapCellsRespectsWidth(t *testing.T) {
	pills := make([]string, 12)
	for i := range pills {
		pills[i] = "[Batman: The Animated Series 1992]" // 35 cells
	}
	rows := wrapCells(pills, 74)
	if len(rows) < 2 {
		t.Fatalf("expected wrapping into multiple rows, got %d", len(rows))
	}
	for i, row := range rows {
		if w := lipgloss.Width(row); w > 74 {
			t.Fatalf("row %d is %d cells wide, budget 74", i, w)
		}
	}
}

func TestIconCellAlignsMixedWidthIcons(t *testing.T) {
	single := iconCell("⚙", 2)  // single-width glyph
	double := iconCell("🔍", 2) // double-width emoji
	if lipgloss.Width(single) != lipgloss.Width(double) {
		t.Fatalf("icon cells misaligned: %q (%d) vs %q (%d)",
			single, lipgloss.Width(single), double, lipgloss.Width(double))
	}
}

func TestOrDash(t *testing.T) {
	if orDash("") != "—" {
		t.Fatal("empty string should render as em dash")
	}
	if orDash("720p") != "720p" {
		t.Fatal("non-empty string should pass through")
	}
}

func TestHealthBadgeUnknownSeeders(t *testing.T) {
	if got := healthBadge(seedersUnknown); got != "⚪ N/A" {
		t.Fatalf("unknown seeders badge = %q, want ⚪ N/A", got)
	}
}
