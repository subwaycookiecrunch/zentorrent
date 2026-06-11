package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type SubtitleResult struct {
	ID       int    `json:"id"`
	FileID   int    `json:"file_id"`
	Language string `json:"language"`
	Title    string `json:"title"`
	Download string `json:"download_url"`
}

func SearchSubtitles(query, lang string) ([]SubtitleResult, error) {
	apiKey := appConfig.Subtitles.APIKey
	if apiKey == "" {
		return nil, fmt.Errorf("no OpenSubtitles API key configured")
	}

	u := fmt.Sprintf("https://api.opensubtitles.com/api/v1/subtitles?query=%s&languages=%s",
		url.QueryEscape(query), lang)

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Api-Key", apiKey)
	req.Header.Set("User-Agent", "ZenTorrent/"+Version)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			ID         int `json:"id"`
			Attributes struct {
				Language string `json:"language"`
				Release  string `json:"release"`
				Files    []struct {
					FileID int `json:"file_id"`
				} `json:"files"`
			} `json:"attributes"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var subs []SubtitleResult
	for _, d := range result.Data {
		if len(d.Attributes.Files) == 0 {
			continue
		}
		subs = append(subs, SubtitleResult{
			ID:       d.ID,
			FileID:   d.Attributes.Files[0].FileID,
			Language: d.Attributes.Language,
			Title:    d.Attributes.Release,
		})
	}
	return subs, nil
}

func DownloadSubtitle(fileID int) (string, error) {
	apiKey := appConfig.Subtitles.APIKey
	if apiKey == "" {
		return "", fmt.Errorf("no API key")
	}

	body := fmt.Sprintf(`{"file_id": %d}`, fileID)
	req, err := http.NewRequest("POST",
		"https://api.opensubtitles.com/api/v1/download",
		strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Api-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ZenTorrent/"+Version)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var dlResp struct {
		Link string `json:"link"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dlResp); err != nil {
		return "", err
	}

	if dlResp.Link == "" {
		return "", fmt.Errorf("no download link returned")
	}

	srtResp, err := http.Get(dlResp.Link)
	if err != nil {
		return "", err
	}
	defer srtResp.Body.Close()

	tmpDir, _ := os.MkdirTemp("", "zt-subs-*")
	srtPath := filepath.Join(tmpDir, "subtitles.srt")
	f, err := os.Create(srtPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	io.Copy(f, srtResp.Body)

	vttPath := filepath.Join(tmpDir, "subtitles.vtt")
	if err := SRTtoVTT(srtPath, vttPath); err != nil {
		return srtPath, nil
	}
	return vttPath, nil
}

func SRTtoVTT(srtPath, vttPath string) error {
	data, err := os.ReadFile(srtPath)
	if err != nil {
		return err
	}

	content := string(data)
	content = strings.ReplaceAll(content, ",", ".")

	f, err := os.Create(vttPath)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintln(f, "WEBVTT")
	fmt.Fprintln(f, "")
	fmt.Fprint(f, content)

	return nil
}

func AutoFetchSubtitle(title string) string {
	if !appConfig.Subtitles.AutoFetch || appConfig.Subtitles.APIKey == "" {
		return ""
	}

	clean := cleanTitle(title)
	subs, err := SearchSubtitles(clean, appConfig.Subtitles.Language)
	if err != nil || len(subs) == 0 {
		return ""
	}

	path, err := DownloadSubtitle(subs[0].FileID)
	if err != nil {
		return ""
	}
	return path
}

func cleanTitle(s string) string {
	for _, tag := range []string{
		".mkv", ".mp4", ".avi", ".webm",
		"1080p", "720p", "2160p", "4k",
		"WEBRip", "BluRay", "BRRip", "HDRip", "WEB-DL",
		"x264", "x265", "HEVC", "AAC", "DTS",
		"YIFY", "YTS", "[YTS", "YTS.MX",
	} {
		s = strings.ReplaceAll(s, tag, "")
		s = strings.ReplaceAll(s, strings.ToLower(tag), "")
	}

	s = strings.ReplaceAll(s, ".", " ")
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "[", "")
	s = strings.ReplaceAll(s, "]", "")
	s = strings.ReplaceAll(s, "(", "")
	s = strings.ReplaceAll(s, ")", "")

	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}
