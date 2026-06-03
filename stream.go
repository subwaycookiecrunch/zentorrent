package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/torrent"
)


type StreamState struct {
	mu           sync.RWMutex
	Filename     string  `json:"filename"`
	FileSize     int64   `json:"filesize"`
	FileSizeFmt  string  `json:"filesize_fmt"`
	Completed    int64   `json:"completed"`
	Progress     float64 `json:"progress"`
	Speed        int64   `json:"speed"`
	SpeedFmt     string  `json:"speed_fmt"`
	Peers        int     `json:"peers"`
	ETA          string  `json:"eta"`
	Status       string  `json:"status"`
	Buffered     float64 `json:"buffered"`
	Resolution   string  `json:"resolution"`
	SubtitlePath string  `json:"-"`
}

var currentStream = &StreamState{}

func streamTorrent(uri string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("> invalid torrent data ignored\n")
		}
	}()

	currentStream.mu.Lock()
	currentStream.Status = "connecting"
	currentStream.mu.Unlock()

	cfg := torrent.NewDefaultClientConfig()
	cfg.Seed = false
	cfg.ListenPort = 0

	cfg.EstablishedConnsPerTorrent = appConfig.MaxPeers
	cfg.HalfOpenConnsPerTorrent = appConfig.MaxPeers / 2
	cfg.DropMutuallyCompletePeers = true

	cfg.DhtStartingNodes = func(network string) dht.StartingNodesGetter {
		return func() ([]dht.Addr, error) {
			return dht.ResolveHostPorts([]string{
				"router.bittorrent.com:6881", "router.utorrent.com:6881",
				"dht.transmissionbt.com:6881", "dht.aelitis.com:6881",
			})
		}
	}

	tmpDir, _ := os.MkdirTemp("", "zt-*")
	cfg.DataDir = tmpDir
	defer os.RemoveAll(tmpDir)

	cl, err := torrent.NewClient(cfg)
	if err != nil {
		fmt.Printf("> error creating client: %v\n", err)
		return
	}
	defer cl.Close()

	if strings.HasPrefix(uri, "magnet:") {
		for _, tr := range []string{
			"udp://open.tracker.cl:1337/announce",
			"udp://tracker.opentrackr.org:1337/announce",
			"udp://tracker.openbittorrent.com:6969/announce",
			"udp://opentracker.i2p.rocks:6969/announce",
			"udp://tracker.torrent.eu.org:451/announce",
			"udp://open.stealth.si:80/announce",
			"http://nyaa.tracker.wf:7777/announce",
		} {
			uri += "&tr=" + tr
		}
	}

	var t *torrent.Torrent
	if strings.HasPrefix(uri, "magnet:") {
		t, err = cl.AddMagnet(uri)
	} else {
		t, err = cl.AddTorrentFromFile(uri)
	}

	if err != nil {
		fmt.Printf("> invalid torrent: %v\n", err)
		return
	}

	currentStream.mu.Lock()
	currentStream.Status = "metadata"
	currentStream.mu.Unlock()

	<-t.GotInfo()

	var vid *torrent.File
	for _, f := range t.Files() {
		if vid == nil || f.Length() > vid.Length() {
			vid = f
		}
	}
	if vid == nil {
		return
	}

	currentStream.mu.Lock()
	currentStream.Filename = vid.DisplayPath()
	currentStream.FileSize = vid.Length()
	currentStream.FileSizeFmt = formatSizeBytes(vid.Length())
	currentStream.Resolution = parseRes(vid.DisplayPath())
	currentStream.Status = "buffering"
	currentStream.mu.Unlock()

	fmt.Printf("> found: %s (%s)\n", vid.DisplayPath(), formatSizeBytes(vid.Length()))
	fmt.Printf("> connecting peers...\n")

	n := t.NumPieces()
	lastPiece := int(n) - 1

	for i := 0; i < int(n); i++ {
		pct := float64(i) / float64(n)
		switch {
		case pct < 0.03:
			t.Piece(i).SetPriority(torrent.PiecePriorityNow) // First 3%: immediate
		case pct < 0.10:
			t.Piece(i).SetPriority(torrent.PiecePriorityHigh) // Next 7%: high
		case pct < 0.20:
			t.Piece(i).SetPriority(torrent.PiecePriorityNormal) // Next 10%: normal
		default:
			t.Piece(i).SetPriority(torrent.PiecePriorityNormal)
		}
	}

	// MP4 moov atom is often at the end, players need it to seek
	if lastPiece > 0 {
		t.Piece(lastPiece).SetPriority(torrent.PiecePriorityNow)
	}

	vid.Download()

	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		rd := vid.NewReader()
		defer rd.Close()
		rd.SetResponsive()
		rd.SetReadahead(20 * 1024 * 1024) // 20MB readahead
		http.ServeContent(w, r, vid.DisplayPath(), time.Time{}, rd)
	})

	mux.HandleFunc("/subtitle", func(w http.ResponseWriter, r *http.Request) {
		currentStream.mu.RLock()
		subPath := currentStream.SubtitlePath
		currentStream.mu.RUnlock()
		if subPath != "" {
			http.ServeFile(w, r, subPath)
		} else {
			http.Error(w, "no subtitle", http.StatusNotFound)
		}
	})

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		currentStream.mu.RLock()
		json.NewEncoder(w).Encode(currentStream)
		currentStream.mu.RUnlock()
	})

	streamAddr := fmt.Sprintf(":%d", appConfig.StreamPort)
	srv := &http.Server{Addr: streamAddr, Handler: mux}
	go srv.ListenAndServe()
	defer srv.Close()

	streamURL := fmt.Sprintf("http://localhost:%d/stream", appConfig.StreamPort)

	go func() {
		subPath := AutoFetchSubtitle(vid.DisplayPath())
		if subPath != "" {
			currentStream.mu.Lock()
			currentStream.SubtitlePath = subPath
			currentStream.mu.Unlock()
		}
	}()

	AddHistory(HistoryEntry{
		Title:      vid.DisplayPath(),
		Magnet:     uri,
		Resolution: currentStream.Resolution,
		FileSize:   currentStream.FileSizeFmt,
	})

	// pre-buffer: wait for at least 1% of pieces before opening player
	targetPieces := int(n) / 100
	if targetPieces < 1 {
		targetPieces = 1
	}
	currentStream.mu.Lock()
	currentStream.Status = "pre-buffering"
	currentStream.mu.Unlock()

	for {
		ready := 0
		for i := 0; i < int(n) && i < targetPieces+5; i++ {
			if t.Piece(i).State().Complete {
				ready++
			}
		}
		if ready >= targetPieces {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	currentStream.mu.Lock()
	currentStream.Status = "streaming"
	currentStream.mu.Unlock()

	go updateStats(t, vid)

	fmt.Printf("> opening player (%s)...\n", DetectPlayer())
	playerCmd, err := LaunchPlayer(streamURL, "")
	if err != nil {
		fmt.Printf("> error starting player: %v\n", err)
		fmt.Printf("> stream available at: %s\n", streamURL)
		Notify("ZenTorrent", "Player failed — stream at "+streamURL)
		select {} // Keep stream alive
	} else {
		Notify("ZenTorrent", "Now streaming: "+vid.DisplayPath())
	}

	if playerCmd != nil {
		playerCmd.Wait()
	} else {
		select {}
	}

	currentStream.mu.Lock()
	currentStream.Status = "stopped"
	currentStream.mu.Unlock()
}

func downloadTorrent(uri string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("> invalid torrent data ignored\n")
		}
	}()

	currentStream.mu.Lock()
	currentStream.Status = "connecting"
	currentStream.mu.Unlock()

	cfg := torrent.NewDefaultClientConfig()
	cfg.Seed = false
	cfg.ListenPort = 0

	cfg.EstablishedConnsPerTorrent = appConfig.MaxPeers
	cfg.HalfOpenConnsPerTorrent = appConfig.MaxPeers / 2
	cfg.DropMutuallyCompletePeers = true

	cfg.DhtStartingNodes = func(network string) dht.StartingNodesGetter {
		return func() ([]dht.Addr, error) {
			return dht.ResolveHostPorts([]string{
				"router.bittorrent.com:6881", "router.utorrent.com:6881",
				"dht.transmissionbt.com:6881", "dht.aelitis.com:6881",
			})
		}
	}

	os.MkdirAll(appConfig.DownloadDir, 0755)
	cfg.DataDir = appConfig.DownloadDir

	cl, err := torrent.NewClient(cfg)
	if err != nil {
		fmt.Printf("> error creating client: %v\n", err)
		return
	}
	defer cl.Close()

	if strings.HasPrefix(uri, "magnet:") {
		for _, tr := range []string{
			"udp://open.tracker.cl:1337/announce",
			"udp://tracker.opentrackr.org:1337/announce",
			"udp://tracker.openbittorrent.com:6969/announce",
			"udp://opentracker.i2p.rocks:6969/announce",
			"udp://tracker.torrent.eu.org:451/announce",
			"udp://open.stealth.si:80/announce",
			"http://nyaa.tracker.wf:7777/announce",
		} {
			uri += "&tr=" + tr
		}
	}

	var t *torrent.Torrent
	if strings.HasPrefix(uri, "magnet:") {
		t, err = cl.AddMagnet(uri)
	} else {
		t, err = cl.AddTorrentFromFile(uri)
	}

	if err != nil {
		fmt.Printf("> invalid torrent: %v\n", err)
		return
	}

	currentStream.mu.Lock()
	currentStream.Status = "metadata"
	currentStream.mu.Unlock()

	<-t.GotInfo()
	t.DownloadAll()

	totalSize := t.Info().TotalLength()

	currentStream.mu.Lock()
	currentStream.Filename = t.Name()
	currentStream.FileSize = totalSize
	currentStream.FileSizeFmt = formatSizeBytes(totalSize)
	currentStream.Resolution = parseRes(t.Name())
	currentStream.Status = "downloading"
	currentStream.mu.Unlock()

	updateStatsDownload(t, totalSize)

	currentStream.mu.Lock()
	currentStream.Status = "stopped"
	currentStream.mu.Unlock()
}

