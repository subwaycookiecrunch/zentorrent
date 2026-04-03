package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

type Result struct {
	Title, Magnet, Resolution, Source string
	Seeders, Episode                  int
}

func searchNyaa(q string) ([]Result, error) {
	fmt.Printf("searching nyaa for %q...\n", q)
	u := "https://nyaa.si/?f=0&c=0_0&s=seeders&o=desc&q=" + url.QueryEscape(q)
	return scrape(u, "nyaa")
}

func searchYTS(q string) ([]Result, error) {
	fmt.Printf("searching yts for %q...\n", q)
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
	fmt.Printf("searching 1337x for %q...\n", q)
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

func selectEp(res []Result) []Result {
	eps := make(map[int]bool)
	var list []int
	for _, r := range res {
		if !eps[r.Episode] {
			eps[r.Episode] = true
			list = append(list, r.Episode)
		}
	}
	sort.Ints(list)
	fmt.Println("\nselect episode:")
	for i, e := range list {
		fmt.Printf("  %d) Episode %d\n", i+1, e)
	}
	v := wait(len(list))
	var out []Result
	for _, r := range res {
		if r.Episode == list[v-1] {
			out = append(out, r)
		}
	}
	return out
}

func selectRes(res []Result) Result {
	sort.Slice(res, func(i, j int) bool { return res[i].Seeders > res[j].Seeders })
	fmt.Println("\nselect resolution:")
	done := make(map[string]bool)
	var list []Result
	for _, r := range res {
		if !done[r.Resolution] {
			done[r.Resolution] = true
			list = append(list, r)
			fmt.Printf("  %d) %s (%d seeders)\n", len(list), r.Resolution, r.Seeders)
		}
	}
	v := wait(len(list))
	return list[v-1]
}

func wait(max int) int {
	sc := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !sc.Scan() {
			os.Exit(0)
		}
		i, _ := strconv.Atoi(strings.TrimSpace(sc.Text()))
		if i > 0 && i <= max {
			return i
		}
	}
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
