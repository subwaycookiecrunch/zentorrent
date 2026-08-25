package search

import (
	"container/list"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"

	_ "modernc.org/sqlite"
)

// DHTConfig tunes the crawler. Zero values select production defaults.
type DHTConfig struct {
	// DBPath for the local metainfo index (created if missing).
	DBPath string
	// Bootstrap UDP addresses; defaults to public router nodes.
	Bootstrap []string
	// NodeWorkers sample nodes concurrently (default 24).
	NodeWorkers int
	// FetchWorkers dial peers for BEP09 metadata (default 12).
	FetchWorkers int
	// DialRPS caps peer dialing (default 40).
	DialRPS float64
	// FetchTimeout bounds one peer metadata exchange (default 12s).
	FetchTimeout time.Duration
	// MaxInfoBytes rejects absurd metadata payloads (default 4 MiB).
	MaxInfoBytes int64
	// MaxFailedPeerAttempts per infohash before giving up this session.
	MaxFailedPeerAttempts int
}

func (c DHTConfig) withDefaults() DHTConfig {
	if len(c.Bootstrap) == 0 {
		c.Bootstrap = []string{
			"router.bittorrent.com:6881",
			"router.utorrent.com:6881",
			"dht.transmissionbt.com:6881",
			"dht.libtorrent.org:25401",
			"router.silotis.us:6881",
		}
	}
	if c.NodeWorkers <= 0 {
		c.NodeWorkers = 24
	}
	if c.FetchWorkers <= 0 {
		c.FetchWorkers = 12
	}
	if c.DialRPS <= 0 {
		c.DialRPS = 40
	}
	if c.FetchTimeout <= 0 {
		c.FetchTimeout = 12 * time.Second
	}
	if c.MaxInfoBytes <= 0 {
		c.MaxInfoBytes = 4 << 20
	}
	if c.MaxFailedPeerAttempts <= 0 {
		c.MaxFailedPeerAttempts = 3
	}
	return c
}

// DHTIndexer passively crawls the Mainline DHT (BEP 05), samples infohashes
// (BEP 51), fetches metadata from peers (BEP 09), and indexes everything
// into a local SQLite FTS store.
type DHTIndexer struct {
	cfg    DHTConfig
	db     *sql.DB
	conn   *net.UDPConn
	nodeID [20]byte

	// KRPC transaction demux.
	pendMu  sync.Mutex
	pending map[string]chan krpcResult
	txSeq   uint16

	// Dedup layers: hot LRU -> epoch bloom -> DB existence.
	lru   *lruSet
	bloom *bloomFilter

	visitedMu sync.Mutex
	visited   map[string]bool

	goodNodesMu sync.Mutex
	goodNodes   []string // responsive node addrs for get_peers

	hashCh    chan [20]byte
	peerCh    chan peerTarget
	nodeQueue chan string

	failMu    sync.Mutex
	failCount map[[20]byte]int

	dialLimiter *rate.Limiter

	done       chan struct{}
	shutdownMu sync.Mutex
	wg         sync.WaitGroup

	// seenMu guards the dedup trio (lru, bloom) which node workers touch
	// concurrently; bloom swaps are included so no torn state is observable.
	seenMu sync.Mutex

	knownHashes atomic.Int64
}

type peerTarget struct {
	addr string
	ih   [20]byte
}

type krpcResult struct {
	resp map[string]any
	err  error
}

