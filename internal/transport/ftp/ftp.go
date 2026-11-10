// Package ftptransport implements the FTP / FTPS byte mover (WBS 9, docs/10 §3).
//
// It only moves bytes into a core.ChunkWriter. Chunk allocation, rate limiting,
// retries and state transitions stay with the Scheduler (ADR-0002).
//
// Security (docs/11 §2): every mirror URL goes through the SSRF Guard before a
// socket is opened, and every passive DATA address goes through the Guard's
// DialDataContext, which pins it to the control-connection IP. Credentials are
// resolved from core.CredentialStore by reference, never taken from the URL.
package ftptransport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/netip"
	"net/textproto"
	"path"
	"sync"

	"github.com/jlaffaye/ftp"

	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/transport/tputil"
)

const maxChunkBytes = 64 << 20

// allowedSchemes maps scheme -> default port. ftps:// defaults to 990 and is
// negotiated as IMPLICIT TLS; ftps://host:21/... uses explicit AUTH TLS.
// docs/10 §3 names DialWithExplicitTLS, so both RFC 4217 forms are covered.
var allowedSchemes = map[string]string{"ftp": "21", "ftps": "990"}

// dataGuard is an optional extension of core.URLGuard. Keeping it out of core
// avoids making the model package depend on the FTP data-channel rule. A guard
// that does not implement it can never open a passive data connection.
type dataGuard interface {
	DialDataContext(ctx context.Context, addr string, control netip.Addr) (net.Conn, error)
}

type session interface {
	FileSize(p string) (int64, error)
	RetrFrom(p string, offset uint64) (io.ReadCloser, error)
	Quit() error
}

type dialFunc func(ctx context.Context, tgt tputil.Target, cred tputil.Credential) (session, error)

type Transport struct {
	guard core.URLGuard
	creds core.CredentialStore
	dial  dialFunc
}

func New(g core.URLGuard, creds core.CredentialStore) *Transport {
	return &Transport{guard: g, creds: creds, dial: realDial(g)}
}

func (t *Transport) Name() string { return "ftp" }

func (t *Transport) Supports(r *core.Resource) bool {
	for _, m := range r.Mirrors {
		if _, ok := allowedSchemes[core.SchemeOf(m.URL)]; ok {
			return true
		}
	}
	return false
}

func (t *Transport) Probe(ctx context.Context, r *core.Resource) (*core.ProbeResult, error) {
	if len(r.Mirrors) == 0 {
		return nil, core.Err(core.E_RESOLVE_FAILED, "resource has no mirror")
	}
	tgt, err := t.target(ctx, r.Mirrors[0].URL)
	if err != nil {
		return nil, err
	}
	cred, err := tputil.LoadCredential(ctx, t.creds, r.Credential, true)
	if err != nil {
		return nil, err
	}
	s, err := t.dial(ctx, tgt, cred)
	if err != nil {
		return nil, classifyFTP(err)
	}
	defer func() { _ = s.Quit() }()
	size, err := s.FileSize(tgt.Path)
	if err != nil {
		return nil, classifyFTP(err)
	}
	if size < 0 {
		return nil, core.Err(core.E_INVALID_INPUT, "FTP server reported a negative file size")
	}
	return &core.ProbeResult{
		Size: size, AcceptRanges: true, Filename: path.Base(tgt.Path),
	}, nil
}

func (t *Transport) Run(ctx context.Context, r *core.Resource, w core.ChunkWriter, b core.Budget) error {
	if len(r.Mirrors) == 0 {
		return core.Err(core.E_RESOLVE_FAILED, "resource has no mirror")
	}
	cred, err := tputil.LoadCredential(ctx, t.creds, r.Credential, true)
	if err != nil {
		return err
	}
	return tputil.RunChunks(ctx, r, w, func(ctx context.Context, c core.Chunk, mirror core.Mirror) error {
		if c.Length <= 0 || c.Length > maxChunkBytes {
			return core.Err(core.E_INVALID_INPUT, "chunk length %d is outside 1..%d", c.Length, maxChunkBytes)
		}
		tgt, err := t.target(ctx, mirror.URL)
		if err != nil {
			return err
		}
		s, err := t.dial(ctx, tgt, cred)
		if err != nil {
			return classifyFTP(err)
		}
		defer func() { _ = s.Quit() }()
		rc, err := s.RetrFrom(tgt.Path, uint64(c.Start))
		if err != nil {
			return classifyFTP(err)
		}
		defer rc.Close()
		buf := make([]byte, c.Length)
		if err := tputil.ReadWithBudget(ctx, rc, buf, b); err != nil {
			return err
		}
		return w.WriteChunk(ctx, c, buf)
	})
}

// target re-checks every mirror at use time: a Resource can be persisted and
// reloaded, so trusting the Resolver's earlier check alone is not enough.
func (t *Transport) target(ctx context.Context, raw string) (tputil.Target, error) {
	tgt, err := tputil.ParseTarget(raw, allowedSchemes)
	if err != nil {
		return tputil.Target{}, err
	}
	if t.guard == nil {
		return tputil.Target{}, core.Err(core.E_RESOLVE_SSRF_BLOCKED, "SSRF guard is not configured")
	}
	if err := t.guard.Check(ctx, raw); err != nil {
		return tputil.Target{}, err
	}
	return tgt, nil
}

