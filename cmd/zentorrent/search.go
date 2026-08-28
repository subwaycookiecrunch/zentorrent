package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/subwaycookiecrunch/zentorrent/internal/engine"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/net/html"
)

type Result struct {
	Title, Magnet, Resolution, Source, Size, Category string
	Seeders, Episode                                  int
}

type sourceFunc struct {
	name string
	fn   func(ctx context.Context, q string) ([]Result, error)
}

var allSources = []sourceFunc{
	{"tgx", func(ctx context.Context, q string) ([]Result, error) { return searchTorrentGalaxy(ctx, q) }},
	{"bitsearch", func(ctx context.Context, q string) ([]Result, error) { return searchBitSearch(ctx, q) }},
	{"solidtorrents", func(ctx context.Context, q string) ([]Result, error) { return searchSolidTorrents(ctx, q) }},
	{"1337x", func(ctx context.Context, q string) ([]Result, error) { return search1337xCtx(ctx, q) }},
	{"tpb", func(ctx context.Context, q string) ([]Result, error) { return searchTPB(ctx, q) }},
	{"yts", func(ctx context.Context, q string) ([]Result, error) { return searchYTSCtx(ctx, q) }},
	{"yts-official", func(ctx context.Context, q string) ([]Result, error) { return searchYTSOfficialCtx(ctx, q) }},
	{"eztv", func(ctx context.Context, q string) ([]Result, error) { return searchEZTV(ctx, q) }},
	{"nyaa", func(ctx context.Context, q string) ([]Result, error) { return searchNyaaRSS(ctx, q) }},
	{"subsplease", func(ctx context.Context, q string) ([]Result, error) { return searchSubsPlease(ctx, q) }},
	{"btdig", func(ctx context.Context, q string) ([]Result, error) { return searchBTDig(ctx, q) }},
	{"zendht", func(ctx context.Context, q string) ([]Result, error) { return SearchDHT(q), nil }},
}

const sourceTimeout = 6 * time.Second

func StartSearchTUI(query string) {
	columns := []table.Column{
		{Title: "Title", Width: 36},
		{Title: "Quality", Width: 7},
		{Title: "Size", Width: 8},
		{Title: "Seeds", Width: 6},
		{Title: "Health", Width: 8},
		{Title: "Category", Width: 11},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(18),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colorBorderLit).
		BorderBottom(true).
		Foreground(colorCyan).
		Bold(true)
	s.Selected = s.Selected.
		Foreground(selectedRowFg).
		Background(colorPurple).
		Bold(true)
	t.SetStyles(s)

	ti := textinput.New()
	ti.Placeholder = "e.g. 10s, 30m, 9h"
	ti.CharLimit = 20
	ti.Width = 20

	m := searchModel{
		query:          query,
		table:          t,
		loading:        true,
		results:        nil,
		sourcesTotal:   len(allSources),
		sourcesDone:    0,
		sourcesRunning: make(map[string]bool),
		sourcesFailed:  make(map[string]bool),
		textInput:      ti,
		tabs:           []string{"All", "Movies", "TV Shows", "Anime"},
		activeTab:      0,
	}

	for _, src := range allSources {
		m.sourcesRunning[src.name] = true
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	if sm, ok := finalModel.(searchModel); ok && sm.forceExit {
		// Exit only after bubbletea restored the terminal, or the shell is
		// left in raw mode on a blank alt screen.
		ExitApp(0)
	}

	if sm, ok := finalModel.(searchModel); ok && sm.selected != nil {
		backups := findBackups(sm.results, sm.selected)
		downgrades := findDowngrades(sm.results, sm.selected)
		if sm.partyMode {
			StartPartyTUI(sm.selected.Magnet)
		} else if sm.downloadMode {
			StartDownloadTUI(sm.selected.Magnet, sm.delay)
		} else {
			// Tier-1 shortcut: debrid cache beats the P2P engine when hit.
			tmdbID := int64(0)
			if d := Discovery(); d != nil {
				if mv, _ := d.ResolveQuery(context.Background(), sm.selected.Title); mv != nil {
					tmdbID = mv.TMDBID
				}
			}
			if maybeDebridShortcut(sm.selected.Title, sm.selected.Magnet,
				extractBTIH(sm.selected.Magnet), tmdbID) {
				select {} // player owns the session now
			}
			if len(GlobalPlaylist.Items) > 0 {

				newItems := []PlaylistItem{{Magnet: sm.selected.Magnet, Title: sm.selected.Title, Status: "queued"}}
				GlobalPlaylist.mu.Lock()
				GlobalPlaylist.Items = append(newItems, GlobalPlaylist.Items...)
				GlobalPlaylist.mu.Unlock()
				StartPlaylistStreamTUI()
			} else {
				StartStreamTUI(sm.selected.Magnet, backups, downgrades)
			}
		}
	}
}

type searchModel struct {
	query           string
	table           table.Model
	loading         bool
	results         []Result
	filteredResults []Result
	quitting        bool
	forceExit       bool // ctrl+c: exit the app after the terminal is restored
	selected        *Result
	frame           int
	sourcesTotal    int
	sourcesDone     int
	sourcesRunning  map[string]bool
	sourcesFailed   map[string]bool
	seen            map[string]bool
	downloadMode    bool
	partyMode       bool
	delay           time.Duration
	inputMode       bool
	textInput       textinput.Model
	inputErr        string // shown under the delay prompt when parsing fails
	status          string // transient feedback for queue/bookmark actions
	statusFrame     int    // tick frame when status was set (auto-clears)
	showInfo        bool
	width           int
	activeTab       int
	tabs            []string
}

type searchPartialMsg struct {
	source  string
	results []Result
	failed  bool
}

type searchTickMsg struct{}

func (m searchModel) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, src := range allSources {
		cmds = append(cmds, fetchSourceCmd(src.name, src.fn, m.query))
	}
	cmds = append(cmds, searchTickCmd())
	return tea.Batch(cmds...)
}

