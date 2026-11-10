// Package sftptransport implements the SFTP byte mover (WBS 9, docs/10 §3).
//
// It only moves bytes into a core.ChunkWriter. Chunk allocation, rate limiting,
// retries and state transitions stay with the Scheduler (ADR-0002).
//
// Security (docs/11 §2/§3): every mirror URL goes through the SSRF Guard before
// dialing; the control socket is the Guard's pinned connection; host keys MUST
// be pinned via the credential (host_key or host_key_fingerprint) — there is no
// "insecure ignore host key" mode anywhere in this package.
package sftptransport

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/transport/tputil"
)

const (
	maxChunkBytes = 64 << 20
	readBlock     = 32 << 10
)

var allowedSchemes = map[string]string{"sftp": "22"}

type session interface {
	Stat(p string) (int64, error)
	ReadAt(p string, dst []byte, off int64) (int, error)
	Close() error
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

func (t *Transport) Name() string { return "sftp" }

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
	cred, err := tputil.LoadCredential(ctx, t.creds, r.Credential, false)
	if err != nil {
		return nil, err
	}
	s, err := t.dial(ctx, tgt, cred)
	if err != nil {
		return nil, classifySFTP(err)
	}
	defer s.Close()
	size, err := s.Stat(tgt.Path)
	if err != nil {
		return nil, classifySFTP(err)
	}
	if size < 0 {
		return nil, core.Err(core.E_INVALID_INPUT, "SFTP server reported a negative file size")
	}
	return &core.ProbeResult{
		Size: size, AcceptRanges: true, Filename: path.Base(tgt.Path),
	}, nil
}

func (t *Transport) Run(ctx context.Context, r *core.Resource, w core.ChunkWriter, b core.Budget) error {
	if len(r.Mirrors) == 0 {
		return core.Err(core.E_RESOLVE_FAILED, "resource has no mirror")
	}
	cred, err := tputil.LoadCredential(ctx, t.creds, r.Credential, false)
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
			return classifySFTP(err)
		}
		defer s.Close()
		buf := make([]byte, c.Length)
		for off := int64(0); off < c.Length; {
			n := c.Length - off
			if n > readBlock {
				n = readBlock
			}
			if b != nil {
				if err := b.Consume(ctx, int(n)); err != nil {
					return err
				}
			}
			got, err := s.ReadAt(tgt.Path, buf[off:off+n], c.Start+off)
			off += int64(got)
			if err != nil && err != io.EOF {
				return classifySFTP(err)
			}
			if got == 0 {
				return core.Err(core.E_TRANSIENT_NETWORK,
					"SFTP body truncated at %d/%d bytes", off, c.Length)
			}
		}
		return w.WriteChunk(ctx, c, buf)
	})
}

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

func realDial(g core.URLGuard) dialFunc {
	return func(ctx context.Context, tgt tputil.Target, cred tputil.Credential) (session, error) {
		hostKeyCB, err := hostKeyCallback(cred)
		if err != nil {
			return nil, err
		}
		auth, err := authMethods(cred)
		if err != nil {
			return nil, err
		}
		cfg := &ssh.ClientConfig{
			User:            cred.Username,
			Auth:            auth,
			HostKeyCallback: hostKeyCB,
			Timeout:         15 * time.Second,
		}
		conn, err := g.DialContext(ctx, "tcp", tgt.Addr)
		if err != nil {
			return nil, tputil.ClassifyNetwork(err)
		}
		sshConn, chans, reqs, err := ssh.NewClientConn(conn, tgt.Addr, cfg)
		if err != nil {
			_ = conn.Close()
			return nil, tputil.ClassifyNetwork(err)
		}
		client := ssh.NewClient(sshConn, chans, reqs)
		sc, err := sftp.NewClient(client)
		if err != nil {
			_ = client.Close()
			return nil, tputil.ClassifyNetwork(err)
		}
		return &realSession{ssh: client, sftp: sc}, nil
	}
}

// hostKeyCallback fails closed. A credential without a pinned key or
// fingerprint is rejected before a single byte is sent to the server.
func hostKeyCallback(c tputil.Credential) (ssh.HostKeyCallback, error) {
	if c.HostKey != "" {
		pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(c.HostKey))
		if err != nil {
			return nil, core.Err(core.E_INVALID_INPUT,
				"host_key is not a valid authorized_keys line")
		}
		return ssh.FixedHostKey(pk), nil
	}
	if fp := strings.TrimSpace(c.HostKeyFingerprint); fp != "" {
		return func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if !strings.EqualFold(ssh.FingerprintSHA256(key), fp) {
				return core.Err(core.E_RESOLVE_SSRF_BLOCKED,
					"SSH host key fingerprint does not match the pinned value")
			}
			return nil
		}, nil
	}
	return nil, core.Err(core.E_RESOLVE_SSRF_BLOCKED,
		"SFTP requires a pinned host_key or host_key_fingerprint; refusing to connect")
}

func authMethods(c tputil.Credential) ([]ssh.AuthMethod, error) {
	if c.Username == "" {
		return nil, core.Err(core.E_INVALID_INPUT, "SFTP credential requires a username")
	}
	var out []ssh.AuthMethod
	if c.PrivateKey != "" {
		var signer ssh.Signer
		var err error
		if c.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(c.PrivateKey), []byte(c.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(c.PrivateKey))
		}
		if err != nil {
			return nil, core.Err(core.E_INVALID_INPUT, "SFTP private_key could not be parsed")
		}
		out = append(out, ssh.PublicKeys(signer))
	}
	if c.Password != "" {
		out = append(out, ssh.Password(c.Password))
	}
	if len(out) == 0 {
		return nil, core.Err(core.E_INVALID_INPUT,
			"SFTP credential requires a password or private_key")
	}
	return out, nil
}

type realSession struct {
	ssh  *ssh.Client
	sftp *sftp.Client
}

func (s *realSession) Stat(p string) (int64, error) {
	fi, err := s.sftp.Stat(p)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func (s *realSession) ReadAt(p string, dst []byte, off int64) (int, error) {
	f, err := s.sftp.Open(p)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.ReadAt(dst, off)
}

func (s *realSession) Close() error {
	_ = s.sftp.Close()
	return s.ssh.Close()
}

func classifySFTP(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return core.Err(core.E_RESOLVE_FAILED, "SFTP path does not exist")
	}
	return tputil.ClassifyNetwork(err)
}
