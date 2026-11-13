package main

// Real libtorrent (2.x) cross-client cases for the WBS 11 external harness.
//
// Unlike the in-process L3 fixtures, these cases seed with a genuinely
// independent BitTorrent engine (the python-libtorrent binding; libtorrent is
// named by BT-03) and download with this project's real bttransport. They are
// still LOOPBACK, ONE-WAY (seeder -> ud) supplementary evidence: BT-03 requires
// bidirectional sessions (ud seeding is explicitly out of scope per ADR-0004),
// and the L4 lab also needs packet capture, a gigabit LAN and chihaya /
// opentracker binaries. They must never be counted toward release_gate_met.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/asdfgh129/ud/internal/interop"
)

const (
	ltSeederScriptDefault = "tools/interop_libtorrent.py"
	ltFallbackClient      = "libtorrent 2.x (python-libtorrent binding)"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ltReady is the first (and only) stdout line of tools/interop_libtorrent.py.
type ltReady struct {
	Status       string `json:"status"`
	Port         int    `json:"port"`
	Engine       string `json:"engine"`
	InfoHash     string `json:"infohash"`
	PeerID       string `json:"peer_id"`
	TorrentState string `json:"torrent_state"`
	Reason       string `json:"reason"`
}

type ltSeeder struct {
	cmd    *exec.Cmd
	stdin  io.Closer
	stderr *bytes.Buffer
}

func (s *ltSeeder) stop() {
	if s == nil {
		return
	}
	if s.stdin != nil {
		_ = s.stdin.Close() // stdin EOF tells the seeder to shut down cleanly
	}
	if s.cmd != nil && s.cmd.Process != nil {
		done := make(chan struct{})
		go func() {
			_, _ = s.cmd.Process.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = s.cmd.Process.Kill()
			<-done
		}
	}
}

func (s *ltSeeder) stderrTail() string {
	if s == nil || s.stderr == nil {
		return ""
	}
	return tail(s.stderr.String(), 6)
}

func startLtSeeder(seedDir, torrentPath string) (*ltSeeder, ltReady, error) {
	script := envOr("UD_LIBTORRENT_SEEDER", ltSeederScriptDefault)
	python := envOr("UD_LIBTORRENT_PYTHON", "python3")
	if _, err := os.Stat(script); err != nil {
		return nil, ltReady{}, fmt.Errorf("seeder script %s not found (set UD_LIBTORRENT_SEEDER)", script)
	}
	cmd := exec.Command(python, script, "--torrent", torrentPath, "--seed-dir", seedDir)
	if extra := os.Getenv("UD_LIBTORRENT_PYTHONPATH"); extra != "" {
		cmd.Env = append(os.Environ(), "PYTHONPATH="+extra+string(os.PathListSeparator)+os.Getenv("PYTHONPATH"))
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, ltReady{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, ltReady{}, err
	}
	if err := cmd.Start(); err != nil {
		return nil, ltReady{}, fmt.Errorf("start seeder: %w", err)
	}
	s := &ltSeeder{cmd: cmd, stdin: stdin, stderr: stderr}

	type lineResult struct {
		line string
		err  error
	}
	ch := make(chan lineResult, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		ch <- lineResult{line: line, err: err}
	}()
	select {
	case r := <-ch:
		if strings.TrimSpace(r.line) == "" {
			s.stop()
			return nil, ltReady{}, fmt.Errorf("seeder produced no READY line (%v); stderr: %s", r.err, s.stderrTail())
		}
		var ready ltReady
		if err := json.Unmarshal([]byte(r.line), &ready); err != nil {
			s.stop()
			return nil, ltReady{}, fmt.Errorf("bad seeder status %q: %w", strings.TrimSpace(r.line), err)
		}
		return s, ready, nil
	case <-time.After(20 * time.Second):
		s.stop()
		return nil, ltReady{}, fmt.Errorf("timed out waiting for libtorrent seeder; stderr: %s", s.stderrTail())
	}
}

// runLibtorrentCases produces BX-03 (.torrent) and BX-04 (pure magnet / BEP 9).
// Environmental unavailability is reported as SKIPPED / UNSUPPORTED, never PASS.
func runLibtorrentCases() []verdict {
	script := envOr("UD_LIBTORRENT_SEEDER", ltSeederScriptDefault)
	if _, err := os.Stat(script); err != nil {
		return ltUnavailable("seeder script unavailable: " + err.Error())
	}
	return []verdict{runLibtorrentCase(false), runLibtorrentCase(true)}
}

func ltCaseMeta(magnet bool) (string, string, int) {
	if magnet {
		return "BX-04", "ud fetches magnet metadata (BEP 9) from libtorrent and downloads", 224 * 1024
	}
	return "BX-03", "ud downloads from a real libtorrent seeder (HTTP tracker, hash-verified)", 320 * 1024
}

func ltUnavailable(reason string) []verdict {
	id1, name1, _ := ltCaseMeta(false)
	id2, name2, _ := ltCaseMeta(true)
	return []verdict{
		{ID: id1, Client: ltFallbackClient, Status: "SKIPPED", Name: name1, Evidence: reason},
		{ID: id2, Client: ltFallbackClient, Status: "SKIPPED", Name: name2, Evidence: reason},
	}
}

func runLibtorrentCase(magnet bool) verdict {
	id, name, size := ltCaseMeta(magnet)
	payload := makePayload(size)

	tr := interop.NewHTTPTracker(false)
	defer tr.Close()
	announce := tr.URL()
	torrentBytes := buildTorrent(payload, announce)
	ih := infohashOf(torrentBytes)

	seedDir, err := os.MkdirTemp("", "udinteropx-lt-")
	if err != nil {
		return verdict{ID: id, Client: ltFallbackClient, Status: "SKIPPED", Name: name,
			InfoHash: ih, Evidence: "cannot create seed dir: " + err.Error()}
	}
	defer os.RemoveAll(seedDir)
	torrentPath := filepath.Join(seedDir, "x.torrent")
	if err := os.WriteFile(filepath.Join(seedDir, "payload.bin"), payload, 0o644); err != nil {
		return verdict{ID: id, Client: ltFallbackClient, Status: "SKIPPED", Name: name,
			InfoHash: ih, Evidence: "cannot write payload: " + err.Error()}
	}
	if err := os.WriteFile(torrentPath, torrentBytes, 0o644); err != nil {
		return verdict{ID: id, Client: ltFallbackClient, Status: "SKIPPED", Name: name,
			InfoHash: ih, Evidence: "cannot write torrent: " + err.Error()}
	}

	s, ready, err := startLtSeeder(seedDir, torrentPath)
	if err != nil {
		return verdict{ID: id, Client: ltFallbackClient, Status: "SKIPPED", Name: name,
			InfoHash: ih, Evidence: "libtorrent seeder not started: " + err.Error()}
	}
	defer s.stop()
	if ready.Status != "READY" {
		status := "SKIPPED"
		if ready.Status == "UNSUPPORTED" {
			status = "UNSUPPORTED"
		}
		return verdict{ID: id, Client: ltFallbackClient, Status: status, Name: name, InfoHash: ih,
			Evidence: fmt.Sprintf("libtorrent seeder unavailable (status=%s): %s", ready.Status, ready.Reason)}
	}
	client := ready.Engine
	if client == "" {
		client = ltFallbackClient
	}
	peer := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: ready.Port}
	tr.SetPeers(interop.CompactPeer{IP: net.ParseIP("127.0.0.1"), Port: ready.Port})
	seederReady := waitSeederAnnounce(tr, ready.Port, 8*time.Second)

	var meta []byte
	kind := ".torrent"
	if magnet {
		kind = "magnet / BEP 9 metadata"
	} else {
		meta = torrentBytes
	}
	got, err := downloadWithUD(meta, magnet, announce, ih, int64(len(payload)), peer)
	if err != nil {
		return verdict{ID: id, Client: client, Status: "FAIL", Name: name, InfoHash: ih,
			Error: err.Error(), Evidence: "seeder stderr: " + s.stderrTail()}
	}
	if sha256Hex(got) != sha256Hex(payload) {
		return verdict{ID: id, Client: client, Status: "FAIL", Name: name, InfoHash: ih,
			Error: "hash mismatch", Evidence: "seeder stderr: " + s.stderrTail()}
	}

	announces := tr.Announces()
	foreign := 0
	for _, a := range announces {
		if a.PeerID != "" && !strings.HasPrefix(a.PeerID, "-UD") {
			foreign++
		}
	}
	return verdict{
		ID: id, Client: client, Status: "PASS", Name: name, InfoHash: ih,
		Evidence: fmt.Sprintf("payload %d bytes, sha256=%s; %s downloaded by bttransport from a real %s seeder at 127.0.0.1:%d (loopback, DHT/LSD/PEX off, seeder state=%s); HTTP tracker saw %d announces (%d with a foreign peer id), completed announce(left=0) observed=%t; one-way supplementary evidence, NOT L4",
			len(payload), sha256Hex(payload), kind, client, ready.Port, ready.TorrentState, len(announces), foreign, seederReady),
	}
}
