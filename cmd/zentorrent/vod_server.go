package main

// vod_server.go — the single :<StreamPort> HTTP server for the whole app.
// Bound once per process; the torrent file/subtitle handlers swap per
// stream, and the embedded web dashboard lives at "/" so phones, tablets
// and TVs on the LAN can search + play while the desktop TUI is running.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
)

type vodServer struct {
	mu sync.Mutex

	fileAtom     atomicFile
	subtitlePath string

	srv  *http.Server
	webM http.Handler
}

// atomicFile holds the *torrent.File currently being served.
type atomicFile struct {
	mu   sync.RWMutex
	file *torrent.File
}

func (a *atomicFile) Set(f *torrent.File) {
	a.mu.Lock()
	a.file = f
	a.mu.Unlock()
}

func (a *atomicFile) Get() *torrent.File {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.file
}

var vod = &vodServer{}

// ensureVODServer binds StreamPort exactly once, killing any stale process
// that still holds the port (legacy behaviour preserved).
func ensureVODServer() error {
	vod.mu.Lock()
	defer vod.mu.Unlock()
	if vod.srv != nil {
		return nil
	}

	mux := http.NewServeMux()

	// Tier-1..3 playback of the active torrent file.
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		f := vod.fileAtom.Get()
		if f == nil {
			http.Error(w, "no active stream", http.StatusServiceUnavailable)
			return
		}
		rd := f.NewReader()
		defer rd.Close()
		rd.SetResponsive()
		rd.SetReadahead(20 * 1024 * 1024)
		http.ServeContent(w, r, f.DisplayPath(), time.Time{}, rd)
	})

	mux.HandleFunc("/subtitle", func(w http.ResponseWriter, r *http.Request) {
		vod.mu.Lock()
		sub := vod.subtitlePath
		vod.mu.Unlock()
		if sub == "" {
			http.Error(w, "no subtitle", http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, sub)
	})

	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		writeJSON(w, currentStreamSnapshot())
	})

	// Web dashboard + REST + WebSocket.
	if services != nil && services.Web != nil {
		mux.Handle("/", services.Web.Handler())
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, "ZenTorrent VOD endpoint — start a stream from the app.")
		})
	}

	addr := fmt.Sprintf(":%d", appConfig.StreamPort)
	bind := func() error {
		srv := &http.Server{Addr: addr, Handler: mux}
		vod.srv = srv
		err := srv.ListenAndServe()
		vod.srv = nil
		return err
	}

	errCh := make(chan error, 1)
	go func() { errCh <- bind() }()

	select {
	case err := <-errCh:
		if strings.Contains(err.Error(), "address already in use") {
			fmt.Printf("> port %d occupied, killing stale process...\n", appConfig.StreamPort)
			killPort(appConfig.StreamPort)
			time.Sleep(500 * time.Millisecond)
			go func() { errCh <- bind() }()
			select {
			case err := <-errCh:
				return fmt.Errorf("stream server failed even after cleanup: %w", err)
			case <-time.After(700 * time.Millisecond):
			}
		} else if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("stream server: %w", err)
		}
	case <-time.After(700 * time.Millisecond):
		// bound successfully
	}

	OnShutdown(func() {
		vod.mu.Lock()
		srv := vod.srv
		vod.mu.Unlock()
		if srv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
		}
	})
	return nil
}

// SetActiveFile points /stream at this file (nil clears it).
func (v *vodServer) SetActiveFile(f *torrent.File) { v.fileAtom.Set(f) }

// SetSubtitle registers an external subtitle sidecar for /subtitle.
func (v *vodServer) SetSubtitle(path string) {
	v.mu.Lock()
	v.subtitlePath = path
	v.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(v)
}

// currentStreamSnapshot copies the live StreamState under its lock so the
// web dashboard's WebSocket can broadcast it safely.
func currentStreamSnapshot() map[string]any {
	currentStream.mu.RLock()
	snap := map[string]any{
		"filename":     currentStream.Filename,
		"filesize_fmt": currentStream.FileSizeFmt,
		"progress":     currentStream.Progress,
		"speed_fmt":    currentStream.SpeedFmt,
		"peers":        currentStream.Peers,
		"eta":          currentStream.ETA,
		"status":       currentStream.Status,
		"buffered":     currentStream.Buffered,
		"tier":         currentStream.Tier,
		"playback_pos": currentStream.PlaybackPosSec,
	}
	currentStream.mu.RUnlock()
	return snap
}