func updateStatsDownload(t *torrent.Torrent, total int64) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var prevCompleted int64

	for range ticker.C {
		completed := t.BytesCompleted()
		speed := (completed - prevCompleted) * 2 // *2 because tick is 500ms
		if speed < 0 {
			speed = 0
		}
		prevCompleted = completed

		progress := 0.0
		if total > 0 {
			progress = float64(completed) / float64(total) * 100
		}

		eta := "∞"
		if speed > 0 && total > completed {
			secs := (total - completed) / speed
			eta = fmtETAv2(secs)
		}

		status := "downloading"
		if progress >= 100 {
			status = "complete"
		}

		currentStream.mu.Lock()
		currentStream.Completed = completed
		currentStream.Progress = progress
		currentStream.Speed = speed
		currentStream.SpeedFmt = fmtSpeed(speed)
		currentStream.Peers = len(t.PeerConns())
		currentStream.ETA = eta
		currentStream.Status = status
		currentStream.mu.Unlock()

		if progress >= 100 {
			Notify("ZenTorrent", "Download complete: "+currentStream.Filename)
			break
		}
	}
}

func updateStats(t *torrent.Torrent, vid *torrent.File) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var prevCompleted int64

	for range ticker.C {
		completed := t.BytesCompleted()
		total := vid.Length()
		speed := (completed - prevCompleted) * 2 // *2 because tick is 500ms
		if speed < 0 {
			speed = 0
		}
		prevCompleted = completed

		progress := 0.0
		if total > 0 {
			progress = float64(completed) / float64(total) * 100
		}

		eta := "∞"
		if speed > 0 && total > completed {
			secs := (total - completed) / speed
			eta = fmtETAv2(secs)
		}

		bufferPieces := t.NumPieces() / 20
		if bufferPieces < 1 {
			bufferPieces = 1
		}
		bufferedCount := 0
		for i := 0; i < int(bufferPieces); i++ {
			if t.Piece(i).State().Complete {
				bufferedCount++
			}
		}
		buffered := float64(bufferedCount) / float64(bufferPieces) * 100

		status := "streaming"
		if progress >= 100 {
			status = "complete"
		} else if buffered < 50 {
			status = "buffering"
		}

		currentStream.mu.Lock()
		currentStream.Completed = completed
		currentStream.Progress = progress
		currentStream.Speed = speed
		currentStream.SpeedFmt = fmtSpeed(speed)
		currentStream.Peers = len(t.PeerConns())
		currentStream.ETA = eta
		currentStream.Buffered = buffered
		currentStream.Status = status
		currentStream.mu.Unlock()

		reprioritize(t, completed, total)

		if progress >= 100 {
			Notify("ZenTorrent", "Download complete: "+currentStream.Filename)
			break
		}
	}
}

