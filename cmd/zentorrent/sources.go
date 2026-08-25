package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

var httpClient = &http.Client{Timeout: 8 * time.Second}

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
