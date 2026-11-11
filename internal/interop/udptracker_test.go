package interop

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

func TestParseUDPURL(t *testing.T) {
	for raw, want := range map[string]string{
		"udp://127.0.0.1:6969/announce": "127.0.0.1:6969",
		"udp://tracker.example:80":      "tracker.example:80",
		"udp://[::1]:6969/announce":     "[::1]:6969",
	} {
		got, err := UDPURLAddr(raw)
		if err != nil || got != want {
			t.Fatalf("UDPURLAddr(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, bad := range []string{
		"http://127.0.0.1:80", "udp://127.0.0.1", "udp://127.0.0.1:0",
		"udp://127.0.0.1:99999", "udp://127.0.0.1:abc",
	} {
		if _, err := UDPURLAddr(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestUDPTrackerLifecycleAndRecords(t *testing.T) {
	tr, err := NewUDPTracker(false,
		WithUDPPeers(CompactPeer{IP: net.ParseIP("127.0.0.1"), Port: 7001}),
		WithUDPStats(ScrapeStat{Complete: 4, Downloaded: 6, Incomplete: 2}))
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if !strings.HasPrefix(tr.URL(), "udp://127.0.0.1:") {
		t.Fatalf("URL = %q", tr.URL())
	}
	addr, err := UDPURLAddr(tr.URL())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	connID, err := UDPConnect(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	var ih [20]byte
	copy(ih[:], "udp-tracker-infohash-1")
	resp, err := UDPAnnounce(ctx, addr, connID, ih, "-UD0000-udptest01", 51413, 1234, false)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Interval != 1800 || resp.Seeders != 4 || resp.Leechers != 2 || len(resp.Peers) != 1 {
		t.Fatalf("announce resp = %+v", resp)
	}
	recs := tr.Announces()
	if len(recs) != 1 || recs[0].InfoHash != ih || recs[0].Left != 1234 || recs[0].Port != 51413 {
		t.Fatalf("announce record = %+v", recs)
	}
	stats, err := UDPScrape(ctx, addr, connID, ih, ih)
	if err != nil || len(stats) != 2 || stats[0].Completed != 6 {
		t.Fatalf("scrape = %+v %v", stats, err)
	}
	connects, scrapes := tr.Counters()
	if connects != 1 || scrapes != 1 {
		t.Fatalf("counters = %d/%d", connects, scrapes)
	}
	tr.Close() // idempotent
	tr.Close()
}

func TestUDPTrackerHandleErrorBranches(t *testing.T) {
	tr, err := NewUDPTracker(false)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	if _, ok := tr.handle([]byte{1, 2, 3}); ok {
		t.Fatal("short datagram must be ignored")
	}
	if _, ok := tr.handle(make([]byte, 12)); ok {
		t.Fatal("12-byte non-connect datagram must be ignored")
	}

	// Connect with a bad action.
	badConnect := make([]byte, udpConnectReqLen)
	binary.BigEndian.PutUint64(badConnect[0:8], uint64(udpProtocolID))
	binary.BigEndian.PutUint32(badConnect[8:12], 99)
	binary.BigEndian.PutUint32(badConnect[12:16], 0xdeadbeef)
	out, ok := tr.handle(badConnect)
	if !ok || binary.BigEndian.Uint32(out[0:4]) != udpActionError ||
		binary.BigEndian.Uint32(out[4:8]) != 0xdeadbeef {
		t.Fatalf("bad connect action reply = %x ok=%v", out, ok)
	}

	// Short announce / scrape.
	shortAnnounce := make([]byte, 20)
	binary.BigEndian.PutUint32(shortAnnounce[8:12], udpActionAnnounce)
	binary.BigEndian.PutUint32(shortAnnounce[12:16], 7)
	if out, ok := tr.handle(shortAnnounce); !ok || binary.BigEndian.Uint32(out[0:4]) != udpActionError {
		t.Fatal("short announce not rejected")
	}
	shortScrape := make([]byte, 10)
	if _, ok := tr.handle(shortScrape); ok {
		t.Fatal("10-byte scrape must be ignored")
	}

	// Stale connection id on announce and scrape.
	stale := make([]byte, udpAnnounceReqLen)
	binary.BigEndian.PutUint32(stale[8:12], udpActionAnnounce)
	binary.BigEndian.PutUint32(stale[12:16], 0x1234)
	binary.BigEndian.PutUint64(stale[0:8], 0xfeed)
	if out, ok := tr.handle(stale); !ok || binary.BigEndian.Uint32(out[0:4]) != udpActionError {
		t.Fatal("stale announce connection id not rejected")
	}
	staleScrape := make([]byte, 16)
	binary.BigEndian.PutUint32(staleScrape[8:12], udpActionScrape)
	binary.BigEndian.PutUint32(staleScrape[12:16], 0x1234)
	binary.BigEndian.PutUint64(staleScrape[0:8], 0xfeed)
	if out, ok := tr.handle(staleScrape); !ok || binary.BigEndian.Uint32(out[0:4]) != udpActionError {
		t.Fatal("stale scrape connection id not rejected")
	}

	// Valid scrape with the right connection id but a truncated hash list.
	validScrape := make([]byte, 16+3)
	binary.BigEndian.PutUint32(validScrape[8:12], udpActionScrape)
	binary.BigEndian.PutUint32(validScrape[12:16], 0x55)
	binary.BigEndian.PutUint64(validScrape[0:8], tr.ConnectionID())
	if out, ok := tr.handle(validScrape); !ok || binary.BigEndian.Uint32(out[0:4]) != udpActionScrape {
		t.Fatal("valid scrape with partial hash should still reply")
	}

	// Unknown action.
	unknown := make([]byte, 16)
	binary.BigEndian.PutUint32(unknown[8:12], 77)
	binary.BigEndian.PutUint32(unknown[12:16], 0x99)
	if out, ok := tr.handle(unknown); !ok || binary.BigEndian.Uint32(out[0:4]) != udpActionError ||
		binary.BigEndian.Uint32(out[4:8]) != 0x99 {
		t.Fatal("unknown action not rejected")
	}
}

// fakeUDPServer returns crafted replies for client error-path tests.
func fakeUDPServer(t *testing.T, respond func(req []byte) []byte) string {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, udpMaxDatagram)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			req := append([]byte(nil), buf[:n]...)
			if reply := respond(req); reply != nil {
				_, _ = conn.WriteTo(reply, from)
			}
		}
	}()
	return conn.LocalAddr().String()
}

func TestUDPClientErrorPaths(t *testing.T) {
	// Echo the transaction id but return a tracker error action.
	errSrv := fakeUDPServer(t, func(req []byte) []byte {
		if len(req) < 16 {
			return nil
		}
		return udpError(binary.BigEndian.Uint32(req[12:16]), "denied")
	})
	if _, err := UDPConnect(context.Background(), errSrv); err == nil ||
		!strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected tracker error, got %v", err)
	}

	// Wrong transaction id.
	badTx := fakeUDPServer(t, func(req []byte) []byte {
		out := make([]byte, udpConnectResLen)
		binary.BigEndian.PutUint32(out[0:4], udpActionConnect)
		binary.BigEndian.PutUint32(out[4:8], 0x11111111)
		return out
	})
	if _, err := UDPConnect(context.Background(), badTx); err == nil ||
		!strings.Contains(err.Error(), "transaction id mismatch") {
		t.Fatalf("expected tx mismatch, got %v", err)
	}

	// Truncated connect response.
	short := fakeUDPServer(t, func([]byte) []byte { return []byte{0, 0, 0, 0} })
	if _, err := UDPConnect(context.Background(), short); err == nil {
		t.Fatal("expected error for short datagram")
	}

	// Valid connect, malformed announce/scrape payloads.
	var ih [20]byte
	mixed := fakeUDPServer(t, func(req []byte) []byte {
		if len(req) < 16 {
			return nil
		}
		action := binary.BigEndian.Uint32(req[8:12])
		tx := binary.BigEndian.Uint32(req[12:16])
		if action == udpActionConnect {
			out := make([]byte, udpConnectResLen)
			binary.BigEndian.PutUint32(out[0:4], udpActionConnect)
			binary.BigEndian.PutUint32(out[4:8], tx)
			binary.BigEndian.PutUint64(out[8:16], 42)
			return out
		}
		// announce: 8-byte header only; scrape: 9 bytes (not 8 + 12n).
		out := make([]byte, 9)
		binary.BigEndian.PutUint32(out[0:4], action)
		binary.BigEndian.PutUint32(out[4:8], tx)
		return out
	})
	connID, err := UDPConnect(context.Background(), mixed)
	if err != nil || connID != 42 {
		t.Fatalf("connect = %d, %v", connID, err)
	}
	if _, err := UDPAnnounce(context.Background(), mixed, connID, ih, "pid", 1, 0, false); err == nil {
		t.Fatal("expected short announce response error")
	}
	if _, err := UDPScrape(context.Background(), mixed, connID, ih); err == nil {
		t.Fatal("expected malformed scrape length error")
	}

	// No responder at all -> read deadline.
	dead, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.LocalAddr().String()
	_ = dead.Close()
	if _, err := UDPConnect(context.Background(), deadAddr); err == nil {
		t.Fatal("expected timeout/error from a dead tracker")
	}
}

func TestCheckUDPResponse(t *testing.T) {
	if err := checkUDPResponse([]byte{1, 2}, 0, 0); err == nil {
		t.Fatal("expected short header error")
	}
	hdr := make([]byte, 8)
	binary.BigEndian.PutUint32(hdr[0:4], udpActionAnnounce)
	binary.BigEndian.PutUint32(hdr[4:8], 0xabc)
	if err := checkUDPResponse(hdr, 0xabc, udpActionAnnounce); err != nil {
		t.Fatalf("valid header: %v", err)
	}
	if err := checkUDPResponse(hdr, 0xabd, udpActionAnnounce); err == nil {
		t.Fatal("expected tx mismatch")
	}
	if err := checkUDPResponse(hdr, 0xabc, udpActionScrape); err == nil {
		t.Fatal("expected unexpected action")
	}
	errHdr := udpError(0xabc, "nope")
	if err := checkUDPResponse(errHdr, 0xabc, udpActionConnect); err == nil ||
		!strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected tracker error, got %v", err)
	}
	_ = randomTxID()
	if got := udpError(7, "x"); len(got) != 9 || string(got[8:]) != "x" {
		t.Fatalf("udpError = %x", got)
	}
}

func TestUDPIPv6Tracker(t *testing.T) {
	tr, err := NewUDPTracker(true, WithUDPPeers(CompactPeer{IP: net.ParseIP("::1"), Port: 7100}))
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer tr.Close()
	addr, err := UDPURLAddr(tr.URL())
	if err != nil {
		t.Fatal(err)
	}
	connID, err := UDPConnect(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	var ih [20]byte
	resp, err := UDPAnnounce(context.Background(), addr, connID, ih, "pid", 1, 0, true)
	if err != nil || len(resp.Peers) != 1 || resp.Peers[0].Port != 7100 {
		t.Fatalf("IPv6 announce = %+v %v", resp, err)
	}
	// A 6-byte IPv4 list must not decode as an 18-byte IPv6 list.
	if _, err := DecodeCompactPeers6(EncodeCompactPeers4([]CompactPeer{
		{IP: net.ParseIP("127.0.0.1"), Port: 1},
	})); err == nil {
		t.Fatal("expected truncation error for a 6-byte IPv6 list")
	}
}
