package main

import "sync"

type PlaylistItem struct {
	Magnet string
	Title  string
	Status string
}

type Playlist struct {
	mu      sync.RWMutex
	Items   []PlaylistItem
	Current int
}

var GlobalPlaylist = &Playlist{Current: -1}

func (p *Playlist) Add(magnet, title string) {
	p.mu.Lock()
	p.Items = append(p.Items, PlaylistItem{Magnet: magnet, Title: title, Status: "queued"})
	p.mu.Unlock()
}
func (p *Playlist) GetNext() *PlaylistItem {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.Current+1 < len(p.Items) {
		return &p.Items[p.Current+1]
	}
	return nil
}

func (p *Playlist) Advance() *PlaylistItem {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Current+1 < len(p.Items) {
		p.Current++
		return &p.Items[p.Current]
	}

	return nil
}

func (p *Playlist) Clear() {
	p.mu.Lock()
	p.Items = []PlaylistItem{}
	p.Current = -1
	p.mu.Unlock()
}
func (p *Playlist) SetStatus(idx int, status string) {
	p.mu.Lock()
	if idx >= 0 && idx < len(p.Items) {
		p.Items[idx].Status = status
	}
	p.mu.Unlock()
}
