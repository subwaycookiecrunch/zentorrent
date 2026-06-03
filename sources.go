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
)

var httpClient = &http.Client{Timeout: 8 * time.Second}

func searchTPB(ctx context.Context, q string) ([]Result, error) {
	u := "https://apibay.org/q.php?q=" + url.QueryEscape(q)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
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
	json.NewDecoder(resp.Body).Decode(&items)

	var res []Result
	for _, it := range items {
		if it.InfoHash == "0000000000000000000000000000000000000000" {
			continue
		}
		seeds, _ := strconv.Atoi(it.Seeders)
		if seeds == 0 {
			continue
		}
		mag := fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", it.InfoHash, url.QueryEscape(it.Name))
		res = append(res, Result{
			Title:      it.Name,
			Magnet:     mag,
			Resolution: parseRes(it.Name),
			Seeders:    seeds,
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
	json.NewDecoder(resp.Body).Decode(&data)

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
	xml.NewDecoder(resp.Body).Decode(&feed)

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
			Seeders:    50,
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
	xml.NewDecoder(resp.Body).Decode(&feed)

	var res []Result
	for _, item := range feed.Channel.Items {
		seeds, _ := strconv.Atoi(item.Seeders)
		mag := item.Link
		if !strings.HasPrefix(mag, "magnet:") {
			continue
		}
		res = append(res, Result{
			Title:      item.Title,
			Magnet:     mag,
			Resolution: parseRes(item.Title),
			Seeders:    seeds,
			Source:     "nyaa",
			Episode:    parseEp(item.Title),
		})
	}
	return res, nil
}
