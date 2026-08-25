// Package engine owns ZenTorrent's single long-lived BitTorrent client —
// shared by streaming, prefetching and search-result prewarming so DHT
// bootstrap, tracker state and NAT traversal stay warm between plays — plus
// the per-torrent metadata cache/mirror acceleration.
package engine

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

var (
	engineMu      sync.Mutex
	engineCl      *torrent.Client
	engineDataDir string
)

// activeStream is the torrent currently being watched, so background jobs
// (search prewarm, DHT indexing) can stay out of its way.
var (
	activeStreamMu sync.Mutex
	activeStream   *torrent.Torrent
)

// prefetchHash is the queued playlist item being warmed in the background.
var (
	prefetchMu   sync.Mutex
	prefetchHash metainfo.Hash
)

func SetActive(t *torrent.Torrent) {
	activeStreamMu.Lock()
	activeStream = t
	activeStreamMu.Unlock()
}

func IsActiveStreaming() bool {
	activeStreamMu.Lock()
	defer activeStreamMu.Unlock()
	return activeStream != nil
}

func MarkPrefetch(t *torrent.Torrent) {
	prefetchMu.Lock()
	prefetchHash = t.InfoHash()
	prefetchMu.Unlock()
}

func UnmarkPrefetch(t *torrent.Torrent) {
	prefetchMu.Lock()
	if prefetchHash == t.InfoHash() {
		prefetchHash = metainfo.Hash{}
	}
	prefetchMu.Unlock()
}

func Get() (*torrent.Client, string, error) {
	engineMu.Lock()
	defer engineMu.Unlock()

	if engineCl != nil {
		return engineCl, engineDataDir, nil
	}

	dir, err := os.MkdirTemp("", "zt-engine-*")
	if err != nil {
		return nil, "", err
	}

	cfg := BaseConfig(dir)
	cfg.EstablishedConnsPerTorrent = 200
	cfg.HalfOpenConnsPerTorrent = 150
	cfg.NominalDialTimeout = 2 * time.Second

	cl, err := torrent.NewClient(cfg)
	if err != nil {
		os.RemoveAll(dir)
		return nil, "", err
	}
	engineCl = cl
	engineDataDir = dir
	return cl, dir, nil
}

var engineTrackers = []string{
	"udp://open.tracker.cl:1337/announce",
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://tracker.openbittorrent.com:6969/announce",
	"udp://opentracker.i2p.rocks:6969/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://open.stealth.si:80/announce",
	"http://nyaa.tracker.wf:7777/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://tracker.tiny-vps.com:6969/announce",
	"udp://tracker.moeking.me:6969/announce",
	"udp://p4p.arenabg.com:1337/announce",
	"udp://tracker.dler.org:6969/announce",
	"udp://9.rarbg.com:2810/announce",
	"udp://tracker2.dler.org:80/announce",
	"udp://open.demonii.com:1337/announce",
	"udp://tracker.cubonegro.xyz:6969/announce",
}

// withTrackers appends the tracker set to magnet URIs, matching the original
// behaviour of always announcing to every known tracker.
func withTrackers(uri string) string {
	if !strings.HasPrefix(uri, "magnet:") {
		return uri
	}
	for _, tr := range engineTrackers {
		uri += "&tr=" + tr
	}
	return uri
}

func extractInfoHash(uri string) (ih metainfo.Hash, ok bool) {
	if !strings.HasPrefix(uri, "magnet:") {
		return ih, false
	}
	m, err := metainfo.ParseMagnetUri(uri)
	if err != nil || m.InfoHash == (metainfo.Hash{}) {
		return ih, false
	}
	return m.InfoHash, true
}

// AddMagnet adds the magnet to the shared client. If this infohash
// was prewarmed from the search screen or played earlier in the session, the
// running torrent comes back with its metadata and peer connections intact.
// Local .torrent paths are also accepted. Each torrent gets its own storage
// dir keyed by infohash so same-named files from different torrents can
// never cross-contaminate.
func AddMagnet(uri string) (*torrent.Torrent, error) {
	cl, root, err := Get()
	if err != nil {
		return nil, err
	}

	if !strings.HasPrefix(uri, "magnet:") {
		return cl.AddTorrentFromFile(uri)
	}

	if ih, ok := extractInfoHash(uri); ok {
		for _, t := range cl.Torrents() {
			if t.InfoHash() == ih {
				return t, nil
			}
		}
	}

	spec, err := torrent.TorrentSpecFromMagnetUri(withTrackers(uri))
	if err != nil {
		return nil, err
	}
	if root != "" {
		dir := filepath.Join(root, spec.InfoHash.HexString())
		os.MkdirAll(dir, 0755)
		spec.Storage = storage.NewFile(dir)
	}
	t, _, err := cl.AddTorrentSpec(spec)
	return t, err
}

