package search

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"testing"
)

func netIPBytes(ip string) []byte { return net.ParseIP(ip).To4() }

func TestBencodeRoundTrip(t *testing.T) {
	samples := strings.Repeat("abcdefghij", 6)[:40] // 2 x 20-byte hashes
	orig := krpcMsg{
		"t":     []byte{0x01, 0x02},
		"y":     "r",
		"other": []any{"x", int64(-7)},
		"r": map[string]any{
			"id":      []byte(strings.Repeat("n", 20)),
			"samples": []byte(samples),
			"num":     int64(2),
			"nested":  map[string]any{"deep": "v"},
		},
	}
	wire := bencodeMap(orig)

	var back any
	n, err := bdecodePrefix(wire, &back)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n != len(wire) {
		t.Errorf("consumed %d of %d bytes", n, len(wire))
	}

	m, ok := back.(map[string]any)
	if !ok {
		t.Fatalf("not a dict: %T", back)
	}
	txb, _ := byteSlice(m, "t")
	if got := string(txb); got != "\x01\x02" {
		t.Errorf("txid = %q", got)
	}
	r := m["r"].(map[string]any)
	if got, _ := r["num"].(int64); got != 2 {
		t.Errorf("num = %v", r["num"])
	}
	samplesB, _ := byteSlice(r, "samples")
	if string(samplesB) != samples {
		t.Errorf("binary samples mangled")
	}
	if neg := m["other"].([]any)[1].(int64); neg != -7 {
		t.Errorf("negative int = %v", neg)
	}
}

func TestBencodeDecodeTruncated(t *testing.T) {
	var out any
	badInputs := [][]byte{
		[]byte("d4:name"),       // unterminated dict
		[]byte("i123"),          // unterminated int
		[]byte("9:short"),       // string longer than buffer
		[]byte("d3:keyd4:deep"), // nested truncation
	}
	for _, bad := range badInputs {
		if err := bdecodeInto(bad, &out); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestBloomFilter(t *testing.T) {
	b := newBloomFilter(1 << 14)

	// Zero false negatives.
	keys := make(map[[20]byte]bool)
	for i := 0; i < 5000; i++ {
		var k [20]byte
		binary.BigEndian.PutUint64(k[:8], uint64(i))
		b.add(k)
		keys[k] = true
	}
	for k := range keys {
		if !b.mayContain(k) {
			t.Fatal("false negative — bloom is unusable")
		}
	}

	// False-positive rate sanity on unseen keys.
	fp := 0
	const probes = 20000
	for i := 5000; i < 5000+probes; i++ {
		var k [20]byte
		binary.BigEndian.PutUint64(k[:8], uint64(i))
		if b.mayContain(k) {
			fp++
		}
	}
	rate := float64(fp) / probes
	if rate > 0.05 {
		t.Fatalf("false positive rate %.3f exceeds 5%%", rate)
	}
}

func TestLRUSet(t *testing.T) {
	s := newLRUSet(3)
	add := func(v string) { s.add([]byte(v)) }

	add("a")
	add("b")
	add("c")
	if !s.contains([]byte("a")) {
		t.Error("a lost before eviction")
	}
	add("d") // evicts a (oldest)
	if s.contains([]byte("a")) {
		t.Error("a should have been evicted")
	}
	if !s.contains([]byte("c")) || !s.contains([]byte("d")) {
		t.Error("recent entries must survive")
	}
	add("b") // refresh b to front, then force another eviction
	add("e")
	if s.contains([]byte("c")) {
		t.Error("c should be evicted next")
	}
	if !s.contains([]byte("b")) {
		t.Error("refreshed b must survive eviction")
	}
}

func TestMetadataAssembler(t *testing.T) {
	// Build an info dict larger than one 16 KiB ut_metadata piece so the
	// multi-piece assembly path is actually exercised.
	bigName := strings.Repeat("z", 40_000)
	infoDict := []byte(fmt.Sprintf("d4:name%d:%s6:lengthi1000ee", len(bigName), bigName))
	sum := sha1.Sum(infoDict)

	f := &metadataAssembler{ih: sum, max: 1 << 20}
	f.setSize(int64(len(infoDict)))
	if f.pieceCount() < 2 {
		t.Fatalf("fixture too small for multi-piece test (%d pieces)", f.pieceCount())
	}

	frame := func(piece int64, chunk []byte) []byte {
		dict := bencodeMap(krpcMsg{"msg_type": int64(1), "piece": piece, "total_size": int64(len(infoDict))})
		return append(dict, chunk...)
	}

	const pieceSize = 16384
	pc := f.pieceCount()
	for i := 0; i < pc; i++ {
		start := i * pieceSize
		end := start + pieceSize
		if end > len(infoDict) {
			end = len(infoDict)
		}
		if err := f.consumeFrame(frame(int64(i), infoDict[start:end])); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if i < pc-1 && f.complete() {
			t.Fatalf("complete after only %d/%d pieces", i+1, pc)
		}
	}
	if !f.complete() {
		t.Fatal("should be complete after all pieces")
	}
	got, err := f.assemble()
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if !bytes.Equal(got, infoDict) {
		t.Error("assembled bytes differ from original info dict")
	}
}

func TestMetadataAssemblerRejectsWrongHash(t *testing.T) {
	infoDict := []byte("d4:name8:test.mkv6:lengthi1000ee")
	var wrongIH [20]byte
	wrongIH[0] = 0xAA

	f := &metadataAssembler{ih: wrongIH, max: 1 << 20}
	f.setSize(int64(len(infoDict)))
	frame := append(
		bencodeMap(krpcMsg{"msg_type": int64(1), "piece": int64(0), "total_size": int64(len(infoDict))}),
		infoDict...)
	if err := f.consumeFrame(frame); err != nil {
		t.Fatal(err)
	}
	if _, err := f.assemble(); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("expected hash mismatch, got %v", err)
	}
}

func TestMetadataAssemblerRejectsOversize(t *testing.T) {
	f := &metadataAssembler{max: 1024}
	if err := f.setSize(10 << 20); err == nil {
		t.Fatal("oversized metadata_size must be rejected")
	}
}

func TestParseCompactNodes(t *testing.T) {
	idx := &DHTIndexer{
		visited:   map[string]bool{},
		nodeQueue: make(chan string, 16),
	}
	nodes := make([]byte, 0, 52)
	nodes = append(nodes, make([]byte, 20)...)          // id
	nodes = append(nodes, netIPBytes("77.11.22.33")...) // ip
	var port [2]byte
	binary.BigEndian.PutUint16(port[:], 6881)
	nodes = append(nodes, port[:]...)
	nodes = append(nodes, make([]byte, 26)...) // zero node: skipped

	idx.ingestCompactNodes(nodes)
	select {
	case a := <-idx.nodeQueue:
		if a != "77.11.22.33:6881" {
			t.Errorf("addr = %q", a)
		}
	default:
		t.Fatal("no node enqueued")
	}
	// Only one entry: the all-zero second node was dropped.
	if len(idx.nodeQueue) != 0 {
		t.Errorf("zero-node should not enqueue; queue depth %d", len(idx.nodeQueue))
	}
}
