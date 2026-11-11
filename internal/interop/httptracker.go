package interop

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

// CompactPeer is a BEP 23 compact peer entry.
type CompactPeer struct {
	IP   net.IP
	Port int
}

// Announce is one recorded BEP 3 announce request.
type Announce struct {
	InfoHash   [20]byte
	PeerID     string
	Port       int
	Uploaded   int64
	Downloaded int64
	Left       int64
	Event      string
	Compact    bool
	NumWant    int
	Passkey    string
	RawQuery   string
	At         time.Time
}

// ScrapeStat is the per-infohash state a tracker reports.
type ScrapeStat struct {
	Complete   int
	Downloaded int
	Incomplete int
}

// HTTPTracker is a self-hosted BEP 3 / BEP 23 HTTP(S) tracker used by the
// local interoperability cases. It uses httptest so every run is loopback-only
// and needs no external binary (no chihaya / opentracker process).
type HTTPTracker struct {
	mu       sync.Mutex
	server   *httptest.Server
	passkey  string
	status   int
	hang     time.Duration
	interval int
	peers4   []CompactPeer
	peers6   []CompactPeer
	stats    ScrapeStat
	announce []Announce
	scrapes  int
}

// HTTPTrackerOption customises a tracker fixture.
type HTTPTrackerOption func(*HTTPTracker)

func WithPasskey(p string) HTTPTrackerOption {
	return func(t *HTTPTracker) { t.passkey = p }
}

func WithPeers(peers ...CompactPeer) HTTPTrackerOption {
	return func(t *HTTPTracker) { t.peers4 = append([]CompactPeer(nil), peers...) }
}

func WithPeers6(peers ...CompactPeer) HTTPTrackerOption {
	return func(t *HTTPTracker) { t.peers6 = append([]CompactPeer(nil), peers...) }
}

func WithStats(s ScrapeStat) HTTPTrackerOption {
	return func(t *HTTPTracker) { t.stats = s }
}

// WithFault makes the tracker return status (e.g. 503) or hang for duration.
func WithFault(status int, hang time.Duration) HTTPTrackerOption {
	return func(t *HTTPTracker) { t.status, t.hang = status, hang }
}

// NewHTTPTracker starts a loopback HTTP (tls=false) or HTTPS (tls=true)
// tracker. The TLS certificate is self-signed; clients in this suite use a
// dedicated http.Client that trusts it (never the process-wide default).
func NewHTTPTracker(tls bool, opts ...HTTPTrackerOption) *HTTPTracker {
	t := &HTTPTracker{interval: 1800}
	for _, o := range opts {
		o(t)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/announce", t.handleAnnounce)
	mux.HandleFunc("/scrape", t.handleScrape)
	if t.passkey != "" {
		mux.HandleFunc("/"+t.passkey+"/announce", t.handleAnnounce)
		mux.HandleFunc("/"+t.passkey+"/scrape", t.handleScrape)
	}
	if tls {
		t.server = httptest.NewTLSServer(mux)
	} else {
		t.server = httptest.NewServer(mux)
	}
	return t
}

// URL returns the announce endpoint (including the passkey path, if set).
func (t *HTTPTracker) URL() string { return t.BaseURL() + "/announce" }

// ScrapeURL returns the scrape endpoint.
func (t *HTTPTracker) ScrapeURL() string { return t.BaseURL() + "/scrape" }

// BaseURL returns the tracker root, with the passkey path when configured.
func (t *HTTPTracker) BaseURL() string {
	if t.passkey != "" {
		return t.server.URL + "/" + t.passkey
	}
	return t.server.URL
}

// Client returns an HTTP client that trusts this tracker's self-signed cert.
func (t *HTTPTracker) Client() *http.Client { return t.server.Client() }

// Close shuts the fixture down.
func (t *HTTPTracker) Close() { t.server.Close() }

// Announces returns a snapshot of recorded announce requests.
func (t *HTTPTracker) Announces() []Announce {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Announce(nil), t.announce...)
}

// Scrapes returns how many scrape requests were served.
func (t *HTTPTracker) Scrapes() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.scrapes
}

// SetPeers replaces the advertised compact peer list at runtime.
func (t *HTTPTracker) SetPeers(peers ...CompactPeer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.peers4 = append([]CompactPeer(nil), peers...)
}

// SetFault changes the injected failure at runtime (0 / no hang clears it).
func (t *HTTPTracker) SetFault(status int, hang time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status, t.hang = status, hang
}

// SetStats changes the reported scrape counters.
func (t *HTTPTracker) SetStats(s ScrapeStat) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stats = s
}