// NewDHTIndexer opens the index database and binds a UDP socket. Call Run to
// start crawling; Search works even while crawling.
func NewDHTIndexer(cfg DHTConfig) (*DHTIndexer, error) {
	cfg = cfg.withDefaults()

	db, err := sql.Open("sqlite", "file:"+cfg.DBPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("dht: open index db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := ensureDHTSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("dht: bind udp: %w", err)
	}

	idx := &DHTIndexer{
		cfg:         cfg,
		db:          db,
		conn:        conn,
		pending:     map[string]chan krpcResult{},
		lru:         newLRUSet(200_000),
		bloom:       newBloomFilter(1 << 21), // ~3M hashes per epoch at <1% FP
		visited:     map[string]bool{},
		hashCh:      make(chan [20]byte, 4096),
		peerCh:      make(chan peerTarget, 4096),
		nodeQueue:   make(chan string, 2048),
		failCount:   map[[20]byte]int{},
		dialLimiter: rate.NewLimiter(rate.Limit(cfg.DialRPS), int(cfg.DialRPS)),
		done:        make(chan struct{}),
	}
	if _, err := rand.Read(idx.nodeID[:]); err != nil {
		idx.Close()
		return nil, fmt.Errorf("dht: node id: %w", err)
	}
	var n int64
	_ = db.QueryRow(`SELECT COUNT(*) FROM dht_torrents`).Scan(&n)
	idx.knownHashes.Store(n)
	return idx, nil
}

// Close stops the indexer if running and releases resources.
func (idx *DHTIndexer) Close() error {
	idx.shutdown()
	idx.wg.Wait()
	return idx.db.Close()
}

// Stats exposes live counters for UIs.
func (idx *DHTIndexer) Stats() (known int64, queueDepth int) {
	known = idx.knownHashes.Load()
	queueDepth = len(idx.hashCh) + len(idx.peerCh)
	return
}

func ensureDHTSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS dht_torrents (
			infohash   TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			size       INTEGER NOT NULL DEFAULT 0,
			files      INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS dht_fts USING fts5(
			name, content='dht_torrents', content_rowid='rowid', tokenize='trigram'
		)`,
		`CREATE TRIGGER IF NOT EXISTS dht_ai AFTER INSERT ON dht_torrents BEGIN
			INSERT INTO dht_fts(rowid, name) VALUES (new.rowid, new.name);
		END`,
		`CREATE TRIGGER IF NOT EXISTS dht_ad AFTER DELETE ON dht_torrents BEGIN
			INSERT INTO dht_fts(dht_fts, rowid, name) VALUES('delete', old.rowid, old.name);
		END`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("dht: schema: %w", err)
		}
	}
	return nil
}

// Run blocks until ctx is cancelled, crawling and indexing. Safe to call once.
func (idx *DHTIndexer) Run(ctx context.Context) error {
	idx.wg.Add(1)
	go idx.readLoop()

	// Seed the node queue with bootstrap resolvers.
	idx.wg.Add(1)
	go func() {
		defer idx.wg.Done()
		for _, b := range idx.cfg.Bootstrap {
			if addr, err := net.ResolveUDPAddr("udp", b); err == nil {
				idx.pushNode(addr.String())
			}
		}
	}()

	// Stage 1: sample nodes for infohashes. Workers pull straight from
	// idx.nodeQueue which bootstrap + crawl results keep topped up.
	var stageWG sync.WaitGroup
	for i := 0; i < idx.cfg.NodeWorkers; i++ {
		stageWG.Add(1)
		go func() {
			defer stageWG.Done()
			defer idx.recoverLoop("node worker")
			for {
				select {
				case <-ctx.Done():
					return
				case <-idx.done:
					return
				case addr := <-idx.nodeQueue:
					idx.sampleNode(ctx, addr)
				case <-time.After(500 * time.Millisecond):
					// queue may be momentarily empty while discovery warms up
				}
			}
		}()
	}

	// Stage 2: resolve infohashes to peer lists.
	for i := 0; i < idx.cfg.FetchWorkers/2+2; i++ {
		stageWG.Add(1)
		go func() {
			defer stageWG.Done()
			defer idx.recoverLoop("peer resolver")
			for {
				select {
				case <-ctx.Done():
					return
				case <-idx.done:
					return
				case ih := <-idx.hashCh:
					idx.resolvePeers(ctx, ih)
				}
			}
		}()
	}

	// Stage 3: BEP09 metadata fetchers.
	for i := 0; i < idx.cfg.FetchWorkers; i++ {
		stageWG.Add(1)
		go func() {
			defer stageWG.Done()
			defer idx.recoverLoop("metadata worker")
			for {
				select {
				case <-ctx.Done():
					return
				case <-idx.done:
					return
				case pt := <-idx.peerCh:
					idx.fetchAndIndex(ctx, pt)
				}
			}
		}()
	}

	<-ctx.Done()
	idx.shutdown()
	stageWG.Wait()
	idx.wg.Wait()
	return nil
}

// shutdown closes the done channel exactly once and unblocks the UDP read
// loop so every stage can wind down. Safe from any goroutine.
func (idx *DHTIndexer) shutdown() {
	idx.shutdownMu.Lock()
	defer idx.shutdownMu.Unlock()
	select {
	case <-idx.done:
		return
	default:
		close(idx.done)
		// Closing the socket forces ReadFromUDP to error out; without this,
		// cancelling ctx alone would strand readLoop inside the kernel call.
		idx.conn.Close()
	}
}

func (idx *DHTIndexer) recoverLoop(stage string) {
	if r := recover(); r != nil {
		fmt.Printf("dht: recovered %s panic: %v\n", stage, r)
	}
}

// ---- node queue ----------------------------------------------------------

func (idx *DHTIndexer) pushNode(addr string) {
	idx.visitedMu.Lock()
	defer idx.visitedMu.Unlock()
	if idx.visited[addr] {
		return
	}
	if len(idx.visited) > 500_000 {
		// Reset instead of refusing: a permanent cap would silently halt the
		// crawl on long sessions. Rediscovery churn is cheaper than stalling.
		idx.visited = map[string]bool{addr: true}
	} else {
		idx.visited[addr] = true
	}
	select {
	case idx.nodeQueue <- addr:
	default: // drop if saturated; the crawler will rediscover the node
	}
}

// ---- KRPC transport --------------------------------------------------------

type krpcMsg map[string]any

func (idx *DHTIndexer) readLoop() {
	defer idx.wg.Done()
	buf := make([]byte, 65536)
	for {
		n, raddr, err := idx.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-idx.done:
				return
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		var v any
		if err := bdecodeInto(buf[:n], &v); err != nil {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		txb, _ := byteSlice(m, "t")
		txid := string(txb)
		if string(maybeString(m, "y")) == "q" {
			// Be a polite passive crawler: reply with a protocol error.
			idx.replyError(raddr, txid, 204, "method unknown")
			continue
		}
		idx.pendMu.Lock()
		ch, ok := idx.pending[txid]
		if ok {
			delete(idx.pending, txid)
		}
		idx.pendMu.Unlock()
		if !ok {
			continue
		}
		if string(maybeString(m, "y")) == "e" {
			if el, ok := m["e"].([]any); ok && len(el) >= 1 {
				ch <- krpcResult{err: fmt.Errorf("dht error %v: %s", el[0], maybeString(el[len(el)-1], ""))}
			} else {
				ch <- krpcResult{err: errors.New("dht error")}
			}
			continue
		}
		if r, ok := m["r"].(map[string]any); ok {
			ch <- krpcResult{resp: r}
		} else {
			ch <- krpcResult{err: errors.New("malformed response")}
		}
	}
}

func (idx *DHTIndexer) replyError(to *net.UDPAddr, txid string, code int, msg string) {
	e := []any{code, msg}
	out := bencodeMap(krpcMsg{"t": []byte(txid), "y": "e", "e": e})
	_, _ = idx.conn.WriteToUDP(out, to)
}

func (idx *DHTIndexer) query(ctx context.Context, addr string, method string, args krpcMsg) (krpcMsg, error) {
	var tx [2]byte
	idx.pendMu.Lock()
	idx.txSeq++
	binary.BigEndian.PutUint16(tx[:], idx.txSeq)
	txid := string(tx[:])
	ch := make(chan krpcResult, 1)
	idx.pending[txid] = ch
	idx.pendMu.Unlock()

	args["t"] = []byte(txid)
	args["y"] = "q"
	args["q"] = method
	packet := bencodeMap(args)

	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		idx.dropPending(txid)
		return nil, err
	}
	if _, err := idx.conn.WriteToUDP(packet, ua); err != nil {
		idx.dropPending(txid)
		return nil, err
	}

	t := time.NewTimer(4 * time.Second)
	defer t.Stop()
	select {
	case res := <-ch:
		return res.resp, res.err
	case <-t.C:
		idx.dropPending(txid)
		return nil, errors.New("dht query timeout")
	case <-ctx.Done():
		idx.dropPending(txid)
		return nil, ctx.Err()
	}
}

func (idx *DHTIndexer) dropPending(txid string) {
	idx.pendMu.Lock()
	if ch, ok := idx.pending[txid]; ok {
		delete(idx.pending, txid)
		close(ch)
	}
	idx.pendMu.Unlock()
}

// ---- crawling stages --------------------------------------------------------

func (idx *DHTIndexer) sampleNode(ctx context.Context, addr string) {
	target := randomTarget()
	resp, err := idx.query(ctx, addr, "sample_infohashes", krpcMsg{
		"id":     idx.nodeID[:],
		"target": target[:],
	})
	if err != nil {
		// Node may not support BEP51; still harvest its routing table.
		idx.harvestNodesFromGetPeers(ctx, addr)
		return
	}
	idx.rememberGoodNode(addr)

	if nodes, ok := byteSlice(resp, "nodes"); ok {
		idx.ingestCompactNodes(nodes)
	}
	samples, ok := byteSlice(resp, "samples")
	if !ok {
		return
	}
	for off := 0; off+20 <= len(samples); off += 20 {
		var ih [20]byte
		copy(ih[:], samples[off:off+20])
		idx.offerHash(ih)
	}
}

func (idx *DHTIndexer) harvestNodesFromGetPeers(ctx context.Context, addr string) {
	var t [20]byte
	rand.Read(t[:])
	resp, err := idx.query(ctx, addr, "get_peers", krpcMsg{
		"id":        idx.nodeID[:],
		"info_hash": t[:],
	})
	if err != nil {
		return
	}
	if nodes, ok := byteSlice(resp, "nodes"); ok {
		idx.ingestCompactNodes(nodes)
	}
}

func (idx *DHTIndexer) ingestCompactNodes(nodes []byte) {
	for off := 0; off+26 <= len(nodes); off += 26 {
		ip := net.IP(nodes[off+20 : off+24])
		port := binary.BigEndian.Uint16(nodes[off+24 : off+26])
		if ip.IsUnspecified() || port == 0 {
			continue
		}
		idx.pushNode(net.JoinHostPort(ip.String(), strconv.Itoa(int(port))))
	}
}

func (idx *DHTIndexer) rememberGoodNode(addr string) {
	idx.goodNodesMu.Lock()
	defer idx.goodNodesMu.Unlock()
	if len(idx.goodNodes) >= 128 {
		// Rotate: drop a random old entry to keep the pool fresh.
		idx.goodNodes[mathrand.Intn(len(idx.goodNodes))] = addr
		return
	}
	idx.goodNodes = append(idx.goodNodes, addr)
}

func (idx *DHTIndexer) randomGoodNode() string {
	idx.goodNodesMu.Lock()
	defer idx.goodNodesMu.Unlock()
	if len(idx.goodNodes) == 0 {
		return ""
	}
	return idx.goodNodes[mathrand.Intn(len(idx.goodNodes))]
}

// offerHash dedups and queues a freshly sampled infohash. The bloom/LRU pair
// is hot-path shared across node workers, so it moves under seenMu.
func (idx *DHTIndexer) offerHash(ih [20]byte) {
	idx.seenMu.Lock()
	if idx.lru.contains(ih[:]) || idx.bloom.mayContain(ih) {
		idx.seenMu.Unlock()
		return
	}
	idx.lru.add(ih[:])
	idx.bloom.add(ih)
	swap := idx.bloom.fillRatio() > 0.5
	if swap {
		idx.bloom = newBloomFilter(1 << 21)
	}
	idx.seenMu.Unlock()

	var exists int
	if err := idx.db.QueryRow(`SELECT 1 FROM dht_torrents WHERE infohash = ?`,
		hex.EncodeToString(ih[:])).Scan(&exists); err == nil {
		return // already indexed in a previous session
	}

	select {
	case idx.hashCh <- ih:
	case <-time.After(2 * time.Second): // backpressure: drop rather than stall
	}
}

// resolvePeers asks a couple of good nodes for peers of ih.
func (idx *DHTIndexer) resolvePeers(ctx context.Context, ih [20]byte) {
	for attempt := 0; attempt < 2; attempt++ {
		node := idx.randomGoodNode()
		if node == "" {
			return
		}
		resp, err := idx.query(ctx, node, "get_peers", krpcMsg{
			"id":        idx.nodeID[:],
			"info_hash": ih[:],
		})
		if err != nil {
			continue
		}
		if nodes, ok := byteSlice(resp, "nodes"); ok {
			idx.ingestCompactNodes(nodes)
		}
		if values, ok := resp["values"].([]any); ok {
			sent := 0
			for _, v := range values {
				p, ok := v.([]byte)
				if !ok || len(p) < 6 {
					continue
				}
				ip := net.IP(p[:4])
				port := binary.BigEndian.Uint16(p[4:6])
				if ip.IsUnspecified() || port == 0 {
					continue
				}
				select {
				case idx.peerCh <- peerTarget{
					addr: net.JoinHostPort(ip.String(), strconv.Itoa(int(port))),
					ih:   ih,
				}:
					sent++
				default:
				}
				if sent >= 6 { // a handful of peers is plenty per hash
					return
				}
			}
			if sent > 0 {
				return
			}
		}
	}
}

// ---- BEP09 metadata fetch -----------------------------------------------------

func (idx *DHTIndexer) fetchAndIndex(ctx context.Context, pt peerTarget) {
	// Claim the attempt BEFORE dialing: counting only failures let concurrent
	// workers for the same hash all pass the cap simultaneously.
	idx.failMu.Lock()
	if idx.failCount[pt.ih] >= idx.cfg.MaxFailedPeerAttempts {
		idx.failMu.Unlock()
		return
	}
	idx.failCount[pt.ih]++
	if len(idx.failCount) > 10_000 {
		// Bound memory; bloom + DB remain the real dedup layers.
		idx.failCount = make(map[[20]byte]int)
	}
	idx.failMu.Unlock()

	if err := idx.dialLimiter.Wait(ctx); err != nil {
		return
	}

	infoBytes, err := fetchMetadataFromPeer(ctx, pt.addr, pt.ih, idx.cfg)
	if err == nil {
		idx.failMu.Lock()
		delete(idx.failCount, pt.ih) // healthy peer: forget prior failures
		idx.failMu.Unlock()
	}
	if err != nil {
		return
	}

	var info metainfo.Info
	if err := bencode.Unmarshal(infoBytes, &info); err != nil {
		return
	}
	name := info.Name
	if name == "" {
		return
	}
	var size int64
	files := len(info.Files)
	if files > 0 {
		for _, f := range info.Files {
			size += f.Length
		}
	} else {
		size = info.Length
	}

	ihHex := hex.EncodeToString(pt.ih[:])
	res, err := idx.db.Exec(`INSERT INTO dht_torrents (infohash, name, size, files)
		VALUES (?,?,?,?)
		ON CONFLICT(infohash) DO NOTHING`, ihHex, name, size, files)
	if err != nil {
		return
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		idx.knownHashes.Add(1)
	}
}

// fetchMetadataFromPeer performs a BitTorrent handshake plus BEP09
// ut_metadata exchange over TCP and returns the verified info dict bytes.
func fetchMetadataFromPeer(ctx context.Context, addr string, ih [20]byte, cfg DHTConfig) ([]byte, error) {
	d := net.Dialer{Timeout: 4 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(cfg.FetchTimeout)
	conn.SetDeadline(deadline)

	var peerID [20]byte
	rand.Read(peerID[:])
	copy(peerID[:8], "-ZT0400-")

	hs := make([]byte, 0, 68)
	hs = append(hs, 19)
	hs = append(hs, "BitTorrent protocol"...)
	reserved := make([]byte, 8)
	reserved[5] = 0x10 // extension protocol
	reserved[7] = 0x01 // DHT
	hs = append(hs, reserved...)
	hs = append(hs, ih[:]...)
	hs = append(hs, peerID[:]...)

	if _, err := conn.Write(hs); err != nil {
		return nil, err
	}
	echo := make([]byte, 68)
	if _, err := io.ReadFull(conn, echo); err != nil {
		return nil, err
	}
	if string(echo[1:20]) != "BitTorrent protocol" {
		return nil, errors.New("bad protocol string")
	}
	// Reserved window is bytes 20..28; extension bit lives in byte 5 of it.
	if echo[20+5]&0x10 == 0 {
		return nil, errors.New("peer lacks extension protocol")
	}
	if !equalBytes(echo[28:48], ih[:]) {
		return nil, errors.New("infohash mismatch")
	}

	// Our extended handshake: we serve nothing, we want ut_metadata.
	extHS := bencodeMap(krpcMsg{
		"m":    map[string]any{"ut_metadata": 2},
		"p":    0,
		"v":    "ZenTorrent 4.0",
		"reqq": 250,
	})
	if err := writeExtMessage(conn, 0, extHS); err != nil {
		return nil, err
	}

	f := &metadataAssembler{ih: ih, max: cfg.MaxInfoBytes}
	var theirUtID byte = 0
	requested := 0

	// requestNext sends the next ut_metadata piece request. When the total
	// size is still unknown we ask for piece 0 anyway: the data reply carries
	// total_size, which unblocks the rest.
	requestNext := func() error {
		if theirUtID == 0 {
			return nil
		}
		if f.totalSize > 0 && requested >= f.pieceCount() {
			return nil
		}
		req := bencodeMap(krpcMsg{"msg_type": 0, "piece": requested})
		if err := writeExtMessage(conn, theirUtID, req); err != nil {
			return err
		}
		requested++
		return nil
	}

	for {
		msg, err := readMessage(conn)
		if err != nil {
			return nil, err
		}
		if len(msg) < 2 || msg[0] != 20 {
			continue // keepalive / standard wire messages (choke, have, ...)
		}
		extID := msg[1]
		payload := msg[2:]

		switch extID {
		case 0: // their extended handshake
			var decoded any
			if err := bdecodeInto(payload, &decoded); err != nil {
				return nil, err
			}
			hs, ok := decoded.(map[string]any)
			if !ok {
				return nil, errors.New("extended handshake not a dict")
			}
			m, _ := hs["m"].(map[string]any)
			if m == nil {
				return nil, errors.New("no extension map")
			}
			id, ok := m["ut_metadata"].(int64)
			if !ok || id == 0 {
				return nil, errors.New("peer lacks ut_metadata")
			}
			theirUtID = byte(id)
			if sz, ok := hs["metadata_size"].(int64); ok && sz > 0 && f.totalSize == 0 {
				if err := f.setSize(sz); err != nil {
					return nil, err
				}
			}
		case theirUtID:
			if err := f.consumeFrame(payload); err != nil {
				return nil, err
			}
			if f.totalSize > 0 && f.complete() {
				return f.assemble()
			}
		}

		if err := requestNext(); err != nil {
			return nil, err
		}
	}
}

func writeExtMessage(conn net.Conn, extID byte, payload []byte) error {
	buf := make([]byte, 0, len(payload)+6)
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(payload)+2))
	buf = append(buf, l[:]...)
	buf = append(buf, 20, extID)
	buf = append(buf, payload...)
	_, err := conn.Write(buf)
	return err
}

func readMessage(conn net.Conn) ([]byte, error) {
	var l [4]byte
	if _, err := io.ReadFull(conn, l[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(l[:])
	if n == 0 {
		return nil, nil // keepalive
	}
	if n > 1<<20 {
		return nil, fmt.Errorf("oversized wire message %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// metadataAssembler reassembles ut_metadata pieces.
type metadataAssembler struct {
	ih        [20]byte
	max       int64
	totalSize int64
	pieces    map[int][]byte
}

func (f *metadataAssembler) setSize(n int64) error {
	if n <= 0 || n > f.max {
		return fmt.Errorf("metadata_size %d out of range", n)
	}
	f.totalSize = n
	// A late-arriving size can strand earlier pieces beyond the piece count,
	// making complete() permanently unsatisfiable.
	for i := range f.pieces {
		if i >= f.pieceCount() {
			delete(f.pieces, i)
		}
	}
	return nil
}

func (f *metadataAssembler) pieceCount() int {
	if f.totalSize == 0 {
		return 0
	}
	return int((f.totalSize + 16383) / 16384)
}

func (f *metadataAssembler) complete() bool {
	if f.pieces == nil {
		return false
	}
	return len(f.pieces) == f.pieceCount()
}

// consumeFrame parses one ut_metadata payload: bencoded dict followed by
// optional raw piece data.
func (f *metadataAssembler) consumeFrame(payload []byte) error {
	var v any
	n, err := bdecodePrefix(payload, &v)
	if err != nil {
		return err
	}
	d, ok := v.(map[string]any)
	if !ok {
		return errors.New("ut_metadata frame not a dict")
	}
	msgType, _ := d["msg_type"].(int64)
	switch msgType {
	case 1: // data
		piece, _ := d["piece"].(int64)
		if sz, ok := d["total_size"].(int64); ok && f.totalSize == 0 {
			if err := f.setSize(sz); err != nil {
				return err
			}
		}
		if f.totalSize > 0 && (piece < 0 || int(piece) >= f.pieceCount()) {
			return fmt.Errorf("ut_metadata piece %d out of range", piece)
		}
		data := payload[n:]
		if f.pieces == nil {
			f.pieces = map[int][]byte{}
		}
		f.pieces[int(piece)] = append([]byte(nil), data...)
		return nil
	case 2: // reject
		return errors.New("metadata piece rejected")
	default:
		return fmt.Errorf("unexpected msg_type %d", msgType)
	}
}

func (f *metadataAssembler) assemble() ([]byte, error) {
	out := make([]byte, 0, f.totalSize)
	for i := 0; i < f.pieceCount(); i++ {
		out = append(out, f.pieces[i]...)
	}
	if int64(len(out)) != f.totalSize {
		return nil, errors.New("assembled size mismatch")
	}
	sum := sha1.Sum(out)
	if !equalBytes(sum[:], f.ih[:]) {
		return nil, errors.New("info dict hash mismatch")
	}
	return out, nil
}

// ---- Search ----------------------------------------------------------------

// Search queries the local DHT metainfo index with trigram fuzzy matching.
// Multi-word queries degrade from whole-phrase to ANDed terms so titles like
// "the dark knight" still surface.
func (idx *DHTIndexer) Search(ctx context.Context, query string, limit int) ([]TorrentCandidate, error) {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 25
	}

	attempts := []string{
		`"` + strings.ReplaceAll(query, `"`, `""`) + `"`,
	}
	terms := strings.Fields(query)
	if len(terms) > 1 {
		parts := make([]string, 0, len(terms))
		for _, t := range terms {
			q := "\""
			parts = append(parts, q+strings.ReplaceAll(t, "\"", q+q)+q)
		}
		attempts = append(attempts, strings.Join(parts, " AND "))
	}

	var out []TorrentCandidate
	for _, match := range attempts {
		rows, err := idx.db.QueryContext(ctx, `
			SELECT t.infohash, t.name, t.size, t.files
			FROM dht_fts f JOIN dht_torrents t ON t.rowid = f.rowid
			WHERE dht_fts MATCH ?
			ORDER BY rank LIMIT ?`, match, limit)
		if err != nil {
			return nil, fmt.Errorf("dht search: %w", err)
		}
		out = out[:0]
		for rows.Next() {
			var (
				ih, name string
				size     int64
				files    int
			)
			if err := rows.Scan(&ih, &name, &size, &files); err == nil {
				out = append(out, TorrentCandidate{
					InfoHash:  ih,
					Title:     name,
					SizeBytes: size,
					Source:    "dht",
				})
			}
		}
		rows.Close()
		if len(out) > 0 {
			return out, rows.Err()
		}
	}
	return out, nil
}