func searchTickCmd() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(_ time.Time) tea.Msg {
		return searchTickMsg{}
	})
}

func fetchSourceCmd(name string, fn func(ctx context.Context, q string) ([]Result, error), query string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), sourceTimeout)
		defer cancel()
		res, err := fn(ctx, query)
		return searchPartialMsg{source: name, results: res, failed: err != nil}
	}
}

func (m searchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		titleW := m.width - 49 // other columns + separators + box chrome
		if titleW < 16 {
			titleW = 16
		}
		if titleW > 36 {
			titleW = 36
		}
		cols := m.table.Columns()
		if len(cols) > 0 && cols[0].Width != titleW {
			cols[0].Width = titleW
			m.table.SetColumns(cols)
			m.rebuildTable()
		}

	case tea.KeyMsg:
		if m.inputMode {
			switch msg.String() {
			case "ctrl+c":
				m.forceExit = true
				m.quitting = true
				return m, tea.Quit
			case "esc":
				m.inputMode = false
				m.inputErr = ""
				m.textInput.Blur()
				return m, nil
			case "enter":
				val := strings.TrimSpace(m.textInput.Value())
				if val != "" {
					parsed, err := time.ParseDuration(val)
					if err == nil {
						m.delay = parsed
						m.downloadMode = true
						m.quitting = true
						return m, tea.Quit
					}
					m.inputErr = "invalid duration — try 30s, 45m, 2h"
					return m, nil
				}
				m.inputMode = false
				m.inputErr = ""
				m.textInput.Blur()
				return m, nil
			default:
				m.inputErr = ""
			}
			m.textInput, cmd = m.textInput.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "left":
			if m.activeTab > 0 {
				m.activeTab--
				m.rebuildTable()
			}
		case "right":
			if m.activeTab < len(m.tabs)-1 {
				m.activeTab++
				m.rebuildTable()
			}
		case "esc", "q":
			m.quitting = true
			return m, tea.Quit
		case "ctrl+c":
			m.forceExit = true
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if m.showInfo {
				m.showInfo = false
				return m, nil
			}
			if len(m.filteredResults) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.filteredResults) {
					m.selected = &m.filteredResults[idx]
					m.downloadMode = false
					m.quitting = true
					return m, tea.Quit
				}
			}
			return m, nil
		case "d":
			if m.showInfo {
				m.showInfo = false
				return m, nil
			}
			if len(m.filteredResults) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.filteredResults) {
					m.selected = &m.filteredResults[idx]
					m.downloadMode = true
					m.quitting = true
					return m, tea.Quit
				}
			}
			return m, nil // consume: bubbles maps bare "d" to half-page-down

		case "a":
			if len(m.filteredResults) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.filteredResults) {
					r := m.filteredResults[idx]
					GlobalPlaylist.Add(r.Magnet, r.Title)
					m.status = "queued: " + truncateCells(r.Title, 30)
					m.statusFrame = m.frame
					return m, nil
				}
			}
		case "p":
			if len(m.filteredResults) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.filteredResults) {
					m.selected = &m.filteredResults[idx]
					m.partyMode = true
					m.quitting = true
					return m, tea.Quit
				}
			}
		case "t":
			if m.showInfo {
				m.showInfo = false
				return m, nil
			}
			if len(m.filteredResults) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.filteredResults) {
					m.selected = &m.filteredResults[idx]
					m.inputMode = true
					m.textInput.SetValue("")
					m.textInput.Focus()
					return m, textinput.Blink
				}
			}
		case "i":
			if !m.loading && len(m.results) > 0 {
				m.showInfo = !m.showInfo
				return m, nil
			}
		case "b":
			if len(m.filteredResults) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.filteredResults) {
					r := m.filteredResults[idx]
					AddBookmark(BookmarkEntry{
						Title:      r.Title,
						Magnet:     r.Magnet,
						Resolution: r.Resolution,
						Source:     r.Source,
						Seeders:    r.Seeders,
					})
					m.status = "bookmarked: " + truncateCells(r.Title, 30)
					m.statusFrame = m.frame
				}
			}
			return m, nil // consume: bubbles maps bare "b" to page-up
		}
	case searchTickMsg:
		m.frame++
		if m.status != "" && m.frame-m.statusFrame > 13 { // ~2s at 150ms/tick
			m.status = ""
		}
		if m.loading {
			return m, searchTickCmd()
		}
	case searchPartialMsg:
		m.sourcesDone++
		delete(m.sourcesRunning, msg.source)
		if msg.failed {
			m.sourcesFailed[msg.source] = true
		}

		if m.seen == nil {
			m.seen = make(map[string]bool)
		}

		for _, r := range msg.results {
			hash := extractBTIH(r.Magnet)
			if hash != "" && m.seen[hash] {
				continue
			}
			if hash != "" {
				m.seen[hash] = true
			}
			m.results = append(m.results, r)
		}

		for i := 0; i < len(m.results); i++ {
			for j := i + 1; j < len(m.results); j++ {
				if m.results[i].Seeders < m.results[j].Seeders {
					m.results[i], m.results[j] = m.results[j], m.results[i]
				}
			}
		}

		var alive []Result
		for _, r := range m.results {
			if r.Seeders != 0 { // 0 = dead; negative = seeder count unknown
				alive = append(alive, r)
			}
		}
		m.results = alive

		if m.sourcesDone >= m.sourcesTotal {
			m.loading = false
		}

		m.rebuildTable()
		return m, nil
	}

	oldIdx := m.table.Cursor()
	m.table, cmd = m.table.Update(msg)
	newIdx := m.table.Cursor()

	if oldIdx != newIdx && newIdx >= 0 && newIdx < len(m.filteredResults) {
		TriggerPrewarm(m.filteredResults[newIdx].Magnet)
	}

	return m, cmd
}