// IsActiveHash reports whether this infohash is the one currently playing.
func IsActiveHash(ih metainfo.Hash) bool {
	activeStreamMu.Lock()
	defer activeStreamMu.Unlock()
	return activeStream != nil && activeStream.InfoHash() == ih
}

// Release drops the torrent and deletes its per-infohash storage dir,
// keeping disk usage identical to the old throwaway-client flow.
func Release(t *torrent.Torrent) {
	if t == nil {
		return
	}
	ih := t.InfoHash()
	t.Drop()

	engineMu.Lock()
	root := engineDataDir
	engineMu.Unlock()

	if root == "" {
		return
	}
	full := filepath.Join(root, ih.HexString())
	if rel, err := filepath.Rel(root, full); err == nil && !strings.HasPrefix(rel, "..") {
		os.RemoveAll(full)
	}
}

// DropIdle frees everything the engine is holding that isn't
// the live stream or a playlist prefetch. Used when the prewarm target
// changes, mirroring the old one-hover-one-torrent behaviour.
func DropIdle() {
	cl, _, err := Get()
	if err != nil {
		return
	}

	prefetchMu.Lock()
	ph := prefetchHash
	prefetchMu.Unlock()
	activeStreamMu.Lock()
	var ah metainfo.Hash
	if activeStream != nil {
		ah = activeStream.InfoHash()
	}
	activeStreamMu.Unlock()

	for _, t := range cl.Torrents() {
		ih := t.InfoHash()
		if ih == ph || ih == ah {
			continue
		}
		Release(t)
	}
}

// ---- metadata cache ----

func metaCachePath(ih metainfo.Hash) string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "zentorrent", "meta", ih.HexString()+".torrent")
}

// StashMeta persists metainfo so the next play of this torrent skips the
// metadata exchange entirely.
func StashMeta(t *torrent.Torrent) {
	if t == nil {
		return
	}
	mi := t.Metainfo()
	path := metaCachePath(t.InfoHash())
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return
	}
	if mi.Write(tmp) != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	tmp.Close()
	os.Rename(tmp.Name(), path)
}

var mirrorClient = &http.Client{Timeout: 8 * time.Second}

// PrimeMetadata races the disk cache and public .torrent mirrors against the
// normal DHT/tracker metadata exchange. SetInfoBytes verifies the infohash
// itself, so wrong or corrupt responses are rejected by the library.
func PrimeMetadata(t *torrent.Torrent, uri string) {
	ih, ok := extractInfoHash(uri)
	if !ok {
		return
	}

	if mi, err := metainfo.LoadFromFile(metaCachePath(ih)); err == nil && len(mi.InfoBytes) > 0 {
		if t.SetInfoBytes(mi.InfoBytes) == nil {
			return // served straight from disk
		}
	}

	hex := strings.ToLower(ih.HexString())
	for _, u := range []string{
		"https://itorrents.org/torrent/" + hex + ".torrent",
		"https://btcache.me/torrent/" + hex,
	} {
		resp, err := mirrorClient.Get(u)
		if err != nil {
			continue
		}
		mi, err := metainfo.Load(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if err != nil {
			continue
		}
		if mi.HashInfoBytes() != ih {
			continue
		}
		if t.SetInfoBytes(mi.InfoBytes) == nil {
			StashMeta(t)
			return
		}
	}
}

// BaseConfig returns the tuned client config used for streaming sessions
// (public DHT bootstrap set, mutual-complete peer dropping).
func BaseConfig(dir string) *torrent.ClientConfig {
	cfg := torrent.NewDefaultClientConfig()
	cfg.Seed = false
	cfg.ListenPort = 0
	cfg.DataDir = dir
	cfg.DropMutuallyCompletePeers = true
	cfg.DhtStartingNodes = func(network string) dht.StartingNodesGetter {
		return func() ([]dht.Addr, error) {
			return dht.ResolveHostPorts([]string{
				"router.bittorrent.com:6881",
				"router.utorrent.com:6881",
				"dht.transmissionbt.com:6881",
				"dht.aelitis.com:6881",
				"router.silotis.us:6881",
				"dht.libtorrent.org:25401",
				"tracker.opentrackr.org:1337",
				"dht.aegir.sexy:6969",
			})
		}
	}
	return cfg
}
