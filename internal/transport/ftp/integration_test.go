package ftptransport

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asdfgh129/ud/internal/config"
	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/security"
)

// A minimal RFC 959 server on loopback. It exists to prove the WBS 9 wiring
// end to end: the real jlaffaye/ftp client, the real SSRFGuard, and the
// pinned passive data channel all work together without any external daemon.
// Probe and Run each open their own control connection, so the fixture must
// accept more than one.
type loopbackFTP struct {
	ctrl    net.Listener
	data    net.Listener
	payload []byte
	poison  bool // reply to PASV with 169.254.169.254 instead of loopback
	t       *testing.T
	mu      sync.Mutex
	conns   []net.Conn
	wg      sync.WaitGroup
}

func startLoopbackFTP(t *testing.T, payload []byte, poison bool) *loopbackFTP {
	t.Helper()
	ctrl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	data, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &loopbackFTP{ctrl: ctrl, data: data, payload: payload, poison: poison, t: t}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		f.serve()
	}()
	return f
}

func (f *loopbackFTP) port() int     { return f.ctrl.Addr().(*net.TCPAddr).Port }
func (f *loopbackFTP) dataPort() int { return f.data.Addr().(*net.TCPAddr).Port }

func (f *loopbackFTP) Close() {
	_ = f.ctrl.Close()
	_ = f.data.Close()
	f.mu.Lock()
	for _, c := range f.conns {
		_ = c.SetDeadline(time.Now())
	}
	f.mu.Unlock()
	f.wg.Wait()
}

// write serialises control replies: the data goroutine also writes 226.
func (f *loopbackFTP) write(conn net.Conn, s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, _ = conn.Write([]byte(s))
}

func (f *loopbackFTP) serve() {
	for {
		conn, err := f.ctrl.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns = append(f.conns, conn)
		f.mu.Unlock()
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			f.handle(conn)
		}()
	}
}

func (f *loopbackFTP) handle(conn net.Conn) {
	defer conn.Close()
	retr := make(chan struct{})
	var once sync.Once
	trigger := func() { once.Do(func() { close(retr) }) }
	defer trigger()

	f.write(conn, "220 fixture ready\r\n")
	rw := bufio.NewReader(conn)
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimSpace(line)
		code := "200 ok"
		switch {
		case strings.HasPrefix(cmd, "USER"):
			code = "331 password required"
		case strings.HasPrefix(cmd, "PASS"):
			code = "230 logged in"
		case cmd == "FEAT":
			// No features advertised: the client then skips OPTS UTF8 and MLSD.
			code = "211 End"
		case strings.HasPrefix(cmd, "SIZE"):
			code = "213 " + strconv.Itoa(len(f.payload))
		case cmd == "EPSV":
			if f.poison {
				// Force the PASV fallback so the poisoned host is exercised.
				code = "500 EPSV not supported"
			} else {
				code = fmt.Sprintf("229 Entering Extended Passive Mode (|||%d|)", f.dataPort())
				f.startData(conn, retr)
			}
		case cmd == "PASV":
			p := f.dataPort()
			host := "127,0,0,1"
			if f.poison {
				host = "169,254,169,254" // cloud metadata address
			}
			code = fmt.Sprintf("227 Entering Passive Mode (%s,%d,%d).", host, p/256, p%256)
			f.startData(conn, retr)
		case strings.HasPrefix(cmd, "REST"):
			code = "350 restart accepted"
		case strings.HasPrefix(cmd, "RETR"):
			f.write(conn, "150 opening data connection\r\n")
			// Only now may the data bytes (and the 226) be emitted, otherwise
			// the client would see 226 before its 150 reply.
			trigger()
			continue
		case cmd == "QUIT":
			f.write(conn, "221 bye\r\n")
			return
		}
		f.write(conn, code+"\r\n")
	}
}

func (f *loopbackFTP) startData(ctrl net.Conn, retr <-chan struct{}) {
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		conn, err := f.data.Accept()
		if err != nil {
			return
		}
		select {
		case <-retr:
		case <-time.After(10 * time.Second):
			_ = conn.Close()
			return
		}
		_, _ = conn.Write(f.payload)
		_ = conn.Close()
		// Response.Close() waits for the 226 on the control connection.
		f.write(ctrl, "226 transfer complete\r\n")
	}()
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

type integrationCreds struct{}

func (integrationCreds) Get(context.Context, core.CredentialRef) (string, error) {
	return `{"username":"alice","password":"pw"}`, nil
}
func (integrationCreds) Put(context.Context, string) (core.CredentialRef, error) {
	return "mem://x", nil
}
func (integrationCreds) Delete(context.Context, core.CredentialRef) error { return nil }

func TestRealFTPClientRoundTrip(t *testing.T) {
	for _, poison := range []bool{false, true} {
		name := "epsv"
		if poison {
			name = "poisoned-pasv"
		}
		t.Run(name, func(t *testing.T) {
			payload := []byte("0123456789-ftp-roundtrip")
			srv := startLoopbackFTP(t, payload, poison)
			defer srv.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			tr := New(devGuard(t, "ftp"), integrationCreds{})
			raw := fmt.Sprintf("ftp://127.0.0.1:%d/pub/a.bin", srv.port())
			r := &core.Resource{
				Credential: "mem://c1",
				Mirrors:    []core.Mirror{{URL: raw}},
				Limits:     core.TaskLimits{MaxConnections: 1},
			}
			p, err := tr.Probe(ctx, r)
			if err != nil {
				t.Fatalf("Probe with the real FTP client: %v", err)
			}
			if p.Size != int64(len(payload)) || p.Filename != "a.bin" || !p.AcceptRanges {
				t.Fatalf("unexpected probe: %+v", p)
			}
			w := newTestWriter(int64(len(payload)), int64(len(payload)))
			if err := tr.Run(ctx, r, w, nil); err != nil {
				t.Fatalf("Run with the real FTP client: %v", err)
			}
			if !bytes.Equal(w.written[0], payload) {
				t.Fatalf("downloaded %q, want %q", w.written[0], payload)
			}
		})
	}
}
