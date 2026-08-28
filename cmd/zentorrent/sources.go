package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

var httpClient = &http.Client{
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

func searchTPB(ctx context.Context, q string) ([]Result, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://apibay.org/q.php?q="+url.QueryEscape(q), nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
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
		Category string `json:"category"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		// A block page or truncated body must surface as a failed source,
		// not a silent green checkmark.
		return nil, err
	}

	res := make([]Result, 0, len(items))
	for _, it := range items {
		if it.InfoHash == "0000000000000000000000000000000000000000" {
			continue
		}
		seeds, _ := strconv.Atoi(it.Seeders)
		if seeds == 0 {
			continue
		}
		sizeBytes, _ := strconv.ParseInt(it.Size, 10, 64)
		mag := fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", it.InfoHash, url.QueryEscape(it.Name))
		cat := "Other"
		switch it.Category {
		case "201", "202", "207", "209", "211":
			cat = "Movies"
		case "205", "208", "212":
			cat = "TV Shows"
		}
		res = append(res, Result{
			Title:      it.Name,
			Magnet:     mag,
			Resolution: parseRes(it.Name),
			Seeders:    seeds,
			Size:       formatSizeBytes(sizeBytes),
			Category:   cat,
			Source:     "tpb",
		})
	}
	return res, nil
}

func searchEZTV(ctx context.Context, q string) ([]Result, error) {
	u := "https://eztv.re/api/get-torrents?limit=50&page=1"
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := httpClient.Do(req)
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
			Season    string `json:"season"`
			Episode   string `json:"episode"`
		} `json:"torrents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	lq := strings.ToLower(q)
	var res []Result
	for _, t := range data.Torrents {
		if !strings.Contains(strings.ToLower(t.Title), lq) {
			continue
		}
		if t.Seeds == 0 {
			continue
		}
		mag := t.MagnetURL
		if mag == "" && t.Hash != "" {
			mag = fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", t.Hash, url.QueryEscape(t.Title))
		}
		if mag == "" {
			continue
		}

		ep := 0
		if t.Episode != "" {
			ep, _ = strconv.Atoi(t.Episode)
		}
		res = append(res, Result{
			Title:      t.Title,
			Magnet:     mag,
			Resolution: parseRes(t.Title),
			Seeders:    t.Seeds,
			Category:   "TV Shows",
			Source:     "eztv",
			Episode:    ep,
		})
	}
	return res, nil
}

type rssItem struct {
	Title string `xml:"title"`
	Link  string `xml:"link"`
	GUID  string `xml:"guid"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssFeed struct {
	Channel rssChannel `xml:"channel"`
}

func searchSubsPlease(ctx context.Context, q string) ([]Result, error) {
	u := "https://subsplease.org/rss/?t&r=1080"
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var feed rssFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, err
	}

	lq := strings.ToLower(q)
	var res []Result
	for _, item := range feed.Channel.Items {
		if !strings.Contains(strings.ToLower(item.Title), lq) {
			continue
		}
		mag := item.Link
		if !strings.HasPrefix(mag, "magnet:") {
			mag = item.GUID
		}
		if !strings.HasPrefix(mag, "magnet:") {
			continue
		}
		res = append(res, Result{
			Title:      item.Title,
			Magnet:     mag,
			Resolution: parseRes(item.Title),
			Seeders:    seedersUnknown, // feed carries no seeder data
			Category:   "Anime",
			Source:     "subsplease",
			Episode:    parseEp(item.Title),
		})
	}
	return res, nil
}

func searchNyaaRSS(ctx context.Context, q string) ([]Result, error) {
	u := "https://nyaa.si/?page=rss&q=" + url.QueryEscape(q) + "&c=1_2&s=seeders&o=desc"
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
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

	var res []Result
	for _, item := range feed.Channel.Items {
		seeds, _ := strconv.Atoi(item.Seeders)
		if mag := item.Link; strings.HasPrefix(mag, "magnet:") {
			res = append(res, Result{
				Title:      item.Title,
				Magnet:     mag,
				Resolution: parseRes(item.Title),
				Seeders:    seeds,
				Category:   "Anime",
				Source:     "nyaa",
				Episode:    parseEp(item.Title),
			})
		}
	}
	return res, nil
}

func searchBTDig(ctx context.Context, q string) ([]Result, error) {
	u := "https://btdig.com/search?q=" + url.QueryEscape(q) + "&order=0"
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// A reset mid-body makes html.Parse return (nil, err); walking nil would
	// panic the whole app.
	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, err
	}

	var res []Result
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" && getAttr(n, "class") == "one_result" {
			titleNode := findClass(n, "torrent_name")
			magnetNode := findClass(n, "torrent_magnet")
			if titleNode != nil && magnetNode != nil {
				aTitle := find(titleNode, "a")
				aMag := find(magnetNode, "a")
				if aTitle != nil && aMag != nil {
					t := getTxt(aTitle)
					m := getAttr(aMag, "href")
					if t != "" && strings.HasPrefix(m, "magnet:") {
						res = append(res, Result{
							Title:      t,
							Magnet:     m,
							Source:     "btdig",
							Category:   guessCategory(t),
							Resolution: parseRes(t),
							Seeders:    seedersUnknown, // scrape exposes no seeders
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

	return res, nil
}

func findClass(n *html.Node, class string) *html.Node {
	if n.Type == html.ElementNode && getAttr(n, "class") == class {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if res := findClass(c, class); res != nil {
			return res
		}
	}
	return nil
}

func searchTorrentGalaxy(ctx context.Context, q string) ([]Result, error) {
	mirrors := []string{
		"https://tgx.rs",
		"https://torrentgalaxy.to",
		"https://tgx.to",
		"https://torrentgalaxy.mx",
	}

	var lastErr error
	for _, mirror := range mirrors {
		searchURL := fmt.Sprintf("%s/torrents.php?search=%s&sort=seeders&order=desc", mirror, url.QueryEscape(q))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml")

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		doc, docErr := html.Parse(resp.Body)
		resp.Body.Close()
		if docErr != nil {
			lastErr = docErr
			continue
		}

		var res []Result
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
					res = append(res, Result{
						Title:      title,
						Magnet:     magnet,
						Resolution: parseRes(title),
						Seeders:    seeds,
						Size:       sizeStr,
						Category:   guessCategory(title),
						Source:     "torrentgalaxy",
						Episode:    parseEp(title),
					})
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)

		if len(res) > 0 {
			return res, nil
		}
	}
	return nil, lastErr
}

func searchBitSearch(ctx context.Context, q string) ([]Result, error) {
	apiURL := "https://bitsearch.to/api/v1/search?q=" + url.QueryEscape(q) + "&sort=seeders"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		var r struct {
			Results []struct {
				Name      string `json:"name"`
				InfoHash  string `json:"info_hash"`
				MagnetURL string `json:"magnet"`
				Size      int64  `json:"size"`
				Seeders   int    `json:"seeders"`
			} `json:"results"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&r); err == nil && len(r.Results) > 0 {
			var res []Result
			for _, item := range r.Results {
				mag := item.MagnetURL
				if mag == "" && item.InfoHash != "" {
					mag = fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", item.InfoHash, url.QueryEscape(item.Name))
				}
				if mag == "" {
					continue
				}
				res = append(res, Result{
					Title:      item.Name,
					Magnet:     mag,
					Resolution: parseRes(item.Name),
					Seeders:    item.Seeders,
					Size:       formatSizeBytes(item.Size),
					Category:   guessCategory(item.Name),
					Source:     "bitsearch",
					Episode:    parseEp(item.Name),
				})
			}
			return res, nil
		}
	} else if resp != nil {
		resp.Body.Close()
	}

	// HTML fallback
	htmlURL := "https://bitsearch.to/search?q=" + url.QueryEscape(q) + "&sort=seeders"
	hReq, err := http.NewRequestWithContext(ctx, http.MethodGet, htmlURL, nil)
	if err != nil {
		return nil, err
	}
	hReq.Header.Set("User-Agent", "Mozilla/5.0")
	hResp, err := httpClient.Do(hReq)
	if err != nil {
		return nil, err
	}
	defer hResp.Body.Close()

	doc, err := html.Parse(hResp.Body)
	if err != nil {
		return nil, err
	}

	var res []Result
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
				res = append(res, Result{
					Title:      title,
					Magnet:     magnet,
					Resolution: parseRes(title),
					Seeders:    seeds,
					Size:       sizeStr,
					Category:   guessCategory(title),
					Source:     "bitsearch",
					Episode:    parseEp(title),
				})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return res, nil
}

func searchSolidTorrents(ctx context.Context, q string) ([]Result, error) {
	u := "https://solidtorrents.to/api/v1/search?q=" + url.QueryEscape(q) + "&sort=seeders"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
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
				Seeders int `json:"seeders"`
			} `json:"swarm"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	var res []Result
	for _, r := range payload.Results {
		if r.Title == "" {
			continue
		}
		mag := r.Magnet
		if mag == "" && r.InfoHash != "" {
			mag = fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", r.InfoHash, url.QueryEscape(r.Title))
		}
		if mag == "" {
			continue
		}
		res = append(res, Result{
			Title:      r.Title,
			Magnet:     mag,
			Resolution: parseRes(r.Title),
			Seeders:    r.Swarm.Seeders,
			Size:       formatSizeBytes(r.Size),
			Category:   guessCategory(r.Title),
			Source:     "solidtorrents",
			Episode:    parseEp(r.Title),
		})
	}
	return res, nil
}

