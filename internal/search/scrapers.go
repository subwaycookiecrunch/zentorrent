package search

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

var scraperHTTPClient = &http.Client{
	Timeout: 8 * time.Second,
	Transport: &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			if strings.HasPrefix(req.URL.Host, "127.0.0.1") || strings.HasPrefix(req.URL.Host, "localhost") {
				return nil, nil
			}
			return http.ProxyFromEnvironment(req)
		},
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        150,
		MaxIdleConnsPerHost: 25,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
		DisableCompression:  false,
		ForceAttemptHTTP2:   true,
	},
}

// ---------------------------------------------------------------------------
// 1. TorrentGalaxy (TGx) Scraper — Premier Bollywood, Dual Audio & Global Cinema
// ---------------------------------------------------------------------------

type TorrentGalaxyScraper struct {
	mirrors []string
}

func NewTorrentGalaxyScraper() *TorrentGalaxyScraper {
	return &TorrentGalaxyScraper{
		mirrors: []string{
			"https://tgx.rs",
			"https://torrentgalaxy.to",
			"https://tgx.to",
			"https://torrentgalaxy.mx",
		},
	}
}

func (s *TorrentGalaxyScraper) Name() string { return "torrentgalaxy" }

func (s *TorrentGalaxyScraper) Search(ctx context.Context, movie *ResolvedMovie, rawQuery string) ([]TorrentCandidate, error) {
	q := chooseQuery(movie, rawQuery)
	if q == "" {
		return nil, nil
	}

	var lastErr error
	for _, mirror := range s.mirrors {
		searchURL := fmt.Sprintf("%s/torrents.php?search=%s&sort=seeders&order=desc", mirror, url.QueryEscape(q))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml")

		resp, err := scraperHTTPClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		cands, parseErr := parseTGxHTML(resp.Body)
		resp.Body.Close()
		if parseErr != nil {
			lastErr = parseErr
			continue
		}
		if len(cands) > 0 {
			return cands, nil
		}
	}
	return nil, lastErr
}

