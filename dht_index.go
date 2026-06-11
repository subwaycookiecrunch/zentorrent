package main

import (
	"context"
	"database/sql"
	"fmt"
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
)

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

	fetchCfg := torrent.NewDefaultClientConfig()
	fetchCfg.DataDir = os.TempDir()
	fetchCfg.NoUpload = true
	fetchCfg.Seed = false
	fetchCfg.ListenPort = 0
	fetchCl, err := torrent.NewClient(fetchCfg)
	if err != nil {
		return
	}
	defer fetchCl.Close()

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
				fmt.Printf("dht insert error: %v\n", err)
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

	words := strings.Fields(query)
	var ftsQuery string
	for i, w := range words {
		if i > 0 {
			ftsQuery += " AND "
		}
		ftsQuery += w + "*"
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
				Seeders:    10,
			})
		}
	}
	return res
}
