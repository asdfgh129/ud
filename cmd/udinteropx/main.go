// Command udinteropx is the real cross-client interoperability harness for
// P1 WBS 11. It drives the project's actual BitTorrent engine
// (internal/transport/bt, the same code the `udd` daemon uses) as a leecher
// against a REAL third-party BitTorrent client — the aria2-next engine bundled
// with the locally installed MotrixNext.app (Rayburst 4.1.1) — over a
// self-hosted BEP 3 HTTP tracker on loopback.
//
// This produces genuine interop evidence: a real .torrent / magnet is seeded by
// a real client, downloaded by our engine, and the file hash is verified. It is
// NOT a substitute for the named-client L4 gate cases BT-01..BT-14 (which name
// qBittorrent / Transmission / libtorrent and require packet capture + a gigabit
// LAN). Those remain SKIPPED / UNSUPPORTED and the L4 release gate stays closed.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/interop"
	bt "github.com/asdfgh129/ud/internal/transport/bt"
)

const clientName = "aria2-next 3.0.9 (MotrixNext Rayburst 4.1.1)"

// verdict is one honest interop result. SKIPPED / UNSUPPORTED are never coerced
// to PASS; only a hash-verified download is PASS.
type verdict struct {
	ID       string `json:"id"`
	Client   string `json:"client"`
	Status   string `json:"status"` // PASS | FAIL | SKIPPED
	Name     string `json:"name"`
	Evidence string `json:"evidence"`
	InfoHash string `json:"infohash,omitempty"`
	Error    string `json:"error,omitempty"`
}

func main() {
	aria2 := os.Getenv("UD_ARIA2_NEXT")
	if aria2 == "" {
		aria2 = "/Applications/MotrixNext.app/Contents/MacOS/aria2-next"
	}
	outPath := os.Getenv("UD_INTEROP_EXTERNAL_OUT")
	if outPath == "" {
		outPath = "dist/interop_external.json"
	}

	if _, err := os.Stat(aria2); err != nil {
		fmt.Fprintf(os.Stderr, "udinteropx: aria2-next not found at %s (set UD_ARIA2_NEXT); nothing to run\n", aria2)
		// Emit an explicit SKIPPED record so the evidence chain is honest.
		emit([]verdict{{
			ID:       "BX-ALL",
			Client:   clientName,
			Status:   "SKIPPED",
			Name:     "real cross-client interop (aria2-next)",
			Evidence: "aria2-next binary not present in this environment",
		}}, outPath)
		os.Exit(0)
	}

	results := []verdict{
		runTorrent(aria2),
		runMagnet(aria2),
	}
	emit(results, outPath)
	// Non-zero exit only if a case we attempted actually FAILED.
	for _, r := range results {
		if r.Status == "FAIL" {
			os.Exit(1)
		}
	}
}

func emit(vs []verdict, outPath string) {
	b, _ := json.MarshalIndent(vs, "", "  ")
	fmt.Println(string(b))
	_ = os.MkdirAll(filepath.Dir(outPath), 0o755)
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "udinteropx: write evidence:", err)
	}
}

// tail returns the last n lines of s.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ---- payload + torrent construction -------------------------------------------

func makePayload(size int) []byte {
	p := make([]byte, size)
	r := rand.New(rand.NewSource(20261111))
	for i := range p {
		p[i] = byte(r.Intn(256))
	}
	return p
}

func buildTorrent(content []byte, announce string) []byte {
	const pieceLen = 16384
	info := metainfo.Info{
		Name:        "payload.bin",
		PieceLength: pieceLen,
		Length:      int64(len(content)),
	}
	pieces, err := metainfo.GeneratePieces(bytes.NewReader(content), pieceLen, nil)
	if err != nil {
		panic(err)
	}
	info.Pieces = pieces
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		panic(err)
	}
	mi := metainfo.MetaInfo{Announce: announce, InfoBytes: bencode.Bytes(infoBytes)}
	raw, err := bencode.Marshal(mi)
	if err != nil {
		panic(err)
	}
	return raw
}

func infohashOf(torrentBytes []byte) string {
	mi, err := metainfo.Load(bytes.NewReader(torrentBytes))
	if err != nil {
		return ""
	}
	return mi.HashInfoBytes().HexString()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}

// ---- aria2-next seeder --------------------------------------------------------

type seeder struct {
	cmd *exec.Cmd
	log *os.File
}

func startAria2(bin, seedDir, announce string) *seeder {
	log, _ := os.OpenFile(seedDir+"/aria2.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	cmd := exec.Command(bin,
		"--dir="+seedDir,
		"--seed-time=600", // stay alive while we download, then we kill it
		"--enable-dht=false",
		"--enable-dht6=false",
		"--enable-peer-exchange=false",
		"--disable-ipv6=true",
		"--bt-tracker="+announce,
		"--check-certificate=false",
		"--console-log-level=debug",
		"--summary-interval=0",
		filepath.Join(seedDir, "x.torrent"),
	)
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		log.Close()
		return &seeder{log: log}
	}
	return &seeder{cmd: cmd, log: log}
}

