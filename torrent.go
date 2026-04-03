package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
)

type App struct {
	client *torrent.Client
	t      *torrent.Torrent
	ui     *UI
}

func NewApp(target string) (*App, error) {
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = "."
	cfg.EstablishedConnsPerTorrent = 50
	cfg.Seed = false
	cfg.Debug = false
	cfg.ListenPort = 0
	
	c, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	app := &App{
		client: c,
		ui:     NewUI(),
	}

	var t *torrent.Torrent
	if strings.HasPrefix(target, "magnet:") {
		t, err = c.AddMagnet(target)
	} else {
		t, err = c.AddTorrentFromFile(target)
	}

	if err != nil {
		c.Close()
		return nil, err
	}

	app.t = t
	return app, nil
}

func (a *App) Stop() {
	if a.t != nil {
		a.t.Drop()
	}
	if a.client != nil {
		a.client.Close()
	}
	fmt.Println("\n\033[Kstopped.")
}

func (a *App) Run() {
	fmt.Printf("fetching metadata for %s...\n", a.t.Name())
	
	<-a.t.GotInfo() 
	a.t.DownloadAll() 
	
	tk := time.NewTicker(time.Second)
	defer tk.Stop()
	
	prev := a.t.BytesCompleted()
	a.ui.Clear()
	
	for {
		cur := a.t.BytesCompleted()
		total := a.t.Info().TotalLength()
		peers := len(a.t.PeerConns())
		
		speed := cur - prev
		if speed < 0 {
			speed = 0
		}
		prev = cur
		
		a.ui.Render(a.t.Name(), cur, total, speed, peers)
		
		if cur == total && total > 0 {
			fmt.Println("\n\033[Kdownload complete!")
			break
		}

		<-tk.C
	}
}
