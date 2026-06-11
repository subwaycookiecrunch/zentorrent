package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/torrent"
)

type StreamState struct {
	mu             sync.RWMutex
	Filename       string  `json:"filename"`
	FileSize       int64   `json:"filesize"`
	FileSizeFmt    string  `json:"filesize_fmt"`
	Completed      int64   `json:"completed"`
	Progress       float64 `json:"progress"`
	Speed          int64   `json:"speed"`
	SpeedFmt       string  `json:"speed_fmt"`
	Peers          int     `json:"peers"`
	ETA            string  `json:"eta"`
	Status         string  `json:"status"`
	Buffered       float64 `json:"buffered"`
	Resolution     string  `json:"resolution"`
	SubtitlePath   string  `json:"-"`
	PlaybackPosSec float64 `json:"playback_pos_sec"`
}

var currentStream = &StreamState{}

func (s *StreamState) updateStatus(st string) {
	s.mu.Lock()
	s.Status = st
	s.mu.Unlock()
}

func baseTorrentConfig(dir string) *torrent.ClientConfig {
	cfg := torrent.NewDefaultClientConfig()
	cfg.Seed = false
	cfg.ListenPort = 0
	cfg.DataDir = dir
	cfg.DropMutuallyCompletePeers = true
	cfg.DhtStartingNodes = func(network string) dht.StartingNodesGetter {
		return func() ([]dht.Addr, error) {
			return dht.ResolveHostPorts([]string{
				"router.bittorrent.com:6881",
				"router.utorrent.com:6881",
				"dht.transmissionbt.com:6881",
				"dht.aelitis.com:6881",
				"router.silotis.us:6881",
				"dht.libtorrent.org:25401",
				"tracker.opentrackr.org:1337",
				"dht.aegir.sexy:6969",
			})
		}
	}
	return cfg
}

