package plugin

import (
	"context"
	"net/url"
	"os"
	"strings"

	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/security"
)

// pluginResult is the narrow, explicitly-allowlisted shape a rule may return.
// Unknown JSON fields are ignored: a plugin can never invent Task state,
// credentials, engines or protocol identities (ADR-0013 frozen Resource).
type pluginResult struct {
	Files    []pluginFile   `json:"files"`
	Mirrors  []pluginMirror `json:"mirrors"`
	Headers  map[string]any `json:"headers"`
	Identity *struct {
		IsAdaptive bool   `json:"isAdaptive"`
		VariantID  string `json:"variantId"`
	} `json:"identity"`
	Checksum *struct {
		Algo   string   `json:"algo"`
		Whole  string   `json:"whole"`
		Pieces []string `json:"pieces"`
	} `json:"checksum"`
}

type pluginFile struct {
	Index    *int   `json:"index"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Offset   int64  `json:"offset"`
	Selected *bool  `json:"selected"`
}

type pluginMirror struct {
	URL      string  `json:"url"`
	Priority int     `json:"priority"`
	Weight   float64 `json:"weight"`
	Geo      string  `json:"geo"`
}

const (
	maxFiles   = 1024
	maxMirrors = 32
	maxHeaders = 64
)

// buildResource validates the rule output before it enters the pipeline.
// Every URL goes through the shared SSRF Guard (ADR-0014 §4) and every output
// path through PathGuard; credential-bearing headers are stripped.
func buildResource(rc core.ResolveContext, m *Manifest, out *pluginResult, guard core.URLGuard) (*core.Resource, error) {
	if len(out.Files) < 1 || len(out.Files) > maxFiles {
		return nil, core.Err(core.E_RESOLVE_FAILED, "plugin returned %d files (want 1..%d)", len(out.Files), maxFiles)
	}
	if len(out.Mirrors) < 1 || len(out.Mirrors) > maxMirrors {
		return nil, core.Err(core.E_RESOLVE_FAILED, "plugin returned %d mirrors (want 1..%d)", len(out.Mirrors), maxMirrors)
	}
	ctx := rc.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	pg := security.PathGuard{}
	files := make([]core.ResourceFile, 0, len(out.Files))
	for i, f := range out.Files {
		if _, err := pg.Clean(os.TempDir(), f.Path); err != nil {
			return nil, err
		}
		if f.Size < 0 || f.Offset < 0 {
			return nil, core.Err(core.E_INVALID_INPUT, "negative file size/offset is forbidden")
		}
		idx := i
		if f.Index != nil {
			if *f.Index < 0 || *f.Index > maxFiles {
				return nil, core.Err(core.E_INVALID_INPUT, "file index out of range")
			}
			idx = *f.Index
		}
		selected := true
		if f.Selected != nil {
			selected = *f.Selected
		}
		files = append(files, core.ResourceFile{
			Index: idx, Path: f.Path, Size: f.Size, Offset: f.Offset, Selected: selected,
		})
	}

	hosts := []string(nil)
	if m.Capabilities.HTTP != nil {
		hosts = m.Capabilities.HTTP.AllowedHosts
	}
	mirrors := make([]core.Mirror, 0, len(out.Mirrors))
	for _, raw := range out.Mirrors {
		u, err := url.Parse(raw.URL)
		if err != nil || u.Hostname() == "" {
			return nil, core.Err(core.E_RESOLVE_SSRF_BLOCKED, "plugin returned an invalid mirror URL")
		}
		scheme := strings.ToLower(u.Scheme)
		if scheme != "http" && scheme != "https" {
			return nil, core.Err(core.E_RESOLVE_SSRF_BLOCKED,
				"plugin mirror scheme %q is not allowed (http/https only)", scheme)
		}
		if len(hosts) > 0 && !HostAllowed(hosts, u.Hostname()) {
			return nil, core.Err(core.E_RESOLVE_SSRF_BLOCKED,
				"plugin mirror host %q is outside allowedHosts", u.Hostname())
		}
		if guard != nil {
			if err := guard.Check(ctx, raw.URL); err != nil {
				return nil, err
			}
		}
		mirrors = append(mirrors, core.Mirror{
			URL: raw.URL, Priority: raw.Priority, Weight: raw.Weight, Geo: raw.Geo,
		})
	}

	headers := make(map[string]string, len(out.Headers))
	for k, v := range out.Headers {
		if len(headers) >= maxHeaders {
			return nil, core.Err(core.E_INVALID_INPUT, "too many Resource headers")
		}
		s, ok := v.(string)
		if !ok {
			return nil, core.Err(core.E_INVALID_INPUT, "Resource header %q must be a string", k)
		}
		if sensitiveHeaders[strings.ToLower(k)] || len(k) > 256 || len(s) > 2048 ||
			strings.ContainsAny(k+s, "\r\n\x00") {
			return nil, core.Err(core.E_RESOLVE_SSRF_BLOCKED,
				"Resource header %q is forbidden or malformed", k)
		}
		headers[k] = s
	}

	identity := core.ProtocolIdentity{}
	if out.Identity != nil {
		identity.IsAdaptive = out.Identity.IsAdaptive
		identity.VariantID = truncate(out.Identity.VariantID, 256)
	}

	var checksum *core.Checksum
	if out.Checksum != nil {
		algo := strings.ToLower(strings.TrimSpace(out.Checksum.Algo))
		if algo != "sha256" && algo != "sha-256" && algo != "sha1" && algo != "md5" {
			return nil, core.Err(core.E_INVALID_INPUT, "unsupported checksum algo %q", out.Checksum.Algo)
		}
		if len(out.Checksum.Pieces) > 4096 {
			return nil, core.Err(core.E_INVALID_INPUT, "too many checksum pieces")
		}
		checksum = &core.Checksum{Algo: algo, Whole: out.Checksum.Whole, Pieces: out.Checksum.Pieces}
	}

	kind := core.KindSingle
	if len(files) > 1 {
		kind = core.KindMulti
	}
	return &core.Resource{
		ID:         "res-" + string(core.NewID()),
		Kind:       kind,
		Engine:     core.EngineGo,
		Files:      files,
		Mirrors:    mirrors,
		Checksum:   checksum,
		Headers:    headers,
		Credential: rc.Credential,
		Identity:   identity,
		Limits:     core.TaskLimits{},
	}, nil
}
