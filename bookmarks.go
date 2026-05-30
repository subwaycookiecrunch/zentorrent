package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// BookmarkEntry represents a saved torrent
type BookmarkEntry struct {
	Title      string    `json:"title"`
	Magnet     string    `json:"magnet"`
	Resolution string    `json:"resolution"`
	Source     string    `json:"source"`
	Seeders    int       `json:"seeders"`
	AddedAt    time.Time `json:"added_at"`
}

// AddBookmark saves a torrent to bookmarks, deduplicating by magnet hash
func AddBookmark(entry BookmarkEntry) {
	entries := loadBookmarks()
	entry.AddedAt = time.Now()

	newHash := extractHistBTIH(entry.Magnet)
	if newHash != "" {
		for _, e := range entries {
			if extractHistBTIH(e.Magnet) == newHash {
				return // Already bookmarked
			}
		}
	}

	entries = append(entries, entry)
	saveBookmarks(entries)
	Notify("ZenTorrent", "Added to Bookmarks: "+entry.Title)
}

func saveBookmarks(entries []BookmarkEntry) {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return
	}
	os.MkdirAll(configDir(), 0755)
	os.WriteFile(bookmarksPath(), data, 0644)
}

func loadBookmarks() []BookmarkEntry {
	data, err := os.ReadFile(bookmarksPath())
	if err != nil {
		return nil
	}
	var entries []BookmarkEntry
	json.Unmarshal(data, &entries)
	
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].AddedAt.After(entries[j].AddedAt)
	})
	return entries
}

func bookmarksPath() string {
	return configDir() + "/bookmarks.json"
}



type bookmarksModel struct {
	table    table.Model
	entries  []BookmarkEntry
	quitting bool
	action   string // "stream", "download", or ""
	selected *BookmarkEntry
}

// StartBookmarksTUI launches the interactive bookmarks viewer
func StartBookmarksTUI() {
	entries := loadBookmarks()

	if len(entries) == 0 {
		emptyStyle := lipgloss.NewStyle().Foreground(colorAmber)
		fmt.Println(emptyStyle.Render("\n  📌 No bookmarks yet. Press 'b' in Search to save one!\n"))
		return
	}

	columns := []table.Column{
		{Title: "Title", Width: 45},
		{Title: "Quality", Width: 9},
		{Title: "Source", Width: 12},
		{Title: "Added", Width: 12},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(min(len(entries), 18)),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colorBorderLit).
		BorderBottom(true).
		Foreground(colorCyan).
		Bold(true)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("#ffffff")).
		Background(colorPurple).
		Bold(true)
	t.SetStyles(s)

	m := bookmarksModel{
		entries: entries,
	}
	m.updateTable()

	p := tea.NewProgram(&m)
	finalModel, err := p.Run()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if bm, ok := finalModel.(*bookmarksModel); ok && bm.selected != nil {
		if bm.action == "stream" {
			StartStreamTUI(bm.selected.Magnet)
		} else if bm.action == "download" {
			StartDownloadTUI(bm.selected.Magnet)
		}
	}
}

func (m *bookmarksModel) updateTable() {
	var rows []table.Row
	for _, e := range m.entries {
		title := e.Title
		if len(title) > 42 {
			title = title[:39] + "..."
		}
		res := e.Resolution
		if res == "" {
			res = "—"
		}
		src := formatSource(e.Source)
		rows = append(rows, table.Row{
			title,
			res,
			src,
			timeAgo(e.AddedAt),
		})
	}
	m.table.SetRows(rows)
}

func (m *bookmarksModel) Init() tea.Cmd {
	return nil
}

func (m *bookmarksModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if len(m.entries) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.entries) {
					m.selected = &m.entries[idx]
					m.action = "stream"
					m.quitting = true
					return m, tea.Quit
				}
			}
		case "s":
			if len(m.entries) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.entries) {
					m.selected = &m.entries[idx]
					m.action = "download"
					m.quitting = true
					return m, tea.Quit
				}
			}
		case "d", "backspace":
			if len(m.entries) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.entries) {
					m.entries = append(m.entries[:idx], m.entries[idx+1:]...)
					saveBookmarks(m.entries)
					m.updateTable()

					if len(m.entries) == 0 {
						m.quitting = true
						return m, tea.Quit
					}
				}
			}
		}
	}

	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m *bookmarksModel) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	headerStyle := lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	countStyle := lipgloss.NewStyle().Foreground(colorTextDim)

	b.WriteString(fmt.Sprintf("  %s  %s\n\n",
		headerStyle.Render("📌 Bookmarks"),
		countStyle.Render(fmt.Sprintf("%d saved", len(m.entries))),
	))

	tableBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderLit)

	b.WriteString(tableBox.Render(m.table.View()))
	b.WriteString("\n\n")

	b.WriteString(footerStyle.Render("  ↑/↓ navigate • enter stream • s download • d delete • q back"))
	b.WriteString("\n")

	return b.String()
}
