package main

// Temporary verification test for a code-review finding. Delete after use.
//
// Reproduces the claimed interleaving:
//   1. user hovers/enters a result -> TriggerPrewarm arms a 500ms timer
//   2. Enter -> p.Run() returns -> StartStreamTUI -> streamTorrent adds the
//      magnet to the shared engine and blocks on <-t.GotInfo()
//   3. activeStream is still nil (engine.SetActive happens only AFTER GotInfo)
//   4. the timer fires, doPrewarm runs, engine.IsActiveStreaming()==false, so
//      engine.DropIdle() sweeps the pending stream torrent
//   5. GotInfo can then never fire -> streamTorrent hits its 45s timeout

import (
	"bytes"
	"github.com/subwaycookiecrunch/zentorrent/internal/engine"
	"os"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

func vbuildInfoBytes(t *testing.T) ([]byte, metainfo.Hash) {
	t.Helper()
	info := metainfo.Info{
		PieceLength: 32768,
		Name:        "zt-verify.bin",
		Length:      4096,
		Pieces:      make([]byte, 20), // one piece
	}
	var buf bytes.Buffer
	if err := bencode.NewEncoder(&buf).Encode(info); err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: buf.Bytes()}
	return mi.InfoBytes, mi.HashInfoBytes()
}

func TestPrewarmSweepDropsPendingStreamTorrent(t *testing.T) {
	infoBytes, ih := vbuildInfoBytes(t)
	magnet := "magnet:?xt=urn:btih:" + ih.HexString()

	cl, dir, err := engine.Get()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cl.Close()
		os.RemoveAll(dir)
	})

	// streamTorrent step 1: register the magnet on the shared engine
	streamT, err := engine.AddMagnet(magnet)
	if err != nil {
		t.Fatal(err)
	}

	gotInfo := make(chan struct{})
	go func() {
		select {
		case <-streamT.GotInfo():
			close(gotInfo)
		case <-time.After(3 * time.Second):
		}
	}()

	time.Sleep(100 * time.Millisecond)

	if engine.IsActiveStreaming() {
		t.Skip("another stream is active in this test binary")
	}
	// The 500ms prewarm timer fires here while activeStream is still nil.
	doPrewarm(magnet) // search.go:990 -> engine.DropIdle() (search.go:999)

	// engine.PrimeMetadata (stream.go:88) tries the disk cache / mirrors next:
	if err := streamT.SetInfoBytes(infoBytes); err == nil {
		t.Fatalf("SetInfoBytes unexpectedly succeeded on swept torrent")
	} else {
		t.Logf("SetInfoBytes on swept stream torrent failed with: %v", err)
	}

	select {
	case <-gotInfo:
		t.Fatal("GotInfo fired after sweep")
	case <-time.After(1500 * time.Millisecond):
		t.Log("stream torrent's GotInfo never fired -> streamTorrent will hit its 45s timeout")
	}

	// Control: the same healthy metadata resolves instantly for a torrent that
	// was not swept, i.e. the failure is caused solely by engine.DropIdle.
	fresh, err := engine.AddMagnet(magnet)
	if err != nil {
		t.Fatal(err)
	}
	if fresh == streamT {
		t.Fatal("expected a new torrent handle after drop")
	}
	if err := fresh.SetInfoBytes(infoBytes); err != nil {
		t.Fatalf("control SetInfoBytes failed: %v", err)
	}
	select {
	case <-fresh.GotInfo():
		t.Log("control torrent resolved immediately")
	case <-time.After(2 * time.Second):
		t.Fatal("control torrent did not resolve")
	}
}
