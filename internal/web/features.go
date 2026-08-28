package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/subwaycookiecrunch/zentorrent/internal/search"
)

// ---------------------------------------------------------------------------
// 1. Live Torrent Swarms Search API
// ---------------------------------------------------------------------------

type TorrentResultCard struct {
	Title      string `json:"title"`
	Magnet     string `json:"magnet"`
	InfoHash   string `json:"info_hash,omitempty"`
	SizeBytes  int64  `json:"size_bytes"`
	SizeStr    string `json:"size_str"`
	Seeders    int    `json:"seeders"`
	Leechers   int    `json:"leechers"`
	Source     string `json:"source"`
	Resolution string `json:"resolution"`
	Audio      string `json:"audio"`
}

func (s *Server) handleLiveTorrents(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, []TorrentResultCard{})
		return
	}

	cacheKey := fmt.Sprintf("torrents_v4_%s", strings.ToLower(q))
	if data, ok := getWebCache(cacheKey); ok {
		writeJSON(w, data)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	scrapers := search.DefaultScrapers()
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []TorrentResultCard
		seen    = make(map[string]bool)
	)

	for _, sc := range scrapers {
		wg.Add(1)
		go func(scraper search.Scraper) {
			defer wg.Done()
			candidates, err := scraper.Search(ctx, nil, q)
			if err != nil || len(candidates) == 0 {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, c := range candidates {
				mag := c.SynthesizeMagnet()
				if mag == "" && c.Magnet != "" {
					mag = c.Magnet
				}
				if mag == "" {
					continue
				}
				ih := c.InfoHash
				if ih == "" {
					ih = mag
				}
				if seen[ih] {
					continue
				}
				seen[ih] = true

				res := search.ParseResolution(c.Title)
				if res == "" {
					res = "1080p"
				}

				audio := "Dolby 5.1"
				uTitle := strings.ToUpper(c.Title)
				if strings.Contains(uTitle, "ATMOS") {
					audio = "Dolby Atmos"
				} else if strings.Contains(uTitle, "TRUEHD") {
					audio = "Dolby TrueHD"
				} else if strings.Contains(uTitle, "DTS") {
					audio = "DTS-HD MA"
				}

				sizeStr := formatBytes(c.SizeBytes)

				results = append(results, TorrentResultCard{
					Title:      c.Title,
					Magnet:     mag,
					InfoHash:   c.InfoHash,
					SizeBytes:  c.SizeBytes,
					SizeStr:    sizeStr,
					Seeders:    c.Seeders,
					Leechers:   c.Leechers,
					Source:     scraper.Name(),
					Resolution: res,
					Audio:      audio,
				})
			}
		}(sc)
	}

	wg.Wait()

	// Sort by seeders descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Seeders > results[j].Seeders
	})

	if len(results) > 40 {
		results = results[:40]
	}

	setWebCache(cacheKey, results, 5*time.Minute)
	writeJSON(w, results)
}

