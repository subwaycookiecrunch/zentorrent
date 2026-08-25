package main

import (
	"fmt"
	"strings"

	"github.com/subwaycookiecrunch/zentorrent/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type configModel struct {
	cursor    int
	quitting  bool
	forceExit bool // ctrl+c: exit the app after the terminal is restored
	saved     bool
}

var configFields = []string{
	"Theme",
	"Player",
	"Notifications",
	"Subtitle Lang",
	"Auto Subtitles",
	"Max Peers",
}

var players = []string{"auto", "vlc", "mpv", "iina"}
var langs = []string{"en", "es", "fr", "de", "it"}

func StartConfigTUI() {
	p := tea.NewProgram(configModel{})
	final, err := p.Run()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	if cm, ok := final.(configModel); ok && cm.forceExit {
		ExitApp(0)
	}
}

func (m configModel) Init() tea.Cmd {
	return nil
}

func (m configModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		m.saved = false
		switch msg.String() {
		case "q", "esc":
			m.quitting = true
			return m, tea.Quit
		case "ctrl+c":
			m.forceExit = true
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(configFields)-1 {
				m.cursor++
			}
		case "left", "h":
			m.toggleValue(-1)
			config.Save(appConfig)
			m.saved = true
		case "right", "l", "enter", " ":
			m.toggleValue(1)
			config.Save(appConfig)
			m.saved = true
		}
	}
	return m, nil
}

func (m *configModel) toggleValue(dir int) {
	field := configFields[m.cursor]
	switch field {
	case "Theme":
		idx := 0
		for i, t := range themeNames {
			if t == appConfig.Theme {
				idx = i
			}
		}
		idx = (idx + dir + len(themeNames)) % len(themeNames)
		appConfig.Theme = themeNames[idx]
		ApplyTheme(appConfig.Theme)
	case "Player":
		idx := 0
		for i, p := range players {
			if p == appConfig.Player {
				idx = i
			}
		}
		idx = (idx + dir + len(players)) % len(players)
		appConfig.Player = players[idx]
	case "Notifications":
		appConfig.Notifications = !appConfig.Notifications
	case "Subtitle Lang":
		idx := 0
		for i, l := range langs {
			if l == appConfig.Subtitles.Language {
				idx = i
			}
		}
		idx = (idx + dir + len(langs)) % len(langs)
		appConfig.Subtitles.Language = langs[idx]
	case "Auto Subtitles":
		appConfig.Subtitles.AutoFetch = !appConfig.Subtitles.AutoFetch
	case "Max Peers":
		if dir > 0 {
			appConfig.MaxPeers += 10
		} else {
			appConfig.MaxPeers -= 10
		}
		if appConfig.MaxPeers < 10 {
			appConfig.MaxPeers = 10
		}
		if appConfig.MaxPeers > 200 {
			appConfig.MaxPeers = 200
		}
	}
}

func (m configModel) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	headerStyle := lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	b.WriteString(fmt.Sprintf("  %s\n\n", headerStyle.Render("⚙  Configuration")))

	labelStyle := lipgloss.NewStyle().Foreground(colorTextSec).Width(20)
	valStyle := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	selLabelStyle := lipgloss.NewStyle().Foreground(colorTextPri).Bold(true).Width(20)
	selValStyle := lipgloss.NewStyle().Foreground(colorPurple).Bold(true)

	for i, field := range configFields {
		val := ""
		switch field {
		case "Theme":
			val = appConfig.Theme
		case "Player":
			val = appConfig.Player
		case "Notifications":
			if appConfig.Notifications {
				val = "ON"
			} else {
				val = "OFF"
			}
		case "Subtitle Lang":
			val = appConfig.Subtitles.Language
		case "Auto Subtitles":
			if appConfig.Subtitles.AutoFetch {
				val = "ON"
			} else {
				val = "OFF"
			}
		case "Max Peers":
			val = fmt.Sprintf("%d", appConfig.MaxPeers)
		}

		cursor := "  "
		lStyle := labelStyle
		vStyle := valStyle

		if i == m.cursor {
			cursor = menuCursorStyle.Render("▸ ")
			lStyle = selLabelStyle
			vStyle = selValStyle
			val = "‹ " + val + " ›"
		} else {
			val = "  " + val + "  "
		}

		b.WriteString(fmt.Sprintf(" %s%s %s\n", cursor, lStyle.Render(field), vStyle.Render(val)))
	}

	b.WriteString("\n")

	if m.saved {
		b.WriteString(lipgloss.NewStyle().Foreground(colorGreen).Render("  ✓ Saved automatically"))
	} else {
		b.WriteString("                       ")
	}

	b.WriteString("\n\n")
	b.WriteString(footerStyle.Render("  ↑/↓ navigate • ←/→ change value • q back"))
	b.WriteString("\n")

	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderLit).
		Padding(0, 1)

	return box.Render(b.String())
}
