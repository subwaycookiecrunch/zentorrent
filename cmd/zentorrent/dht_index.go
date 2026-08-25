package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/subwaycookiecrunch/zentorrent/internal/engine"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	_ "modernc.org/sqlite"
)

var (
	dhtDB         *sql.DB
	dhtFetchQueue = make(map[metainfo.Hash]bool)
	dhtFetchMu    sync.Mutex

	// One shared client for metadata fetches instead of a fresh one per
	// announce, and a cap so indexing can't stampede while the user streams.
	dhtFetchOnce sync.Once
	dhtFetchCl   *torrent.Client
	dhtFetchSema = make(chan struct{}, 2)
)

func ensureDHTFetchClient() *torrent.Client {
	dhtFetchOnce.Do(func() {
		cfg := torrent.NewDefaultClientConfig()
		cfg.DataDir = os.TempDir()
		cfg.NoUpload = true
		cfg.Seed = false
		cfg.ListenPort = 0
		cl, err := torrent.NewClient(cfg)
		if err == nil {
			dhtFetchCl = cl
		}
	})
	return dhtFetchCl
}

func InitDHTIndex() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	appDir := filepath.Join(configDir, "zentorrent")
	os.MkdirAll(appDir, 0755)

	dbPath := filepath.Join(appDir, "dht.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS torrents (
			infohash TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			size INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE VIRTUAL TABLE IF NOT EXISTS torrents_fts USING fts5(title, content='torrents', content_rowid='rowid');

		CREATE TRIGGER IF NOT EXISTS torrents_ai AFTER INSERT ON torrents BEGIN
			INSERT INTO torrents_fts(rowid, title) VALUES (new.rowid, new.title);
		END;
	`)
	if err != nil {
		return err
	}
	dhtDB = db
	return nil
}

func StartDHTIndexer() {
	if dhtDB == nil {
		return
	}

	cfg := torrent.NewDefaultClientConfig()
	cfg.NoUpload = true
	cfg.Seed = false
	cfg.DisableAggressiveUpload = true
	cfg.DisableWebseeds = true
	cfg.DisableWebtorrent = true
	cfg.DataDir = os.TempDir()
	cfg.ListenPort = 0

	cfg.ConfigureAnacrolixDhtServer = func(sc *dht.ServerConfig) {
		sc.OnAnnouncePeer = func(infoHash metainfo.Hash, ip net.IP, port int, portOk bool) {
			go func() {
				defer func() { recover() }()
				handleDHTAnnounce(infoHash)
			}()
		}
	}

	cl, err := torrent.NewClient(cfg)
	if err != nil {
		return
	}
	defer cl.Close()

	select {}
}

func handleDHTAnnounce(ih metainfo.Hash) {
	var exists bool
	hashStr := ih.HexString()
	err := dhtDB.QueryRow("SELECT 1 FROM torrents WHERE infohash = ?", hashStr).Scan(&exists)
	if err == nil {
		return
	}

	dhtFetchMu.Lock()
	if dhtFetchQueue[ih] {
		dhtFetchMu.Unlock()
		return
	}
	dhtFetchQueue[ih] = true
	dhtFetchMu.Unlock()

	defer func() {
		dhtFetchMu.Lock()
		delete(dhtFetchQueue, ih)
		dhtFetchMu.Unlock()
	}()

	if engine.IsActiveStreaming() {
		return
	}

	dhtFetchSema <- struct{}{}
	defer func() { <-dhtFetchSema }()

	fetchCl := ensureDHTFetchClient()
	if fetchCl == nil {
		return
	}

	t, new := fetchCl.AddTorrentInfoHash(ih)
	if !new {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	select {
	case <-t.GotInfo():
		info := t.Info()
		if info != nil {
			title := info.Name
			var size int64
			if info.Length != 0 {
				size = info.Length
			} else {
				for _, f := range info.Files {
					size += f.Length
				}
			}

			_, err = dhtDB.Exec("INSERT OR IGNORE INTO torrents (infohash, title, size) VALUES (?, ?, ?)", hashStr, title, size)
			if err != nil {
				// No fmt.Printf here: this runs on a background goroutine
				// inside the TUI process and would shred rendered frames.
				_ = err
			}
		}
	case <-ctx.Done():
	}
	t.Drop()
}

func SearchDHT(query string) []Result {
	if dhtDB == nil {
		return nil
	}

	// Quote each token: raw concatenation turns punctuation (S24E13, [Group],
	// 4-K) into FTS5 syntax errors, silently returning zero results.
	words := strings.Fields(query)
	var ftsQuery string
	for i, w := range words {
		if i > 0 {
			ftsQuery += " AND "
		}
		ftsQuery += `"` + strings.ReplaceAll(w, `"`, `""`) + `"*`
	}

	rows, err := dhtDB.Query(`
		SELECT t.infohash, t.title, t.size
		FROM torrents t
		JOIN torrents_fts f ON t.rowid = f.rowid
		WHERE torrents_fts MATCH ?
		LIMIT 50
	`, ftsQuery)

	if err != nil {
		return nil
	}
	defer rows.Close()

	var res []Result
	for rows.Next() {
		var hash, title string
		var size int64
		if err := rows.Scan(&hash, &title, &size); err == nil {
			res = append(res, Result{
				Title:      title,
				Magnet:     fmt.Sprintf("magnet:?xt=urn:btih:%s", hash),
				Size:       formatSizeBytes(size),
				Source:     "zendht",
				Resolution: parseRes(title),
				Category:   guessCategory(title),
				Seeders:    seedersUnknown,
			})
		}
	}
	return res
}
