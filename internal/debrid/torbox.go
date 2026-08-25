package debrid

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TorBox resolves magnets via torbox.app's torrent cache
// (https://torbox.app/api).
type TorBox struct {
	CommonClient
	base string
}

func NewTorBox(apiKey string) *TorBox {
	return &TorBox{
		CommonClient: newCommon(apiKey),
		base:         "https://torbox.app/api",
	}
}

func (t *TorBox) Name() string { return "TorBox" }

type torboxResponse struct {
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

type torboxTorrent struct {
	ID            int    `json:"id"`
	Hash          string `json:"hash"`
	Name          string `json:"name"`
	Size          int64  `json:"size"`
	Cached        bool   `json:"cached"`
	DownloadState string `json:"download_state"` // cached | downloading | completed | …
	Files         []struct {
		ID   int64  `json:"file_id"`
		Name string `json:"short_name"`
		Size int64  `json:"size"`
	} `json:"files"`
}

func (t *TorBox) get(ctx context.Context, path string, out any) error {
	if t.APIKey == "" {
		return ErrUnauthorized
	}
	if err := t.limiter.Wait(ctx); err != nil {
		return err
	}
	u := t.base + path
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+sep+"api_key="+url.QueryEscape(t.APIKey), nil)
	if err != nil {
		return err
	}
	return t.do(req, out)
}

// createTorrent uploads a magnet for processing.
func (t *TorBox) createTorrent(ctx context.Context, magnet string) (int, error) {
	if t.APIKey == "" {
		return 0, ErrUnauthorized
	}
	if err := t.limiter.Wait(ctx); err != nil {
		return 0, err
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		defer pw.Close()
		mw.WriteField("magnet", magnet)
		mw.WriteField("seed", "3")
		mw.WriteField("allow_zip", "false")
		mw.Close()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/torrents/createtorrent", pr)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+t.APIKey)

	resp, err := t.hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return 0, ErrUnauthorized
	default:
		return 0, fmt.Errorf("torbox: HTTP %d: %.120s", resp.StatusCode, string(body))
	}

	var parsed struct {
		torboxResponse
		Data struct {
			TorrentID int `json:"torrent_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, err
	}
	if !parsed.Success || parsed.Data.TorrentID == 0 {
		return 0, fmt.Errorf("torbox: create failed: %s", parsed.Error)
	}
	return parsed.Data.TorrentID, nil
}

func (t *TorBox) do(req *http.Request, out any) error {
	resp, err := t.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return ErrUnauthorized
	default:
		return fmt.Errorf("torbox: HTTP %d: %.120s", resp.StatusCode, string(body))
	}
	var wrap torboxResponse
	if err := json.Unmarshal(body, &wrap); err != nil {
		return err
	}
	if !wrap.Success {
		return fmt.Errorf("torbox: %s", wrap.Error)
	}
	if out != nil && len(wrap.Data) > 0 {
		return json.Unmarshal(wrap.Data, out)
	}
	return nil
}

func (t *TorBox) doMultipart(req *http.Request, out any) error {
	resp, err := t.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return ErrUnauthorized
	default:
		return fmt.Errorf("torbox: HTTP %d: %.120s", resp.StatusCode, string(body))
	}
	return json.Unmarshal(body, out)
}

// findCached checks the user's list / cache for this hash.
func (t *TorBox) findTorrent(ctx context.Context, hash string, cachedOnly bool) (*torboxTorrent, error) {
	path := "/torrents/mylist?search=" + url.QueryEscape(hash) +
		"&bypass_cache=false"
	if cachedOnly {
		path += "&cached=true"
	}
	var torrents []torboxTorrent
	if err := t.get(ctx, path, &torrents); err != nil {
		return nil, err
	}
	for i := range torrents {
		if strings.EqualFold(torrents[i].Hash, hash) {
			return &torrents[i], nil
		}
	}
	return nil, nil
}

// Resolve implements Provider.
func (t *TorBox) Resolve(ctx context.Context, item MediaItem) (*StreamSource, error) {
	hash := strings.ToLower(item.InfoHash)
	if hash == "" {
		hash = InfoHashFromMagnet(item.Magnet)
	}
	if item.Magnet == "" && hash == "" {
		return nil, ErrNoStream
	}

	// Cached lookup first — zero cost when present.
	if hash != "" {
		if found, err := t.findTorrent(ctx, hash, true); err == nil && found != nil {
			return t.requestDownloadLink(ctx, found, item)
		}
	}

	tor, err := t.createTorrent(ctx, item.Magnet)
	if err != nil {
		return nil, err
	}
	_ = tor

	deadline := time.Now().Add(90 * time.Second)
	for {
		found, err := t.findTorrent(ctx, hash, false)
		if err != nil {
			return nil, err
		}
		if found != nil && (found.DownloadState == "completed" || found.Cached) {
			return t.requestDownloadLink(ctx, found, item)
		}
		if time.Now().After(deadline) {
			return nil, ErrNotCached
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// requestDownloadLink asks TorBox for a direct web-download link for the
// largest file in the finished torrent.
func (t *TorBox) requestDownloadLink(ctx context.Context, tor *torboxTorrent, item MediaItem) (*StreamSource, error) {
	var links []struct {
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
		Link     string `json:"link"`
	}
	if err := t.get(ctx, fmt.Sprintf("/torrents/requestdl/%d?zip_link=false&torrent_id=%d", tor.ID, tor.ID), &links); err != nil {
		return nil, err
	}

	best := -1
	var bestSize int64
	for i, l := range links {
		if l.Link != "" && l.Size > bestSize && !looksLikeSample(l.Filename) {
			best, bestSize = i, l.Size
		}
	}
	if best < 0 {
		return nil, ErrNoStream
	}

	title := tor.Name
	if title == "" {
		title = cleanName(links[best].Filename)
	}
	return &StreamSource{
		Type:         StreamDebrid,
		URL:          links[best].Link,
		Title:        title,
		Quality:      QualityFromSize(bestSize),
		ProviderName: t.Name(),
	}, nil
}
