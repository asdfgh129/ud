package resolve

import (
	"net/url"
	"strings"

	"github.com/asdfgh129/ud/internal/core"
)

// DirectHTTP is the P0 resolver: one HTTP(S) direct link -> one single-file Resource.
// It performs no network I/O; size is learned later by Transport.Probe.
type DirectHTTP struct {
	guard core.URLGuard
}

func NewDirectHTTP(g core.URLGuard) *DirectHTTP { return &DirectHTTP{guard: g} }

func (d *DirectHTTP) Name() string { return "direct-http" }

func (d *DirectHTTP) Match(in core.Input) bool {
	return in.Kind == core.InputURL && (core.SchemeOf(in.Raw) == "http" || core.SchemeOf(in.Raw) == "https")
}

func (d *DirectHTTP) Resolve(ctx core.ResolveContext, in core.Input) (*core.Resource, error) {
	u, err := url.Parse(in.Raw)
	if err != nil {
		return nil, core.Err(core.E_INVALID_INPUT, "malformed URL: %v", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, core.Err(core.E_CAPABILITY_UNAVAILABLE, "P0 transport supports http/https only, got %q", scheme)
	}
	// SSRF Guard validates scheme/port/IP and pins DNS before any connection.
	if d.guard != nil {
		if err := d.guard.Check(ctx.Ctx, in.Raw); err != nil {
			return nil, err
		}
	}
	name := filenameFromURL(u)
	return &core.Resource{
		ID:     "res-" + string(core.NewID()),
		Kind:   core.KindSingle,
		Engine: core.EngineGo,
		Files:  []core.ResourceFile{{Index: 0, Path: name, Selected: true}},
		Mirrors: []core.Mirror{{
			URL: in.Raw, Priority: 1, Weight: 1,
		}},
		Headers:    map[string]string{},
		Credential: ctx.Credential,
		Identity:   core.ProtocolIdentity{},
		Limits:     core.TaskLimits{},
	}, nil
}

// Unsupported is registered LAST. It gives a readable, phase-aware reason for
// every input class P0 does not implement (FR-RSV-02), instead of a silent nil.
type Unsupported struct{}

func (Unsupported) Name() string          { return "unsupported" }
func (Unsupported) Match(core.Input) bool { return true }

func (Unsupported) Resolve(_ core.ResolveContext, in core.Input) (*core.Resource, error) {
	phase := map[core.InputKind]string{
		core.InputMagnet: "P1", core.InputTorrent: "P1",
		core.InputSite: "P2", core.InputTelegram: "P3", core.InputED2K: "P3",
	}
	// Metalink and the thunder/flashget/qqdl wrappers are implemented as
	// built-in resolvers since P1 (WBS 8). Reaching Unsupported for them means
	// the registration chain is broken, not that the feature is unplanned.
	if in.Kind == core.InputMetalink || in.Kind == core.InputThunder {
		return nil, core.Err(core.E_RESOLVE_FAILED,
			"输入类别 %q 已在 P1 实现，但未匹配到内置 Resolver（请检查注册链）", in.Kind)
	}
	p := phase[in.Kind]
	if p == "" {
		p = "后续里程碑"
	}
	return nil, core.Err(core.E_CAPABILITY_UNAVAILABLE,
		"P0 只实现 HTTP(S) 直链；输入类别 %q 计划在 %s 接入", in.Kind, p)
}