func parseTGxHTML(r io.Reader) ([]TorrentCandidate, error) {
	doc, err := html.Parse(io.LimitReader(r, 2<<20))
	if err != nil {
		return nil, err
	}

	var cands []TorrentCandidate
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" && strings.Contains(getAttr(n, "class"), "tgxtablerow") {
			var title, magnet, sizeStr string
			var seeds int

			var innerWalk func(*html.Node)
			innerWalk = func(in *html.Node) {
				if in.Type == html.ElementNode {
					if in.Data == "a" {
						href := getAttr(in, "href")
						if strings.HasPrefix(href, "magnet:") {
							magnet = href
						} else if strings.Contains(href, "/torrent/") && strings.Contains(getAttr(in, "class"), "txlight") {
							title = getTxt(in)
						}
					} else if in.Data == "span" && strings.Contains(getAttr(in, "class"), "badge") {
						txt := getTxt(in)
						if strings.Contains(txt, "B") || strings.Contains(txt, "MB") || strings.Contains(txt, "GB") {
							sizeStr = txt
						}
					} else if in.Data == "font" && getAttr(in, "color") == "green" {
						if s, err := strconv.Atoi(getTxt(in)); err == nil {
							seeds = s
						}
					}
				}
				for c := in.FirstChild; c != nil; c = c.NextSibling {
					innerWalk(c)
				}
			}
			innerWalk(n)

			if title != "" && magnet != "" {
				ih := InfoHashFromMagnet(magnet)
				cands = append(cands, TorrentCandidate{
					InfoHash:  ih,
					Title:     title,
					Magnet:    magnet,
					Seeders:   seeds,
					SizeBytes: parseSizeBytes(sizeStr),
					Source:    "tgx",
				})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return cands, nil
}

// ---------------------------------------------------------------------------
// 2. BitSearch Scraper — Massive Index (Hindi, Regional & Global)
// ---------------------------------------------------------------------------

type BitSearchScraper struct {
	base string
}

func NewBitSearchScraper() *BitSearchScraper {
	return &BitSearchScraper{base: "https://bitsearch.to"}
}

func (s *BitSearchScraper) Name() string { return "bitsearch" }

func (s *BitSearchScraper) Search(ctx context.Context, movie *ResolvedMovie, rawQuery string) ([]TorrentCandidate, error) {
	q := chooseQuery(movie, rawQuery)
	if q == "" {
		return nil, nil
	}

	searchURL := fmt.Sprintf("%s/api/v1/search?q=%s&sort=seeders", s.base, url.QueryEscape(q))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/json")

	resp, err := scraperHTTPClient.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		var res struct {
			Results []struct {
				Name      string `json:"name"`
				InfoHash  string `json:"info_hash"`
				MagnetURL string `json:"magnet"`
				Size      int64  `json:"size"`
				Seeders   int    `json:"seeders"`
				Leechers  int    `json:"leechers"`
			} `json:"results"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&res); err == nil && len(res.Results) > 0 {
			var out []TorrentCandidate
			for _, r := range res.Results {
				mag := r.MagnetURL
				if mag == "" && r.InfoHash != "" {
					mag = fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", r.InfoHash, url.QueryEscape(r.Name))
				}
				out = append(out, TorrentCandidate{
					InfoHash:  strings.ToLower(r.InfoHash),
					Title:     r.Name,
					Magnet:    mag,
					SizeBytes: r.Size,
					Seeders:   r.Seeders,
					Leechers:  r.Leechers,
					Source:    "bitsearch",
				})
			}
			return out, nil
		}
	} else if resp != nil {
		resp.Body.Close()
	}

	// HTML search fallback
	htmlURL := fmt.Sprintf("%s/search?q=%s&sort=seeders", s.base, url.QueryEscape(q))
	hReq, err := http.NewRequestWithContext(ctx, http.MethodGet, htmlURL, nil)
	if err != nil {
		return nil, err
	}
	hReq.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	hResp, err := scraperHTTPClient.Do(hReq)
	if err != nil {
		return nil, err
	}
	defer hResp.Body.Close()

	return parseBitSearchHTML(hResp.Body)
}

func parseBitSearchHTML(r io.Reader) ([]TorrentCandidate, error) {
	doc, err := html.Parse(io.LimitReader(r, 2<<20))
	if err != nil {
		return nil, err
	}

	var cands []TorrentCandidate
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "li" && strings.Contains(getAttr(n, "class"), "card") {
			var title, magnet, sizeStr string
			var seeds int

			var innerWalk func(*html.Node)
			innerWalk = func(in *html.Node) {
				if in.Type == html.ElementNode {
					if in.Data == "a" {
						href := getAttr(in, "href")
						if strings.HasPrefix(href, "magnet:") {
							magnet = href
						} else if strings.HasPrefix(href, "/torrents/") {
							title = getTxt(in)
						}
					} else if in.Data == "div" && strings.Contains(getAttr(in, "class"), "stats") {
						txt := getTxt(in)
						if strings.Contains(txt, "GB") || strings.Contains(txt, "MB") {
							sizeStr = txt
						}
					} else if in.Data == "font" || strings.Contains(getAttr(in, "class"), "seeders") {
						if s, err := strconv.Atoi(strings.TrimSpace(getTxt(in))); err == nil {
							seeds = s
						}
					}
				}
				for c := in.FirstChild; c != nil; c = c.NextSibling {
					innerWalk(c)
				}
			}
			innerWalk(n)

			if title != "" && magnet != "" {
				ih := InfoHashFromMagnet(magnet)
				cands = append(cands, TorrentCandidate{
					InfoHash:  ih,
					Title:     title,
					Magnet:    magnet,
					Seeders:   seeds,
					SizeBytes: parseSizeBytes(sizeStr),
					Source:    "bitsearch",
				})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return cands, nil
}

// ---------------------------------------------------------------------------
// 3. SolidTorrents Scraper — Fast REST API
// ---------------------------------------------------------------------------

type SolidTorrentsScraper struct {
	base string
}

func NewSolidTorrentsScraper() *SolidTorrentsScraper {
	return &SolidTorrentsScraper{base: "https://solidtorrents.to"}
}

func (s *SolidTorrentsScraper) Name() string { return "solidtorrents" }

func (s *SolidTorrentsScraper) Search(ctx context.Context, movie *ResolvedMovie, rawQuery string) ([]TorrentCandidate, error) {
	q := chooseQuery(movie, rawQuery)
	if q == "" {
		return nil, nil
	}

	u := fmt.Sprintf("%s/api/v1/search?q=%s&sort=seeders", s.base, url.QueryEscape(q))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/json")

	resp, err := scraperHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var payload struct {
		Results []struct {
			Title    string `json:"title"`
			InfoHash string `json:"infoHash"`
			Magnet   string `json:"magnet"`
			Size     int64  `json:"size"`
			Swarm    struct {
				Seeders  int `json:"seeders"`
				Leechers int `json:"leechers"`
			} `json:"swarm"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	out := make([]TorrentCandidate, 0, len(payload.Results))
	for _, r := range payload.Results {
		if r.Title == "" {
			continue
		}
		mag := r.Magnet
		if mag == "" && r.InfoHash != "" {
			mag = fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", r.InfoHash, url.QueryEscape(r.Title))
		}
		out = append(out, TorrentCandidate{
			InfoHash:  strings.ToLower(r.InfoHash),
			Title:     r.Title,
			Magnet:    mag,
			SizeBytes: r.Size,
			Seeders:   r.Swarm.Seeders,
			Leechers:  r.Swarm.Leechers,
			Source:    "solidtorrents",
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 4. Resilient 1337x Scraper — Multi-Mirror Fallback
// ---------------------------------------------------------------------------

type Resilient1337xScraper struct {
	mirrors []string
}

func NewResilient1337xScraper() *Resilient1337xScraper {
	return &Resilient1337xScraper{
		mirrors: []string{
			"https://1337x.to",
			"https://1337x.st",
			"https://1337x.ws",
			"https://1337x.eu",
			"https://1337x.so",
		},
	}
}

func (s *Resilient1337xScraper) Name() string { return "1337x" }

func (s *Resilient1337xScraper) Search(ctx context.Context, movie *ResolvedMovie, rawQuery string) ([]TorrentCandidate, error) {
	q := chooseQuery(movie, rawQuery)
	if q == "" {
		return nil, nil
	}

	var lastErr error
	for _, mirror := range s.mirrors {
		u := fmt.Sprintf("%s/search/%s/1/", mirror, url.QueryEscape(q))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml")

		resp, err := scraperHTTPClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		type interimRow struct {
			title   string
			pageURL string
			seeds   int
			sizeStr string
		}
		var rows []interimRow

		doc, docErr := html.Parse(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		if docErr != nil {
			lastErr = docErr
			continue
		}

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
					a := findNode(tds[0], "a")
					if a != nil {
						href := getAttr(a, "href")
						if strings.HasPrefix(href, "/torrent/") {
							t := getTxt(a)
							sCount, _ := strconv.Atoi(getTxt(tds[1]))
							sizeText := getTxt(tds[4])
							rows = append(rows, interimRow{
								title:   t,
								pageURL: mirror + href,
								seeds:   sCount,
								sizeStr: sizeText,
							})
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)

		if len(rows) == 0 {
			continue
		}

		// Resolve magnet links in parallel
		type resolved struct {
			idx int
			mag string
		}
		ch := make(chan resolved, len(rows))
		sem := make(chan struct{}, 6)
		var wg sync.WaitGroup

		for i, r := range rows {
			wg.Add(1)
			go func(idx int, detailURL string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				mag := resolve1337xMagnetDoc(ctx, detailURL)
				ch <- resolved{idx: idx, mag: mag}
			}(i, r.pageURL)
		}
		go func() {
			wg.Wait()
			close(ch)
		}()

		mags := make(map[int]string)
		for r := range ch {
			if r.mag != "" {
				mags[r.idx] = r.mag
			}
		}

		var candidates []TorrentCandidate
		for i, r := range rows {
			if mag, ok := mags[i]; ok && mag != "" {
				candidates = append(candidates, TorrentCandidate{
					InfoHash:  InfoHashFromMagnet(mag),
					Title:     r.title,
					Magnet:    mag,
					Seeders:   r.seeds,
					SizeBytes: parseSizeBytes(r.sizeStr),
					Source:    "1337x",
				})
			}
		}

		if len(candidates) > 0 {
			return candidates, nil
		}
	}
	return nil, lastErr
}

func resolve1337xMagnetDoc(ctx context.Context, pageURL string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := scraperHTTPClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	doc, err := html.Parse(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	return findMagnetNode(doc)
}

// ---------------------------------------------------------------------------
// 5. ThePirateBay (TPB) Scraper via APIBay
// ---------------------------------------------------------------------------

type TPBScraper struct{}

func NewTPBScraper() *TPBScraper { return &TPBScraper{} }

func (s *TPBScraper) Name() string { return "tpb" }

func (s *TPBScraper) Search(ctx context.Context, movie *ResolvedMovie, rawQuery string) ([]TorrentCandidate, error) {
	q := chooseQuery(movie, rawQuery)
	if q == "" {
		return nil, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://apibay.org/q.php?q="+url.QueryEscape(q), nil)
	if err != nil {
		return nil, err
	}
	resp, err := scraperHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var items []struct {
		Name     string `json:"name"`
		InfoHash string `json:"info_hash"`
		Seeders  string `json:"seeders"`
		Leechers string `json:"leechers"`
		Size     string `json:"size"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}

	var out []TorrentCandidate
	for _, it := range items {
		if it.InfoHash == "0000000000000000000000000000000000000000" {
			continue
		}
		seeds, _ := strconv.Atoi(it.Seeders)
		leech, _ := strconv.Atoi(it.Leechers)
		sizeBytes, _ := strconv.ParseInt(it.Size, 10, 64)
		mag := fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", it.InfoHash, url.QueryEscape(it.Name))

		out = append(out, TorrentCandidate{
			InfoHash:  strings.ToLower(it.InfoHash),
			Title:     it.Name,
			Magnet:    mag,
			Seeders:   seeds,
			Leechers:  leech,
			SizeBytes: sizeBytes,
			Source:    "tpb",
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 6. YTS Scraper
// ---------------------------------------------------------------------------

type YTSScraper struct{}

func NewYTSScraper() *YTSScraper { return &YTSScraper{} }

func (s *YTSScraper) Name() string { return "yts" }

func (s *YTSScraper) Search(ctx context.Context, movie *ResolvedMovie, rawQuery string) ([]TorrentCandidate, error) {
	q := chooseQuery(movie, rawQuery)
	if q == "" {
		return nil, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://yts.mx/api/v2/list_movies.json?query_term="+url.QueryEscape(q), nil)
	if err != nil {
		return nil, err
	}
	resp, err := scraperHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var r struct {
		Data struct {
			Movies []struct {
				Title    string `json:"title"`
				Torrents []struct {
					Hash      string `json:"hash"`
					Quality   string `json:"quality"`
					Size      string `json:"size"`
					Seeds     int    `json:"seeds"`
					Peers     int    `json:"peers"`
					SizeBytes int64  `json:"size_bytes"`
				} `json:"torrents"`
			} `json:"movies"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}

	var out []TorrentCandidate
	for _, m := range r.Data.Movies {
		for _, t := range m.Torrents {
			title := fmt.Sprintf("%s [%s]", m.Title, t.Quality)
			mag := fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", t.Hash, url.QueryEscape(title))
			out = append(out, TorrentCandidate{
				InfoHash:  strings.ToLower(t.Hash),
				Title:     title,
				Magnet:    mag,
				Seeders:   t.Seeds,
				Leechers:  t.Peers,
				SizeBytes: t.SizeBytes,
				Source:    "yts",
			})
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 7. EZTV Scraper (TV Shows)
// ---------------------------------------------------------------------------

type EZTVScraper struct{}

func NewEZTVScraper() *EZTVScraper { return &EZTVScraper{} }

func (s *EZTVScraper) Name() string { return "eztv" }

func (s *EZTVScraper) Search(ctx context.Context, movie *ResolvedMovie, rawQuery string) ([]TorrentCandidate, error) {
	q := chooseQuery(movie, rawQuery)
	if q == "" {
		return nil, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://eztv.re/api/get-torrents?limit=50&page=1", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := scraperHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var data struct {
		Torrents []struct {
			Title     string `json:"title"`
			MagnetURL string `json:"magnet_url"`
			Hash      string `json:"hash"`
			Seeds     int    `json:"seeds"`
			Peers     int    `json:"peers"`
			SizeBytes int64  `json:"size_bytes"`
		} `json:"torrents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	lq := strings.ToLower(q)
	var out []TorrentCandidate
	for _, t := range data.Torrents {
		if !strings.Contains(strings.ToLower(t.Title), lq) {
			continue
		}
		mag := t.MagnetURL
		if mag == "" && t.Hash != "" {
			mag = fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", t.Hash, url.QueryEscape(t.Title))
		}
		if mag == "" {
			continue
		}
		out = append(out, TorrentCandidate{
			InfoHash:  strings.ToLower(t.Hash),
			Title:     t.Title,
			Magnet:    mag,
			Seeders:   t.Seeds,
			Leechers:  t.Peers,
			SizeBytes: t.SizeBytes,
			Source:    "eztv",
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 8. Nyaa RSS Scraper (Anime & Asian Cinema)
// ---------------------------------------------------------------------------

type NyaaScraper struct{}

func NewNyaaScraper() *NyaaScraper { return &NyaaScraper{} }

func (s *NyaaScraper) Name() string { return "nyaa" }

func (s *NyaaScraper) Search(ctx context.Context, movie *ResolvedMovie, rawQuery string) ([]TorrentCandidate, error) {
	q := chooseQuery(movie, rawQuery)
	if q == "" {
		return nil, nil
	}

	u := "https://nyaa.si/?page=rss&q=" + url.QueryEscape(q) + "&c=1_2&s=seeders&o=desc"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := scraperHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	type nyaaItem struct {
		Title   string `xml:"title"`
		Link    string `xml:"link"`
		Seeders string `xml:"seeders"`
	}
	type nyaaChannel struct {
		Items []nyaaItem `xml:"item"`
	}
	type nyaaFeed struct {
		Channel nyaaChannel `xml:"channel"`
	}

	var feed nyaaFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, err
	}

	var out []TorrentCandidate
	for _, it := range feed.Channel.Items {
		if strings.HasPrefix(it.Link, "magnet:") {
			seeds, _ := strconv.Atoi(it.Seeders)
			out = append(out, TorrentCandidate{
				InfoHash: InfoHashFromMagnet(it.Link),
				Title:    it.Title,
				Magnet:   it.Link,
				Seeders:  seeds,
				Source:   "nyaa",
			})
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Helpers & Default Registry
// ---------------------------------------------------------------------------

func chooseQuery(movie *ResolvedMovie, rawQuery string) string {
	if movie != nil && movie.Title != "" {
		return movie.Title
	}
	return strings.TrimSpace(rawQuery)
}

func parseSizeBytes(s string) int64 {
	s = strings.ToUpper(strings.TrimSpace(s))
	re := regexp.MustCompile(`([\d.]+)\s*(GB|G|MB|M|KB|K|B)?`)
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	unit := ""
	if len(m) > 2 {
		unit = m[2]
	}
	switch unit {
	case "GB", "G":
		return int64(val * 1024 * 1024 * 1024)
	case "MB", "M":
		return int64(val * 1024 * 1024)
	case "KB", "K":
		return int64(val * 1024)
	default:
		return int64(val)
	}
}

func findNode(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if res := findNode(c, tag); res != nil {
			return res
		}
	}
	return nil
}

func findMagnetNode(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "a" {
		href := getAttr(n, "href")
		if strings.HasPrefix(href, "magnet:?") {
			return href
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if res := findMagnetNode(c); res != "" {
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
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(getTxt(c))
	}
	return strings.TrimSpace(sb.String())
}

// DefaultScrapers returns the production array of scrapers used by the Aggregator.
func DefaultScrapers() []Scraper {
	return []Scraper{
		NewTorrentGalaxyScraper(),
		NewBitSearchScraper(),
		NewSolidTorrentsScraper(),
		NewResilient1337xScraper(),
		NewTPBScraper(),
		NewYTSScraper(),
		NewEZTVScraper(),
		NewNyaaScraper(),
	}
}
