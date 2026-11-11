package interop

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

func TestCompactPeerEncoding(t *testing.T) {
	v4 := []CompactPeer{
		{IP: net.ParseIP("127.0.0.1"), Port: 6881},
		{IP: net.ParseIP("10.1.2.3"), Port: 65535},
	}
	raw4 := EncodeCompactPeers4(v4)
	if len(raw4) != 12 {
		t.Fatalf("IPv4 compact length = %d", len(raw4))
	}
	got4, err := DecodeCompactPeers4(raw4)
	if err != nil || len(got4) != 2 || got4[0].Port != 6881 || got4[1].Port != 65535 {
		t.Fatalf("IPv4 round trip: %+v %v", got4, err)
	}
	raw6 := EncodeCompactPeers6([]CompactPeer{{IP: net.ParseIP("::1"), Port: 7003}})
	if len(raw6) != 18 {
		t.Fatalf("IPv6 compact length = %d", len(raw6))
	}
	got6, err := DecodeCompactPeers6(raw6)
	if err != nil || len(got6) != 1 || !got6[0].IP.IsLoopback() || got6[0].Port != 7003 {
		t.Fatalf("IPv6 round trip: %+v %v", got6, err)
	}
	if _, err := DecodeCompactPeers4([]byte{1, 2, 3, 4}); err == nil {
		t.Fatal("expected error for truncated IPv4 list")
	}
	if _, err := DecodeCompactPeers6(make([]byte, 17)); err == nil {
		t.Fatal("expected error for truncated IPv6 list")
	}
	// A v4 address passed to the v6 encoder is up-converted to 16 bytes.
	if len(EncodeCompactPeers6([]CompactPeer{{IP: net.ParseIP("127.0.0.1"), Port: 1}})) != 18 {
		t.Fatal("v4-mapped v6 encoding failed")
	}
	// A non-IP entry is skipped instead of emitting a malformed record.
	if len(EncodeCompactPeers4([]CompactPeer{{Port: 1}})) != 0 {
		t.Fatal("nil IP should be skipped")
	}
}

func TestHTTPTrackerAnnounceVariants(t *testing.T) {
	tr := NewHTTPTracker(false,
		WithPeers(CompactPeer{IP: net.ParseIP("127.0.0.1"), Port: 6881}),
		WithPeers6(CompactPeer{IP: net.ParseIP("::1"), Port: 6882}),
		WithStats(ScrapeStat{Complete: 2, Downloaded: 5, Incomplete: 1}))
	defer tr.Close()

	var ih [20]byte
	copy(ih[:], "12345678901234567890")
	resp, err := AnnounceHTTP(context.Background(), tr.Client(), tr.URL(), ih, "-UD0000-test-pid", 51413, 100)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Interval != 1800 || resp.TrackerID == "" || resp.Peers == "" || resp.Peers6 == "" {
		t.Fatalf("announce reply incomplete: %+v", resp)
	}
	recs := tr.Announces()
	if len(recs) != 1 || recs[0].InfoHash != ih || recs[0].Port != 51413 ||
		!recs[0].Compact || recs[0].Left != 100 || recs[0].Event != "started" {
		t.Fatalf("announce record = %+v", recs)
	}

	// Non-compact dialect returns a bencoded peer list.
	req := httptest.NewRequest(http.MethodGet, tr.URL()+"?info_hash="+string(ih[:])+"&compact=0", nil)
	rr := httptest.NewRecorder()
	tr.handleAnnounce(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "peer id") {
		t.Fatalf("non-compact reply = %d %q", rr.Code, rr.Body.String())
	}
	if _, err := ScrapeHTTP(context.Background(), tr.Client(), tr.ScrapeURL(), ih); err != nil {
		t.Fatal(err)
	}
	if tr.Scrapes() != 1 {
		t.Fatalf("scrapes = %d", tr.Scrapes())
	}
}

