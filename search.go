package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

// StartSearchTUI launches the interactive search UI
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
		query:   query,
		table:   t,
		loading: true,
		results: nil,
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if sm, ok := finalModel.(searchModel); ok && sm.selected != nil {
		// Stream the selected magnet
		StartStreamTUI(sm.selected.Magnet)
	}
}

type searchModel struct {
	query    string
	table    table.Model
	loading  bool
	results  []Result
	quitting bool
	selected *Result
	frame    int
}

type searchResultsMsg []Result

func (m searchModel) Init() tea.Cmd {
	return tea.Batch(
		fetchResultsCmd(m.query),
		searchTickCmd(),
	)
}

type searchTickMsg struct{}

func searchTickCmd() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(_ time.Time) tea.Msg {
		return searchTickMsg{}
	})
}

func fetchResultsCmd(query string) tea.Cmd {
	return func() tea.Msg {
		var wg sync.WaitGroup
		var mu sync.Mutex
		var all []Result

		// Priority 1: YTS Official (en.yts-official.biz)
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, _ := searchYTSOfficial(query)
			mu.Lock()
			all = append(all, res...)
			mu.Unlock()
		}()

		// Priority 2: YTS.mx API
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, _ := searchYTS(query)
			mu.Lock()
			all = append(all, res...)
			mu.Unlock()
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			res, _ := searchNyaa(query)
			mu.Lock()
			all = append(all, res...)
			mu.Unlock()
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			res, _ := search1337x(query)
			mu.Lock()
			all = append(all, res...)
			mu.Unlock()
		}()

		wg.Wait()

		// Deduplicate by magnet hash (btih)
		seen := make(map[string]bool)
		var deduped []Result
		for _, r := range all {
			hash := extractBTIH(r.Magnet)
			if hash != "" && seen[hash] {
				continue
			}
			if hash != "" {
				seen[hash] = true
			}
			deduped = append(deduped, r)
		}

		for i := 0; i < len(deduped); i++ {
			for j := i + 1; j < len(deduped); j++ {
				if deduped[i].Seeders < deduped[j].Seeders {
					deduped[i], deduped[j] = deduped[j], deduped[i]
				}
			}
		}

		return searchResultsMsg(deduped)
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
	case searchResultsMsg:
		m.loading = false
		m.results = msg

		var rows []table.Row
		for _, r := range m.results {
			title := r.Title
			if len(title) > 45 {
				title = title[:42] + "..."
			}

			// Color-code seeders
			seedStr := fmt.Sprintf("%d", r.Seeders)

			// Format source with badge
			source := formatSource(r.Source)

			rows = append(rows, table.Row{
				title,
				r.Resolution,
				seedStr,
				source,
			})
		}
		m.table.SetRows(rows)
		return m, nil
	}

	m.table, cmd = m.table.Update(msg)
	return m, cmd
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
	default:
		return src
	}
}

func (m searchModel) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	// Header
	headerStyle := lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	queryStyle := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	b.WriteString(fmt.Sprintf("  %s %s\n", headerStyle.Render("🔍 Search:"), queryStyle.Render(m.query)))

	if m.loading {
		// Animated loading
		spinners := []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}
		spin := spinners[m.frame%len(spinners)]
		loadStyle := lipgloss.NewStyle().Foreground(colorAmber).Bold(true)
		sourceStyle := lipgloss.NewStyle().Foreground(colorTextDim).Italic(true)

		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  %s Searching all sources...\n\n", loadStyle.Render(spin)))
		b.WriteString(sourceStyle.Render("  ├─ YTS Official  (en.yts-official.biz)\n"))
		b.WriteString(sourceStyle.Render("  ├─ YTS.mx        (yts.mx)\n"))
		b.WriteString(sourceStyle.Render("  ├─ 1337x         (1337x.to)\n"))
		b.WriteString(sourceStyle.Render("  └─ Nyaa          (nyaa.si)\n"))

		box := lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(1, 2)
		return box.Render(b.String())
	}

	if len(m.results) == 0 {
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

	// Results count
	countStyle := lipgloss.NewStyle().Foreground(colorTextDim)
	b.WriteString(countStyle.Render(fmt.Sprintf("  %d results", len(m.results))))
	b.WriteString("\n\n")

	// Table
	tableBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderLit)

	b.WriteString(tableBox.Render(m.table.View()))
	b.WriteString("\n\n")

	// Footer
	b.WriteString(footerStyle.Render("  ↑/↓ navigate • enter stream • b bookmark • q back"))
	b.WriteString("\n")

	return b.String()
}

// Scraper functions
func searchNyaa(q string) ([]Result, error) {
	u := "https://nyaa.si/?f=0&c=0_0&s=seeders&o=desc&q=" + url.QueryEscape(q)
	return scrape(u, "nyaa")
}

func searchYTS(q string) ([]Result, error) {
	u := "https://yts.mx/api/v2/list_movies.json?query_term=" + url.QueryEscape(q)
	resp, err := http.Get(u)
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

func search1337x(q string) ([]Result, error) {
	return scrape("https://1337x.to/search/"+url.QueryEscape(q)+"/1/", "1337x")
}

func scrape(u, src string) ([]Result, error) {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := http.DefaultClient.Do(req)
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

// extractBTIH extracts the info hash from a magnet link for dedup purposes
func extractBTIH(magnet string) string {
	m := regexp.MustCompile(`(?i)btih:([a-fA-F0-9]+)`).FindStringSubmatch(magnet)
	if len(m) > 1 {
		return strings.ToUpper(m[1])
	}
	return ""
}

// searchYTSOfficial scrapes en.yts-official.biz for movies
func searchYTSOfficial(q string) ([]Result, error) {
	browseURL := "https://en.yts-official.biz/browse-movies?keyword=" + url.QueryEscape(q)
	req, _ := http.NewRequest("GET", browseURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, _ := html.Parse(resp.Body)

	// Step 1: Extract movie detail page URLs from browse results
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

	// Limit to first 5 movies
	if len(movieURLs) > 5 {
		movieURLs = movieURLs[:5]
		movieTitles = movieTitles[:5]
	}

	// Step 2: Scrape each detail page in parallel for magnet links
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []Result

	for i, mURL := range movieURLs {
		wg.Add(1)
		go func(pageURL, title string) {
			defer wg.Done()
			res := scrapeYTSOfficialDetail(pageURL, title)
			mu.Lock()
			results = append(results, res...)
			mu.Unlock()
		}(mURL, movieTitles[i])
	}

	wg.Wait()
	return results, nil
}

// scrapeYTSOfficialDetail extracts magnet links from a YTS Official movie detail page
func scrapeYTSOfficialDetail(pageURL, title string) []Result {
	req, _ := http.NewRequest("GET", pageURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	resp, err := http.DefaultClient.Do(req)
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

// collectAllText collects all text content from an HTML node tree
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
