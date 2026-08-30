package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/subwaycookiecrunch/zentorrent/internal/metadata"
)

// suggestionSource abstracts the type-ahead backend so tests can inject a
// stub; production wires the v4 metadata catalog.
type suggestionSource interface {
	Suggest(ctx context.Context, prefix string, limit int) ([]metadata.Suggestion, error)
}

const suggestDebounce = 25 * time.Millisecond

var defaultTrending = []metadata.Suggestion{
	{Title: "Stranger Things", Year: 2016, MediaType: "tv", VoteAverage: 8.6, Genres: "Sci-Fi, Drama"},
	{Title: "Breaking Bad", Year: 2008, MediaType: "tv", VoteAverage: 9.5, Genres: "Crime, Drama"},
	{Title: "Interstellar", Year: 2014, MediaType: "movie", VoteAverage: 8.7, Genres: "Sci-Fi, Adventure"},
	{Title: "Inception", Year: 2010, MediaType: "movie", VoteAverage: 8.8, Genres: "Action, Sci-Fi"},
	{Title: "One Piece", Year: 1999, MediaType: "anime", VoteAverage: 9.0, Genres: "Animation, Action"},
	{Title: "Stree 2", Year: 2024, MediaType: "movie", VoteAverage: 7.6, Genres: "Comedy, Horror"},
}

type (
	menuSuggestValue struct {
		seq   int
		value string
	}
	menuSuggestResult struct {
		seq   int
		value string
		items []metadata.Suggestion
	}
	animTickMsg time.Time
)

func animTickCmd() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return animTickMsg(t)
	})
}

type menuAction int

const (
	actionNone menuAction = iota
	actionSearch
	actionStream
	actionDownload
	actionZenPlayer
	actionWeb
	actionBookmarks
	actionHistory
	actionSources
	actionSettings
	actionParty
	actionQuit
)

type menuItem struct {
	icon      string
	label     string
	desc      string
	action    menuAction
	iconColor lipgloss.TerminalColor
	textColor lipgloss.TerminalColor
	descColor lipgloss.TerminalColor
}

var menuItems = []menuItem{
	{icon: "🔍", label: "Search", desc: "Find movies, TV shows, anime", action: actionSearch},
	{icon: "⚡", label: "Stream", desc: "Paste magnet/torrent to stream", action: actionStream},
	{icon: "⬇️", label: "Download", desc: "Download torrent/magnet to disk", action: actionDownload},
	{icon: "📻", label: "ZenPlayer", desc: "Retro cassette music player & YouTube streaming", action: actionZenPlayer},
	{icon: "📌", label: "Bookmarks", desc: "View saved watchlist", action: actionBookmarks},
	{icon: "📜", label: "History", desc: "Continue watching recent streams", action: actionHistory},
	{icon: "🍿", label: "ZenParty", desc: "Host or join a watch-together session", action: actionParty},
	{icon: "🌐", label: "Watch Online", desc: "Launch private cinema tunnel & browser", action: actionWeb},
	{icon: "⚙", label: "Config", desc: "View & edit current settings", action: actionSettings},
	{icon: "✕", label: "Quit", desc: "Exit ZenTorrent", action: actionQuit},
}

var (
	inputLabelStyle = lipgloss.NewStyle().
			Foreground(colorOrange).
			Bold(true)
	selectedRowFg = lipgloss.Color("#ffffff")
)

type menuModel struct {
	cursor    int
	action    menuAction
	quitting  bool
	inputMode string
	textInput textinput.Model
	query     string
	uri       string
	width     int
	height    int
	frame     int // Animation frame tick counter

	// Type-ahead state
	suggester suggestionSource
	sugg      []metadata.Suggestion
	sugSel    int
	sugSeq    int
	typed     string
	suppress  bool
}

func newMenuModel() menuModel {
	ti := textinput.New()
	ti.CharLimit = 500
	ti.Width = 50

	var sugg suggestionSource
	if d := Discovery(); d != nil {
		sugg = d
	} else {
		sugg = CatalogHandle()
	}

	return menuModel{
		cursor:    0,
		textInput: ti,
		sugSel:    -1,
		suggester: sugg,
		frame:     0,
	}
}

func pillLabel(s metadata.Suggestion) string {
	if s.Year > 0 {
		return fmt.Sprintf("%s (%d)", s.Title, s.Year)
	}
	return s.Title
}

func (m *menuModel) suggestMatch(seq int, value string) bool {
	return m.suggester != nil && seq == m.sugSeq &&
		m.inputMode == "search" && m.textInput.Value() == value
}

func (m *menuModel) fillFromPill() {
	if m.sugSel < 0 || m.sugSel >= len(m.sugg) {
		return
	}
	m.suppress = true
	m.textInput.SetValue(pillLabel(m.sugg[m.sugSel]))
	m.suppress = false
	m.textInput.SetCursor(len([]rune(m.textInput.Value())))
}