// waitAria2ListenPort polls the seeder log for the TCP listen port aria2 actually
// bound (we don't pre-choose a port, because a stale seeder from a prior run can
// already hold it and make aria2 fall back to another one).
func waitAria2ListenPort(logPath string, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	re := regexp.MustCompile(`listening on \[TCP\] 127\.0\.0\.1:(\d+)`)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(logPath); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if m := re.FindStringSubmatch(line); m != nil {
					if p, err := strconv.Atoi(m[1]); err == nil {
						return p
					}
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return 0
}

func (s *seeder) stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
	if s.log != nil {
		s.log.Close()
	}
}

// ---- ud-side download (real engine) ------------------------------------------

func downloadWithUD(torrentBytes []byte, magnet bool, announce, infoHash string, size int64, peer net.Addr) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "udinteropx-dl-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "downloaded.bin")

	w := &fileChunkWriter{f: mustCreate(out), size: size}
	res := &core.Resource{Kind: core.KindTorrent, Identity: core.ProtocolIdentity{
		Trackers: [][]string{{announce}},
		Private:  true, // tracker-only: disable DHT/PEX, mirror the loopback setup
	}}
	if magnet {
		res.Identity.InfoHashV1 = infoHash
	} else {
		res.Identity.TorrentMeta = torrentBytes
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Mirror the proven L3-05 wiring: hand the real seeder's address to the
	// transport as a bootstrap peer so it dials it directly (tracker alone is not
	// enough for anacrolix to reliably learn the peer on loopback).
	tr := bt.New(noopGuard{}, noopCreds{}, bt.WithBootstrapPeers(peer))
	if err := tr.Run(ctx, res, w, noopBudget{}); err != nil {
		return nil, err
	}
	got, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	return got, nil
}

// fileChunkWriter implements core.ChunkWriter by assembling the selected file.
type fileChunkWriter struct {
	mu     sync.Mutex
	f      *os.File
	size   int64
	next   int64
	chunks [][]byte // assembled in order, indexed by chunk position
}

func mustCreate(p string) *os.File {
	f, err := os.Create(p)
	if err != nil {
		panic(err)
	}
	return f
}

func (w *fileChunkWriter) Next(ctx context.Context) (core.Chunk, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.next >= w.size {
		return core.Chunk{}, false, nil
	}
	start := w.next
	l := int64(1 << 18) // 256 KiB
	if start+l > w.size {
		l = w.size - start
	}
	w.next += l
	return core.Chunk{Start: start, Length: l}, true, nil
}

func (w *fileChunkWriter) WriteChunk(ctx context.Context, c core.Chunk, p []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.f.Seek(c.Start, io.SeekStart); err != nil {
		return err
	}
	_, err := w.f.Write(p)
	return err
}

func (w *fileChunkWriter) Verified(ctx context.Context, c core.Chunk, sum core.Hash) error {
	return nil
}
func (w *fileChunkWriter) Release(ctx context.Context, c core.Chunk, err error) error { return nil }

// noopBudget satisfies core.Budget without rate limiting.
type noopBudget struct{}

func (noopBudget) Consume(ctx context.Context, n int) error { return nil }

// noopGuard / noopCreds satisfy the constructor signatures; the BT transport's
// Run path does not consult them for loopback downloads.
type noopGuard struct{}

func (noopGuard) Check(ctx context.Context, raw string) error { return nil }
func (noopGuard) DialContext(ctx context.Context, n, a string) (net.Conn, error) {
	return nil, nil
}
func (noopGuard) MaxRedirects() int { return 0 }

type noopCreds struct{}

func (noopCreds) Put(ctx context.Context, s string) (core.CredentialRef, error) {
	return "mem://x", nil
}
func (noopCreds) Get(ctx context.Context, r core.CredentialRef) (string, error) { return "", nil }
func (noopCreds) Delete(ctx context.Context, r core.CredentialRef) error        { return nil }

// ---- cases -------------------------------------------------------------------