func (m *searchModel) rebuildTable() {
	// Remember which result is highlighted so progressive loading can't move
	// the selection out from under the user mid-browse.
	highlighted := ""
	prevCursor := m.table.Cursor()
	if prevCursor >= 0 && prevCursor < len(m.filteredResults) {
		r := m.filteredResults[prevCursor]
		highlighted = r.Magnet + "|" + r.Title
	}

	var rows []table.Row
	m.filteredResults = nil
	activeCategory := m.tabs[m.activeTab]
	for _, r := range m.results {
		if activeCategory != "All" && r.Category != activeCategory {
			continue
		}
		m.filteredResults = append(m.filteredResults, r)
		title := truncateCells(r.Title, 33)
		size := r.Size
		if size == "" {
			size = "—"
		}
		cat := r.Category
		if cat == "" {
			cat = "Other"
		}
		rows = append(rows, table.Row{
			title,
			r.Resolution,
			size,
			fmt.Sprintf("%d", r.Seeders),
			healthBadge(r.Seeders),
			cat,
		})
	}
	m.table.SetRows(rows)
	if len(rows) > 0 {
		next := 0
		if highlighted != "" {
			for i, r := range m.filteredResults {
				if r.Magnet+"|"+r.Title == highlighted {
					next = i
					break
				}
			}
			if next == 0 && prevCursor > 0 && prevCursor < len(rows) {
				// Highlighted row vanished (deduped/filtered): stay near
				// where the user was rather than snapping to the top.
				next = prevCursor
			}
		} else if prevCursor > 0 && prevCursor < len(rows) {
			next = prevCursor
		}
		m.table.SetCursor(next)
	}
}

