package search

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Regression guard: concurrent metadata workers must NOT lose failCount
// updates — one infohash may never exceed cfg.MaxFailedPeerAttempts failed
// dials, and exhausted entries persist until a peer succeeds.
func TestVerifyFailCountLostUpdate(t *testing.T) {
	dir := t.TempDir()
	idx, err := NewDHTIndexer(DHTConfig{DBPath: filepath.Join(dir, "idx.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var dials int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			atomic.AddInt64(&dials, 1)
			time.Sleep(100 * time.Millisecond) // emulate a slow/handshaking peer
			c.Close()
		}
	}()

	var ih [20]byte
	ih[0] = 0xAB

	const peersPerHash = 6 // resolvePeers enqueues at most 6 per hash (dht.go:581)
	const workers = 12     // default FetchWorkers

	ctx := context.Background()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pt := range idx.peerCh { // exact stage-3 dispatch (dht.go:293-294)
				idx.fetchAndIndex(ctx, pt)
			}
		}()
	}

	// Enqueue all peers for one infohash back-to-back, like resolvePeers does.
	for i := 0; i < peersPerHash; i++ {
		idx.peerCh <- peerTarget{addr: ln.Addr().String(), ih: ih}
	}
	close(idx.peerCh)
	wg.Wait()

	gotDials := atomic.LoadInt64(&dials)
	idx.failMu.Lock()
	stored := idx.failCount[ih]
	mapLen := len(idx.failCount)
	idx.failMu.Unlock()

	t.Logf("cfg.MaxFailedPeerAttempts=%d actualFailedDials=%d storedFailCount=%d failCountLen=%d",
		idx.cfg.MaxFailedPeerAttempts, gotDials, stored, mapLen)

	if gotDials > int64(idx.cfg.MaxFailedPeerAttempts) {
		t.Errorf("cap defeated: %d failed dials for one infohash with MaxFailedPeerAttempts=%d",
			gotDials, idx.cfg.MaxFailedPeerAttempts)
	}
	if int64(stored) < gotDials {
		t.Errorf("lost updates: %d failures raised counter only to %d", gotDials, stored)
	}
	if mapLen == 0 && gotDials > 0 {
		t.Errorf("expected exhausted hash to persist in failCount, map empty")
	}
}
