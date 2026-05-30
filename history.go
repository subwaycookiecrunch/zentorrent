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

// HistoryEntry represents a single stream event
type HistoryEntry struct {
	Title      string    `json:"title"`
	Magnet     string    `json:"magnet"`
	Resolution string    `json:"resolution"`
	Source     string    `json:"source"`
	FileSize   string    `json:"file_size"`
	Timestamp  time.Time `json:"timestamp"`
}

const maxHistoryEntries = 100

// AddHistory appends a new entry to the history file, deduplicating by magnet hash
func AddHistory(entry HistoryEntry) {
	entries := loadHistory()
	entry.Timestamp = time.Now()

	// Deduplicate: if same btih hash exists, update its timestamp instead of adding a copy
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

	// Prune to max entries
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

// GetHistory returns the last N history entries (newest first)
func GetHistory(limit int) []HistoryEntry {
	entries := loadHistory()

	// Sort newest first
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})

	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

// ClearHistory wipes all history
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

// PrintHistory displays history in a styled table (non-interactive fallback)
func PrintHistory() {
	entries := GetHistory(20)

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(colorPurple).
		PaddingBottom(1)

	rowStyle := lipgloss.NewStyle().
		Foreground(colorTextSec)

	timeStyle := lipgloss.NewStyle().
		Foreground(colorCyan)

	if len(entries) == 0 {
		println(headerStyle.Render("📜 No history yet. Stream something first!"))
		return
	}

	println(headerStyle.Render("📜 Recent Streams"))
	println()

	for i, e := range entries {
		ago := timeAgo(e.Timestamp)
		title := e.Title
		if len(title) > 50 {
			title = title[:47] + "..."
		}

		res := e.Resolution
		if res == "" {
			res = "?"
		}

		line := rowStyle.Render(
			padRight(title, 52) + " " +
				padRight(res, 8) + " " +
				timeStyle.Render(ago),
		)
		_ = i
		println("  " + line)
	}
}

// ── Interactive History TUI ──

type historyModel struct {
	table    table.Model
	entries  []HistoryEntry
	quitting bool
	selected *HistoryEntry
}

// StartHistoryTUI launches the interactive history viewer
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
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if hm, ok := finalModel.(historyModel); ok && hm.selected != nil {
		StartStreamTUI(hm.selected.Magnet)
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
		case "esc", "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if len(m.entries) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.entries) {
					m.selected = &m.entries[idx]
					m.quitting = true
					return m, tea.Quit
				}
			}
		case "d", "backspace":
			// Delete selected entry
			if len(m.entries) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.entries) {
					m.entries = append(m.entries[:idx], m.entries[idx+1:]...)
					saveHistory(m.entries)

					// Rebuild table rows
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

	b.WriteString(footerStyle.Render("  ↑/↓ navigate • enter re-stream • d delete • q back"))
	b.WriteString("\n")

	return b.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return intToStr(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return intToStr(int(d.Hours())) + "h ago"
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "yesterday"
		}
		return intToStr(days) + "d ago"
	}
}

func intToStr(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
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