func formatSource(src string) string {
	switch src {
	case "tgx", "torrentgalaxy":
		return "TorrentGalaxy"
	case "bitsearch":
		return "BitSearch"
	case "solidtorrents":
		return "SolidTorrents"
	case "yts-official":
		return "YTS Official"
	case "yts":
		return "YTS.mx"
	case "1337x":
		return "1337x"
	case "nyaa":
		return "Nyaa"
	case "tpb":
		return "TPB"
	case "eztv":
		return "EZTV"
	case "subsplease":
		return "SubsPlease"
	default:
		return src
	}
}

// seedersUnknown marks sources that expose no seeder count; such rows sort
// below any known-seeder result instead of fabricating a healthy number.
const seedersUnknown = -1

func healthBadge(seeds int) string {
	switch {
	case seeds < 0:
		return "⚪ N/A"
	case seeds >= 50:
		return "🟢 Fast"
	case seeds >= 10:
		return "🟡 OK"
	case seeds >= 1:
		return "🔴 Slow"
	default:
		return "⚫ Dead"
	}
}

func (m searchModel) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	headerStyle := lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	queryStyle := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	b.WriteString(fmt.Sprintf("  %s %s\n\n  ", headerStyle.Render("🔍 Search:"), queryStyle.Render(m.query)))

	for i, t := range m.tabs {
		if i == m.activeTab {
			b.WriteString(lipgloss.NewStyle().Foreground(selectedRowFg).Background(colorPurple).Padding(0, 1).Bold(true).Render(t) + "  ")
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(colorTextDim).Render(t) + "  ")
		}
	}
	b.WriteString("\n")

	if m.loading && len(m.results) == 0 {
		spinners := []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}
		spin := spinners[m.frame%len(spinners)]
		loadStyle := lipgloss.NewStyle().Foreground(colorAmber).Bold(true)
		doneStyle := lipgloss.NewStyle().Foreground(colorGreen)
		waitStyle := lipgloss.NewStyle().Foreground(colorTextDim).Italic(true)

		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  %s Searching %d sources...\n\n", loadStyle.Render(spin), m.sourcesTotal))

		for _, src := range allSources {
			if m.sourcesRunning[src.name] {
				b.WriteString(waitStyle.Render(fmt.Sprintf("  ├─ %-14s searching...\n", src.name)))
			} else if m.sourcesFailed[src.name] {
				failStyle := lipgloss.NewStyle().Foreground(colorRed)
				b.WriteString(failStyle.Render(fmt.Sprintf("  ├─ %-14s ✗\n", src.name)))
			} else {
				b.WriteString(doneStyle.Render(fmt.Sprintf("  ├─ %-14s ✓\n", src.name)))
			}
		}

		box := lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(1, 2)
		return box.Render(b.String())
	}

	if len(m.results) == 0 && !m.loading {
		emptyStyle := lipgloss.NewStyle().Foreground(colorRed)
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  %s No results found.\n", emptyStyle.Render("✕")))
		b.WriteString(footerStyle.Render("\n  press q to go back"))
		b.WriteString("\n")

		box := lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(1, 2)
		return box.Render(b.String())
	}

	countStyle := lipgloss.NewStyle().Foreground(colorTextDim)
	if m.loading {
		countStyle = lipgloss.NewStyle().Foreground(colorAmber)
		b.WriteString(countStyle.Render(fmt.Sprintf("  %d results (%d/%d sources)", len(m.results), m.sourcesDone, m.sourcesTotal)))
	} else {
		b.WriteString(countStyle.Render(fmt.Sprintf("  %d results", len(m.results))))
	}
	b.WriteString("\n\n")

	tableBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderLit)

	b.WriteString(tableBox.Render(m.table.View()))
	b.WriteString("\n\n")

	if m.showInfo && len(m.filteredResults) > 0 {
		idx := m.table.Cursor()
		if idx >= 0 && idx < len(m.filteredResults) {
			r := m.filteredResults[idx]
			b.WriteString("\n")
			infoBox := lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(colorPurple).
				Padding(0, 1).
				Width(50)

			lbl := lipgloss.NewStyle().Foreground(colorTextDim)
			val := lipgloss.NewStyle().Foreground(colorTextPri).Bold(true)

			info := fmt.Sprintf(
				"%s %s\n%s %s\n%s %s\n%s %s\n%s %s\n%s %d\n%s %s",
				lbl.Render("  Title:"), val.Render(r.Title),
				lbl.Render("  Quality:"), val.Render(r.Resolution),
				lbl.Render("  Size:"), val.Render(r.Size),
				lbl.Render("  Category:"), val.Render(r.Category),
				lbl.Render("  Source:"), val.Render(formatSource(r.Source)),
				lbl.Render("  Seeders:"), r.Seeders,
				lbl.Render("  Health:"), val.Render(healthBadge(r.Seeders)),
			)
			b.WriteString(infoBox.Render(info))
			b.WriteString("\n")
		}
	}

	if m.inputMode {
		b.WriteString("\n  " + lipgloss.NewStyle().Foreground(colorPurple).Render("Enter delay before download:"))
		b.WriteString("\n  " + m.textInput.View() + "\n")
		if m.inputErr != "" {
			errStyle := lipgloss.NewStyle().Foreground(colorRed)
			b.WriteString("  " + errStyle.Render(m.inputErr) + "\n")
		}
	} else {
		if m.status != "" {
			okStyle := lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
			b.WriteString("  " + okStyle.Render("✓ "+m.status) + "\n\n")
		}
		b.WriteString(footerStyle.Render("  ↑/↓ navigate • enter stream • d download • p party • t timed • a queue • i info • b bookmark • q back"))
		b.WriteString("\n")
	}

	return b.String()
}