func streamTorrent(uri string, backups []string, downgrades []string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("> invalid torrent data ignored\n")
		}
	}()

	currentStream.updateStatus("connecting")

	tmpDir, _ := os.MkdirTemp("", "zt-*")
	defer os.RemoveAll(tmpDir)

	cfg := baseTorrentConfig(tmpDir)
	cfg.EstablishedConnsPerTorrent = 200
	cfg.HalfOpenConnsPerTorrent = 150
	cfg.NominalDialTimeout = 2 * time.Second

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
			"udp://exodus.desync.com:6969/announce",
			"udp://tracker.tiny-vps.com:6969/announce",
			"udp://tracker.moeking.me:6969/announce",
			"udp://p4p.arenabg.com:1337/announce",
			"udp://tracker.dler.org:6969/announce",
			"udp://9.rarbg.com:2810/announce",
			"udp://tracker2.dler.org:80/announce",
			"udp://open.demonii.com:1337/announce",
			"udp://tracker.cubonegro.xyz:6969/announce",
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

	currentStream.updateStatus("metadata")

	metaTimer := time.NewTimer(45 * time.Second)
	select {
	case <-t.GotInfo():
		metaTimer.Stop()
	case <-metaTimer.C:
		currentStream.updateStatus("timeout")
		fmt.Printf("> timed out waiting for peers (45s). torrent may be dead.\n")
		Notify("ZenTorrent", "Connection timed out — no peers found")
		return
	}

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

	nowCount := 50
	if n < int(nowCount) {
		nowCount = int(n)
	}
	highCount := nowCount * 3
	if highCount > int(n) {
		highCount = int(n)
	}

	for i := 0; i < int(n); i++ {
		p := torrent.PiecePriorityNormal
		if i < nowCount {
			p = torrent.PiecePriorityNow
		} else if i < highCount {
			p = torrent.PiecePriorityHigh
		}
		t.Piece(i).SetPriority(p)
	}

	vid.Download()

	var activeVidMu sync.RWMutex
	activeVid := vid

	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		activeVidMu.RLock()
		v := activeVid
		activeVidMu.RUnlock()

		rd := v.NewReader()
		defer rd.Close()
		rd.SetResponsive()
		rd.SetReadahead(20 * 1024 * 1024)
		http.ServeContent(w, r, v.DisplayPath(), time.Time{}, rd)
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

	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	streamAddr := fmt.Sprintf(":%d", appConfig.StreamPort)
	srv := &http.Server{Addr: streamAddr, Handler: mux}
	listenErr := make(chan error, 1)
	go func() {
		listenErr <- srv.ListenAndServe()
	}()
	defer srv.Close()

	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-listenErr:
		if strings.Contains(err.Error(), "address already in use") {
			fmt.Printf("> port %d occupied, killing stale process...\n", appConfig.StreamPort)
			killPort(appConfig.StreamPort)
			time.Sleep(500 * time.Millisecond)
			srv = &http.Server{Addr: streamAddr, Handler: mux}
			listenErr = make(chan error, 1)
			go func() {
				listenErr <- srv.ListenAndServe()
			}()
			time.Sleep(100 * time.Millisecond)
			select {
			case err := <-listenErr:
				fmt.Printf("> FATAL: stream server failed on port %d even after cleanup: %v\n", appConfig.StreamPort, err)
				return
			default:
			}
		} else {
			fmt.Printf("> FATAL: stream server failed to start on port %d: %v\n", appConfig.StreamPort, err)
			return
		}
	default:
	}

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

	pieceLength := t.Info().PieceLength
	var targetPieces int
	if pieceLength > 0 {
		targetPieces = int((256 * 1024) / pieceLength)
	}
	if targetPieces < 1 {
		targetPieces = 1
	}
	currentStream.updateStatus("pre-buffering")

	maxWait := time.After(5 * time.Second)
	for {
		ready := 0
		checkRange := targetPieces + 20
		if checkRange > int(n) {
			checkRange = int(n)
		}
		for i := 0; i < checkRange; i++ {
			if t.Piece(i).State().Complete {
				ready++
			}
		}
		if ready >= targetPieces {
			break
		}
		select {
		case <-maxWait:
			goto launch
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}
launch:
	firstComplete := 0
	for i := 0; i < int(n) && i < 50; i++ {
		if t.Piece(i).State().Complete {
			firstComplete++
		} else {
			break
		}
	}
	fmt.Printf("> first %d/%d sequential pieces ready (piece size: %s)\n", firstComplete, targetPieces, formatSizeBytes(pieceLength))

	currentStream.updateStatus("streaming")

	go updateStats(t, vid)

	if !isPartyHost && !isPartyJoiner {
		go func() {
			time.Sleep(8 * time.Second)
			for len(backups) > 0 || len(downgrades) > 0 {
				time.Sleep(2 * time.Second)

				currentStream.mu.RLock()
				speed := currentStream.Speed
				currentStream.mu.RUnlock()

				if speed < 500*1024 {
					var backupMag string
					var isDowngrade bool

					if len(backups) > 0 {
						backupMag = backups[0]
						backups = backups[1:]
					} else if len(downgrades) > 0 {
						backupMag = downgrades[0]
						downgrades = downgrades[1:]
						isDowngrade = true
					} else {
						return
					}

					bt, err := cl.AddMagnet(backupMag)
					if err != nil {
						continue
					}
					<-bt.GotInfo()
					bVid := bt.Files()[0]
					for _, f := range bt.Files() {
						if f.Length() > bVid.Length() {
							bVid = f
						}
					}
					bVid.Download()

					pos, err := MPVGetTimePos()
					if err != nil {
						return
					}

					activeVidMu.Lock()
					activeVid = bVid
					activeVidMu.Unlock()

					swapURL := fmt.Sprintf("http://localhost:%d/stream?t=%d", appConfig.StreamPort, time.Now().Unix())
					MPVHotSwap(swapURL, pos)

					if isDowngrade {
						Notify("ZenTorrent", "Downgrading resolution to prevent buffering (Adaptive Bitrate)")
					}

					t.Drop()
					t = bt
				}
			}
		}()
	}

	startTime := 0
	history := GetHistory(50)
	magHash := extractBTIH(uri)
	for _, h := range history {
		if h.Progress > 0 && extractBTIH(h.Magnet) == magHash {
			startTime = int(h.Progress)
			if startTime > 5 {
				startTime -= 5
			}
			break
		}
	}

	fmt.Printf("> opening player (%s)...\n", DetectPlayer())
	fmt.Printf("> stream: %s\n", streamURL)

	ready := false
	for attempt := 0; attempt < 5; attempt++ {
		if checkStream(streamURL) {
			ready = true
			break
		}
		if attempt == 0 {
			fmt.Printf("> waiting for stream to become ready")
		} else {
			fmt.Print(".")
		}
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
	if ready {
		fmt.Println(" ok")
	} else {
		fmt.Println(" timeout")
		fmt.Printf("> stream may not be ready. try: curl -v %s\n", streamURL)
	}

	playerCmd, err := LaunchPlayer(streamURL, "", startTime)
	if err != nil {
		fmt.Printf("> error starting player: %v\n", err)
		fmt.Printf("> stream available at: %s\n", streamURL)
		Notify("ZenTorrent", "Player failed — stream at "+streamURL)
		select {}
	} else {
		Notify("ZenTorrent", "Now streaming: "+vid.DisplayPath())
	}

	if playerCmd != nil {
		exitCh := make(chan error, 1)
		go func() {
			exitCh <- playerCmd.Wait()
		}()

		select {
		case err := <-exitCh:
			if err != nil {
				fmt.Printf("> player exited: %v\n", err)
				logPath := filepath.Join(os.TempDir(), "zt_mpv.log")
				if data, readErr := os.ReadFile(logPath); readErr == nil && len(data) > 0 {
					fmt.Fprintf(os.Stderr, "> mpv log (%s):\n%s\n", logPath, string(data))
				}
			} else {
				fmt.Println("> player closed")
			}
		}
	} else {
		select {}
	}

	currentStream.mu.RLock()
	finalProgress := currentStream.PlaybackPosSec
	currentStream.mu.RUnlock()
	if finalProgress > 5 {
		AddHistory(HistoryEntry{
			Title:      vid.DisplayPath(),
			Magnet:     uri,
			Resolution: currentStream.Resolution,
			FileSize:   currentStream.FileSizeFmt,
			Progress:   finalProgress,
		})
	}

	currentStream.updateStatus("stopped")
}

func downloadTorrent(uri string, delay time.Duration) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("> invalid torrent data ignored\n")
		}
	}()

	currentStream.mu.Lock()
	if delay > 0 {
		currentStream.Status = "scheduled"
		currentStream.ETA = fmt.Sprintf("Starts in %v", delay)
	} else {
		currentStream.Status = "connecting"
	}
	currentStream.mu.Unlock()

	if delay > 0 {
		timer := time.NewTimer(delay)
		ticker := time.NewTicker(1 * time.Second)
		startTime := time.Now()
		
		waitLoop:
		for {
			select {
			case <-timer.C:
				ticker.Stop()
				break waitLoop
			case now := <-ticker.C:
				rem := delay - now.Sub(startTime)
				if rem < 0 {
					rem = 0
				}
				currentStream.mu.Lock()
				currentStream.ETA = fmt.Sprintf("Starts in %s", rem.Round(time.Second))
				currentStream.mu.Unlock()
			}
		}

		currentStream.mu.Lock()
		currentStream.Status = "connecting"
		currentStream.ETA = "∞"
		currentStream.mu.Unlock()
	}

	os.MkdirAll(appConfig.DownloadDir, 0755)
	cfg := baseTorrentConfig(appConfig.DownloadDir)
	cfg.EstablishedConnsPerTorrent = appConfig.MaxPeers
	cfg.HalfOpenConnsPerTorrent = appConfig.MaxPeers / 2

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
			"udp://exodus.desync.com:6969/announce",
			"udp://tracker.tiny-vps.com:6969/announce",
			"udp://tracker.moeking.me:6969/announce",
			"udp://p4p.arenabg.com:1337/announce",
			"udp://tracker.dler.org:6969/announce",
			"udp://9.rarbg.com:2810/announce",
			"udp://tracker2.dler.org:80/announce",
			"udp://open.demonii.com:1337/announce",
			"udp://tracker.cubonegro.xyz:6969/announce",
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

	currentStream.updateStatus("metadata")

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

	currentStream.updateStatus("stopped")
}