func TestHTTPTrackerFailurePaths(t *testing.T) {
	tr := NewHTTPTracker(false, WithPasskey("secret"))
	defer tr.Close()
	var ih [20]byte
	copy(ih[:], "abcdefghijklmnopqrst")

	// Missing info_hash -> bencoded failure reason.
	rr := httptest.NewRecorder()
	tr.handleAnnounce(rr, httptest.NewRequest(http.MethodGet, "/secret/announce", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "failure reason") {
		t.Fatalf("missing info_hash reply = %d %q", rr.Code, rr.Body.String())
	}
	// Wrong length info_hash.
	rr = httptest.NewRecorder()
	tr.handleAnnounce(rr, httptest.NewRequest(http.MethodGet, "/secret/announce?info_hash=short", nil))
	if !strings.Contains(rr.Body.String(), "must be 20 bytes") {
		t.Fatalf("short infohash reply = %q", rr.Body.String())
	}
	// Wrong passkey (bare /announce path when a passkey is required).
	rr = httptest.NewRecorder()
	tr.handleAnnounce(rr, httptest.NewRequest(http.MethodGet, "/announce?info_hash="+string(ih[:]), nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("wrong passkey status = %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	tr.handleScrape(rr, httptest.NewRequest(http.MethodGet, "/scrape?info_hash="+string(ih[:]), nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("scrape wrong passkey status = %d", rr.Code)
	}
	// Scrape with a valid passkey but a malformed hash is still a valid empty reply.
	rr = httptest.NewRecorder()
	tr.handleScrape(rr, httptest.NewRequest(http.MethodGet, "/secret/scrape?info_hash=x", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("scrape status = %d", rr.Code)
	}

	// Injected 503 (both handlers) and injected hang (cancelled by ctx).
	tr.SetFault(503, 0)
	if _, err := AnnounceHTTP(context.Background(), tr.Client(), tr.URL(), ih, "pid", 1, 0); err == nil {
		t.Fatal("expected announce error on injected 503")
	}
	if _, err := ScrapeHTTP(context.Background(), tr.Client(), tr.ScrapeURL(), ih); err == nil {
		t.Fatal("expected scrape error on injected 503")
	}
	tr.SetFault(0, 2*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := AnnounceHTTP(ctx, tr.Client(), tr.URL(), ih, "pid", 1, 0); err == nil {
		t.Fatal("expected announce timeout on injected hang")
	}
	tr.SetFault(0, 0)

	// Runtime peer / stats replacement.
	tr.SetPeers(CompactPeer{IP: net.ParseIP("127.0.0.9"), Port: 9999})
	tr.SetStats(ScrapeStat{Complete: 8, Downloaded: 9, Incomplete: 10})
	resp, err := AnnounceHTTP(context.Background(), tr.Client(), tr.URL(), ih, "pid", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	peers, err := DecodeCompactPeers4([]byte(resp.Peers))
	if err != nil || len(peers) != 1 || peers[0].Port != 9999 {
		t.Fatalf("updated peers = %+v %v", peers, err)
	}
	sc, err := ScrapeHTTP(context.Background(), tr.Client(), tr.ScrapeURL(), ih)
	if err != nil || sc.Files[string(ih[:])].Complete != 8 {
		t.Fatalf("updated stats = %+v %v", sc.Files, err)
	}
}

func TestHTTPClientErrors(t *testing.T) {
	var ih [20]byte
	ctx := context.Background()
	if _, err := AnnounceHTTP(ctx, http.DefaultClient, "://bad-url", ih, "pid", 1, 0); err == nil {
		t.Fatal("expected invalid URL error")
	}
	if _, err := ScrapeHTTP(ctx, http.DefaultClient, "://bad-url", ih); err == nil {
		t.Fatal("expected invalid URL error")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("down"))
	}))
	defer srv.Close()
	if _, err := AnnounceHTTP(ctx, srv.Client(), srv.URL, ih, "pid", 1, 0); err == nil {
		t.Fatal("expected HTTP 502 error")
	}
	if _, err := ScrapeHTTP(ctx, srv.Client(), srv.URL, ih); err == nil {
		t.Fatal("expected HTTP 502 error")
	}

	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not bencode"))
	}))
	defer garbage.Close()
	if _, err := AnnounceHTTP(ctx, garbage.Client(), garbage.URL, ih, "pid", 1, 0); err == nil {
		t.Fatal("expected decode error")
	}
	if _, err := ScrapeHTTP(ctx, garbage.Client(), garbage.URL, ih); err == nil {
		t.Fatal("expected decode error")
	}

	failure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		blob, _ := bencode.Marshal(map[string]any{"failure reason": "banned"})
		_, _ = w.Write(blob)
	}))
	defer failure.Close()
	if _, err := AnnounceHTTP(ctx, failure.Client(), failure.URL, ih, "pid", 1, 0); err == nil ||
		!strings.Contains(err.Error(), "banned") {
		t.Fatalf("expected tracker failure reason, got %v", err)
	}
	if _, err := ScrapeHTTP(ctx, failure.Client(), failure.URL, ih); err == nil {
		t.Fatal("expected scrape failure reason")
	}
}

func TestHTTPTrackerHelpers(t *testing.T) {
	if got := passkeyFromPath("/pk/announce"); got != "pk" {
		t.Fatalf("passkeyFromPath = %q", got)
	}
	if got := passkeyFromPath("/announce"); got != "" {
		t.Fatalf("passkeyFromPath bare = %q", got)
	}
	if got := passkeyFromPath("/a/b/scrape"); got != "b" {
		t.Fatalf("passkeyFromPath nested = %q", got)
	}
	if atoi("42") != 42 || atoi("x") != 0 || parseInt64("7") != 7 || parseInt64("x") != 0 {
		t.Fatal("numeric helpers broken")
	}
	if truncate("abcd", 2) != "ab..." || truncate("ab", 4) != "ab" {
		t.Fatal("truncate broken")
	}

	rr := httptest.NewRecorder()
	writeBencode(rr, func() {}) // functions cannot be bencoded
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("writeBencode error status = %d", rr.Code)
	}
}
