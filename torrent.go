package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
)

func startEngine(tgt string) (*torrent.Client, *torrent.Torrent) {
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = "."
	cfg.EstablishedConnsPerTorrent = 50
	cfg.Seed = false
	
	c, err := torrent.NewClient(cfg)
	if err != nil {
		panic(err)
	}

	var t *torrent.Torrent
	if strings.HasPrefix(tgt, "magnet:") {
		t, err = c.AddMagnet(tgt)
	} else {
		t, err = c.AddTorrentFromFile(tgt)
	}
	if err != nil {
		panic(err)
	}

	return c, t
}

func runDownload(t *torrent.Torrent) {
	fmt.Printf("metadata %s...\n", t.Name())
	<-t.GotInfo() 
	t.DownloadAll() 
	
	tk := time.NewTicker(time.Second)
	prv := t.BytesCompleted()
	
	fmt.Print("\033[H\033[2J")
	drawn := false
	
	for {
		c := t.BytesCompleted()
		tot := t.Info().TotalLength()
		
		spd := c - prv
		if spd < 0 { spd = 0 }
		prv = c
		
		if drawn { fmt.Print("\033[7A") }
		drawn = true

		pct := 0.0
		f := 0
		if tot > 0 { 
			pct = float64(c) / float64(tot) * 100 
			f = int(float64(c) / float64(tot) * 30)
			if f > 30 { f = 30 }
		}
		
		bar := strings.Repeat("=", f) + strings.Repeat("-", 30-f)
		
		eta := "?"
		if spd > 0 && tot > c {
			rem := (tot - c) / spd
			if rem < 60 { eta = fmt.Sprintf("%ds", rem) } else if rem < 3600 {
				eta = fmt.Sprintf("%dm%ds", rem/60, rem%60)
			} else {
				eta = fmt.Sprintf("%dh%dm", rem/3600, (rem%3600)/60)
			}
		}

		nm := t.Name()
		if len(nm) > 47 { nm = nm[:47] + "..." }

		fmt.Printf("\033[K%s\n\033[K\n\033[K[%s] %.1f%%\n\033[K\n\033[K%s/s\n\033[KETA: %s\n\033[KPeers: %d\n", 
			nm, bar, pct, formatSize(spd), eta, len(t.PeerConns()))
		
		if c == tot && tot > 0 {
			fmt.Println("\n\033[Kdone")
			break
		}
		<-tk.C
	}
}

func formatSize(b int64) string {
	if b < 1024 { return fmt.Sprintf("%d B", b) }
	if b < 1024*1024 { return fmt.Sprintf("%.1f KB", float64(b)/1024) }
	return fmt.Sprintf("%.2f MB", float64(b)/(1024*1024))
}