// realDial builds a dial function shared by the control and passive data
// sockets. jlaffaye/ftp routes BOTH through DialWithDialFunc, which is what
// makes the data-channel SSRF rule enforceable at all.
func realDial(g core.URLGuard) dialFunc {
	return func(ctx context.Context, tgt tputil.Target, cred tputil.Credential) (session, error) {
		dg, ok := g.(dataGuard)
		if !ok {
			return nil, core.Err(core.E_CAPABILITY_UNAVAILABLE,
				"FTP data-channel guard is unavailable; refusing an unvalidated passive connection")
		}
		tlsOn := tgt.Scheme == "ftps"
		implicit := tlsOn && tgt.Port == "990"
		tlsCfg := &tls.Config{ServerName: tgt.Host, MinVersion: tls.VersionTLS12}
		dial := newDialFunc(ctx, g, dg, tgt, tlsOn, implicit, tlsCfg)

		opts := []ftp.DialOption{
			ftp.DialWithContext(ctx),
			ftp.DialWithDialFunc(dial),
			ftp.DialWithTrustPasvIP(false), // never trust the host in a PASV reply
			ftp.DialWithDisabledEPSV(false),
		}
		if tlsOn && !implicit {
			opts = append(opts, ftp.DialWithExplicitTLS(tlsCfg))
		}
		c, err := ftp.Dial(tgt.Addr, opts...)
		if err != nil {
			return nil, classifyFTP(err)
		}
		user, pass := cred.Username, cred.Password
		if user == "" {
			user, pass = "anonymous", "anonymous@ud.invalid"
		}
		if err := c.Login(user, pass); err != nil {
			_ = c.Quit()
			return nil, classifyFTP(err)
		}
		return &realSession{c: c}, nil
	}
}

// newDialFunc is shared by the control and passive data sockets. Extracted so
// the SSRF split can be tested without a live FTP server; jlaffaye/ftp routes
// BOTH sockets through DialWithDialFunc (openDataConn checks options.dialFunc
// first), which is what makes the data-channel rule enforceable at all.
func newDialFunc(ctx context.Context, g core.URLGuard, dg dataGuard,
	tgt tputil.Target, tlsOn, implicit bool, tlsCfg *tls.Config) func(string, string) (net.Conn, error) {

	var mu sync.Mutex
	var controlIP netip.Addr
	return func(network, address string) (net.Conn, error) {
		if network != "tcp" {
			return nil, core.Err(core.E_INVALID_INPUT, "unexpected network %q", network)
		}
		if address == tgt.Addr {
			// Control connection: must use the IP pinned by Check.
			conn, err := g.DialContext(ctx, "tcp", address)
			if err != nil {
				return nil, err
			}
			if ta, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
				if ip, valid := netip.AddrFromSlice(ta.IP); valid {
					mu.Lock()
					controlIP = ip.Unmap()
					mu.Unlock()
				}
			}
			if implicit {
				tc := tls.Client(conn, tlsCfg)
				if err := tc.HandshakeContext(ctx); err != nil {
					_ = conn.Close()
					return nil, err
				}
				return tc, nil
			}
			return conn, nil
		}
		// Passive data connection: the SERVER chose this address. The Guard
		// re-validates it against the pinned control IP before dialing.
		mu.Lock()
		ctl := controlIP
		mu.Unlock()
		conn, err := dg.DialDataContext(ctx, address, ctl)
		if err != nil {
			return nil, err
		}
		if tlsOn {
			// Deliberately lazy handshake: the library's own TLS data path
			// waits for the first Read (see jlaffaye/ftp issue #282).
			return tls.Client(conn, tlsCfg), nil
		}
		return conn, nil
	}
}

type realSession struct{ c *ftp.ServerConn }

func (s *realSession) FileSize(p string) (int64, error) { return s.c.FileSize(p) }
func (s *realSession) RetrFrom(p string, off uint64) (io.ReadCloser, error) {
	return s.c.RetrFrom(p, off)
}
func (s *realSession) Quit() error { return s.c.Quit() }

// classifyFTP maps protocol replies to permanent / transient errors. Server
// messages are never echoed back: they can contain paths or account names that
// must not reach logs or the REST error body.
func classifyFTP(err error) error {
	if err == nil {
		return nil
	}
	var te *textproto.Error
	if errors.As(err, &te) {
		switch te.Code {
		case 530, 531, 532, 533: // authentication
			return core.Err(core.E_RESOLVE_FAILED, "FTP authentication failed (code %d)", te.Code)
		case 550, 553, 554: // not found / action not taken
			return core.Err(core.E_RESOLVE_FAILED, "FTP server rejected the request (code %d)", te.Code)
		default:
			return core.Err(core.E_TRANSIENT_NETWORK, "FTP protocol error (code %d)", te.Code)
		}
	}
	return tputil.ClassifyNetwork(err)
}
