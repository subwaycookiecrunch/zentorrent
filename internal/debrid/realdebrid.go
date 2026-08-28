package debrid

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RealDebrid resolves magnets to instant direct links when the swarm is
// cached on RD's servers (https://api.real-debrid.com/rest/1.0).
type RealDebrid struct {
	CommonClient
	base string
}

func NewRealDebrid(apiKey string) *RealDebrid {
	return NewRealDebridWithHTTP(apiKey, nil)
}

func NewRealDebridWithHTTP(apiKey string, hc *http.Client) *RealDebrid {
	return &RealDebrid{
		CommonClient: newCommonWithHTTP(apiKey, hc),
		base:         "https://api.real-debrid.com/rest/1.0",
	}
}

func (r *RealDebrid) Name() string { return "Real-Debrid" }

// rdAvailability maps the instantAvailability response shape:
// {"<hash>": {"rd": [ {"<file-index>": {...}}, ... ]}} — non-empty "rd"
// arrays mean at least one cached file set.
type rdAvailability map[string]struct {
	RD []map[string]json.RawMessage `json:"rd"`
}

// rdTorrentInfo is the subset of /torrents/info we need.
type rdTorrentInfo struct {
	ID     string   `json:"id"`
	Status string   `json:"status"` // magnet_error | magnet_conversion | waiting_files | downloading | downloaded
	Links  []string `json:"links"`
	Files  []struct {
		ID       int    `json:"id"`
		Path     string `json:"path"`
		Bytes    int64  `json:"bytes"`
		Selected int    `json:"selected"`
	} `json:"files"`
}

// rdUnrestrict is the /unrestrict/link response.
type rdUnrestrict struct {
	Download string `json:"download"`
	Filename string `json:"filename"`
	Filesize int64  `json:"filesize"`
}

