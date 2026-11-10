package resolve

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/asdfgh129/ud/internal/core"
)

// makeSingleFileTorrent builds a valid single-file .torrent (bencoded metainfo)
// and returns the raw bytes plus the hex infohash.
func makeSingleFileTorrent(t *testing.T, content []byte, name string) ([]byte, string) {
	t.Helper()
	info := metainfo.Info{
		Name:        name,
		PieceLength: 16384,
		Length:      int64(len(content)),
	}
	pieces, err := metainfo.GeneratePieces(bytes.NewReader(content), info.PieceLength, nil)
	if err != nil {
		t.Fatalf("GeneratePieces: %v", err)
	}
	info.Pieces = pieces
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := metainfo.MetaInfo{
		InfoBytes:    bencode.Bytes(infoBytes),
		Announce:     "http://tracker.example.com/announce",
		AnnounceList: metainfo.AnnounceList{{"http://a.example.com/announce"}, {"http://b.example.com/announce"}},
	}
	raw, err := bencode.Marshal(mi)
	if err != nil {
		t.Fatalf("marshal metainfo: %v", err)
	}
	return raw, mi.HashInfoBytes().HexString()
}

func TestTorrentResolverRawBencode(t *testing.T) {
	content := []byte("universal downloader test payload 1234567890")
	raw, ih := makeSingleFileTorrent(t, content, "payload.bin")

	r := NewTorrentResolver(nil)
	res, err := r.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: string(raw), Kind: core.InputTorrent})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Kind != core.KindTorrent {
		t.Fatalf("kind = %v", res.Kind)
	}
	if res.Identity.InfoHashV1 != ih {
		t.Fatalf("infohash = %q, want %q", res.Identity.InfoHashV1, ih)
	}
	if res.Identity.Private {
		t.Fatal("public torrent must not be flagged private")
	}
	// Two announce tiers from AnnounceList must be preserved.
	if len(res.Identity.Trackers) != 2 {
		t.Fatalf("tracker tiers = %d, want 2", len(res.Identity.Trackers))
	}
	if res.Files[0].Path != "payload.bin" || res.Files[0].Size != int64(len(content)) || !res.Files[0].Selected {
		t.Fatalf("files = %+v", res.Files)
	}
	// TorrentMeta is carried in-memory for the first run (json:"-").
	if len(res.Identity.TorrentMeta) == 0 {
		t.Fatal("TorrentMeta must be carried for the BT Transport")
	}
}

func TestTorrentResolverBase64(t *testing.T) {
	content := []byte("base64 wrapped torrent content----")
	raw, _ := makeSingleFileTorrent(t, content, "b64.bin")
	encoded := base64.StdEncoding.EncodeToString(raw)

	r := NewTorrentResolver(nil)
	res, err := r.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: encoded, Kind: core.InputTorrent})
	if err != nil {
		t.Fatalf("resolve base64: %v", err)
	}
	if res.Identity.InfoHashV1 == "" || res.Files[0].Size != int64(len(content)) {
		t.Fatalf("base64 decode failed: %+v", res)
	}
}

func TestTorrentResolverLocalFile(t *testing.T) {
	content := []byte("file-backed torrent content")
	raw, _ := makeSingleFileTorrent(t, content, "local.bin")
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.torrent")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	r := NewTorrentResolver(nil)
	res, err := r.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: path, Kind: core.InputTorrent})
	if err != nil {
		t.Fatalf("resolve file: %v", err)
	}
	if res.Files[0].Size != int64(len(content)) {
		t.Fatalf("file resolve size wrong: %+v", res.Files)
	}
}

func TestTorrentResolverPrivateFlag(t *testing.T) {
	content := []byte("private torrent payload")
	info := metainfo.Info{Name: "priv.bin", PieceLength: 16384, Length: int64(len(content))}
	pieces, err := metainfo.GeneratePieces(bytes.NewReader(content), 16384, nil)
	if err != nil {
		t.Fatal(err)
	}
	info.Pieces = pieces
	priv := true
	info.Private = &priv
	infoBytes, _ := bencode.Marshal(info)
	mi := metainfo.MetaInfo{InfoBytes: bencode.Bytes(infoBytes)}
	raw, _ := bencode.Marshal(mi)

	r := NewTorrentResolver(nil)
	res, err := r.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: string(raw), Kind: core.InputTorrent})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !res.Identity.Private {
		t.Fatal("private flag must be propagated from the info dictionary")
	}
}

// A remote .torrent URL is explicitly rejected: v1 fetches bytes out-of-band and
// submits them inline.
func TestTorrentResolverRejectsRemoteURL(t *testing.T) {
	r := NewTorrentResolver(nil)
	_, err := r.Resolve(core.ResolveContext{Ctx: context.Background()},
		core.Input{Raw: "https://example.com/a.torrent", Kind: core.InputTorrent})
	if e := core.AsError(err); e == nil || e.Code != core.E_CAPABILITY_UNAVAILABLE {
		t.Fatalf("remote .torrent must be E_CAPABILITY_UNAVAILABLE, got %v", err)
	}
}