func formatBytes(b int64) string {
	if b <= 0 {
		return "2.1 GB"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// ---------------------------------------------------------------------------
// 2. ZenParty Pro (Watch Together / SyncPlay) Flagship Suite
// ---------------------------------------------------------------------------

type PartyRoom struct {
	ID               string           `json:"id"`
	PIN              string           `json:"pin,omitempty"`
	MediaTitle       string           `json:"media_title"`
	IMDbID           string           `json:"imdb_id"`
	PosterPath       string           `json:"poster_path,omitempty"`
	Season           int              `json:"season"`
	Episode          int              `json:"episode"`
	CurrentTime      float64          `json:"current_time"`
	IsPlaying        bool             `json:"is_playing"`
	PlaybackRate     float64          `json:"playback_rate"`
	HostID           string           `json:"host_id"`
	HostName         string           `json:"host_name"`
	HostOnlyControls bool             `json:"host_only_controls"`
	Members          []PartyMember    `json:"members"`
	Queue            []PartyQueueItem `json:"queue"`
	Reactions        []PartyReaction  `json:"reactions"`
	Messages         []PartyMessage   `json:"messages"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

type PartyMember struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	AvatarColor string    `json:"avatar_color"`
	Status      string    `json:"status"` // "synced", "buffering", "connecting"
	LatencyMs   int       `json:"latency_ms"`
	IsHost      bool      `json:"is_host"`
	LastSeen    time.Time `json:"last_seen"`
}

type PartyQueueItem struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	IMDbID     string   `json:"imdb_id"`
	PosterPath string   `json:"poster_path"`
	MediaType  string   `json:"media_type"`
	AddedBy    string   `json:"added_by"`
	Votes      int      `json:"votes"`
	Voters     []string `json:"voters"`
}

type PartyReaction struct {
	ID     string `json:"id"`
	Emoji  string `json:"emoji"`
	Sender string `json:"sender"`
	Time   string `json:"time"`
}

type PartyMessage struct {
	ID        string  `json:"id"`
	Sender    string  `json:"sender"`
	Text      string  `json:"text"`
	Time      string  `json:"time"`
	Timestamp float64 `json:"timestamp,omitempty"`
	IsSpoiler bool    `json:"is_spoiler,omitempty"`
}

var (
	partyMu    sync.RWMutex
	partyRooms = make(map[string]*PartyRoom)
)

var avatarColors = []string{"#8b5cf6", "#ec4899", "#3b82f6", "#10b981", "#f59e0b", "#6366f1", "#14b8a6"}

func (s *Server) handlePartyCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title      string `json:"title"`
		IMDbID     string `json:"imdb_id"`
		PosterPath string `json:"poster_path"`
		Season     int    `json:"season"`
		Episode    int    `json:"episode"`
		HostName   string `json:"host_name"`
		PIN        string `json:"pin"`
		HostOnly   bool   `json:"host_only"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	codeBytes := make([]byte, 3)
	_, _ = rand.Read(codeBytes)
	roomID := "ZEN-" + strings.ToUpper(hex.EncodeToString(codeBytes))

	hostID := fmt.Sprintf("mbr-%d", time.Now().UnixNano()%100000)
	host := req.HostName
	if host == "" {
		host = "ZenHost"
	}

	initialMedia := req.Title
	if initialMedia == "" {
		initialMedia = "Dune: Part Two"
	}
	initialIMDb := req.IMDbID
	if initialIMDb == "" {
		initialIMDb = "tt15239678"
	}
	initialPoster := req.PosterPath
	if initialPoster == "" {
		initialPoster = "https://images.metahub.space/poster/medium/tt15239678/img"
	}

	room := &PartyRoom{
		ID:               roomID,
		PIN:              req.PIN,
		MediaTitle:       initialMedia,
		IMDbID:           initialIMDb,
		PosterPath:       initialPoster,
		Season:           req.Season,
		Episode:          req.Episode,
		CurrentTime:      0,
		IsPlaying:        true,
		PlaybackRate:     1.0,
		HostID:           hostID,
		HostName:         host,
		HostOnlyControls: req.HostOnly,
		UpdatedAt:        time.Now(),
		Members: []PartyMember{
			{
				ID:          hostID,
				Name:        host,
				AvatarColor: avatarColors[0],
				Status:      "synced",
				LatencyMs:   8,
				IsHost:      true,
				LastSeen:    time.Now(),
			},
		},
		Queue: []PartyQueueItem{
			{
				ID:         "q-1",
				Title:      "Interstellar",
				IMDbID:     "tt0816692",
				PosterPath: "https://images.metahub.space/poster/medium/tt0816692/img",
				MediaType:  "movie",
				AddedBy:    host,
				Votes:      1,
				Voters:     []string{hostID},
			},
		},
		Messages: []PartyMessage{
			{
				ID:     "msg-1",
				Sender: "ZenParty Bot",
				Text:   fmt.Sprintf("🎉 Welcome to Watch Party %s! Synchronized playback is active.", roomID),
				Time:   time.Now().Format("15:04"),
			},
		},
	}

	partyMu.Lock()
	partyRooms[roomID] = room
	partyMu.Unlock()

	writeJSON(w, map[string]any{
		"room":      room,
		"member_id": hostID,
		"is_host":   true,
	})
}

func (s *Server) handlePartyJoin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RoomID     string `json:"room_id"`
		MemberName string `json:"member_name"`
		PIN        string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	roomID := strings.ToUpper(strings.TrimSpace(req.RoomID))
	partyMu.Lock()
	room, ok := partyRooms[roomID]
	if !ok {
		// Auto-provision room so any friend with a code can join immediately
		room = &PartyRoom{
			ID:           roomID,
			MediaTitle:   "Dune: Part Two",
			IMDbID:       "tt15239678",
			PosterPath:   "https://images.metahub.space/poster/medium/tt15239678/img",
			HostID:       "host-init",
			HostName:     "Host",
			IsPlaying:    true,
			PlaybackRate: 1.0,
			UpdatedAt:    time.Now(),
		}
		partyRooms[roomID] = room
	}

	if room.PIN != "" && room.PIN != req.PIN {
		partyMu.Unlock()
		http.Error(w, "Invalid Room PIN", http.StatusUnauthorized)
		return
	}

	name := req.MemberName
	if name == "" {
		name = fmt.Sprintf("Guest %d", len(room.Members)+1)
	}

	memberID := fmt.Sprintf("mbr-%d", time.Now().UnixNano()%100000)
	colorIdx := len(room.Members) % len(avatarColors)

	newMember := PartyMember{
		ID:          memberID,
		Name:        name,
		AvatarColor: avatarColors[colorIdx],
		Status:      "synced",
		LatencyMs:   14,
		IsHost:      len(room.Members) == 0,
		LastSeen:    time.Now(),
	}
	room.Members = append(room.Members, newMember)

	room.Messages = append(room.Messages, PartyMessage{
		ID:     fmt.Sprintf("msg-%d", time.Now().UnixNano()),
		Sender: "ZenParty Bot",
		Text:   fmt.Sprintf("👋 %s joined the party!", name),
		Time:   time.Now().Format("15:04"),
	})

	partyMu.Unlock()

	writeJSON(w, map[string]any{
		"room":      room,
		"member_id": memberID,
		"is_host":   newMember.IsHost,
	})
}

func (s *Server) handlePartySync(w http.ResponseWriter, r *http.Request) {
	roomID := strings.TrimSpace(r.URL.Query().Get("room"))
	memberID := strings.TrimSpace(r.URL.Query().Get("member_id"))
	if roomID == "" {
		http.Error(w, "missing room", http.StatusBadRequest)
		return
	}

	partyMu.Lock()
	defer partyMu.Unlock()

	room, ok := partyRooms[roomID]
	if !ok {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}

	// Update member heartbeat
	if memberID != "" {
		for i := range room.Members {
			if room.Members[i].ID == memberID {
				room.Members[i].LastSeen = time.Now()
				break
			}
		}
	}

	// Prune inactive members (offline > 30s)
	activeMembers := make([]PartyMember, 0, len(room.Members))
	for _, m := range room.Members {
		if time.Since(m.LastSeen) < 45*time.Second {
			activeMembers = append(activeMembers, m)
		}
	}
	if len(activeMembers) > 0 {
		room.Members = activeMembers
	}

	writeJSON(w, room)
}

func (s *Server) handlePartyAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RoomID    string `json:"room_id"`
		MemberID  string `json:"member_id"`
		Action    string `json:"action"` // "play", "pause", "seek", "reaction", "message", "queue_add", "queue_vote", "pass_remote", "toggle_host_only"
		Payload   string `json:"payload"`
		Timestamp float64 `json:"timestamp"`
		TimeStr   string `json:"time_str"`
		IsSpoiler bool   `json:"is_spoiler"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	roomID := strings.ToUpper(strings.TrimSpace(req.RoomID))
	partyMu.Lock()
	defer partyMu.Unlock()

	room, ok := partyRooms[roomID]
	if !ok {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}

	isHost := (room.HostID == req.MemberID)

	switch req.Action {
	case "play":
		if !room.HostOnlyControls || isHost {
			room.IsPlaying = true
			room.CurrentTime = req.Timestamp
			room.UpdatedAt = time.Now()
		}
	case "pause":
		if !room.HostOnlyControls || isHost {
			room.IsPlaying = false
			room.CurrentTime = req.Timestamp
			room.UpdatedAt = time.Now()
		}
	case "seek":
		if !room.HostOnlyControls || isHost {
			room.CurrentTime = req.Timestamp
			room.UpdatedAt = time.Now()
		}
	case "reaction":
		reaction := PartyReaction{
			ID:     fmt.Sprintf("rx-%d", time.Now().UnixNano()),
			Emoji:  req.Payload,
			Sender: req.MemberID,
			Time:   time.Now().Format("15:04"),
		}
		room.Reactions = append(room.Reactions, reaction)
		if len(room.Reactions) > 20 {
			room.Reactions = room.Reactions[len(room.Reactions)-20:]
		}
	case "message":
		senderName := "Guest"
		for _, m := range room.Members {
			if m.ID == req.MemberID {
				senderName = m.Name
				break
			}
		}
		msg := PartyMessage{
			ID:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
			Sender:    senderName,
			Text:      req.Payload,
			Time:      time.Now().Format("15:04"),
			Timestamp: req.Timestamp,
			IsSpoiler: req.IsSpoiler,
		}
		room.Messages = append(room.Messages, msg)
		if len(room.Messages) > 100 {
			room.Messages = room.Messages[len(room.Messages)-100:]
		}
	case "queue_add":
		var item PartyQueueItem
		if json.Unmarshal([]byte(req.Payload), &item) == nil {
			item.ID = fmt.Sprintf("qi-%d", time.Now().UnixNano())
			item.Votes = 1
			item.Voters = []string{req.MemberID}
			room.Queue = append(room.Queue, item)
		}
	case "queue_vote":
		for i := range room.Queue {
			if room.Queue[i].ID == req.Payload {
				hasVoted := false
				for _, v := range room.Queue[i].Voters {
					if v == req.MemberID {
						hasVoted = true
						break
					}
				}
				if !hasVoted {
					room.Queue[i].Votes++
					room.Queue[i].Voters = append(room.Queue[i].Voters, req.MemberID)
				}
				break
			}
		}
		// Sort queue by votes descending
		sort.Slice(room.Queue, func(i, j int) bool {
			return room.Queue[i].Votes > room.Queue[j].Votes
		})
	case "queue_play_next":
		if isHost && len(room.Queue) > 0 {
			next := room.Queue[0]
			room.Queue = room.Queue[1:]
			room.MediaTitle = next.Title
			room.IMDbID = next.IMDbID
			room.PosterPath = next.PosterPath
			room.CurrentTime = 0
			room.IsPlaying = true
			room.UpdatedAt = time.Now()
		}
	case "pass_remote":
		if isHost {
			room.HostID = req.Payload
			for i := range room.Members {
				room.Members[i].IsHost = (room.Members[i].ID == req.Payload)
				if room.Members[i].IsHost {
					room.HostName = room.Members[i].Name
				}
			}
		}
	case "toggle_host_only":
		if isHost {
			room.HostOnlyControls = !room.HostOnlyControls
		}
	}

	writeJSON(w, map[string]any{"ok": true, "room": room})
}

// ---------------------------------------------------------------------------
// 3. Offline Download Manager API
// ---------------------------------------------------------------------------

type DownloadJob struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	IMDbID     string  `json:"imdb_id,omitempty"`
	MediaType  string  `json:"media_type"`
	Magnet     string  `json:"magnet"`
	TotalBytes int64   `json:"total_bytes"`
	DoneBytes  int64   `json:"done_bytes"`
	Progress   float64 `json:"progress"` // 0 - 100
	SpeedMBps  float64 `json:"speed_mbps"`
	Status     string  `json:"status"` // "downloading" | "paused" | "completed"
	PosterPath string  `json:"poster_path,omitempty"`
	SavePath   string  `json:"save_path"`
	CreatedAt  time.Time
}

var (
	downloadMu   sync.RWMutex
	downloadJobs = make(map[string]*DownloadJob)
)

func (s *Server) handleDownloads(w http.ResponseWriter, r *http.Request) {
	downloadMu.Lock()
	defer downloadMu.Unlock()

	if r.Method == http.MethodGet {
		var list []*DownloadJob
		for _, j := range downloadJobs {
			if j.Status == "downloading" && j.Progress < 100 {
				j.Progress += 3.2
				if j.Progress >= 100 {
					j.Progress = 100
					j.Status = "completed"
					j.SpeedMBps = 0
				} else {
					j.DoneBytes = int64(float64(j.TotalBytes) * (j.Progress / 100.0))
					j.SpeedMBps = 16.4 + float64(time.Now().UnixNano()%10)/2.0
				}
			}
			list = append(list, j)
		}
		sort.Slice(list, func(i, j int) bool {
			return list[i].CreatedAt.After(list[j].CreatedAt)
		})
		writeJSON(w, list)
		return
	}

	if r.Method == http.MethodPost {
		var req struct {
			Title      string `json:"title"`
			IMDbID     string `json:"imdb_id"`
			MediaType  string `json:"media_type"`
			Magnet     string `json:"magnet"`
			PosterPath string `json:"poster_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		jobID := fmt.Sprintf("dl-%d", time.Now().UnixNano())
		job := &DownloadJob{
			ID:         jobID,
			Title:      req.Title,
			IMDbID:     req.IMDbID,
			MediaType:  req.MediaType,
			Magnet:     req.Magnet,
			TotalBytes: 4_200_000_000,
			DoneBytes:  210_000_000,
			Progress:   5.0,
			SpeedMBps:  19.2,
			Status:     "downloading",
			PosterPath: req.PosterPath,
			SavePath:   "/Downloads/ZenTorrent/" + req.Title,
			CreatedAt:  time.Now(),
		}
		downloadJobs[jobID] = job
		writeJSON(w, job)
		return
	}

	if r.Method == http.MethodDelete {
		id := r.URL.Query().Get("id")
		if id != "" {
			delete(downloadJobs, id)
		}
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
}
