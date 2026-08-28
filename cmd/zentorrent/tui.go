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
)

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
	icon   string
	label  string
	desc   string
	action menuAction
}

var menuItems = []menuItem{
	{"🔍", "Search", "Find movies, TV shows, anime", actionSearch},
	{"⚡", "Stream", "Paste magnet/torrent to stream", actionStream},
	{"⬇️", "Download", "Download torrent/magnet to disk", actionDownload},
	{"📻", "ZenPlayer", "Retro cassette music player & YouTube streaming", actionZenPlayer},
	{"📌", "Bookmarks", "View saved watchlist", actionBookmarks},
	{"📜", "History", "Continue watching recent streams", actionHistory},
	{"🍿", "ZenParty", "Host or join a watch-together session", actionParty},
	{"🌐", "Watch Online", "Launch private cinema tunnel & browser", actionWeb},
	{"⚙", "Config", "View & edit current settings", actionSettings},
	{"✕", "Quit", "Exit ZenTorrent", actionQuit},
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

	pillIdleStyle = lipgloss.NewStyle().
			Foreground(colorCyan)

	// Foreground for rows rendered on top of the accent color; recomputed by
	// ApplyTheme since light accents need dark text.
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
	width     int // terminal width; 0 until first WindowSizeMsg

	// Netflix-style type-ahead state (search input only).
	suggester suggestionSource
	sugg      []metadata.Suggestion
	sugSel    int    // -1 = free text
	sugSeq    int    // debounce generation counter
	typed     string // what the user typed before pill navigation
	suppress  bool   // true while we programmatically fill the input
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
	}
}

// pillLabel renders one suggestion the way Netflix would.
func pillLabel(s metadata.Suggestion) string {
	if s.Year > 0 {
		return fmt.Sprintf("%s (%d)", s.Title, s.Year)
	}
	return s.Title
}

// menuKickSuggest debounces a catalog lookup for the current input text.
// suggestMatch drops stale suggestion traffic (older debounce generation or
// an input that has since changed).
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
	// Single debounced lookup: the tick carries the snapshot of the input so
	// stale generations are dropped by suggestMatch.
	return tea.Tick(suggestDebounce, func(time.Time) tea.Msg {
		return menuSuggestValue{seq: seq, value: value}
	})
}

// inputWidth keeps the text input inside the box on narrow terminals:
// label (~13 cells) + box chrome (6) must fit alongside it.
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
	return textinput.Blink
}

func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
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
				// Raw mode means ^C never raises SIGINT; without this the
				// only way out of a stuck input is killing the terminal.
				m.action = actionQuit
				m.quitting = true
				return m, tea.Quit
			case "esc":
				if m.inputMode == "search" && (len(m.sugg) > 0 || m.sugSel >= 0) {
					// first esc dismisses pills; second leaves the box
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
			// Netflix-style pill navigation while suggestions are up.
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
						m.sugSel = -1 // stay at free text, don't run past it
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
		b.WriteString("\n")

		if m.inputMode == "search" && len(m.sugg) > 0 {
			headerTitle := "POPULAR MATCHES:"
			if strings.TrimSpace(m.textInput.Value()) == "" {
				headerTitle = "TRENDING RECOMMENDATIONS:"
			}
			b.WriteString("\n  " + lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render(headerTitle) + "\n")
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
						Background(colorPurple).
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
	b.WriteString(footerStyle.Render("  ↑/↓ navigate • enter select • q quit"))

	return menuBoxStyle.Render(b.String())
}