// ---- bencode (minimal, allocation-conscious) ---------------------------------

func bencodeMap(m krpcMsg) []byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf strings.Builder
	buf.WriteByte('d')
	for _, k := range keys {
		bencodeAppend(&buf, k)
		bencodeAppend(&buf, m[k])
	}
	buf.WriteByte('e')
	return []byte(buf.String())
}

func bencodeAppend(buf *strings.Builder, v any) {
	switch x := v.(type) {
	case string:
		buf.WriteString(strconv.Itoa(len(x)))
		buf.WriteByte(':')
		buf.WriteString(x)
	case []byte:
		buf.WriteString(strconv.Itoa(len(x)))
		buf.WriteByte(':')
		buf.Write(x)
	case int:
		buf.WriteByte('i')
		buf.WriteString(strconv.Itoa(x))
		buf.WriteByte('e')
	case int64:
		buf.WriteByte('i')
		buf.WriteString(strconv.FormatInt(x, 10))
		buf.WriteByte('e')
	case []any:
		buf.WriteByte('l')
		for _, item := range x {
			bencodeAppend(buf, item)
		}
		buf.WriteByte('e')
	case map[string]any:
		buf.Write(bencodeMap(x))
	default:
		panic(fmt.Sprintf("bencode: unsupported type %T", v))
	}
}

