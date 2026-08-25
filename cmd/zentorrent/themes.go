package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type Theme struct {
	Name      string
	Accent    lipgloss.Color
	Secondary lipgloss.Color
	Success   lipgloss.Color
	Warning   lipgloss.Color
	Error     lipgloss.Color
	TextPri   lipgloss.Color
	TextSec   lipgloss.Color
	TextDim   lipgloss.Color
	Border    lipgloss.Color
	BorderLit lipgloss.Color
	Bg        lipgloss.Color
}

var themes = map[string]Theme{
	"purple": {
		Name:      "purple",
		Accent:    "#7c3aed",
		Secondary: "#06b6d4",
		Success:   "#10b981",
		Warning:   "#f59e0b",
		Error:     "#ef4444",
		TextPri:   "#e4e4e7",
		TextSec:   "#a1a1aa",
		TextDim:   "#82828b",
		Border:    "#3f3f46",
		BorderLit: "#52525b",
		Bg:        "#18181b",
	},
	"tokyo": {
		Name:      "tokyo",
		Accent:    "#7aa2f7",
		Secondary: "#bb9af7",
		Success:   "#9ece6a",
		Warning:   "#e0af68",
		Error:     "#f7768e",
		TextPri:   "#c0caf5",
		TextSec:   "#a9b1d6",
		TextDim:   "#7b83ad",
		Border:    "#3b4261",
		BorderLit: "#414868",
		Bg:        "#1a1b26",
	},
	"catppuccin": {
		Name:      "catppuccin",
		Accent:    "#cba6f7",
		Secondary: "#89b4fa",
		Success:   "#a6e3a1",
		Warning:   "#f9e2af",
		Error:     "#f38ba8",
		TextPri:   "#cdd6f4",
		TextSec:   "#bac2de",
		TextDim:   "#838ba7",
		Border:    "#45475a",
		BorderLit: "#585b70",
		Bg:        "#1e1e2e",
	},
	"dracula": {
		Name:      "dracula",
		Accent:    "#bd93f9",
		Secondary: "#8be9fd",
		Success:   "#50fa7b",
		Warning:   "#f1fa8c",
		Error:     "#ff5555",
		TextPri:   "#f8f8f2",
		TextSec:   "#d0d0d0",
		TextDim:   "#8592b9",
		Border:    "#44475a",
		BorderLit: "#6272a4",
		Bg:        "#282a36",
	},
	"gruvbox": {
		Name:      "gruvbox",
		Accent:    "#fe8019",
		Secondary: "#83a598",
		Success:   "#b8bb26",
		Warning:   "#fabd2f",
		Error:     "#fb4934",
		TextPri:   "#ebdbb2",
		TextSec:   "#d5c4a1",
		TextDim:   "#9d8e7f",
		Border:    "#504945",
		BorderLit: "#665c54",
		Bg:        "#282828",
	},
	"nord": {
		Name:      "nord",
		Accent:    "#88c0d0",
		Secondary: "#81a1c1",
		Success:   "#a3be8c",
		Warning:   "#ebcb8b",
		Error:     "#bf616a",
		TextPri:   "#eceff4",
		TextSec:   "#d8dee9",
		TextDim:   "#939db6",
		Border:    "#3b4252",
		BorderLit: "#434c5e",
		Bg:        "#2e3440",
	},
	"rose": {
		Name:      "rose",
		Accent:    "#f43f5e",
		Secondary: "#fb7185",
		Success:   "#4ade80",
		Warning:   "#fbbf24",
		Error:     "#ef4444",
		TextPri:   "#fce7f3",
		TextSec:   "#f9a8d4",
		TextDim:   "#cf87ad",
		Border:    "#4a1942",
		BorderLit: "#831843",
		Bg:        "#1a0a14",
	},
	"midnight": {
		Name:      "midnight",
		Accent:    "#60a5fa",
		Secondary: "#38bdf8",
		Success:   "#34d399",
		Warning:   "#fbbf24",
		Error:     "#f87171",
		TextPri:   "#e2e8f0",
		TextSec:   "#94a3b8",
		TextDim:   "#7e8ca6",
		Border:    "#1e293b",
		BorderLit: "#334155",
		Bg:        "#0f172a",
	},
}

var themeNames = []string{"purple", "tokyo", "catppuccin", "dracula", "gruvbox", "nord", "rose", "midnight"}

func ApplyTheme(name string) {
	t, ok := themes[name]
	if !ok {
		t = themes["purple"]
	}

	colorPurple = t.Accent
	colorCyan = t.Secondary
	colorGreen = t.Success
	colorAmber = t.Warning
	colorRed = t.Error
	colorTextPri = t.TextPri
	colorTextSec = t.TextSec
	colorTextDim = t.TextDim
	colorBorder = t.Border
	colorBorderLit = t.BorderLit
	colorBg = t.Bg

	menuTitleStyle = lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	menuSubtitleStyle = lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	menuVersionStyle = lipgloss.NewStyle().Foreground(colorTextDim).Italic(true)
	menuItemStyle = lipgloss.NewStyle().Foreground(colorTextSec).PaddingLeft(4)
	menuSelectedStyle = lipgloss.NewStyle().Foreground(colorTextPri).Bold(true).PaddingLeft(2)
	menuDescStyle = lipgloss.NewStyle().Foreground(colorTextDim).Italic(true)
	menuSelectedDescStyle = lipgloss.NewStyle().Foreground(colorTextSec).Italic(true)
	menuCursorStyle = lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	menuBoxStyle = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(colorBorder).Padding(0, 3)
	inputLabelStyle = lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	footerStyle = lipgloss.NewStyle().Foreground(colorTextDim)
	pillIdleStyle = lipgloss.NewStyle().Foreground(colorCyan)

	// Highlighted table rows sit on the accent color; white text vanishes on
	// light accents (catppuccin/gruvbox), so pick per-theme.
	selectedRowFg = readableOn(colorPurple)
}

// readableOn returns black or white — whichever keeps text legible when
// rendered on top of the given background color (WCAG luminance heuristic).
func readableOn(bg lipgloss.Color) lipgloss.Color {
	r, g, b := parseHexColor(bg)
	lum := (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 255
	if lum > 0.55 {
		return lipgloss.Color("#0b0b10")
	}
	return lipgloss.Color("#ffffff")
}

func parseHexColor(c lipgloss.Color) (r, g, b int) {
	s := string(c)
	s = strings.TrimPrefix(s, "#")
	if len(s) == 6 {
		fmt.Sscanf(s, "%02x%02x%02x", &r, &g, &b)
	}
	return
}
