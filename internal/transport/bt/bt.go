// Package bttransport is the P1 WBS 7 BitTorrent / magnet Transport.
//
// It moves bytes from a torrent into the scheduler-owned ChunkWriter. The frozen
// Resource (ADR-0013) already carries the BT identity (infohash, private flag,
// trackers, raw metainfo), so no new fields are added. anacrolix/torrent is used
// as an in-process client; piece download is driven by the library and we copy
// the selected file's bytes back into the ChunkWriter via file.NewReader().
package bttransport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"os"
	"path/filepath"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/asdfgh129/ud/internal/core"
)

// Transport is the BitTorrent / magnet Transport.
type Transport struct {
	guard          core.URLGuard
	creds          core.CredentialStore
	bootstrapPeers []net.Addr // optional known peers (test seam + private-trackers bootstrap)
}

// Option customizes the Transport at construction.
type Option func(*Transport)

// WithBootstrapPeers injects known peer addresses into every added torrent. This
// is a legitimate production feature (bootstrap / known-peer list) and doubles as
// the loopback test seam.
func WithBootstrapPeers(addrs ...net.Addr) Option {
	return func(t *Transport) { t.bootstrapPeers = append(t.bootstrapPeers, addrs...) }
}

func New(g core.URLGuard, creds core.CredentialStore, opts ...Option) *Transport {
	t := &Transport{guard: g, creds: creds}
	for _, o := range opts {
		o(t)
	}
	return t
}

func (t *Transport) Name() string { return "bt" }

func (t *Transport) Supports(r *core.Resource) bool {
	return r != nil && r.Kind == core.KindTorrent
}

// Probe learns the selected file size. For an embedded metainfo it is a pure
// parse; for a pure magnet it spins up a temporary client to fetch metadata
// (DHT / trackers / bootstrap peers), bounded by ctx.
func (t *Transport) Probe(ctx context.Context, r *core.Resource) (*core.ProbeResult, error) {
	if !t.Supports(r) {
		return nil, core.Err(core.E_RESOLVE_FAILED, "bt transport requires a torrent resource")
	}
	if len(r.Identity.TorrentMeta) > 0 {
		mi, err := metainfo.Load(bytes.NewReader(r.Identity.TorrentMeta))
		if err != nil {
			return nil, core.Err(core.E_INVALID_INPUT, "invalid embedded metainfo: %v", err)
		}
		info, err := mi.UnmarshalInfo()
		if err != nil {
			return nil, core.Err(core.E_INVALID_INPUT, "invalid embedded info dictionary: %v", err)
		}
		name, size := pickInfoFile(&info, r)
		return &core.ProbeResult{Size: size, AcceptRanges: true, Filename: name}, nil
	}
	// Pure magnet: fetch metadata via the torrent network.
	dataDir, err := os.MkdirTemp("", "ud-bt-probe-")
	if err != nil {
		return nil, core.Err(core.E_INTERNAL, "cannot create probe dir: %v", err)
	}
	defer os.RemoveAll(dataDir)
	cfg := clientConfig(r, dataDir)
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, core.Err(core.E_TRANSIENT_NETWORK, "bt client init failed: %v", err)
	}
	defer cl.Close()
	spec := &torrent.TorrentSpec{Trackers: r.Identity.Trackers}
	if r.Identity.InfoHashV1 != "" {
		ih, err := infoHashFromHex(r.Identity.InfoHashV1)
		if err != nil {
			return nil, err
		}
		spec.InfoHash = ih
	}
	if spec.InfoHash == (metainfo.Hash{}) {
		return nil, core.Err(core.E_RESOLVE_FAILED, "magnet has no usable infohash")
	}
	tor, _, err := cl.AddTorrentSpec(spec)
	if err != nil {
		return nil, core.Err(core.E_RESOLVE_FAILED, "cannot add magnet: %v", err)
	}
	t.injectBootstrap(tor)
	if err := waitInfo(ctx, tor); err != nil {
		return nil, core.Err(core.E_RESOLVE_FAILED, "magnet metadata not available: %v", err)
	}
	info := tor.Info()
	name, size := pickInfoFile(info, r)
	return &core.ProbeResult{Size: size, AcceptRanges: true, Filename: name}, nil
}

