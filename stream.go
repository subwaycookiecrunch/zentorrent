package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/torrent"
)

func streamMagnet(mag string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("> invalid magnet data ignored\n")
		}
	}()

	cfg := torrent.NewDefaultClientConfig()
	cfg.Seed = false
	cfg.ListenPort = 0
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

	cl, _ := torrent.NewClient(cfg)
	defer cl.Close()

	for _, tr := range []string{
		"udp://open.tracker.cl:1337/announce", "udp://tracker.opentrackr.org:1337/announce",
		"udp://tracker.openbittorrent.com:6969/announce", "udp://opentracker.i2p.rocks:6969/announce",
		"udp://tracker.torrent.eu.org:451/announce", "udp://open.stealth.si:80/announce", "http://nyaa.tracker.wf:7777/announce",
	} {
		mag += "&tr=" + tr
	}
	t, err := cl.AddMagnet(mag)
	if err != nil {
		fmt.Printf("> invalid magnet: %v\n", err)
		return
	}

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

	fmt.Printf("> found: %s (%.1f GB)\n> connecting peers...\n> opening vlc...\n", vid.DisplayPath(), float64(vid.Length())/1024/1024/1024)

	n := t.NumPieces()
	for i := 0; i < int(n); i++ {
		pct := float64(i) / float64(n)
		if pct < 0.05 {
			t.Piece(i).SetPriority(torrent.PiecePriorityNow)
		} else if pct < 0.15 {
			t.Piece(i).SetPriority(torrent.PiecePriorityHigh)
		} else {
			t.Piece(i).SetPriority(torrent.PiecePriorityNormal)
		}
	}
	vid.Download()

	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		rd := vid.NewReader()
		defer rd.Close()
		rd.SetResponsive()
		http.ServeContent(w, r, vid.DisplayPath(), time.Time{}, rd)
	})

	srv := &http.Server{Addr: ":8888", Handler: mux}
	go srv.ListenAndServe()
	defer srv.Close()

	bin := "vlc"
	if _, err := exec.LookPath(bin); err != nil {
		bin = `C:\Program Files\VideoLAN\VLC\vlc.exe`
	}
	cmd := exec.Command(bin, "http://localhost:8888/stream",
		"--network-caching=30000", "--file-caching=1000",
		"--disc-caching=1000", "--live-caching=1000", "--prefetch-buffer-size=131072")
	if err := cmd.Start(); err != nil {
		fmt.Printf("> error starting vlc: %v\n", err)
		return
	}
	cmd.Wait()
}

func StartExtensionServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
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
			go streamMagnet(req.Magnet)
		}
	})
	go http.ListenAndServe(":9999", mux)
}
