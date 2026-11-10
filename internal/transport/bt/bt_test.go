package bttransport

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/asdfgh129/ud/internal/core"
)

// noopCreds is a credential store that never holds secrets; the BT Transport only
// needs it to satisfy the constructor signature.
type noopCreds struct{}

func (noopCreds) Get(context.Context, core.CredentialRef) (string, error) { return "", nil }
func (noopCreds) Put(context.Context, string) (core.CredentialRef, error) { return "mem://x", nil }
func (noopCreds) Delete(context.Context, core.CredentialRef) error        { return nil }

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestInfoHashFromHex(t *testing.T) {
	const ih = "0123456789abcdef0123456789abcdef01234567"
	h, err := infoHashFromHex(ih)
	if err != nil {
		t.Fatalf("valid infohash: %v", err)
	}
	if h.HexString() != ih {
		t.Fatalf("roundtrip = %q, want %q", h.HexString(), ih)
	}
	if _, err := infoHashFromHex("zzzz"); err == nil {
		t.Fatal("non-hex must error")
	}
	if _, err := infoHashFromHex("abcd"); err == nil {
		t.Fatal("short hex must error")
	}
}

func TestHonestPeerID(t *testing.T) {
	id := honestPeerID()
	if len(id) != 20 {
		t.Fatalf("peer id length = %d, want 20", len(id))
	}
	if string(id[:8]) != "-UD0000-" {
		t.Fatalf("peer id prefix = %q, want -UD0000-", string(id[:8]))
	}
}

func TestPickInfoFileSingle(t *testing.T) {
	info := &metainfo.Info{Name: "f.bin", Length: 1234, PieceLength: 16384}
	res := &core.Resource{Files: []core.ResourceFile{{Path: "f.bin", Selected: true}}}
	name, size := pickInfoFile(info, res)
	if name != "f.bin" || size != 1234 {
		t.Fatalf("got %q %d", name, size)
	}
}

func TestPickInfoFileMulti(t *testing.T) {
	info := &metainfo.Info{
		PieceLength: 16384,
		Files: []metainfo.FileInfo{
			{Path: []string{"a.txt"}, Length: 10},
			{Path: []string{"b.txt"}, Length: 20},
		},
	}
	// No selection -> largest file wins.
	res := &core.Resource{}
	name, size := pickInfoFile(info, res)
	if name != "b.txt" || size != 20 {
		t.Fatalf("largest: got %q %d", name, size)
	}
	// Explicit selection overrides size.
	res = &core.Resource{Files: []core.ResourceFile{{Path: "a.txt", Selected: true}}}
	name, size = pickInfoFile(info, res)
	if name != "a.txt" || size != 10 {
		t.Fatalf("selected: got %q %d", name, size)
	}
}

func TestSelectedResourcePath(t *testing.T) {
	if got := selectedResourcePath(&core.Resource{Files: []core.ResourceFile{{Path: "x", Selected: true}, {Path: "y"}}}); got != "x" {
		t.Fatalf("got %q", got)
	}
	if got := selectedResourcePath(&core.Resource{Files: []core.ResourceFile{{Path: "only"}}}); got != "only" {
		t.Fatalf("got %q", got)
	}
	if got := selectedResourcePath(&core.Resource{}); got != "" {
		t.Fatalf("empty got %q", got)
	}
}

func TestProbeEmbeddedMetainfo(t *testing.T) {
	content := []byte("probe embedded content payload")
	info := metainfo.Info{Name: "probe.bin", PieceLength: 16384, Length: int64(len(content))}
	pieces, err := metainfo.GeneratePieces(bytes.NewReader(content), 16384, nil)
	mustNoErr(t, err)
	info.Pieces = pieces
	infoBytes, err := bencode.Marshal(info)
	mustNoErr(t, err)
	mi := metainfo.MetaInfo{InfoBytes: bencode.Bytes(infoBytes)}
	raw, err := bencode.Marshal(mi)
	mustNoErr(t, err)

	res := &core.Resource{
		Kind:     core.KindTorrent,
		Files:    []core.ResourceFile{{Path: "probe.bin", Selected: true}},
		Identity: core.ProtocolIdentity{TorrentMeta: raw},
	}
	tr := New(nil, noopCreds{})
	pr, err := tr.Probe(context.Background(), res)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if pr.Size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", pr.Size, len(content))
	}
	if pr.Filename != "probe.bin" {
		t.Fatalf("filename = %q", pr.Filename)
	}
	if !pr.AcceptRanges {
		t.Fatal("AcceptRanges must be true")
	}
}

func TestBuildSpec(t *testing.T) {
	content := []byte("buildspec content payload")
	info := metainfo.Info{Name: "bs.bin", PieceLength: 16384, Length: int64(len(content))}
	info.Pieces, _ = metainfo.GeneratePieces(bytes.NewReader(content), 16384, nil)
	infoBytes, _ := bencode.Marshal(info)
	mi := metainfo.MetaInfo{InfoBytes: bencode.Bytes(infoBytes)}
	raw, _ := bencode.Marshal(mi)

	// Embedded metainfo -> spec carries InfoBytes.
	res := &core.Resource{Kind: core.KindTorrent, Identity: core.ProtocolIdentity{TorrentMeta: raw}}
	spec, err := buildSpec(res)
	mustNoErr(t, err)
	if len(spec.InfoBytes) == 0 {
		t.Fatal("embedded spec must carry InfoBytes")
	}

	// Pure magnet -> spec carries infohash + trackers.
	res2 := &core.Resource{Kind: core.KindTorrent, Identity: core.ProtocolIdentity{
		InfoHashV1: "0123456789abcdef0123456789abcdef01234567",
		Trackers:   [][]string{{"udp://t:80"}},
	}}
	spec2, err := buildSpec(res2)
	mustNoErr(t, err)
	if spec2.InfoHash.HexString() != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("infohash = %q", spec2.InfoHash.HexString())
	}
	if len(spec2.Trackers) != 1 || spec2.Trackers[0][0] != "udp://t:80" {
		t.Fatalf("trackers = %v", spec2.Trackers)
	}

	// Neither -> error.
	if _, err := buildSpec(&core.Resource{Kind: core.KindTorrent}); err == nil {
		t.Fatal("expected error when neither metainfo nor infohash present")
	}
}