func (m *menuModel) restoreTyped() {
	m.suppress = true
	m.textInput.SetValue(m.typed)
	m.suppress = false
	m.textInput.SetCursor(len([]rune(m.textInput.Value())))
}

func (m *menuModel) menuKickSuggest() tea.Cmd {
	if m.suggester == nil || m.inputMode != "search" {
		return nil
	}
	m.sugSeq++
	seq, value := m.sugSeq, m.textInput.Value()
	return tea.Tick(suggestDebounce, func(time.Time) tea.Msg {
		return menuSuggestValue{seq: seq, value: value}
	})
}

func (m *menuModel) inputWidth() int {
	max := 50
	if m.width > 0 && m.width-20 < max {
		max = m.width - 20
	}
	if max < 20 {
		max = 20
	}
	return max
}

func StartMainMenu() {
	for {
		m := newMenuModel()
		p := tea.NewProgram(m, tea.WithAltScreen())
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
				StartStreamTUI(fm.uri, nil, nil)
			}
		case actionDownload:
			if fm.uri != "" {
				StartDownloadTUI(fm.uri, 0)
			}
		case actionZenPlayer:
			if err := LaunchZenPlayer(""); err != nil {
				fmt.Printf("ZenPlayer error: %v\n", err)
				time.Sleep(2 * time.Second)
			}
		case actionBookmarks:
			StartBookmarksTUI()
		case actionHistory:
			StartHistoryTUI()
		case actionParty:
			StartPartyTUI("")
		case actionSettings:
			StartConfigTUI()
		case actionWeb:
			StartWatchOnlineSession()
		default:
			return
		}
	}
}

func (m menuModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, animTickCmd())
}

func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case animTickMsg:
		m.frame++
		return m, animTickCmd()

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textInput.Width = m.inputWidth()
		return m, nil

	case menuSuggestValue:
		if msg.value == "" || !m.suggestMatch(msg.seq, msg.value) {
			return m, nil
		}
		sug := m.suggester
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			items, err := sug.Suggest(ctx, msg.value, 5)
			if err != nil {
				items = nil
			}
			return menuSuggestResult{seq: msg.seq, value: msg.value, items: items}
		}

	case menuSuggestResult:
		if !m.suggestMatch(msg.seq, msg.value) {
			return m, nil
		}
		m.sugg = msg.items
		return m, nil

	case tea.KeyMsg:
		key := msg.String()

		if m.inputMode != "" {
			switch key {
			case "ctrl+c":
				m.action = actionQuit
				m.quitting = true
				return m, tea.Quit
			case "esc":
				if m.inputMode == "search" && (len(m.sugg) > 0 || m.sugSel >= 0) {
					m.sugg = nil
					if m.sugSel >= 0 {
						m.sugSel = -1
						m.restoreTyped()
					}
					return m, nil
				}
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
			case "tab":
				if m.inputMode == "search" && len(m.sugg) > 0 {
					if m.sugSel == -1 {
						m.typed = m.textInput.Value()
						m.sugSel = 0
					} else {
						m.sugSel = (m.sugSel + 1) % len(m.sugg)
					}
					m.fillFromPill()
					return m, nil
				}
			}
			if m.inputMode == "search" && len(m.sugg) > 0 && (key == "down" || key == "up") {
				if key == "down" {
					if m.sugSel == -1 {
						m.typed = m.textInput.Value()
					}
					if m.sugSel < len(m.sugg)-1 {
						m.sugSel++
					}
					m.fillFromPill()
				} else {
					m.sugSel--
					if m.sugSel < 0 {
						m.sugSel = -1
						m.restoreTyped()
					} else {
						m.fillFromPill()
					}
				}
				return m, nil
			}

			prev := m.textInput.Value()
			var cmd tea.Cmd
			m.textInput, cmd = m.textInput.Update(msg)
			if m.inputMode == "search" && m.textInput.Value() != prev && !m.suppress {
				m.sugSel = -1
				m.typed = m.textInput.Value()
				if strings.TrimSpace(m.textInput.Value()) == "" {
					m.sugg = defaultTrending
				} else if kick := m.menuKickSuggest(); kick != nil {
					cmd = tea.Batch(cmd, kick)
				}
			}
			return m, cmd
		}

		// Quick hotkey numbers [1-9]
		switch key {
		case "1":
			m.cursor = 0
			return m.handleEnterSelection()
		case "2":
			m.cursor = 1
			return m.handleEnterSelection()
		case "3":
			m.cursor = 2
			return m.handleEnterSelection()
		case "4":
			m.cursor = 3
			return m.handleEnterSelection()
		case "5":
			m.cursor = 4
			return m.handleEnterSelection()
		case "6":
			m.cursor = 5
			return m.handleEnterSelection()
		case "7":
			m.cursor = 6
			return m.handleEnterSelection()
		case "8":
			m.cursor = 7
			return m.handleEnterSelection()
		case "9":
			m.cursor = 8
			return m.handleEnterSelection()
		case "/":
			m.cursor = 0
			return m.handleEnterSelection()
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
			return m.handleEnterSelection()
		}
	}

	return m, nil
}

