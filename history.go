package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type HistoryEntry struct {
	Title      string    `json:"title"`
	Magnet     string    `json:"magnet"`
	Resolution string    `json:"resolution"`
	Source     string    `json:"source"`
	FileSize   string    `json:"file_size"`
	Progress   float64   `json:"progress"`
	Timestamp  time.Time `json:"timestamp"`
}

const maxHistoryEntries = 100

func AddHistory(entry HistoryEntry) {
	entries := loadHistory()
	entry.Timestamp = time.Now()

	newHash := extractHistBTIH(entry.Magnet)
	if newHash != "" {
		for i, e := range entries {
			if extractHistBTIH(e.Magnet) == newHash {
				entries[i].Timestamp = entry.Timestamp
				entries[i].Title = entry.Title
				entries[i].Resolution = entry.Resolution
				entries[i].FileSize = entry.FileSize
				saveHistory(entries)
				return
			}
		}
	}

	entries = append(entries, entry)

	if len(entries) > maxHistoryEntries {
		entries = entries[len(entries)-maxHistoryEntries:]
	}

	saveHistory(entries)
}

func saveHistory(entries []HistoryEntry) {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return
	}
	os.MkdirAll(configDir(), 0755)
	os.WriteFile(historyPath(), data, 0644)
}

func extractHistBTIH(magnet string) string {
	for _, part := range strings.Split(magnet, "&") {
		if strings.HasPrefix(part, "xt=urn:btih:") || strings.HasPrefix(part, "magnet:?xt=urn:btih:") {
			hash := strings.TrimPrefix(part, "magnet:?xt=urn:btih:")
			hash = strings.TrimPrefix(hash, "xt=urn:btih:")
			return strings.ToUpper(hash)
		}
	}
	return ""
}

func GetHistory(limit int) []HistoryEntry {
	entries := loadHistory()

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})

	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

func ClearHistory() error {
	return os.Remove(historyPath())
}

func loadHistory() []HistoryEntry {
	data, err := os.ReadFile(historyPath())
	if err != nil {
		return nil
	}
	var entries []HistoryEntry
	json.Unmarshal(data, &entries)
	return entries
}

type historyModel struct {
	table    table.Model
	entries  []HistoryEntry
	quitting bool
	selected *HistoryEntry
	action   string
}

func StartHistoryTUI() {
	entries := GetHistory(50)

	if len(entries) == 0 {
		emptyStyle := lipgloss.NewStyle().Foreground(colorAmber)
		fmt.Println(emptyStyle.Render("\n  📜 No history yet. Search and stream something first!\n"))
		return
	}

	columns := []table.Column{
		{Title: "Title", Width: 45},
		{Title: "Quality", Width: 9},
		{Title: "Size", Width: 10},
		{Title: "When", Width: 12},
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

	var rows []table.Row
	for _, e := range entries {
		title := e.Title
		if len(title) > 42 {
			title = title[:39] + "..."
		}
		res := e.Resolution
		if res == "" {
			res = "—"
		}
		size := e.FileSize
		if size == "" {
			size = "—"
		}
		rows = append(rows, table.Row{
			title,
			res,
			size,
			timeAgo(e.Timestamp),
		})
	}
	t.SetRows(rows)

	m := historyModel{
		table:   t,
		entries: entries,
		action:  "stream",
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if hm, ok := finalModel.(historyModel); ok && hm.selected != nil {
		if hm.action == "party" {
			StartPartyTUI(hm.selected.Magnet)
		} else {
			StartStreamTUI(hm.selected.Magnet, nil, nil)
		}
	}
}

func (m historyModel) Init() tea.Cmd {
	return nil
}

func (m historyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q":
			m.quitting = true
			return m, tea.Quit
		case "ctrl+c":
			os.Exit(0)
		case "enter":
			if len(m.entries) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.entries) {
					m.selected = &m.entries[idx]
					m.quitting = true
					return m, tea.Quit
				}
			}
		case "p":
			if len(m.entries) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.entries) {
					m.selected = &m.entries[idx]
					m.action = "party"
					m.quitting = true
					return m, tea.Quit
				}
			}
		case "d", "backspace":
			if len(m.entries) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.entries) {
					m.entries = append(m.entries[:idx], m.entries[idx+1:]...)
					saveHistory(m.entries)

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
						size := e.FileSize
						if size == "" {
							size = "—"
						}
						rows = append(rows, table.Row{
							title,
							res,
							size,
							timeAgo(e.Timestamp),
						})
					}
					m.table.SetRows(rows)

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

func (m historyModel) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	headerStyle := lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	countStyle := lipgloss.NewStyle().Foreground(colorTextDim)

	b.WriteString(fmt.Sprintf("  %s  %s\n\n",
		headerStyle.Render("📜 Watch History"),
		countStyle.Render(fmt.Sprintf("%d entries", len(m.entries))),
	))

	tableBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderLit)

	b.WriteString(tableBox.Render(m.table.View()))
	b.WriteString("\n\n")

	b.WriteString(footerStyle.Render("  ↑/↓ navigate • enter re-stream • p party • d delete • q back"))
	b.WriteString("\n")

	return b.String()
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "yesterday"
		}
		return strconv.Itoa(days) + "d ago"
	}
}

func padRight(s string, n int) string {
	rs := []rune(s)
	if len(rs) >= n {
		return string(rs[:n])
	}
	for len(rs) < n {
		rs = append(rs, ' ')
	}
	return string(rs)
}