func (t *HTTPTracker) fault() (int, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status, t.hang
}

func (t *HTTPTracker) handleAnnounce(w http.ResponseWriter, r *http.Request) {
	status, hang := t.fault()
	if hang > 0 {
		select {
		case <-time.After(hang):
		case <-r.Context().Done():
			return
		}
	}
	if status != 0 {
		http.Error(w, "injected tracker failure", status)
		return
	}
	q := r.URL.Query()
	hashes := q["info_hash"]
	if len(hashes) == 0 {
		writeBencode(w, map[string]any{"failure reason": "missing info_hash"})
		return
	}
	rec := Announce{
		PeerID:     q.Get("peer_id"),
		Port:       atoi(q.Get("port")),
		Uploaded:   parseInt64(q.Get("uploaded")),
		Downloaded: parseInt64(q.Get("downloaded")),
		Left:       parseInt64(q.Get("left")),
		Event:      q.Get("event"),
		Compact:    q.Get("compact") == "1",
		NumWant:    atoi(q.Get("numwant")),
		Passkey:    passkeyFromPath(r.URL.Path),
		RawQuery:   r.URL.RawQuery,
		At:         time.Now(),
	}
	if len(hashes[0]) == 20 {
		copy(rec.InfoHash[:], hashes[0])
	} else {
		writeBencode(w, map[string]any{"failure reason": "info_hash must be 20 bytes"})
		return
	}
	if t.passkey != "" && rec.Passkey != t.passkey {
		http.Error(w, "invalid passkey", http.StatusForbidden)
		return
	}

	t.mu.Lock()
	t.announce = append(t.announce, rec)
	peers4 := append([]CompactPeer(nil), t.peers4...)
	peers6 := append([]CompactPeer(nil), t.peers6...)
	stat := t.stats
	interval := t.interval
	t.mu.Unlock()

	resp := map[string]any{
		"interval":   interval,
		"complete":   stat.Complete,
		"incomplete": stat.Incomplete,
		"tracker id": "ud-interop-fixture",
	}
	if rec.Compact {
		resp["peers"] = string(EncodeCompactPeers4(peers4))
		resp["peers6"] = string(EncodeCompactPeers6(peers6))
	} else {
		list := make([]map[string]any, 0, len(peers4))
		for _, p := range peers4 {
			list = append(list, map[string]any{
				"peer id": "-UDTRK1-0123456789", "ip": p.IP.String(), "port": p.Port,
			})
		}
		resp["peers"] = list
	}
	writeBencode(w, resp)
}

func (t *HTTPTracker) handleScrape(w http.ResponseWriter, r *http.Request) {
	status, hang := t.fault()
	if hang > 0 {
		select {
		case <-time.After(hang):
		case <-r.Context().Done():
			return
		}
	}
	if status != 0 {
		http.Error(w, "injected tracker failure", status)
		return
	}
	if t.passkey != "" && passkeyFromPath(r.URL.Path) != t.passkey {
		http.Error(w, "invalid passkey", http.StatusForbidden)
		return
	}
	t.mu.Lock()
	t.scrapes++
	stat := t.stats
	t.mu.Unlock()

	files := map[string]any{}
	for _, raw := range r.URL.Query()["info_hash"] {
		if len(raw) != 20 {
			continue
		}
		files[raw] = map[string]any{
			"complete": stat.Complete, "downloaded": stat.Downloaded,
			"incomplete": stat.Incomplete,
		}
	}
	writeBencode(w, map[string]any{"files": files})
}

func writeBencode(w http.ResponseWriter, v any) {
	blob, err := bencode.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-bittorrent")
	_, _ = w.Write(blob)
}

// ---------------------------------------------------------------------------
// Compact peer encoding / decoding (BEP 23)
// ---------------------------------------------------------------------------

// EncodeCompactPeers4 encodes IPv4 peers as 4-byte IP + 2-byte port entries.
func EncodeCompactPeers4(peers []CompactPeer) []byte {
	out := make([]byte, 0, 6*len(peers))
	for _, p := range peers {
		ip := p.IP.To4()
		if ip == nil {
			continue
		}
		out = append(out, ip...)
		var port [2]byte
		binary.BigEndian.PutUint16(port[:], uint16(p.Port))
		out = append(out, port[:]...)
	}
	return out
}

// EncodeCompactPeers6 encodes IPv6 peers as 16-byte IP + 2-byte port entries.
func EncodeCompactPeers6(peers []CompactPeer) []byte {
	out := make([]byte, 0, 18*len(peers))
	for _, p := range peers {
		ip := p.IP.To16()
		if ip == nil {
			continue
		}
		out = append(out, ip...)
		var port [2]byte
		binary.BigEndian.PutUint16(port[:], uint16(p.Port))
		out = append(out, port[:]...)
	}
	return out
}

