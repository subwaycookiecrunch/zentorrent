package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type menuAction int

const (
	actionNone menuAction = iota
	actionSearch
	actionStream
	actionDownload
	actionBookmarks
	actionHistory
	actionSources
	actionSettings
	actionQuit
)

type menuItem struct {
	icon   string
	label  string
	desc   string
	action menuAction
}

var menuItems = []menuItem{
	{"🔍", "Search", "Find movies, TV shows, anime", actionSearch},
	{"⚡", "Stream", "Paste magnet/torrent to stream", actionStream},
	{"⬇️ ", "Download", "Download torrent/magnet to disk", actionDownload},
	{"📌", "Bookmarks", "View saved watchlist", actionBookmarks},
	{"📜", "History", "Continue watching recent streams", actionHistory},
	{"⚙ ", "Config", "View & edit current settings", actionSettings},
	{"✕ ", "Quit", "Exit ZenTorrent", actionQuit},
}

var (
	colorPurple    = lipgloss.Color("#7c3aed")
	colorCyan      = lipgloss.Color("#06b6d4")
	colorGreen     = lipgloss.Color("#10b981")
	colorAmber     = lipgloss.Color("#f59e0b")
	colorRed       = lipgloss.Color("#ef4444")
	colorTextPri   = lipgloss.Color("#e4e4e7")
	colorTextSec   = lipgloss.Color("#a1a1aa")
	colorTextDim   = lipgloss.Color("#71717a")
	colorBorder    = lipgloss.Color("#3f3f46")
	colorBorderLit = lipgloss.Color("#52525b")
	colorBg        = lipgloss.Color("#18181b")

	menuTitleStyle = lipgloss.NewStyle().
			Foreground(colorPurple).
			Bold(true)

	menuSubtitleStyle = lipgloss.NewStyle().
				Foreground(colorCyan).
				Bold(true)

	menuVersionStyle = lipgloss.NewStyle().
				Foreground(colorTextDim).
				Italic(true)

	menuItemStyle = lipgloss.NewStyle().
			Foreground(colorTextSec).
			PaddingLeft(4)

	menuSelectedStyle = lipgloss.NewStyle().
				Foreground(colorTextPri).
				Bold(true).
				PaddingLeft(2)

	menuDescStyle = lipgloss.NewStyle().
			Foreground(colorTextDim).
			Italic(true)

	menuSelectedDescStyle = lipgloss.NewStyle().
				Foreground(colorTextSec).
				Italic(true)

	menuCursorStyle = lipgloss.NewStyle().
			Foreground(colorPurple).
			Bold(true)

	menuBoxStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 3)

	inputLabelStyle = lipgloss.NewStyle().
			Foreground(colorCyan).
			Bold(true)

	footerStyle = lipgloss.NewStyle().
			Foreground(colorTextDim)
)

type menuModel struct {
	cursor    int
	action    menuAction
	quitting  bool
	inputMode string // "" = menu, "search", "stream", "download"
	textInput textinput.Model
	query     string
	uri       string
}

func newMenuModel() menuModel {
	ti := textinput.New()
	ti.CharLimit = 500
	ti.Width = 50

	return menuModel{
		cursor:    0,
		textInput: ti,
	}
}

func StartMainMenu() {
	m := newMenuModel()
	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fm := finalModel.(menuModel)

	switch fm.action {
	case actionSearch:
		if fm.query != "" {
			StartSearchTUI(fm.query)
		}
	case actionStream:
		if fm.uri != "" {
			StartStreamTUI(fm.uri)
		}
	case actionDownload:
		if fm.uri != "" {
			StartDownloadTUI(fm.uri)
		}
	case actionBookmarks:
		StartBookmarksTUI()
	case actionHistory:
		StartHistoryTUI()
	case actionSettings:
		StartConfigTUI()
	case actionQuit:
	}
}

func (m menuModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()

		if m.inputMode != "" {
			switch key {
			case "esc":
				m.inputMode = ""
				m.textInput.Blur()
				return m, nil
			case "enter":
				val := strings.TrimSpace(m.textInput.Value())
				if val == "" {
					return m, nil
				}
				if m.inputMode == "search" {
					m.query = val
					m.action = actionSearch
				} else if m.inputMode == "stream" {
					m.uri = val
					m.action = actionStream
				} else if m.inputMode == "download" {
					m.uri = val
					m.action = actionDownload
				}
				m.quitting = true
				return m, tea.Quit
			}
			var cmd tea.Cmd
			m.textInput, cmd = m.textInput.Update(msg)
			return m, cmd
		}

		switch key {
		case "q", "ctrl+c":
			m.action = actionQuit
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(menuItems)-1 {
				m.cursor++
			}
		case "enter":
			item := menuItems[m.cursor]
			switch item.action {
			case actionSearch:
				m.inputMode = "search"
				m.textInput.Placeholder = "Enter movie, show, or anime name..."
				m.textInput.SetValue("")
				m.textInput.Focus()
				return m, textinput.Blink
			case actionStream:
				m.inputMode = "stream"
				m.textInput.Placeholder = "Paste magnet link or /path/to/file.torrent"
				m.textInput.SetValue("")
				m.textInput.Focus()
				return m, textinput.Blink
			case actionDownload:
				m.inputMode = "download"
				m.textInput.Placeholder = "Paste magnet link or /path/to/file.torrent"
				m.textInput.SetValue("")
				m.textInput.Focus()
				return m, textinput.Blink
			case actionBookmarks, actionHistory, actionSettings:
				m.action = item.action
				m.quitting = true
				return m, tea.Quit
			case actionQuit:
				m.action = actionQuit
				m.quitting = true
				return m, tea.Quit
			}
		}
	}

	return m, nil
}

func (m menuModel) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	banner := menuTitleStyle.Render(asciiArt)
	sub := menuSubtitleStyle.Render(subtitle) + "  " + menuVersionStyle.Render("v"+Version)

	b.WriteString(banner)
	b.WriteString("\n")
	b.WriteString(sub)
	b.WriteString("\n")

	if m.inputMode != "" {
		var label string
		if m.inputMode == "search" {
			label = "  " + inputLabelStyle.Render("🔍 Search:") + " "
		} else if m.inputMode == "stream" {
			label = "  " + inputLabelStyle.Render("⚡ Stream:") + " "
		} else {
			label = "  " + inputLabelStyle.Render("⬇️  Download:") + " "
		}
		b.WriteString(label)
		b.WriteString(m.textInput.View())
		b.WriteString("\n\n")
		b.WriteString(footerStyle.Render("  enter to confirm • esc to go back"))
		b.WriteString("\n")

		return menuBoxStyle.Render(b.String())
	}

	for i, item := range menuItems {
		if i == m.cursor {
			cursor := menuCursorStyle.Render("▸")
			name := menuSelectedStyle.Render(item.icon + "  " + item.label)
			desc := menuSelectedDescStyle.Render(" — " + item.desc)
			b.WriteString(fmt.Sprintf("  %s %s%s\n", cursor, name, desc))
		} else {
			name := menuItemStyle.Render(item.icon + "  " + item.label)
			desc := menuDescStyle.Render(" — " + item.desc)
			b.WriteString(fmt.Sprintf("    %s%s\n", name, desc))
		}
	}

	b.WriteString("\n")
	b.WriteString(footerStyle.Render("  ↑/↓ navigate • enter select • q quit"))

	return menuBoxStyle.Render(b.String())
}