func (m menuModel) handleEnterSelection() (tea.Model, tea.Cmd) {
	item := menuItems[m.cursor]
	switch item.action {
	case actionSearch:
		m.inputMode = "search"
		m.textInput.Placeholder = "Enter movie, show, or anime name..."
		m.textInput.SetValue("")
		m.textInput.Width = m.inputWidth()
		m.textInput.Focus()
		m.sugg = defaultTrending
		m.sugSel = -1
		return m, textinput.Blink
	case actionStream:
		m.inputMode = "stream"
		m.textInput.Placeholder = "Paste magnet link or /path/to/file.torrent"
		m.textInput.SetValue("")
		m.textInput.Width = m.inputWidth()
		m.textInput.Focus()
		return m, textinput.Blink
	case actionDownload:
		m.inputMode = "download"
		m.textInput.Placeholder = "Paste magnet link or /path/to/file.torrent"
		m.textInput.SetValue("")
		m.textInput.Width = m.inputWidth()
		m.textInput.Focus()
		return m, textinput.Blink
	case actionZenPlayer, actionWeb, actionBookmarks, actionHistory, actionSettings, actionParty:
		m.action = item.action
		m.quitting = true
		return m, tea.Quit
	case actionQuit:
		m.action = actionQuit
		m.quitting = true
		return m, tea.Quit
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
	b.WriteString("\n\n")

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
		b.WriteString("\n")

		if m.inputMode == "search" && len(m.sugg) > 0 {
			headerTitle := "POPULAR MATCHES:"
			if strings.TrimSpace(m.textInput.Value()) == "" {
				headerTitle = "TRENDING RECOMMENDATIONS:"
			}
			b.WriteString("\n  " + lipgloss.NewStyle().Foreground(colorOrange).Bold(true).Render(headerTitle) + "\n")
			for i, s := range m.sugg {
				icon := "🎬"
				if s.MediaType == "tv" {
					icon = "📺"
				} else if s.MediaType == "anime" {
					icon = "🌸"
				}

				titleStr := pillLabel(s)
				metaStr := ""
				if s.VoteAverage > 0 {
					metaStr += fmt.Sprintf(" • ⭐ %.1f", s.VoteAverage)
				}
				if s.Genres != "" {
					g := s.Genres
					if len(g) > 22 {
						g = g[:20] + ".."
					}
					metaStr += " • " + g
				}

				rowText := fmt.Sprintf("%s %s%s", icon, titleStr, metaStr)
				maxW := 62
				if m.width > 0 && m.width-12 < maxW {
					maxW = m.width - 12
				}
				if maxW < 30 {
					maxW = 30
				}
				rowText = truncateCells(rowText, maxW)

				if i == m.sugSel {
					b.WriteString("  " + lipgloss.NewStyle().
						Foreground(selectedRowFg).
						Background(colorOrange).
						Bold(true).
						Padding(0, 1).
						Render("▸ "+rowText) + "\n")
				} else {
					b.WriteString("    " + lipgloss.NewStyle().
						Foreground(lipgloss.Color("#d4d4d8")).
						Render(rowText) + "\n")
				}
			}
			b.WriteString("\n" + footerStyle.Render("  ↑/↓ navigate • tab fill • enter search & stream • esc back"))
			b.WriteString("\n")
			return menuBoxStyle.Render(b.String())
		}

		b.WriteString("\n")
		b.WriteString(footerStyle.Render("  enter to confirm • esc to go back"))
		b.WriteString("\n")

		return menuBoxStyle.Render(b.String())
	}

	for i, item := range menuItems {
		icon := iconCell(item.icon, 2)
		if i == m.cursor {
			cursor := menuCursorStyle.Render("▸")
			name := menuSelectedStyle.Render(icon + "  " + item.label)
			desc := menuSelectedDescStyle.Render(" — " + item.desc)
			b.WriteString(fmt.Sprintf("  %s %s%s\n", cursor, name, desc))
		} else {
			name := menuItemStyle.Render(icon + "  " + item.label)
			desc := menuDescStyle.Render(" — " + item.desc)
			b.WriteString(fmt.Sprintf("    %s%s\n", name, desc))
		}
	}

	b.WriteString("\n")
	b.WriteString(footerStyle.Render("↑/↓ navigate  •  enter select  •  q quit"))

	return menuBoxStyle.Render(b.String())
}

