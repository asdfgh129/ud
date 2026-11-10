package sftptransport

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/asdfgh129/ud/internal/config"
	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/security"
)

// A minimal loopback SSH + SFTP server. This proves the WBS 9 wiring end to
// end: the real x/crypto/ssh handshake with a PINNED host key, the real
// pkg/sftp client, the real SSRFGuard, and the Scheduler's ChunkWriter.
type loopbackSFTP struct {
	ln          net.Listener
	dir         string
	hostKeyLine string
	wg          sync.WaitGroup
}

func startLoopbackSFTP(t *testing.T, dir string) *loopbackSFTP {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "alice" && string(pass) == "pw" {
				return nil, nil
			}
			return nil, errors.New("bad credentials")
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &loopbackSFTP{
		ln:          ln,
		dir:         dir,
		hostKeyLine: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))),
	}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(conn, cfg)
		}
	}()
	return f
}

func (f *loopbackSFTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *loopbackSFTP) Close() {
	_ = f.ln.Close()
	f.wg.Wait()
}

func (f *loopbackSFTP) handle(conn net.Conn, cfg *ssh.ServerConfig) {
	defer conn.Close()
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		ch, requests, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range requests {
				var payload struct{ Name string }
				ok := req.Type == "subsystem" &&
					ssh.Unmarshal(req.Payload, &payload) == nil && payload.Name == "sftp"
				_ = req.Reply(ok, nil)
				if !ok {
					continue
				}
				server, err := sftp.NewServer(ch,
					sftp.WithServerWorkingDirectory(f.dir), sftp.ReadOnly())
				if err != nil {
					return
				}
				_ = server.Serve()
				_ = server.Close()
				return
			}
		}()
	}
}

func devGuard(t *testing.T, scheme string) *security.SSRFGuard {
	t.Helper()
	return security.NewSSRFGuard(config.SecurityConfig{
		AllowedSchemes:       []string{scheme},
		AllowedPorts:         []int{21, 22, 990},
		MaxRedirects:         3,
		BlockPrivateNetworks: true,
		AllowLoopbackForDev:  true,
	})
}

type integrationCreds struct{ hostKey string }

func (c integrationCreds) Get(context.Context, core.CredentialRef) (string, error) {
	return fmt.Sprintf(`{"username":"alice","password":"pw","host_key":%q}`, c.hostKey), nil
}
func (c integrationCreds) Put(context.Context, string) (core.CredentialRef, error) {
	return "mem://x", nil
}
func (c integrationCreds) Delete(context.Context, core.CredentialRef) error { return nil }

func TestRealSSHAndSFTPRoundTrip(t *testing.T) {
	payload := []byte("0123456789-sftp-roundtrip")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := startLoopbackSFTP(t, dir)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	tr := New(devGuard(t, "sftp"), integrationCreds{hostKey: srv.hostKeyLine})
	raw := fmt.Sprintf("sftp://127.0.0.1:%d%s/a.bin", srv.port(), dir)
	r := &core.Resource{
		Credential: "mem://c1",
		Mirrors:    []core.Mirror{{URL: raw}},
		Limits:     core.TaskLimits{MaxConnections: 1},
	}
	p, err := tr.Probe(ctx, r)
	if err != nil {
		t.Fatalf("Probe with the real SSH/SFTP client: %v", err)
	}
	if p.Size != int64(len(payload)) || p.Filename != "a.bin" || !p.AcceptRanges {
		t.Fatalf("unexpected probe: %+v", p)
	}
	w := newTestWriter(int64(len(payload)), 7)
	if err := tr.Run(ctx, r, w, nil); err != nil {
		t.Fatalf("Run with the real SSH/SFTP client: %v", err)
	}
	var got []byte
	for i := 0; i < len(w.written); i++ {
		got = append(got, w.written[i]...)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("downloaded %q, want %q", got, payload)
	}
}

// A wrong pinned key must abort the handshake before any SFTP traffic.
func TestRealSFTPRejectsWrongHostKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := startLoopbackSFTP(t, dir)
	defer srv.Close()
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _ := ssh.NewPublicKey(other.Public())
	wrongLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(otherPub)))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tr := New(devGuard(t, "sftp"), integrationCreds{hostKey: wrongLine})
	raw := fmt.Sprintf("sftp://127.0.0.1:%d%s/a.bin", srv.port(), dir)
	r := &core.Resource{
		Credential: "mem://c1",
		Mirrors:    []core.Mirror{{URL: raw}},
		Limits:     core.TaskLimits{MaxConnections: 1},
	}
	if _, err := tr.Probe(ctx, r); err == nil {
		t.Fatal("handshake with a mismatched pinned host key must fail")
	}
}
