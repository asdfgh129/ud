package resolve

import (
	"net/url"
	"strings"

	"github.com/asdfgh129/ud/internal/core"
)

// fileSchemes are the direct-file protocols delivered by P1 WBS 9. The SSRF
// Guard narrows this further to its configured AllowedSchemes / AllowedPorts
// (defaults already include ftp / ftps / sftp and 21 / 22 / 990).
var fileSchemes = map[string]bool{"ftp": true, "ftps": true, "sftp": true}

// DirectFile resolves one FTP / FTPS / SFTP URL into a single-file Resource.
// It is a pure parser: no network I/O, no credential plaintext. Size is
// learned later by the matching Transport.Probe (ADR-0013 keeps Resource
// shape fixed, so no protocol-specific fields are added).
type DirectFile struct {
	guard core.URLGuard
}

func NewDirectFile(g core.URLGuard) *DirectFile { return &DirectFile{guard: g} }

func (d *DirectFile) Name() string { return "direct-file" }

func (d *DirectFile) Match(in core.Input) bool {
	return in.Kind == core.InputURL && fileSchemes[core.SchemeOf(in.Raw)]
}

func (d *DirectFile) Resolve(ctx core.ResolveContext, in core.Input) (*core.Resource, error) {
	u, err := url.Parse(in.Raw)
	if err != nil {
		return nil, core.Err(core.E_INVALID_INPUT, "malformed URL: %v", err)
	}
	if !fileSchemes[strings.ToLower(u.Scheme)] {
		return nil, core.Err(core.E_INVALID_INPUT,
			"direct-file resolver requires ftp/ftps/sftp, got %q", u.Scheme)
	}
	// Credentials must travel as a core.CredentialRef, never in the URL: the
	// URL is persisted (redacted) in SQLite and echoed by the API.
	if u.User != nil {
		return nil, core.Err(core.E_RESOLVE_SSRF_BLOCKED,
			"credentials embedded in a file URL are forbidden; use credentialRef")
	}
	if u.EscapedPath() == "" || u.EscapedPath() == "/" {
		return nil, core.Err(core.E_INVALID_INPUT, "file URL must name a file path")
	}
	if d.guard != nil {
		if err := d.guard.Check(guardContext(ctx), in.Raw); err != nil {
			return nil, err
		}
	}
	return newDirectResource(ctx, in, u), nil
}
