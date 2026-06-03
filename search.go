package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/net/html"
)

type Result struct {
	Title, Magnet, Resolution, Source string
	Seeders, Episode                  int
}

type sourceFunc struct {
	name string
	fn   func(ctx context.Context, q string) ([]Result, error)
}

var allSources = []sourceFunc{
	{"yts", func(ctx context.Context, q string) ([]Result, error) { return searchYTSCtx(ctx, q) }},
	{"yts-official", func(ctx context.Context, q string) ([]Result, error) { return searchYTSOfficialCtx(ctx, q) }},
	{"tpb", func(ctx context.Context, q string) ([]Result, error) { return searchTPB(ctx, q) }},
	{"eztv", func(ctx context.Context, q string) ([]Result, error) { return searchEZTV(ctx, q) }},
	{"1337x", func(ctx context.Context, q string) ([]Result, error) { return search1337xCtx(ctx, q) }},
	{"nyaa", func(ctx context.Context, q string) ([]Result, error) { return searchNyaaRSS(ctx, q) }},
	{"subsplease", func(ctx context.Context, q string) ([]Result, error) { return searchSubsPlease(ctx, q) }},
}

const sourceTimeout = 6 * time.Second

func StartSearchTUI(query string) {
	columns := []table.Column{
		{Title: "Title", Width: 48},
		{Title: "Quality", Width: 9},
		{Title: "Seeds", Width: 7},
		{Title: "Source", Width: 14},
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
		Foreground(lipgloss.Color("#ffffff")).
		Background(colorPurple).
		Bold(true)
	t.SetStyles(s)

	m := searchModel{
		query:          query,
		table:          t,
		loading:        true,
		results:        nil,
		sourcesTotal:   len(allSources),
		sourcesDone:    0,
		sourcesRunning: make(map[string]bool),
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

	if sm, ok := finalModel.(searchModel); ok && sm.selected != nil {
		StartStreamTUI(sm.selected.Magnet)
	}
}

type searchModel struct {
	query          string
	table          table.Model
	loading        bool
	results        []Result
	quitting       bool
	selected       *Result
	frame          int
	sourcesTotal   int
	sourcesDone    int
	sourcesRunning map[string]bool
	seen           map[string]bool
}

type searchPartialMsg struct {
	source  string
	results []Result
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
		res, _ := fn(ctx, query)
		return searchPartialMsg{source: name, results: res}
	}
}

func (m searchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if !m.loading && len(m.results) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.results) {
					m.selected = &m.results[idx]
					m.quitting = true
					return m, tea.Quit
				}
			}
		case "b":
			if !m.loading && len(m.results) > 0 {
				idx := m.table.Cursor()
				if idx >= 0 && idx < len(m.results) {
					r := m.results[idx]
					AddBookmark(BookmarkEntry{
						Title:      r.Title,
						Magnet:     r.Magnet,
						Resolution: r.Resolution,
						Source:     r.Source,
						Seeders:    r.Seeders,
					})
				}
			}
		}
	case searchTickMsg:
		m.frame++
		if m.loading {
			return m, searchTickCmd()
		}
	case searchPartialMsg:
		m.sourcesDone++
		delete(m.sourcesRunning, msg.source)

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

		// re-sort by seeders
		for i := 0; i < len(m.results); i++ {
			for j := i + 1; j < len(m.results); j++ {
				if m.results[i].Seeders < m.results[j].Seeders {
					m.results[i], m.results[j] = m.results[j], m.results[i]
				}
			}
		}

		if m.sourcesDone >= m.sourcesTotal {
			m.loading = false
		}

		m.rebuildTable()
		return m, nil
	}

	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m *searchModel) rebuildTable() {
	var rows []table.Row
	for _, r := range m.results {
		title := r.Title
		if len(title) > 45 {
			title = title[:42] + "..."
		}
		rows = append(rows, table.Row{
			title,
			r.Resolution,
			fmt.Sprintf("%d", r.Seeders),
			formatSource(r.Source),
		})
	}
	m.table.SetRows(rows)
}

func formatSource(src string) string {
	switch src {
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

func (m searchModel) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	headerStyle := lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	queryStyle := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	b.WriteString(fmt.Sprintf("  %s %s\n", headerStyle.Render("🔍 Search:"), queryStyle.Render(m.query)))

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

	b.WriteString(footerStyle.Render("  ↑/↓ navigate • enter stream • b bookmark • q back"))
	b.WriteString("\n")

	return b.String()
}

// existing scrapers wrapped with context support

func searchYTSCtx(ctx context.Context, q string) ([]Result, error) {
	u := "https://yts.mx/api/v2/list_movies.json?query_term=" + url.QueryEscape(q)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
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
					Hash, Quality string
					Seeds         int
				}
			}
		}
	}
	json.NewDecoder(resp.Body).Decode(&r)

	var res []Result
	for _, m := range r.Data.Movies {
		for _, t := range m.Torrents {
			res = append(res, Result{
				Title:      m.Title,
				Magnet:     fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", t.Hash, url.QueryEscape(m.Title)),
				Resolution: t.Quality,
				Seeders:    t.Seeds,
				Source:     "yts",
			})
		}
	}
	return res, nil
}

func search1337xCtx(ctx context.Context, q string) ([]Result, error) {
	return scrapeCtx(ctx, "https://1337x.to/search/"+url.QueryEscape(q)+"/1/", "1337x")
}

func searchYTSOfficialCtx(ctx context.Context, q string) ([]Result, error) {
	browseURL := "https://en.yts-official.biz/browse-movies?keyword=" + url.QueryEscape(q)
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
			ch <- scrapeYTSOfficialDetail(pageURL, title)
		}(mURL, movieTitles[i])
	}

	var results []Result
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
	var f func(*html.Node)
	f = func(n *html.Node) {
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
						Episode:    parseEp(t),
						Resolution: parseRes(t),
					})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)
	return res, nil
}

func scrapeYTSOfficialDetail(pageURL, title string) []Result {
	req, _ := http.NewRequest("GET", pageURL, nil)
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
			Source:     "yts-official",
		})
	}
	return results
}

// html helpers

func findMag(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "a" {
		if strings.HasPrefix(getAttr(n, "href"), "magnet:") {
			return getAttr(n, "href")
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if res := findMag(c); res != "" {
			return res
		}
	}
	return ""
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
	m := regexp.MustCompile(`(?i)(480|720|1080|2160|4k)p?`).FindString(s)
	if m != "" {
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