func reprioritize(t *torrent.Torrent, completed, total int64) {
	if total == 0 {
		return
	}

	n := t.NumPieces()
	playbackPiece := int(float64(completed) / float64(total) * float64(n))

	for i := 0; i < int(n); i++ {
		if t.Piece(i).State().Complete {
			continue
		}

		dist := i - playbackPiece
		switch {
		case dist < 0:
			t.Piece(i).SetPriority(torrent.PiecePriorityNone)
		case dist < 10:
			t.Piece(i).SetPriority(torrent.PiecePriorityNow)
		case dist < 50:
			t.Piece(i).SetPriority(torrent.PiecePriorityHigh)
		default:
			t.Piece(i).SetPriority(torrent.PiecePriorityNormal)
		}
	}
}

func StartExtensionServer() {
	mux := http.NewServeMux()

	// Legacy endpoint (v1 compat)
	mux.HandleFunc("/stream", handleExtensionRequest)
	// New v2 endpoint
	mux.HandleFunc("/api/magnet", handleExtensionRequest)

	addr := fmt.Sprintf(":%d", appConfig.ExtPort)
	go http.ListenAndServe(addr, mux)
}

func handleExtensionRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var req struct{ Magnet string }
	json.NewDecoder(r.Body).Decode(&req)
	if req.Magnet != "" {
		go streamTorrent(req.Magnet)
	}
}

func formatSizeBytes(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func fmtETAv2(s int64) string {
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	if s < 3600 {
		return fmt.Sprintf("%dm %ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh %dm", s/3600, (s%3600)/60)
}

func fmtSpeed(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B/s", b)
	}
	if b < 1024*1024 {
		return fmt.Sprintf("%.1f KB/s", float64(b)/1024)
	}
	return fmt.Sprintf("%.2f MB/s", float64(b)/(1024*1024))
}