func (r *RealDebrid) get(ctx context.Context, path string, out any) error {
	if r.APIKey == "" {
		return ErrUnauthorized
	}
	if err := r.limiter.Wait(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	return doJSON(r.hc, req, out)
}

func (r *RealDebrid) postForm(ctx context.Context, path string, form url.Values, out any) error {
	if r.APIKey == "" {
		return ErrUnauthorized
	}
	if err := r.limiter.Wait(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base+path,
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return doJSON(r.hc, req, out)
}

// InstantAvailable reports whether the infohash is web-cached on RD.
func (r *RealDebrid) InstantAvailable(ctx context.Context, infoHash string) (bool, error) {
	var avl rdAvailability
	if err := r.get(ctx, "/torrents/instantAvailability/"+infoHash, &avl); err != nil {
		return false, err
	}
	entry, ok := avl[strings.ToLower(infoHash)]
	return ok && len(entry.RD) > 0, nil
}

// Resolve implements Provider. Fast path: cache check first so uncached
// torrents fail in one round-trip instead of a full add+poll cycle.
func (r *RealDebrid) Resolve(ctx context.Context, item MediaItem) (*StreamSource, error) {
	if item.Magnet == "" && item.InfoHash == "" {
		return nil, ErrNoStream
	}
	hash := strings.ToLower(item.InfoHash)
	if hash == "" {
		hash = InfoHashFromMagnet(item.Magnet)
	}
	if hash != "" {
		cached, err := r.InstantAvailable(ctx, hash)
		if err == nil && !cached {
			return nil, ErrNotCached
		}
		// On check errors fall through to the add path; RD may still know it.
	}
	return r.resolveMagnet(ctx, item)
}

func (r *RealDebrid) resolveMagnet(ctx context.Context, item MediaItem) (*StreamSource, error) {
	magnet := item.Magnet
	if magnet == "" {
		magnet = "magnet:?xt=urn:btih:" + item.InfoHash
	}

	var added struct {
		ID string `json:"id"`
	}
	form := url.Values{"magnet": {magnet}}
	if err := r.postForm(ctx, "/torrents/addMagnet", form, &added); err != nil {
		return nil, fmt.Errorf("rd addMagnet: %w", err)
	}
	if added.ID == "" {
		return nil, ErrNoStream
	}
	defer func() {
		_ = r.postForm(context.WithoutCancel(ctx), "/torrents/delete/"+added.ID, url.Values{}, nil)
	}()

	// Select the largest file, then poll until RD finishes conversion.
	info, err := r.pollInfo(ctx, added.ID)
	if err != nil {
		return nil, err
	}
	biggest, size := pickBiggest(info.Files)
	if biggest < 0 || len(info.Links) == 0 {
		return nil, ErrNotCached
	}

	// Links are per selected-file; with a single selection the link list has
	// exactly one entry. Restrict it into a direct download URL.
	var un rdUnrestrict
	linkIdx := linkIndexFor(info, biggest)
	if linkIdx >= len(info.Links) {
		linkIdx = len(info.Links) - 1
	}
	if err := r.postForm(ctx, "/unrestrict/link",
		url.Values{"link": {info.Links[linkIdx]}}, &un); err != nil {
		return nil, fmt.Errorf("rd unrestrict: %w", err)
	}
	if un.Download == "" {
		return nil, ErrNoStream
	}

	quality := QualityFromSize(size)
	title := cleanName(info.Files[biggest].Path)
	return &StreamSource{
		Type:         StreamDebrid,
		URL:          un.Download,
		Title:        title,
		Quality:      quality,
		ProviderName: r.Name(),
	}, nil
}

func (r *RealDebrid) pollInfo(ctx context.Context, id string) (*rdTorrentInfo, error) {
	deadline := time.Now().Add(90 * time.Second)
	for {
		var info rdTorrentInfo
		if err := r.get(ctx, "/torrents/info/"+id, &info); err != nil {
			return nil, err
		}
		switch info.Status {
		case "downloaded":
			return &info, nil
		case "magnet_error", "virus", "dead":
			return nil, ErrNoStream
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("rd: timeout waiting for %s", id)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func linkIndexFor(info *rdTorrentInfo, fileID int) int {
	// With single-file selection RD exposes exactly one unrestricted link;
	// multi-selections keep index alignment with chosen files order.
	for i := range info.Files {
		if info.Files[i].ID == fileID {
			return i
		}
	}
	return 0
}

func pickBiggest(files []struct {
	ID       int    `json:"id"`
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Selected int    `json:"selected"`
}) (int, int64) {
	best, size := -1, int64(0)
	for i, f := range files {
		if f.Bytes > size && !looksLikeSample(f.Path) {
			best, size = i, f.Bytes
		}
	}
	if best == -1 && len(files) > 0 {
		best, size = 0, files[0].Bytes
	}
	return best, size
}

func looksLikeSample(path string) bool {
	p := strings.ToLower(path)
	return strings.Contains(p, ".sample.") || strings.HasSuffix(p, "-sample")
}

func cleanName(path string) string {
	path = strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "/")
	if i := strings.LastIndex(path, "/"); i >= 0 {
		path = path[i+1:]
	}
	return path
}

// doJSON performs the request and decodes the body, mapping auth failures.
func doJSON(hc *http.Client, req *http.Request, out any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNoStream
	default:
		return fmt.Errorf("debrid: HTTP %d: %.120s", resp.StatusCode, string(body))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// InfoHashFromMagnet extracts the 40-char hex infohash from a magnet URI
// (self-contained copy so debrid stays decoupled from internal/search).
func InfoHashFromMagnet(magnet string) string {
	u, err := url.Parse(magnet)
	if err != nil {
		return ""
	}
	for _, xt := range u.Query()["xt"] {
		const prefix = "urn:btih:"
		if !strings.HasPrefix(strings.ToLower(xt), prefix) {
			continue
		}
		v := xt[len(prefix):]
		if len(v) == 40 {
			return strings.ToLower(v)
		}
		if len(v) == 32 {
			if raw, err := base32.StdEncoding.DecodeString(strings.ToUpper(v)); err == nil && len(raw) == 20 {
				return hex.EncodeToString(raw)
			}
		}
	}
	return ""
}

// QualityFromSize buckets a file size into a human quality tier.
func QualityFromSize(bytes int64) string {
	switch {
	case bytes >= 12<<30:
		return "4K REMUX"
	case bytes >= 8<<30:
		return "4K"
	case bytes >= 3<<30:
		return "1080p"
	case bytes >= 1<<30:
		return "1080p"
	case bytes >= 500<<20:
		return "720p"
	default:
		return "SD"
	}
}