func runTorrent(aria2 string) verdict {
	payload := makePayload(300 * 1024)
	tr := interop.NewHTTPTracker(false)
	announce := tr.URL()

	torrentBytes := buildTorrent(payload, announce)
	ih := infohashOf(torrentBytes)

	seedDir, _ := os.MkdirTemp("", "udinteropx-seed-")
	defer os.RemoveAll(seedDir)
	_ = os.WriteFile(filepath.Join(seedDir, "payload.bin"), payload, 0o644)
	_ = os.WriteFile(filepath.Join(seedDir, "x.torrent"), torrentBytes, 0o644)

	s := startAria2(aria2, seedDir, announce)
	defer s.stop()
	port := waitAria2ListenPort(seedDir+"/aria2.log", 8*time.Second)
	if port == 0 {
		return verdict{ID: "BX-01", Client: clientName, Status: "FAIL",
			Name:     "ud downloads from aria2-next seeder (HTTP tracker, hash-verified)",
			InfoHash: ih, Error: "aria2-next did not bind a listen port"}
	}
	// Inject the seeder's real address into the tracker AND as a bootstrap peer.
	peer := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
	tr.SetPeers(interop.CompactPeer{IP: net.ParseIP("127.0.0.1"), Port: port})

	if os.Getenv("UD_INTEROP_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[debug] BX-01 aria2 listen port=%d announce=%s infohash=%s\n", port, announce, ih)
	}

	// Download with our real engine.
	got, err := downloadWithUD(torrentBytes, false, announce, ih, int64(len(payload)), peer)
	if err != nil {
		return verdict{ID: "BX-01", Client: clientName, Status: "FAIL",
			Name:     "ud downloads from aria2-next seeder (HTTP tracker, hash-verified)",
			InfoHash: ih, Error: err.Error()}
	}
	if sha256Hex(got) != sha256Hex(payload) {
		return verdict{ID: "BX-01", Client: clientName, Status: "FAIL",
			Name:     "ud downloads from aria2-next seeder (HTTP tracker, hash-verified)",
			InfoHash: ih, Error: "hash mismatch"}
	}
	return verdict{ID: "BX-01", Client: clientName, Status: "PASS",
		Name:     "ud downloads from aria2-next seeder (HTTP tracker, hash-verified)",
		InfoHash: ih, Evidence: fmt.Sprintf("payload %d bytes, sha256=%s; downloaded via bttransport and hash-verified", len(payload), sha256Hex(payload))}
}

func runMagnet(aria2 string) verdict {
	payload := makePayload(200 * 1024)
	tr := interop.NewHTTPTracker(false)
	announce := tr.URL()

	torrentBytes := buildTorrent(payload, announce)
	ih := infohashOf(torrentBytes)

	seedDir, _ := os.MkdirTemp("", "udinteropx-seedm-")
	defer os.RemoveAll(seedDir)
	_ = os.WriteFile(filepath.Join(seedDir, "payload.bin"), payload, 0o644)
	_ = os.WriteFile(filepath.Join(seedDir, "x.torrent"), torrentBytes, 0o644)

	s := startAria2(aria2, seedDir, announce)
	defer s.stop()
	port := waitAria2ListenPort(seedDir+"/aria2.log", 8*time.Second)
	if port == 0 {
		return verdict{ID: "BX-02", Client: clientName, Status: "FAIL",
			Name:     "ud fetches magnet metadata (BEP 9) from aria2-next and downloads",
			InfoHash: ih, Error: "aria2-next did not bind a listen port"}
	}
	// Inject the seeder's real address into the tracker AND as a bootstrap peer.
	peer := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
	tr.SetPeers(interop.CompactPeer{IP: net.ParseIP("127.0.0.1"), Port: port})

	if os.Getenv("UD_INTEROP_DEBUG") != "" {
		alive := s.cmd != nil && s.cmd.Process != nil
		if alive {
			if err := s.cmd.Process.Signal(syscall.Signal(0)); err == nil {
				fmt.Fprintf(os.Stderr, "[debug] BX-02 aria2 pid=%d ALIVE\n", s.cmd.Process.Pid)
			} else {
				fmt.Fprintf(os.Stderr, "[debug] BX-02 aria2 pid=%d NOT alive: %v\n", s.cmd.Process.Pid, err)
			}
		} else {
			fmt.Fprintln(os.Stderr, "[debug] BX-02 aria2 did not start")
		}
		if b, err := os.ReadFile(seedDir + "/aria2.log"); err == nil {
			fmt.Fprintf(os.Stderr, "[debug] BX-02 aria2.log:\n%s\n", tail(string(b), 40))
			_ = os.WriteFile("/tmp/aria2_debug.log", b, 0o644)
		}
		fmt.Fprintf(os.Stderr, "[debug] BX-02 seedDir=%s announce=%s port=%d infohash=%s\n", seedDir, announce, port, ih)
		if c, derr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second); derr != nil {
			fmt.Fprintf(os.Stderr, "[debug] BX-02 dial aria2 port %d FAILED: %v\n", port, derr)
		} else {
			fmt.Fprintf(os.Stderr, "[debug] BX-02 dial aria2 port %d OK\n", port)
			c.Close()
		}
	}

	// Download via magnet (infohash + tracker only): metadata comes from the
	// bootstrap peer (aria2) via BEP 9.
	got, err := downloadWithUD(nil, true, announce, ih, int64(len(payload)), peer)
	if err != nil {
		return verdict{ID: "BX-02", Client: clientName, Status: "FAIL",
			Name:     "ud fetches magnet metadata (BEP 9) from aria2-next and downloads",
			InfoHash: ih, Error: err.Error()}
	}
	if sha256Hex(got) != sha256Hex(payload) {
		return verdict{ID: "BX-02", Client: clientName, Status: "FAIL",
			Name:     "ud fetches magnet metadata (BEP 9) from aria2-next and downloads",
			InfoHash: ih, Error: "hash mismatch"}
	}
	return verdict{ID: "BX-02", Client: clientName, Status: "PASS",
		Name:     "ud fetches magnet metadata (BEP 9) from aria2-next and downloads",
		InfoHash: ih, Evidence: fmt.Sprintf("magnet (infohash %s) resolved via BEP 9, %d bytes hash-verified", ih, len(payload))}
}