// bdecodeInto decodes the first value in data into *out.
func bdecodeInto(data []byte, out *any) error {
	_, err := bdecodePrefix(data, out)
	return err
}

// bdecodePrefix decodes the first bencoded value, returning bytes consumed.
func bdecodePrefix(data []byte, out *any) (int, error) {
	if len(data) == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	switch data[0] {
	case 'i':
		end := -1
		for i := 1; i < len(data); i++ {
			if data[i] == 'e' {
				end = i
				break
			}
		}
		if end < 0 {
			return 0, io.ErrUnexpectedEOF
		}
		n, err := strconv.ParseInt(string(data[1:end]), 10, 64)
		if err != nil {
			return 0, err
		}
		*out = n
		return end + 1, nil
	case 'l':
		pos := 1
		var list []any
		for {
			if pos >= len(data) {
				return 0, io.ErrUnexpectedEOF
			}
			if data[pos] == 'e' {
				*out = list
				return pos + 1, nil
			}
			var item any
			consumed, err := bdecodePrefix(data[pos:], &item)
			if err != nil {
				return 0, err
			}
			list = append(list, item)
			pos += consumed
		}
	case 'd':
		pos := 1
		dict := map[string]any{}
		for {
			if pos >= len(data) {
				return 0, io.ErrUnexpectedEOF
			}
			if data[pos] == 'e' {
				*out = dict
				return pos + 1, nil
			}
			var keyAny any
			consumed, err := bdecodePrefix(data[pos:], &keyAny)
			if err != nil {
				return 0, err
			}
			keyBytes, ok := keyAny.([]byte)
			if !ok {
				return 0, errors.New("dict key not string")
			}
			pos += consumed
			var val any
			consumed, err = bdecodePrefix(data[pos:], &val)
			if err != nil {
				return 0, err
			}
			dict[string(keyBytes)] = val
			pos += consumed
		}
	default:
		// Byte string: <len>:<bytes>
		colon := -1
		for i := 0; i < len(data) && i < 20; i++ {
			if data[i] == ':' {
				colon = i
				break
			}
		}
		if colon <= 0 {
			return 0, errors.New("bad string length")
		}
		n, err := strconv.Atoi(string(data[:colon]))
		if err != nil || n < 0 {
			return 0, errors.New("bad string length")
		}
		start := colon + 1
		if start+n > len(data) {
			return 0, io.ErrUnexpectedEOF
		}
		raw := make([]byte, n)
		copy(raw, data[start:start+n])
		*out = raw
		return start + n, nil
	}
}