func updateStatsDownload(t *torrent.Torrent, total int64) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var prevCompleted int64

	for range ticker.C {
		completed := t.BytesCompleted()
		speed := (completed - prevCompleted) * 2
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
			
			fullPath := filepath.Join(appConfig.DownloadDir, currentStream.Filename)
			go fireWebhook("download_complete", currentStream.Filename, fullPath)
			
			break
		}
	}
}

func updateStats(t *torrent.Torrent, vid *torrent.File) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var prevCompleted int64
	var prefetched bool

	for range ticker.C {
		pos, err := MPVGetTimePos()
		if err == nil {
			currentStream.mu.Lock()
			currentStream.PlaybackPosSec = pos
			currentStream.mu.Unlock()

			if !prefetched {
				dur, errDur := MPVGetDuration()
				if errDur == nil && dur > 0 && (pos/dur) >= 0.8 {
					prefetched = true
					go prefetchNext()
				}
			}
		}

		completed := t.BytesCompleted()
		total := vid.Length()
		speed := (completed - prevCompleted) * 2
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
		case dist < 30:
			t.Piece(i).SetPriority(torrent.PiecePriorityNow)
		case dist < 100:
			t.Piece(i).SetPriority(torrent.PiecePriorityHigh)
		default:
			t.Piece(i).SetPriority(torrent.PiecePriorityNormal)
		}
	}
}