// DecodeCompactPeers4 parses 6-byte IPv4 compact entries. A truncated list is
// an error: malformed peer lists must never be silently accepted.
func DecodeCompactPeers4(b []byte) ([]CompactPeer, error) {
	if len(b)%6 != 0 {
		return nil, fmt.Errorf("IPv4 compact peer list length %d is not a multiple of 6", len(b))
	}
	out := make([]CompactPeer, 0, len(b)/6)
	for i := 0; i < len(b); i += 6 {
		out = append(out, CompactPeer{
			IP:   net.IPv4(b[i], b[i+1], b[i+2], b[i+3]),
			Port: int(binary.BigEndian.Uint16(b[i+4 : i+6])),
		})
	}
	return out, nil
}

// DecodeCompactPeers6 parses 18-byte IPv6 compact entries.
func DecodeCompactPeers6(b []byte) ([]CompactPeer, error) {
	if len(b)%18 != 0 {
		return nil, fmt.Errorf("IPv6 compact peer list length %d is not a multiple of 18", len(b))
	}
	out := make([]CompactPeer, 0, len(b)/18)
	for i := 0; i < len(b); i += 18 {
		ip := make(net.IP, 16)
		copy(ip, b[i:i+16])
		out = append(out, CompactPeer{IP: ip, Port: int(binary.BigEndian.Uint16(b[i+16 : i+18]))})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Minimal independent BEP 3 client used by the local cases
// ---------------------------------------------------------------------------

// AnnounceResponse is a bencoded tracker announce reply.
type AnnounceResponse struct {
	Interval       int    `bencode:"interval"`
	MinInterval    int    `bencode:"min interval"`
	Complete       int    `bencode:"complete"`
	Incomplete     int    `bencode:"incomplete"`
	Peers          string `bencode:"peers"`
	Peers6         string `bencode:"peers6"`
	FailureReason  string `bencode:"failure reason"`
	WarningMessage string `bencode:"warning message"`
	TrackerID      string `bencode:"tracker id"`
}

// ScrapeFile holds one infohash's counters.
type ScrapeFile struct {
	Complete   int    `bencode:"complete"`
	Downloaded int    `bencode:"downloaded"`
	Incomplete int    `bencode:"incomplete"`
	Name       string `bencode:"name"`
}

// ScrapeResponse is a bencoded tracker scrape reply.
type ScrapeResponse struct {
	Files         map[string]ScrapeFile `bencode:"files"`
	FailureReason string                `bencode:"failure reason"`
}

// AnnounceHTTP performs a compact BEP 3 announce against a tracker URL.
func AnnounceHTTP(ctx context.Context, client *http.Client, announceURL string,
	infoHash [20]byte, peerID string, port int, left int64) (*AnnounceResponse, error) {
	u, err := url.Parse(announceURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("info_hash", string(infoHash[:]))
	q.Set("peer_id", peerID)
	q.Set("port", strconv.Itoa(port))
	q.Set("uploaded", "0")
	q.Set("downloaded", "0")
	q.Set("left", strconv.FormatInt(left, 10))
	q.Set("compact", "1")
	q.Set("numwant", "50")
	q.Set("event", "started")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tracker returned HTTP %d: %s", resp.StatusCode, truncate(string(body), 120))
	}
	var out AnnounceResponse
	if err := bencode.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode announce reply: %w", err)
	}
	if out.FailureReason != "" {
		return nil, fmt.Errorf("tracker failure: %s", out.FailureReason)
	}
	return &out, nil
}

// ScrapeHTTP performs a BEP 3 scrape against a scrape URL.
func ScrapeHTTP(ctx context.Context, client *http.Client, scrapeURL string,
	infoHashes ...[20]byte) (*ScrapeResponse, error) {
	u, err := url.Parse(scrapeURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	for _, h := range infoHashes {
		q.Add("info_hash", string(h[:]))
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tracker returned HTTP %d", resp.StatusCode)
	}
	var out ScrapeResponse
	if err := bencode.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode scrape reply: %w", err)
	}
	if out.FailureReason != "" {
		return nil, fmt.Errorf("tracker failure: %s", out.FailureReason)
	}
	return &out, nil
}

func passkeyFromPath(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) >= 2 && (parts[len(parts)-1] == "announce" || parts[len(parts)-1] == "scrape") {
		return parts[len(parts)-2]
	}
	return ""
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func parseInt64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
