package resolve

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/asdfgh129/ud/internal/core"
)

// TorrentResolver parses .torrent files (inline base64, raw bencode, or a local
// path) into a torrent-kind Resource. It performs NO network I/O: a remote
// http(s)://*.torrent URL is explicitly rejected so the caller can fetch the
// bytes out-of-band and submit them inline (WBS 7 v1).
type TorrentResolver struct {
	guard core.URLGuard
}

func NewTorrentResolver(g core.URLGuard) *TorrentResolver { return &TorrentResolver{guard: g} }

func (r *TorrentResolver) Name() string { return "torrent" }

func (r *TorrentResolver) Match(in core.Input) bool {
	if in.Kind == core.InputTorrent {
		return true
	}
	raw := strings.ToLower(strings.TrimSpace(in.Raw))
	if strings.HasSuffix(raw, ".torrent") {
		return true
	}
	// Inline forms: base64 of a .torrent, or a raw bencoded dictionary.
	if looksLikeTorrent(in.Raw) {
		return true
	}
	return false
}

func (r *TorrentResolver) Resolve(ctx core.ResolveContext, in core.Input) (*core.Resource, error) {
	raw, err := decodeTorrentBytes(in.Raw)
	if err != nil {
		return nil, err
	}
	mi, err := metainfo.Load(bytes.NewReader(raw))
	if err != nil {
		return nil, core.Err(core.E_INVALID_INPUT, "invalid .torrent content: %v", err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, core.Err(core.E_INVALID_INPUT, "invalid torrent info dictionary: %v", err)
	}
	infoHash := hex.EncodeToString(mi.HashInfoBytes().Bytes())

	priv := info.Private != nil && *info.Private
	trackers := mi.UpvertedAnnounceList()

	// Choose the single selected file. The frozen Resource can only describe one
	// output file, so multi-file torrents pick one (largest, or matching out).
	out := ""
	if o, ok := ctx.Options["out"].(string); ok {
		out = strings.TrimSpace(o)
	}
	var files []core.ResourceFile
	var selectedSize int64
	switch {
	case info.Length > 0: // single-file torrent
		files = []core.ResourceFile{{Index: 0, Path: sanitizeBTName(info.BestName()), Size: info.Length, Selected: true}}
		selectedSize = info.Length
	default: // multi-file torrent
		ufs := info.UpvertedFiles()
		best := -1
		var bestSize int64
		for i, f := range ufs {
			files = append(files, core.ResourceFile{
				Index:    i,
				Path:     sanitizeBTName(strings.Join(f.Path, "/")),
				Size:     f.Length,
				Offset:   f.TorrentOffset,
				Selected: false,
			})
			if f.Length > bestSize {
				bestSize = f.Length
				best = i
			}
			if out != "" && strings.EqualFold(filepath.Base(strings.Join(f.Path, "/")), out) {
				best = i
			}
		}
		if best >= 0 {
			files[best].Selected = true
			selectedSize = files[best].Size
		}
	}

	res := &core.Resource{
		ID:     "res-" + string(core.NewID()),
		Kind:   core.KindTorrent,
		Engine: core.EngineGo,
		Files:  files,
		// No HTTP-style mirror list; sources live in Identity (trackers / DHT / PEX).
		Mirrors:    []core.Mirror{},
		Headers:    map[string]string{},
		Credential: ctx.Credential,
		Identity: core.ProtocolIdentity{
			InfoHashV1:  infoHash,
			Private:     priv,
			Trackers:    trackers,
			TorrentMeta: raw, // json:"-" — available on the first in-memory run; see bt.Run
		},
		Limits: core.TaskLimits{},
	}
	_ = selectedSize
	return res, nil
}

// decodeTorrentBytes resolves the various inline forms of a .torrent into raw
// bencoded bytes, or returns a capability error for remote URLs.
func decodeTorrentBytes(raw string) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, core.Err(core.E_INVALID_INPUT, "empty torrent input")
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "magnet:") {
		return nil, core.Err(core.E_CAPABILITY_UNAVAILABLE,
			"远程 .torrent 需先拉取字节后以内联 base64 / bencode 提交（WBS 7 v1）")
	}
	// 1) base64 (try standard, then raw, then URL-safe).
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) > 0 && b[0] == 'd' {
			return b, nil
		}
	}
	// 2) raw bencoded dictionary.
	if len(s) > 0 && s[0] == 'd' {
		return []byte(s), nil
	}
	// 3) local file path.
	if b, err := os.ReadFile(s); err == nil && len(b) > 0 && b[0] == 'd' {
		return b, nil
	}
	return nil, core.Err(core.E_INVALID_INPUT, "input is not a base64 / bencoded / local .torrent")
}

// looksLikeTorrent reports whether raw is an inline torrent form we can parse
// without hitting the network.
func looksLikeTorrent(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "magnet:") {
		return false
	}
	if len(s) > 0 && s[0] == 'd' {
		return true
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) > 0 && b[0] == 'd' {
			return true
		}
	}
	return false
}

// sanitizeBTName keeps a torrent file/path element usable as an output filename
// without allowing path traversal; the Scheduler's PathGuard does the final
// validation when the task directory is applied.
func sanitizeBTName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\x00", "")
	s = filepath.Base(s)
	if s == "" || s == "." || s == ".." {
		return "download.bin"
	}
	return s
}