func StartExtensionServer() {
	mux := http.NewServeMux()

	mux.HandleFunc("/stream", handleExtensionRequest)
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
		go streamTorrent(req.Magnet, nil, nil)
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
		return fmt.Sprintf("%.2f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	}
	return fmt.Sprintf("%d B", b)
}

func prefetchNext() {
	nextItem := GlobalPlaylist.GetNext()
	if nextItem == nil {
		return
	}

	GlobalPlaylist.SetStatus(GlobalPlaylist.Current+1, "prefetching")
	fmt.Printf("\n[Prefetch] Starting background prefetch for: %s\n", nextItem.Title)

	cfg := torrent.NewDefaultClientConfig()
	cfg.Seed = false
	cfg.ListenPort = 0
	cfg.DataDir = appConfig.DownloadDir
	
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		return
	}
	
	var t *torrent.Torrent
	if strings.HasPrefix(nextItem.Magnet, "magnet:") {
		t, _ = cl.AddMagnet(nextItem.Magnet)
	} else {
		t, _ = cl.AddTorrentFromFile(nextItem.Magnet)
	}
	
	if t == nil {
		cl.Close()
		return
	}
	
	<-t.GotInfo()
	
	if t.NumPieces() > 0 {
		t.Piece(0).SetPriority(torrent.PiecePriorityNow)
	}
	
	go func() {
		time.Sleep(2 * time.Minute)
		cl.Close()
	}()
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

func killPort(port int) {
	out, err := exec.Command("lsof", fmt.Sprintf("-i:%d", port), "-t").Output()
	if err != nil || len(out) == 0 {
		return
	}
	for _, pidStr := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid == os.Getpid() {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		proc.Signal(syscall.SIGTERM)
	}
	time.Sleep(300 * time.Millisecond)
	for _, pidStr := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid == os.Getpid() {
			continue
		}
		proc, _ := os.FindProcess(pid)
		if proc != nil {
			proc.Signal(syscall.SIGKILL)
		}
	}
}