func byteSlice(m map[string]any, key string) ([]byte, bool) {
	v, ok := m[key].([]byte)
	return v, ok
}

func maybeString(v any, def string) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return def
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func randomTarget() [20]byte {
	var t [20]byte
	rand.Read(t[:])
	return t
}

// atomicInt64 replaced by sync/atomic.Int64 (kept name out of the API).

// ---- dedup primitives --------------------------------------------------------

// bloomFilter: fixed-size bit array with double hashing from SHA-1.
type bloomFilter struct {
	bits []uint64
	m    uint64
	k    uint32
}

func newBloomFilter(expected int) *bloomFilter {
	m := uint64(expected) * 24
	words := (m + 63) / 64
	return &bloomFilter{bits: make([]uint64, words), m: words * 64, k: 5}
}

func (b *bloomFilter) idxs(key [20]byte) [5]uint64 {
	h := sha1.Sum(key[:])
	h1 := binary.BigEndian.Uint64(h[0:8])
	h2 := binary.BigEndian.Uint64(h[8:16])
	var out [5]uint64
	for i := uint32(0); i < b.k; i++ {
		out[i] = (h1 + uint64(i)*h2 + uint64(i)*uint64(i)) % b.m
	}
	return out
}

func (b *bloomFilter) add(key [20]byte) {
	for _, i := range b.idxs(key) {
		b.bits[i/64] |= 1 << (i % 64)
	}
}