func searchYTSCtx(ctx context.Context, q string) ([]Result, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://yts.mx/api/v2/list_movies.json?query_term="+url.QueryEscape(q), nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var r struct {
		Data struct {
			Movies []struct {
				Title    string
				Torrents []struct {
					Hash, Quality, Size string
					Seeds               int
					SizeBytes           int64 `json:"size_bytes"`
				}
			}
		}
	}
	json.NewDecoder(resp.Body).Decode(&r)

	var res []Result
	for _, m := range r.Data.Movies {
		for _, t := range m.Torrents {
			size := t.Size
			if size == "" && t.SizeBytes > 0 {
				size = formatSizeBytes(t.SizeBytes)
			}
			res = append(res, Result{
				Title:      m.Title,
				Magnet:     fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", t.Hash, url.QueryEscape(m.Title)),
				Resolution: t.Quality,
				Seeders:    t.Seeds,
				Size:       size,
				Category:   "Movies",
				Source:     "yts",
			})
		}
	}
	return res, nil
}

func search1337xCtx(ctx context.Context, q string) ([]Result, error) {
	results, err := scrapeCtx(ctx, "https://1337x.to/search/"+url.QueryEscape(q)+"/1/", "1337x")
	if err != nil {
		return nil, err
	}

	type resolved struct {
		idx int
		mag string
	}
	ch := make(chan resolved, len(results))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	pending := 0
	for i, r := range results {
		if !strings.HasPrefix(r.Magnet, "https://1337x.to/") {
			continue
		}
		pending++
		wg.Add(1)
		go func(idx int, pageURL string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ch <- resolved{idx, resolve1337xMagnet(ctx, pageURL)}
		}(i, r.Magnet)
	}
	go func() {
		wg.Wait()
		close(ch)
	}()

	got := make(map[int]string)
	for n := 0; n < pending; n++ {
		select {
		case r := <-ch:
			if r.mag != "" {
				got[r.idx] = r.mag
			}
		case <-ctx.Done():
			n = pending
		}
	}

	valid := make([]Result, 0, len(results))
	for i, r := range results {
		if mag, ok := got[i]; ok {
			r.Magnet = mag
		} else if ok == false && strings.HasPrefix(r.Magnet, "https://1337x.to/") {
			continue
		}
		if r.Magnet != "" {
			valid = append(valid, r)
		}
	}
	return valid, nil
}

func resolve1337xMagnet(ctx context.Context, pageURL string) string {
	req, _ := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	doc, _ := html.Parse(resp.Body)
	return findMag(doc)
}