// Run drains the authoritative chunk plan from the ChunkWriter, downloading the
// selected torrent file and copying its bytes back through the Budget.
func (t *Transport) Run(ctx context.Context, r *core.Resource, w core.ChunkWriter, b core.Budget) error {
	if !t.Supports(r) {
		return core.Err(core.E_RESOLVE_FAILED, "bt transport requires a torrent resource")
	}
	dataDir, err := os.MkdirTemp("", "ud-bt-")
	if err != nil {
		return core.Err(core.E_INTERNAL, "cannot create bt data dir: %v", err)
	}
	defer os.RemoveAll(dataDir)

	cfg := clientConfig(r, dataDir)
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		return core.Err(core.E_TRANSIENT_NETWORK, "bt client init failed: %v", err)
	}
	defer cl.Close()

	spec, err := buildSpec(r)
	if err != nil {
		return err
	}
	tor, _, err := cl.AddTorrentSpec(spec)
	if err != nil {
		return core.Err(core.E_RESOLVE_FAILED, "cannot add torrent: %v", err)
	}
	t.injectBootstrap(tor)

	if err := waitInfo(ctx, tor); err != nil {
		return core.Err(core.E_TRANSIENT_NETWORK, "torrent metadata unavailable: %v", err)
	}

	file := pickFile(tor, r)
	if file == nil {
		return core.Err(core.E_RESOLVE_FAILED, "torrent contains no files")
	}
	file.Download()

	reader := file.NewReader()
	defer reader.Close()
	reader.SetContext(ctx)
	reader.SetReadahead(1 << 20)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c, ok, err := w.Next(ctx)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		if c.Start < 0 || c.Start+c.Length > file.Length() {
			_ = w.Release(ctx, c, core.Err(core.E_INVALID_INPUT,
				"chunk [%d,%d) outside file bounds [0,%d)", c.Start, c.Start+c.Length, file.Length()))
			return core.Err(core.E_INVALID_INPUT, "chunk range outside the selected file")
		}
		buf := make([]byte, c.Length)
		if _, err := reader.Seek(c.Start, io.SeekStart); err != nil {
			_ = w.Release(ctx, c, err)
			return core.Err(core.E_TRANSIENT_NETWORK, "seek failed at %d: %v", c.Start, err)
		}
		if err := readWithBudget(ctx, reader, buf, b); err != nil {
			_ = w.Release(ctx, c, err)
			return err
		}
		if err := w.WriteChunk(ctx, c, buf); err != nil {
			return err
		}
	}
	return nil
}

// buildSpec assembles a TorrentSpec from the Resource identity.
func buildSpec(r *core.Resource) (*torrent.TorrentSpec, error) {
	if len(r.Identity.TorrentMeta) > 0 {
		mi, err := metainfo.Load(bytes.NewReader(r.Identity.TorrentMeta))
		if err != nil {
			return nil, core.Err(core.E_INVALID_INPUT, "invalid metainfo: %v", err)
		}
		spec := torrent.TorrentSpecFromMetaInfo(mi)
		if len(r.Identity.Trackers) > 0 && len(spec.Trackers) == 0 {
			spec.Trackers = r.Identity.Trackers
		}
		return spec, nil
	}
	if r.Identity.InfoHashV1 != "" {
		ih, err := infoHashFromHex(r.Identity.InfoHashV1)
		if err != nil {
			return nil, err
		}
		return &torrent.TorrentSpec{InfoHash: ih, Trackers: r.Identity.Trackers}, nil
	}
	return nil, core.Err(core.E_RESOLVE_FAILED, "resource has neither metainfo nor infohash")
}

// clientConfig builds a per-task anacrolix client config honouring the private
// flag: private torrents MUST NOT use DHT or PEX (BT-08), and we never upload by
// default (Seed=false per ADR-0004).
func clientConfig(r *core.Resource, dataDir string) *torrent.ClientConfig {
	// Start from the defaults so the rate limiters and other required fields are
	// populated, then apply the BT-specific policy for this Resource.
	cfg := torrent.NewDefaultClientConfig()
	cfg.NoDHT = r.Identity.Private
	cfg.DisablePEX = r.Identity.Private
	cfg.NoUpload = true
	cfg.Seed = false
	cfg.PeerID = honestPeerID()
	cfg.ListenHost = func(network string) string { return "" }
	cfg.ListenPort = 0
	cfg.DefaultStorage = storage.NewFile(dataDir)
	if os.Getenv("UD_BT_DEBUG") != "" {
		cfg.Debug = true
	}
	return cfg
}