func (b *bloomFilter) mayContain(key [20]byte) bool {
	for _, i := range b.idxs(key) {
		if b.bits[i/64]&(1<<(i%64)) == 0 {
			return false
		}
	}
	return true
}

func (b *bloomFilter) fillRatio() float64 {
	var set uint64
	for _, w := range b.bits {
		set += popcount(w)
	}
	return float64(set) / float64(b.m)
}

func popcount(x uint64) uint64 {
	n := 0
	for x != 0 {
		x &= x - 1
		n++
	}
	return uint64(n)
}

// lruSet is a bounded insertion-recency set for hot-path dedup.
type lruSet struct {
	cap   int
	ll    *list.List
	items map[string]*list.Element
	mu    sync.Mutex
}

type lruEntry struct {
	key string
}

func newLRUSet(capacity int) *lruSet {
	return &lruSet{
		cap:   capacity,
		ll:    list.New(),
		items: map[string]*list.Element{},
	}
}

func (s *lruSet) contains(key []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.items[string(key)]
	return ok
}

func (s *lruSet) add(key []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := string(key)
	if el, ok := s.items[k]; ok {
		s.ll.MoveToFront(el)
		return
	}
	el := s.ll.PushFront(&lruEntry{key: k})
	s.items[k] = el
	if s.ll.Len() > s.cap {
		oldest := s.ll.Back()
		if oldest != nil {
			s.ll.Remove(oldest)
			delete(s.items, oldest.Value.(*lruEntry).key)
		}
	}
}
