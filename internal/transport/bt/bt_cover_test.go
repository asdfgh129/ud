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

func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := bencode.Marshal(v)
	mustNoErr(t, err)
	return b
}

// seedSingleFileT starts an in-process seeder for a single-file torrent on
// loopback and returns its listen address, the MetaInfo (so the leecher can
// derive the infohash or embedded metainfo), and a cleanup function.
func seedSingleFileT(t *testing.T, content []byte) (net.Addr, *metainfo.MetaInfo, func()) {
	t.Helper()
	info := metainfo.Info{Name: "seed.bin", PieceLength: 16384, Length: int64(len(content))}
	pieces, err := metainfo.GeneratePieces(bytes.NewReader(content), 16384, nil)
	mustNoErr(t, err)
	info.Pieces = pieces
	infoBytes, err := bencode.Marshal(info)
	mustNoErr(t, err)
	mi := &metainfo.MetaInfo{InfoBytes: bencode.Bytes(infoBytes)}
	_ = mi.HashInfoBytes()

	seedDir := t.TempDir()
	mustNoErr(t, os.WriteFile(filepath.Join(seedDir, info.Name), content, 0o600))
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
	seedTor, _, err := seedCl.AddTorrentSpec(&torrent.TorrentSpec{
		InfoBytes: infoBytes,
		InfoHash:  mi.HashInfoBytes(),
	})
	mustNoErr(t, err)
	seedTor.DownloadAll()

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
	return seedAddr, mi, func() { seedCl.Close() }
}

func TestProbeRequiresTorrent(t *testing.T) {
	tr := New(nil, noopCreds{})
	_, err := tr.Probe(context.Background(), &core.Resource{Kind: core.KindSingle})
	if err == nil || !strings.Contains(err.Error(), "torrent resource") {
		t.Fatalf("expected torrent-resource error, got %v", err)
	}
}

func TestRunRequiresTorrent(t *testing.T) {
	tr := New(nil, noopCreds{})
	err := tr.Run(context.Background(), &core.Resource{Kind: core.KindSingle}, &memChunkWriter{}, nil)
	if err == nil || !strings.Contains(err.Error(), "torrent resource") {
		t.Fatalf("expected torrent-resource error, got %v", err)
	}
}

func TestRunBuildSpecError(t *testing.T) {
	// Resource has neither embedded metainfo nor an infohash -> buildSpec fails
	// before any client is created.
	tr := New(nil, noopCreds{})
	err := tr.Run(context.Background(), &core.Resource{Kind: core.KindTorrent}, &memChunkWriter{}, nil)
	if err == nil || !strings.Contains(err.Error(), "neither metainfo nor infohash") {
		t.Fatalf("expected buildSpec error, got %v", err)
	}
}

func TestProbeInvalidEmbeddedMetainfo(t *testing.T) {
	tr := New(nil, noopCreds{})
	res := &core.Resource{
		Kind:     core.KindTorrent,
		Identity: core.ProtocolIdentity{TorrentMeta: []byte("this is not bencode")},
	}
	_, err := tr.Probe(context.Background(), res)
	if err == nil || !strings.Contains(err.Error(), "invalid embedded metainfo") {
		t.Fatalf("expected invalid metainfo error, got %v", err)
	}
}

func TestProbeInvalidInfoDict(t *testing.T) {
	// Outer metainfo parses (InfoBytes is valid raw bencode), but the embedded
	// info dictionary is an integer, not a dict -> UnmarshalInfo fails.
	mi := metainfo.MetaInfo{InfoBytes: bencode.Bytes([]byte("i42e"))}
	raw, err := bencode.Marshal(mi)
	mustNoErr(t, err)
	tr := New(nil, noopCreds{})
	res := &core.Resource{
		Kind:     core.KindTorrent,
		Identity: core.ProtocolIdentity{TorrentMeta: raw},
	}
	_, err = tr.Probe(context.Background(), res)
	if err == nil || !strings.Contains(err.Error(), "invalid embedded info dictionary") {
		t.Fatalf("expected invalid info dict error, got %v", err)
	}
}

func TestProbePureMagnetNoInfohash(t *testing.T) {
	// Trackers but no infohash and no metainfo -> pure-magnet path bails early.
	tr := New(nil, noopCreds{})
	res := &core.Resource{
		Kind:     core.KindTorrent,
		Identity: core.ProtocolIdentity{Trackers: [][]string{{"udp://tracker:80/announce"}}},
	}
	_, err := tr.Probe(context.Background(), res)
	if err == nil || !strings.Contains(err.Error(), "no usable infohash") {
		t.Fatalf("expected no-infohash error, got %v", err)
	}
}

