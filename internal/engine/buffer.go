package engine

import (
	"sync"

	"github.com/anacrolix/torrent"
)

// Buffer.go — dynamic seek-window scheduling.

// vodScheduler keeps the swarm focused just ahead of where the player
// actually is, instead of where the download happens to be. Priorities are
// only written when they change, so steady-state cost per tick is a few map
// compares rather than resetting every piece in the torrent.
//
// The initial layout front-loads the head of the file at maximum urgency and
// raises the tail as well: players probe the end of MP4/MKV containers (moov
// atom, cue sheet) while opening, and stalling on those bytes delays the
// first frame. Everything is relative to the video file's own piece range,
// which matters for multi-file torrents where the movie doesn't start at
// piece zero.
type Scheduler struct {
	mu       sync.Mutex
	t        *torrent.Torrent
	file     *torrent.File
	first    int
	last     int
	count    int
	fileLen  int64
	pieceLen int64
	pos      float64
	dur      float64
	speedBps int64
	center   int
	assigned map[int]torrent.PiecePriority
}

const (
	vodAheadSeconds  = 30
	vodBackPieces    = 2
	vodMinNowPieces  = 10
	vodMaxNowPieces  = 80
	vodMaxHighPieces = 300
)

func NewVODScheduler(t *torrent.Torrent, vid *torrent.File) *Scheduler {
	pl := t.Info().PieceLength
	if pl <= 0 {
		pl = 1
	}
	first := vid.BeginPieceIndex()
	last := vid.EndPieceIndex() - 1 // inclusive; EndPieceIndex is exclusive
	s := &Scheduler{
		t:        t,
		file:     vid,
		first:    first,
		last:     last,
		count:    last - first + 1,
		fileLen:  vid.Length(),
		pieceLen: pl,
		center:   first,
		assigned: make(map[int]torrent.PiecePriority, last-first+1),
	}

	headN := PiecesCovering(4<<20, pl)
	if headN > s.count {
		headN = s.count
	}
	tailN := PiecesCovering(3<<20, pl)
	if tailN > s.count/4 {
		tailN = s.count / 4
	}
	if headN+tailN > s.count {
		tailN = s.count - headN
		if tailN < 0 {
			tailN = 0
		}
	}

	for i := 0; i < s.count; i++ {
		idx := first + i
		prio := torrent.PiecePriorityNormal
		switch {
		case i < headN:
			prio = torrent.PiecePriorityNow
		case i >= s.count-tailN:
			prio = torrent.PiecePriorityHigh
		}
		s.assigned[idx] = prio
		t.Piece(idx).SetPriority(prio)
	}
	return s
}

func PiecesCovering(bytes, pieceLen int64) int {
	if pieceLen <= 0 {
		pieceLen = 1
	}
	n := int((bytes + pieceLen - 1) / pieceLen)
	if n < 1 {
		return 1
	}
	return n
}

func (s *Scheduler) Update(pos float64, dur float64, speedBps int64) {
	s.mu.Lock()
	if pos > 0 {
		s.pos = pos
	}
	if dur > 0 {
		s.dur = dur
	}
	s.speedBps = speedBps
	s.mu.Unlock()
}

func (s *Scheduler) Tick() {
	s.mu.Lock()
	defer s.mu.Unlock()

	center := s.center
	if s.dur > 0 && s.pos > 0 {
		frac := s.pos / s.dur
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		center = s.first + int(frac*float64(s.count))
	} else {
		frac := float64(s.file.BytesCompleted()) / float64(s.fileLen)
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		center = s.first + int(frac*float64(s.count))
	}
	if center < s.first {
		center = s.first
	}
	if center > s.last+1 {
		center = s.last + 1
	}
	s.center = center

	nowN := vodMaxNowPieces
	if s.speedBps > 0 {
		nowN = int(uint64(s.speedBps) * vodAheadSeconds / uint64(s.pieceLen))
	}
	if nowN < vodMinNowPieces {
		nowN = vodMinNowPieces
	}
	if nowN > vodMaxNowPieces {
		nowN = vodMaxNowPieces
	}
	highN := nowN * 3
	if highN > vodMaxHighPieces {
		highN = vodMaxHighPieces
	}
	if highN > s.count {
		highN = s.count
	}

	for idx, cur := range s.assigned {
		d := idx - center
		want := torrent.PiecePriorityNormal
		switch {
		case d < -vodBackPieces:
			want = torrent.PiecePriorityNone
		case d >= 0 && d < nowN:
			want = torrent.PiecePriorityNow
		case d >= 0 && d < highN:
			want = torrent.PiecePriorityHigh
		}
		if want != cur {
			s.assigned[idx] = want
			s.t.Piece(idx).SetPriority(want)
		}
	}
}

// bufferedPercent reports completion over the window just ahead of the
// playback position instead of the head of the torrent.
func (s *Scheduler) BufferedPercent(window int) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	if window < 1 {
		window = 1
	}
	start := s.center
	end := s.center + window
	if start < s.first {
		start = s.first
	}
	if end > s.last+1 {
		end = s.last + 1
	}
	total := end - start
	if total <= 0 {
		return 100
	}
	ready := 0
	for i := start; i < end; i++ {
		if s.t.Piece(i).State().Complete {
			ready++
		}
	}
	return float64(ready) / float64(total) * 100
}