// memChunkWriter is an in-memory ChunkWriter that emits one chunk covering the
// whole file and assembles written bytes by offset.
type memChunkWriter struct {
	mu   sync.Mutex
	data []byte
	size int64
}

func (m *memChunkWriter) Next(ctx context.Context) (core.Chunk, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.size == 0 {
		return core.Chunk{}, false, nil
	}
	ch := core.Chunk{Start: 0, Length: m.size}
	m.size = 0
	return ch, true, nil
}

func (m *memChunkWriter) WriteChunk(ctx context.Context, c core.Chunk, p []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if int64(len(m.data)) < c.Start+int64(len(p)) {
		nd := make([]byte, c.Start+int64(len(p)))
		copy(nd, m.data)
		m.data = nd
	}
	copy(m.data[c.Start:c.Start+int64(len(p))], p)
	return nil
}

func (m *memChunkWriter) Verified(ctx context.Context, c core.Chunk, sum core.Hash) error { return nil }
func (m *memChunkWriter) Release(ctx context.Context, c core.Chunk, err error) error      { return nil }

// TestTransportLoopbackDownload seeds a single-file torrent on 127.0.0.1 and uses
// the BT Transport to download it via a bootstrap peer — a real protocol loopback
// with no external network access.
func TestTransportLoopbackDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping loopback integration test in -short mode")
	}
	content := []byte("Universal Downloader loopback seed payload -- deterministic BT transport integration content 1234567890")
	info := metainfo.Info{Name: "loopback.bin", PieceLength: 16384, Length: int64(len(content))}
	pieces, err := metainfo.GeneratePieces(bytes.NewReader(content), 16384, nil)
	mustNoErr(t, err)
	info.Pieces = pieces
	infoBytes, err := bencode.Marshal(info)
	mustNoErr(t, err)
	mi := metainfo.MetaInfo{InfoBytes: bencode.Bytes(infoBytes)}
	_ = mi.HashInfoBytes()

	// Seeder storage: anacrolix NewFile(baseDir) keeps a single-file torrent at
	// baseDir/<info.Name>.
	seedDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(seedDir, info.Name), content, 0o600); err != nil {
		t.Fatal(err)
	}
	seedCfg := torrent.NewDefaultClientConfig()
	seedCfg.NoDHT = true
	seedCfg.DisablePEX = true
	seedCfg.NoUpload = false
	seedCfg.Seed = true
	seedCfg.ListenHost = func(network string) string {
		if strings.HasSuffix(network, "6") {
			return "::1"
		}
		return "127.0.0.1"
	}
	seedCfg.ListenPort = 0
	seedCfg.DefaultStorage = storage.NewFile(seedDir)
	seedCl, err := torrent.NewClient(seedCfg)
	mustNoErr(t, err)
	defer seedCl.Close()
	seedTor, _, err := seedCl.AddTorrentSpec(&torrent.TorrentSpec{
		InfoBytes: infoBytes,
		InfoHash:  mi.HashInfoBytes(),
	})
	mustNoErr(t, err)
	seedTor.DownloadAll()

	// Wait for the seeder to verify its piece before the leecher connects.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if seedTor.PieceState(0).Complete {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !seedTor.PieceState(0).Complete {
		t.Fatal("seeder never completed its piece")
	}
	addrs := seedCl.ListenAddrs()
	if len(addrs) == 0 {
		t.Fatal("seeder has no listen address")
	}
	// Prefer the IPv4 loopback address so the leecher connects deterministically.
	var seedAddr net.Addr
	for _, a := range addrs {
		if a != nil && strings.Contains(a.String(), "127.0.0.1") {
			seedAddr = a
			break
		}
	}
	if seedAddr == nil {
		for _, a := range addrs {
			if a != nil {
				seedAddr = a
				break
			}
		}
	}
	if seedAddr == nil {
		t.Fatal("seeder listen address is nil")
	}

	raw, err := bencode.Marshal(mi)
	mustNoErr(t, err)
	res := &core.Resource{
		Kind:     core.KindTorrent,
		Files:    []core.ResourceFile{{Path: info.Name, Selected: true}},
		Identity: core.ProtocolIdentity{TorrentMeta: raw, Private: true},
	}
	// Private:true disables DHT/PEX on the leecher so only the injected bootstrap
	// peer is used — fully deterministic on localhost.
	tr := New(nil, noopCreds{}, WithBootstrapPeers(seedAddr))
	mcw := &memChunkWriter{size: int64(len(content))}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := tr.Run(ctx, res, mcw, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !bytes.Equal(mcw.data, content) {
		t.Fatalf("downloaded content mismatch: got %d bytes, want %d", len(mcw.data), len(content))
	}
}
