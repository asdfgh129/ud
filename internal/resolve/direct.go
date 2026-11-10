package resolve

import (
	"context"
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
		if err := d.guard.Check(guardContext(ctx), in.Raw); err != nil {
			return nil, err
		}
	}
	return newDirectResource(ctx, in, u), nil
}

// guardContext keeps nil ResolveContext.Ctx (a legitimate caller shortcut)
// from reaching the DNS resolver, which requires a non-nil context.
func guardContext(ctx core.ResolveContext) context.Context {
	if ctx.Ctx != nil {
		return ctx.Ctx
	}
	return context.Background()
}

// newDirectResource builds the common single-file Resource for a direct URL.
// Shared by DirectHTTP (P0) and DirectFile (P1 WBS 9); the Resource shape is
// frozen by ADR-0013, so new protocols must NOT add fields here.
func newDirectResource(ctx core.ResolveContext, in core.Input, u *url.URL) *core.Resource {
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
	}
}

// Unsupported is registered LAST. It gives a readable, phase-aware reason for
// every input class not yet implemented (FR-RSV-02), instead of a silent nil.
// The implemented set grew in P1: HTTP(S) (WBS 2), Metalink + link wrappers
// (WBS 8) and FTP / FTPS / SFTP direct URLs (WBS 9).
type Unsupported struct{}

func (Unsupported) Name() string          { return "unsupported" }
func (Unsupported) Match(core.Input) bool { return true }

func (Unsupported) Resolve(_ core.ResolveContext, in core.Input) (*core.Resource, error) {
	phase := map[core.InputKind]string{
		core.InputSite: "P2", core.InputTelegram: "P3", core.InputED2K: "P3",
	}
	// BT magnet / .torrent (P1 WBS 7), Metalink and the thunder/flashget/qqdl
	// wrappers (P1 WBS 8) are implemented as built-in resolvers. Reaching
	// Unsupported for any of them means the registration chain is broken, not
	// that the feature is unplanned.
	if in.Kind == core.InputMetalink || in.Kind == core.InputThunder ||
		in.Kind == core.InputMagnet || in.Kind == core.InputTorrent {
		return nil, core.Err(core.E_RESOLVE_FAILED,
			"输入类别 %q 已在 P1 实现，但未匹配到内置 Resolver（请检查注册链）", in.Kind)
	}
	p := phase[in.Kind]
	if p == "" {
		p = "后续里程碑"
	}
	return nil, core.Err(core.E_CAPABILITY_UNAVAILABLE,
		"当前已实现 HTTP(S) / FTP(S) / SFTP 直链、Metalink、链接封装与 BT/磁力下载；输入类别 %q 计划在 %s 接入",
		in.Kind, p)
}