func searchYTSOfficialCtx(ctx context.Context, q string) ([]Result, error) {
	var browseURL = "https://en.yts-official.biz/browse-movies?keyword=" + url.QueryEscape(q)
	req, _ := http.NewRequestWithContext(ctx, "GET", browseURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	doc, _ := html.Parse(resp.Body)

	var movieURLs []string
	var movieTitles []string
	var crawlBrowse func(*html.Node)
	crawlBrowse = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			href := getAttr(n, "href")
			class := getAttr(n, "class")
			if class == "browse-movie-title" && strings.HasPrefix(href, "/movies/") {
				fullURL := "https://en.yts-official.biz" + href
				title := getTxt(n)
				movieURLs = append(movieURLs, fullURL)
				movieTitles = append(movieTitles, title)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			crawlBrowse(c)
		}
	}
	crawlBrowse(doc)
	if len(movieURLs) == 0 {
		return nil, nil
	}
	if len(movieURLs) > 5 {
		movieURLs = movieURLs[:5]
		movieTitles = movieTitles[:5]
	}

	ch := make(chan []Result, len(movieURLs))
	for i, mURL := range movieURLs {
		go func(pageURL, title string) {
			ch <- scrapeYTSOfficialDetail(ctx, pageURL, title)
		}(mURL, movieTitles[i])
	}

	results := make([]Result, 0)
	for range movieURLs {
		select {
		case res := <-ch:
			results = append(results, res...)
		case <-ctx.Done():
			return results, nil
		}
	}
	return results, nil
}

func scrapeCtx(ctx context.Context, u, src string) ([]Result, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, _ := html.Parse(resp.Body)
	var res []Result
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			var tds []*html.Node
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && c.Data == "td" {
					tds = append(tds, c)
				}
			}
			if len(tds) >= 4 {
				t := getTxt(tds[0])
				m := ""
				s := 0
				if src == "nyaa" {
					t = getAttr(find(tds[1], "a"), "title")
					m = findMag(tds[2])
					s, _ = strconv.Atoi(getTxt(tds[5]))
				} else {
					a := find(tds[0], "a")
					if a != nil && strings.HasPrefix(getAttr(a, "href"), "/torrent") {
						t = getTxt(a)
						s, _ = strconv.Atoi(getTxt(tds[1]))
						m = "https://1337x.to" + getAttr(a, "href")
					}
				}
				if t != "" && m != "" {
					res = append(res, Result{
						Title:      t,
						Magnet:     m,
						Seeders:    s,
						Source:     src,
						Category:   guessCategory(t),
						Episode:    parseEp(t),
						Resolution: parseRes(t),
					})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return res, nil
}

func scrapeYTSOfficialDetail(ctx context.Context, pageURL, title string) []Result {
	req, _ := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	doc, _ := html.Parse(resp.Body)

	var magnets []string
	var qualities []string
	var crawlMagnets func(*html.Node)
	crawlMagnets = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			href := getAttr(n, "href")
			class := getAttr(n, "class")
			if strings.Contains(class, "magnet-download") && strings.HasPrefix(href, "magnet:") {
				magnets = append(magnets, href)
				dlTitle := getAttr(n, "title")
				quality := parseRes(dlTitle)
				qualities = append(qualities, quality)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			crawlMagnets(c)
		}
	}
	crawlMagnets(doc)

	seeds := 0
	bodyText := collectAllText(doc)
	psMatch := regexp.MustCompile(`P/S\s+(\d+)\s*/\s*(\d+)`).FindStringSubmatch(bodyText)
	if len(psMatch) > 2 {
		seeds, _ = strconv.Atoi(psMatch[2])
	}

	var results []Result
	for i, mag := range magnets {
		quality := "unknown"
		if i < len(qualities) {
			quality = qualities[i]
		}
		results = append(results, Result{
			Title:      title,
			Magnet:     mag,
			Resolution: quality,
			Seeders:    seeds,
			Category:   "Movies",
			Source:     "yts-official",
		})
	}
	return results
}