func (t *Transport) injectBootstrap(tor *torrent.Torrent) {
	if len(t.bootstrapPeers) == 0 {
		return
	}
	peers := make([]torrent.PeerInfo, 0, len(t.bootstrapPeers))
	for _, a := range t.bootstrapPeers {
		peers = append(peers, torrent.PeerInfo{Addr: a})
	}
	tor.AddPeers(peers)
}

// waitInfo blocks until the torrent metadata AND its file handles are ready.
//
// Polling Torrent.Info() is not sufficient: anacrolix publishes t.info under
// nameMu inside setInfo() but calls initFiles() only afterwards, so a caller
// that sees Info() != nil and immediately calls Files() races with the
// t.files write (found by the WBS 11 pure-magnet interop case under -race).
// GotInfo() is closed by onSetInfo(), which runs after setInfo() completed,
// so it is the correct readiness signal for both metadata and file handles.
func waitInfo(ctx context.Context, tor *torrent.Torrent) error {
	select {
	case <-tor.GotInfo():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// pickInfoFile returns the display name and size of the file selected for
// download, derived from the metainfo Info and the Resource's selected file.
func pickInfoFile(info *metainfo.Info, r *core.Resource) (string, int64) {
	if info.Length > 0 { // single-file torrent
		return info.BestName(), info.Length
	}
	want := selectedResourcePath(r)
	var best string
	var bestSize int64
	for _, f := range info.UpvertedFiles() {
		if want != "" && filepath.Base(filepath.Join(f.Path...)) == want {
			return filepath.Join(f.Path...), f.Length
		}
		if f.Length > bestSize {
			bestSize = f.Length
			best = filepath.Join(f.Path...)
		}
	}
	return best, bestSize
}

// pickFile chooses the *torrent.File to download/read from the live torrent.
func pickFile(tor *torrent.Torrent, r *core.Resource) *torrent.File {
	files := tor.Files()
	if len(files) == 0 {
		return nil
	}
	if len(files) == 1 {
		return files[0]
	}
	want := selectedResourcePath(r)
	var best *torrent.File
	var bestSize int64
	for _, f := range files {
		if filepath.Base(f.DisplayPath()) == want {
			return f
		}
		if f.Length() > bestSize {
			bestSize = f.Length()
			best = f
		}
	}
	return best
}

func selectedResourcePath(r *core.Resource) string {
	for _, f := range r.Files {
		if f.Selected {
			return f.Path
		}
	}
	if len(r.Files) > 0 {
		return r.Files[0].Path
	}
	return ""
}

// honestPeerID returns a 20-byte peer ID that honestly identifies this client
// (BT-14). The "-UD0000-" prefix marks Universal Downloader; the trailing 12
// chars are hex of 6 random bytes, keeping the id 20 bytes long.
func honestPeerID() string {
	b := make([]byte, 20)
	copy(b, "-UD0000-")
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	hex.Encode(b[8:20], rnd[:])
	return string(b)
}

// infoHashFromHex decodes a 40-char hex SHA-1 infohash into metainfo.Hash without
// panicking on malformed input (metainfo.NewHashFromHex does). Resolvers already
// guarantee a valid value, but a hand-crafted Resource must not crash the daemon.
func infoHashFromHex(s string) (metainfo.Hash, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 20 {
		return metainfo.Hash{}, core.Err(core.E_INVALID_INPUT,
			"invalid infohash %q: must be 40 hex digits", s)
	}
	var h metainfo.Hash
	copy(h[:], b)
	return h, nil
}

// readWithBudget fills dst entirely, charging every byte to the single global
// Limiter (if any). Mirrors the HTTP transport's budget accounting.
func readWithBudget(ctx context.Context, src io.Reader, dst []byte, b core.Budget) error {
	const block = 32 << 10
	for off := 0; off < len(dst); {
		n := len(dst) - off
		if n > block {
			n = block
		}
		if b != nil {
			if err := b.Consume(ctx, n); err != nil {
				return err
			}
		}
		got, err := io.ReadFull(src, dst[off:off+n])
		off += got
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return core.Err(core.E_TRANSIENT_NETWORK,
					"torrent read truncated at %d/%d bytes", off, len(dst))
			}
			return core.Err(core.E_TRANSIENT_NETWORK, "torrent read error: %v", err)
		}
	}
	return nil
}