func TestBuildSpecErrors(t *testing.T) {
	// Garbage metainfo.
	if _, err := buildSpec(&core.Resource{Kind: core.KindTorrent, Identity: core.ProtocolIdentity{TorrentMeta: []byte("garbage")}}); err == nil {
		t.Fatal("expected invalid metainfo error")
	}
	// Bad infohash hex.
	if _, err := buildSpec(&core.Resource{Kind: core.KindTorrent, Identity: core.ProtocolIdentity{InfoHashV1: "zzzz"}}); err == nil {
		t.Fatal("expected invalid infohash error")
	}
}

// TestProbePureMagnetMetadata fetches torrent metadata over a loopback bootstrap
// peer from a magnet that carries only an infohash — exercising the pure-magnet
// Probe branch (no embedded metainfo).
func TestProbePureMagnetMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping loopback integration test in -short mode")
	}
	content := []byte("pure magnet metadata fetch payload for BT transport probe path coverage 1234567890")
	seedAddr, _, cleanup := seedSingleFileT(t, content)
	defer cleanup()

	// The leecher only knows the infohash, reconstructed independently.
	info := metainfo.Info{Name: "seed.bin", PieceLength: 16384, Length: int64(len(content))}
	info.Pieces, _ = metainfo.GeneratePieces(bytes.NewReader(content), 16384, nil)
	mi := metainfo.MetaInfo{InfoBytes: bencode.Bytes(mustMarshal(t, info))}
	ih := mi.HashInfoBytes().HexString()

	res := &core.Resource{
		Kind:     core.KindTorrent,
		Files:    []core.ResourceFile{{Path: "seed.bin", Selected: true}},
		Identity: core.ProtocolIdentity{InfoHashV1: ih},
	}
	tr := New(nil, noopCreds{}, WithBootstrapPeers(seedAddr))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pr, err := tr.Probe(ctx, res)
	if err != nil {
		t.Fatalf("probe pure magnet: %v", err)
	}
	if pr.Size != int64(len(content)) {
		t.Fatalf("probe size = %d, want %d", pr.Size, len(content))
	}
	if pr.Filename != "seed.bin" {
		t.Fatalf("filename = %q", pr.Filename)
	}
}

// oobChunkWriter emits a single chunk whose range is deliberately outside the
// file bounds so the Transport must Release it and bail out.
type oobChunkWriter struct {
	mu       sync.Mutex
	released bool
	size     int64
}

func (m *oobChunkWriter) Next(ctx context.Context) (core.Chunk, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.size == 0 {
		return core.Chunk{}, false, nil
	}
	ch := core.Chunk{Start: 0, Length: m.size}
	m.size = 0
	return ch, true, nil
}

func (m *oobChunkWriter) WriteChunk(ctx context.Context, c core.Chunk, p []byte) error { return nil }
func (m *oobChunkWriter) Verified(ctx context.Context, c core.Chunk, sum core.Hash) error {
	return nil
}
func (m *oobChunkWriter) Release(ctx context.Context, c core.Chunk, err error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.released = true
	return nil
}

// TestRunOutOfBoundsChunk verifies the Transport rejects a chunk range that
// exceeds the selected file length instead of reading garbage.
func TestRunOutOfBoundsChunk(t *testing.T) {
	content := []byte("out of bounds chunk rejection test payload for BT transport coverage")
	info := metainfo.Info{Name: "seed.bin", PieceLength: 16384, Length: int64(len(content))}
	info.Pieces, _ = metainfo.GeneratePieces(bytes.NewReader(content), 16384, nil)
	mi := metainfo.MetaInfo{InfoBytes: bencode.Bytes(mustMarshal(t, info))}
	raw, err := bencode.Marshal(mi)
	mustNoErr(t, err)

	res := &core.Resource{
		Kind:     core.KindTorrent,
		Files:    []core.ResourceFile{{Path: "seed.bin", Selected: true}},
		Identity: core.ProtocolIdentity{TorrentMeta: raw, Private: true},
	}
	tr := New(nil, noopCreds{})
	mcw := &oobChunkWriter{size: 9999999}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := tr.Run(ctx, res, mcw, nil); err == nil {
		t.Fatal("expected error for out-of-bounds chunk")
	}
	if !mcw.released {
		t.Fatal("expected Release to be called for the out-of-bounds chunk")
	}
}
