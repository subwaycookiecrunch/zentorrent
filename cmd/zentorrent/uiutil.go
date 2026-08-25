package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// truncateCells cuts s to at most max terminal cells, breaking only on rune
// boundaries. Byte slicing (title[:30]) tears multibyte UTF-8 sequences and
// renders U+FFFD garbage for Hindi/CJK titles.
func truncateCells(s string, max int) string {
	if max < 4 {
		max = 4
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	return ansi.Truncate(s, max-1, "…")
}

// orDash renders empty strings as an em dash in table cells.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// iconCell pads an icon glyph to a fixed cell width so menu labels align
// regardless of whether the emoji is single-width (⚙, ✕) or double-width.
func iconCell(icon string, cells int) string {
	w := lipgloss.Width(icon)
	if w > cells {
		w = cells
	}
	return icon + strings.Repeat(" ", cells-w)
}

// wrapCells greedily wraps pre-rendered segments into rows no wider than
// max cells, keeping ANSI styling intact across line breaks.
func wrapCells(segments []string, max int) []string {
	var rows []string
	var cur []string
	curW := 0
	for _, seg := range segments {
		w := lipgloss.Width(seg)
		if curW > 0 && curW+1+w > max {
			rows = append(rows, strings.Join(cur, " "))
			cur, curW = nil, 0
		}
		cur = append(cur, seg)
		curW += w
		if curW > 0 {
			curW++ // separating space
		}
	}
	if len(cur) > 0 {
		rows = append(rows, strings.Join(cur, " "))
	}
	return rows
}