func findMag(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "a" {
		href := getAttr(n, "href")
		if strings.HasPrefix(href, "magnet:?") {
			return href
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if res := findMag(c); res != "" {
			return res
		}
	}
	return ""
}

func guessCategory(title string) string {
	t := strings.ToLower(title)
	if strings.Contains(t, "s01") || strings.Contains(t, "s02") || strings.Contains(t, "s03") || regexp.MustCompile(`s\d+e\d+`).MatchString(t) || strings.Contains(t, "season") {
		return "TV Shows"
	}
	if regexp.MustCompile(`\[subsplease\]|\[erai-raws\]|\[horriblesubs\]|\[judas\]`).MatchString(t) {
		return "Anime"
	}
	return "Movies"
}

func findBackups(results []Result, selected *Result) []string {
	var backups []string
	targetTitle := normalizeTitle(selected.Title)
	targetHash := extractBTIH(selected.Magnet)

	for _, r := range results {
		if len(backups) >= 3 {
			break
		}
		hash := extractBTIH(r.Magnet)
		if hash == "" || hash == targetHash {
			continue
		}
		if r.Category != selected.Category {
			continue
		}
		if r.Resolution != selected.Resolution {
			continue
		}
		if normalizeTitle(r.Title) == targetTitle {
			backups = append(backups, r.Magnet)
		}
	}
	return backups
}

func findDowngrades(results []Result, selected *Result) []string {
	var downgrades []string
	targetTitle := normalizeTitle(selected.Title)
	targetHash := extractBTIH(selected.Magnet)

	targetRes := "720p"
	if selected.Resolution == "2160p" || selected.Resolution == "4k" {
		targetRes = "1080p"
	} else if selected.Resolution == "720p" {
		targetRes = "480p"
	} else if selected.Resolution == "" || selected.Resolution == "480p" {
		return nil
	}

	for _, r := range results {
		if len(downgrades) >= 3 {
			break
		}
		hash := extractBTIH(r.Magnet)
		if hash == "" || hash == targetHash {
			continue
		}
		if r.Category != selected.Category {
			continue
		}
		if r.Resolution != targetRes {
			continue
		}
		if normalizeTitle(r.Title) == targetTitle {
			downgrades = append(downgrades, r.Magnet)
		}
	}
	return downgrades
}

func normalizeTitle(s string) string {
	s = strings.ToLower(s)
	re := regexp.MustCompile(`\[.*?\]|\(.*?\)|1080p|720p|4k|2160p|x264|x265|hevc|web-dl|bluray|yify|yts|eztv|tgx|rarbg`)
	s = re.ReplaceAllString(s, "")
	re2 := regexp.MustCompile(`[^a-z0-9]`)
	s = re2.ReplaceAllString(s, " ")
	re3 := regexp.MustCompile(`\s+`)
	return strings.TrimSpace(re3.ReplaceAllString(s, " "))
}

func getAttr(n *html.Node, k string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}

func getTxt(n *html.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	res := ""
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		res += getTxt(c)
	}
	return strings.TrimSpace(res)
}

var (
	prewarmMu    sync.Mutex
	prewarmMag   string
	prewarmTimer *time.Timer
)

func TriggerPrewarm(magnet string) {
	prewarmMu.Lock()
	if magnet == prewarmMag {
		prewarmMu.Unlock()
		return
	}
	prewarmMag = magnet
	if prewarmTimer != nil {
		prewarmTimer.Stop()
	}
	prewarmTimer = time.AfterFunc(500*time.Millisecond, func() {
		go doPrewarm(magnet)
	})
	prewarmMu.Unlock()
}

func doPrewarm(magnet string) {
	prewarmMu.Lock()
	defer prewarmMu.Unlock()

	// Never compete with an active stream for bandwidth or connections.
	if engine.IsActiveStreaming() {
		return
	}

	engine.DropIdle()

	t, err := engine.AddMagnet(magnet)
	if err != nil || t == nil {
		return
	}

	go func() {
		select {
		case <-t.GotInfo():
			engine.StashMeta(t)
		case <-time.After(45 * time.Second):
			// dead result — the next hover's sweep will clean it up
		}
	}()
}

func find(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if res := find(c, tag); res != nil {
			return res
		}
	}
	return nil
}

func parseEp(s string) int {
	m := regexp.MustCompile(`(?i)(?:-|E|EP|Episode|S\d+E|v)\s*(\d+)`).FindStringSubmatch(s)
	if len(m) > 1 {
		v, _ := strconv.Atoi(m[1])
		return v
	}
	return 0
}

func parseRes(s string) string {
	if m := regexp.MustCompile(`(?i)(480|720|1080|2160|4k)p?`).FindString(s); m != "" {
		return strings.ToLower(m)
	}
	return "unknown"
}

func extractBTIH(magnet string) string {
	m := regexp.MustCompile(`(?i)btih:([a-fA-F0-9]+)`).FindStringSubmatch(magnet)
	if len(m) > 1 {
		return strings.ToUpper(m[1])
	}
	return ""
}

func collectAllText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(collectAllText(c))
	}
	return sb.String()
}
