package resolve

import (
	"encoding/hex"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/asdfgh129/ud/internal/core"
)

// MagnetResolver parses `magnet:?` URIs into a torrent-kind Resource. It performs
// NO network I/O: the infohash, display name and trackers come straight from the
// URI; piece/file metadata is fetched later by the BT Transport (WBS 7).
type MagnetResolver struct {
	guard core.URLGuard
}

func NewMagnetResolver(g core.URLGuard) *MagnetResolver { return &MagnetResolver{guard: g} }

func (r *MagnetResolver) Name() string { return "magnet" }

func (r *MagnetResolver) Match(in core.Input) bool {
	return in.Kind == core.InputMagnet || strings.HasPrefix(strings.ToLower(in.Raw), "magnet:")
}

func (r *MagnetResolver) Resolve(ctx core.ResolveContext, in core.Input) (*core.Resource, error) {
	m, err := metainfo.ParseMagnetUri(in.Raw)
	if err != nil {
		return nil, core.Err(core.E_INVALID_INPUT, "invalid magnet URI: %v", err)
	}
	infoHash := hex.EncodeToString(m.InfoHash[:])
	if infoHash == "" {
		return nil, core.Err(core.E_INVALID_INPUT, "magnet URI is missing the xt (infohash) parameter")
	}
	// metainfo keeps the flat list of "tr" values; expose them as one announce tier.
	var trackers [][]string
	if len(m.Trackers) > 0 {
		trackers = append(trackers, append([]string(nil), m.Trackers...))
	}
	name := m.DisplayName
	if name == "" {
		name = infoHash
	}
	res := &core.Resource{
		ID:     "res-" + string(core.NewID()),
		Kind:   core.KindTorrent,
		Engine: core.EngineGo,
		Files:  []core.ResourceFile{{Index: 0, Path: name, Selected: true}},
		// BT has no HTTP-style mirror list; sources come from trackers / DHT / PEX
		// carried in Identity, so Mirrors stays empty (the Scheduler tolerates this).
		Mirrors:    []core.Mirror{},
		Headers:    map[string]string{},
		Credential: ctx.Credential,
		Identity: core.ProtocolIdentity{
			// Private is unknown until metadata arrives; the Transport re-derives it
			// from the torrent info and enforces DHT/PEX disabling accordingly.
			InfoHashV1: infoHash,
			Trackers:   trackers,
		},
		Limits: core.TaskLimits{},
	}
	return res, nil
}
