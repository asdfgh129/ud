package interop

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BEP 15 wire constants.
const (
	udpProtocolID = int64(0x41727101980)

	udpActionConnect  = uint32(0)
	udpActionAnnounce = uint32(1)
	udpActionScrape   = uint32(2)
	udpActionError    = uint32(3)

	udpConnectReqLen  = 16
	udpConnectResLen  = 16
	udpAnnounceReqLen = 98
	udpAnnounceResMin = 20
	udpScrapeResMin   = 8
	udpMaxDatagram    = 1500
)

// UDPAnnounceRecord is a recorded BEP 15 announce request.
type UDPAnnounceRecord struct {
	InfoHash   [20]byte
	PeerID     string
	Port       int
	Downloaded int64
	Left       int64
	Uploaded   int64
	Event      uint32
	NumWant    int32
	At         time.Time
}

// UDPTracker is a self-hosted BEP 15 UDP tracker bound to loopback only.
// Set ipv6 to serve 18-byte IPv6 compact peer entries instead of 6-byte IPv4.
type UDPTracker struct {
	mu     sync.Mutex
	conn   net.PacketConn
	ipv6   bool
	connID uint64
	peers  []CompactPeer
	stats  ScrapeStat

	announces []UDPAnnounceRecord
	scrapes   int
	connects  int

	wg     sync.WaitGroup
	closed bool
}

// UDPTrackerOption customises a UDP tracker fixture.
type UDPTrackerOption func(*UDPTracker)

// WithUDPPeers sets the compact peer list returned by announce replies.
func WithUDPPeers(peers ...CompactPeer) UDPTrackerOption {
	return func(t *UDPTracker) { t.peers = append([]CompactPeer(nil), peers...) }
}

// WithUDPStats sets the counters returned by scrape replies.
func WithUDPStats(s ScrapeStat) UDPTrackerOption {
	return func(t *UDPTracker) { t.stats = s }
}

// NewUDPTracker starts a loopback BEP 15 tracker.
func NewUDPTracker(ipv6 bool, opts ...UDPTrackerOption) (*UDPTracker, error) {
	network, addr := "udp4", "127.0.0.1:0"
	if ipv6 {
		network, addr = "udp6", "[::1]:0"
	}
	conn, err := net.ListenPacket(network, addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s %s: %w", network, addr, err)
	}
	t := &UDPTracker{conn: conn, ipv6: ipv6}
	var seed [8]byte
	if _, err := rand.Read(seed[:]); err != nil {
		_ = conn.Close()
		return nil, err
	}
	t.connID = binary.BigEndian.Uint64(seed[:])
	for _, o := range opts {
		o(t)
	}
	t.wg.Add(1)
	go t.serve()
	return t, nil
}

// URL is the udp:// announce URL for this tracker.
func (t *UDPTracker) URL() string { return "udp://" + t.conn.LocalAddr().String() + "/announce" }

// Addr returns just the host:port, suitable for a UDP client dial.
func (t *UDPTracker) Addr() string { return t.conn.LocalAddr().String() }

// ConnectionID is the BEP 15 connection id handed out by connect.
func (t *UDPTracker) ConnectionID() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connID
}

// Announces returns a snapshot of recorded announces.
func (t *UDPTracker) Announces() []UDPAnnounceRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]UDPAnnounceRecord(nil), t.announces...)
}

// Counters returns connect / scrape counts.
func (t *UDPTracker) Counters() (connects, scrapes int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connects, t.scrapes
}

// Close stops the fixture.
func (t *UDPTracker) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.mu.Unlock()
	_ = t.conn.Close()
	t.wg.Wait()
}

func (t *UDPTracker) serve() {
	defer t.wg.Done()
	buf := make([]byte, udpMaxDatagram)
	for {
		n, from, err := t.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		req := append([]byte(nil), buf[:n]...)
		resp, ok := t.handle(req)
		if ok {
			_ = t.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			_, _ = t.conn.WriteTo(resp, from)
		}
	}
}

func (t *UDPTracker) handle(req []byte) ([]byte, bool) {
	if len(req) < 8 {
		return nil, false
	}
	// BEP 15 connect has a different header layout from every other action:
	// the 8-byte magic protocol id replaces the connection id, so action and
	// transaction id live at 8:12 and 12:16.
	if len(req) >= udpConnectReqLen && binary.BigEndian.Uint64(req[0:8]) == uint64(udpProtocolID) {
		action := binary.BigEndian.Uint32(req[8:12])
		txID := binary.BigEndian.Uint32(req[12:16])
		if action != udpActionConnect {
			return udpError(txID, "bad connect action"), true
		}
		t.mu.Lock()
		t.connects++
		connID := t.connID
		t.mu.Unlock()
		out := make([]byte, udpConnectResLen)
		binary.BigEndian.PutUint32(out[0:4], udpActionConnect)
		binary.BigEndian.PutUint32(out[4:8], txID)
		binary.BigEndian.PutUint64(out[8:16], connID)
		return out, true
	}

	// All BEP 15 requests keep action at 8:12 and transaction id at 12:16;
	// 0:8 is the connection id (or the magic protocol id for connect).
	if len(req) < 16 {
		return nil, false
	}
	action := binary.BigEndian.Uint32(req[8:12])
	txID := binary.BigEndian.Uint32(req[12:16])
	switch action {
	case udpActionAnnounce:
		if len(req) < udpAnnounceReqLen {
			return udpError(txID, "short announce"), true
		}
		connID := binary.BigEndian.Uint64(req[0:8])
		t.mu.Lock()
		valid := connID == t.connID
		t.mu.Unlock()
		if !valid {
			return udpError(txID, "stale connection id"), true
		}
		rec := UDPAnnounceRecord{
			PeerID:     string(req[36:56]),
			Downloaded: int64(binary.BigEndian.Uint64(req[56:64])),
			Left:       int64(binary.BigEndian.Uint64(req[64:72])),
			Uploaded:   int64(binary.BigEndian.Uint64(req[72:80])),
			Event:      binary.BigEndian.Uint32(req[80:84]),
			NumWant:    int32(binary.BigEndian.Uint32(req[92:96])),
			Port:       int(binary.BigEndian.Uint16(req[96:98])),
			At:         time.Now(),
		}
		copy(rec.InfoHash[:], req[16:36])

		t.mu.Lock()
		t.announces = append(t.announces, rec)
		var peerBytes []byte
		if t.ipv6 {
			peerBytes = EncodeCompactPeers6(t.peers)
		} else {
			peerBytes = EncodeCompactPeers4(t.peers)
		}
		stat := t.stats
		t.mu.Unlock()

		out := make([]byte, udpAnnounceResMin+len(peerBytes))
		binary.BigEndian.PutUint32(out[0:4], udpActionAnnounce)
		binary.BigEndian.PutUint32(out[4:8], txID)
		binary.BigEndian.PutUint32(out[8:12], 1800)
		binary.BigEndian.PutUint32(out[12:16], uint32(stat.Incomplete))
		binary.BigEndian.PutUint32(out[16:20], uint32(stat.Complete))
		copy(out[20:], peerBytes)
		return out, true

	case udpActionScrape:
		if len(req) < 16 {
			return udpError(txID, "short scrape"), true
		}
		connID := binary.BigEndian.Uint64(req[0:8])
		t.mu.Lock()
		valid := connID == t.connID
		if valid {
			t.scrapes++
		}
		stat := t.stats
		t.mu.Unlock()
		if !valid {
			return udpError(txID, "stale connection id"), true
		}
		n := (len(req) - 16) / 20
		out := make([]byte, udpScrapeResMin+12*n)
		binary.BigEndian.PutUint32(out[0:4], udpActionScrape)
		binary.BigEndian.PutUint32(out[4:8], txID)
		for i := 0; i < n; i++ {
			off := 8 + 12*i
			binary.BigEndian.PutUint32(out[off:off+4], uint32(stat.Complete))
			binary.BigEndian.PutUint32(out[off+4:off+8], uint32(stat.Downloaded))
			binary.BigEndian.PutUint32(out[off+8:off+12], uint32(stat.Incomplete))
		}
		return out, true

	default:
		return udpError(txID, "unsupported action"), true
	}
}

func udpError(txID uint32, msg string) []byte {
	out := make([]byte, 8+len(msg))
	binary.BigEndian.PutUint32(out[0:4], udpActionError)
	binary.BigEndian.PutUint32(out[4:8], txID)
	copy(out[8:], msg)
	return out
}

// ---------------------------------------------------------------------------
// Minimal independent BEP 15 client used by the local cases
// ---------------------------------------------------------------------------

// UDPAnnounceResponse is a decoded announce reply.
type UDPAnnounceResponse struct {
	Interval  uint32
	Leechers  uint32
	Seeders   uint32
	Peers     []CompactPeer
	IPv6Peers bool
}

// UDPScrapeStat is one infohash's counters in a scrape reply.
type UDPScrapeStat struct {
	Seeders   uint32
	Completed uint32
	Leechers  uint32
}

// UDPConnect performs a BEP 15 connect handshake.
func UDPConnect(ctx context.Context, addr string) (uint64, error) {
	req := make([]byte, udpConnectReqLen)
	binary.BigEndian.PutUint64(req[0:8], uint64(udpProtocolID))
	binary.BigEndian.PutUint32(req[8:12], udpActionConnect)
	tx := randomTxID()
	binary.BigEndian.PutUint32(req[12:16], tx)
	// Only the 8-byte header is mandatory here: BEP 15 error replies are
	// variable length and must be decoded (not rejected as "short") before the
	// action-specific length checks below.
	resp, err := udpExchange(ctx, addr, req, 8)
	if err != nil {
		return 0, err
	}
	if err := checkUDPResponse(resp, tx, udpActionConnect); err != nil {
		return 0, err
	}
	if len(resp) < udpConnectResLen {
		return 0, errors.New("short connect response")
	}
	return binary.BigEndian.Uint64(resp[8:16]), nil
}

// UDPAnnounce sends a compact BEP 15 announce and decodes the peer list.
func UDPAnnounce(ctx context.Context, addr string, connID uint64, infoHash [20]byte,
	peerID string, port int, left int64, ipv6 bool) (*UDPAnnounceResponse, error) {
	req := make([]byte, udpAnnounceReqLen)
	binary.BigEndian.PutUint64(req[0:8], connID)
	binary.BigEndian.PutUint32(req[8:12], udpActionAnnounce)
	tx := randomTxID()
	binary.BigEndian.PutUint32(req[12:16], tx)
	copy(req[16:36], infoHash[:])
	copy(req[36:56], peerID)
	binary.BigEndian.PutUint64(req[64:72], uint64(left))
	binary.BigEndian.PutUint32(req[80:84], 2) // event=started
	binary.BigEndian.PutUint32(req[88:92], 0) // key
	binary.BigEndian.PutUint32(req[92:96], uint32(50))
	binary.BigEndian.PutUint16(req[96:98], uint16(port))
	resp, err := udpExchange(ctx, addr, req, 8)
	if err != nil {
		return nil, err
	}
	if err := checkUDPResponse(resp, tx, udpActionAnnounce); err != nil {
		return nil, err
	}
	if len(resp) < udpAnnounceResMin {
		return nil, errors.New("short announce response")
	}
	out := &UDPAnnounceResponse{
		Interval: binary.BigEndian.Uint32(resp[8:12]),
		Leechers: binary.BigEndian.Uint32(resp[12:16]),
		Seeders:  binary.BigEndian.Uint32(resp[16:20]),
	}
	raw := resp[20:]
	if ipv6 {
		out.IPv6Peers = true
		out.Peers, err = DecodeCompactPeers6(raw)
	} else {
		out.Peers, err = DecodeCompactPeers4(raw)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UDPScrape sends a BEP 15 scrape and decodes the counters.
func UDPScrape(ctx context.Context, addr string, connID uint64,
	infoHashes ...[20]byte) ([]UDPScrapeStat, error) {
	req := make([]byte, 16+20*len(infoHashes))
	binary.BigEndian.PutUint64(req[0:8], connID)
	binary.BigEndian.PutUint32(req[8:12], udpActionScrape)
	tx := randomTxID()
	binary.BigEndian.PutUint32(req[12:16], tx)
	for i, h := range infoHashes {
		copy(req[16+20*i:36+20*i], h[:])
	}
	resp, err := udpExchange(ctx, addr, req, 8)
	if err != nil {
		return nil, err
	}
	if err := checkUDPResponse(resp, tx, udpActionScrape); err != nil {
		return nil, err
	}
	if (len(resp)-8)%12 != 0 || len(resp) < udpScrapeResMin {
		return nil, fmt.Errorf("malformed scrape response length %d", len(resp))
	}
	n := (len(resp) - 8) / 12
	out := make([]UDPScrapeStat, 0, n)
	for i := 0; i < n; i++ {
		off := 8 + 12*i
		out = append(out, UDPScrapeStat{
			Seeders:   binary.BigEndian.Uint32(resp[off : off+4]),
			Completed: binary.BigEndian.Uint32(resp[off+4 : off+8]),
			Leechers:  binary.BigEndian.Uint32(resp[off+8 : off+12]),
		})
	}
	return out, nil
}

func udpExchange(ctx context.Context, addr string, req []byte, minLen int) ([]byte, error) {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	}
	if _, err := conn.Write(req); err != nil {
		return nil, err
	}
	buf := make([]byte, udpMaxDatagram)
	n, err := conn.Read(buf)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("no response from tracker")
		}
		return nil, fmt.Errorf("read tracker response: %w", err)
	}
	if n < minLen {
		return nil, fmt.Errorf("short UDP datagram: %d bytes", n)
	}
	return append([]byte(nil), buf[:n]...), nil
}

func checkUDPResponse(resp []byte, tx uint32, action uint32) error {
	if len(resp) < 8 {
		return errors.New("short UDP response header")
	}
	gotTx := binary.BigEndian.Uint32(resp[4:8])
	if gotTx != tx {
		return fmt.Errorf("transaction id mismatch: got %08x want %08x", gotTx, tx)
	}
	gotAction := binary.BigEndian.Uint32(resp[0:4])
	if gotAction == udpActionError {
		return fmt.Errorf("tracker error: %s", strings.TrimSpace(string(resp[8:])))
	}
	if gotAction != action {
		return fmt.Errorf("unexpected action %d, want %d", gotAction, action)
	}
	return nil
}

func randomTxID() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint32(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint32(b[:])
}

// UDPURLAddr extracts host:port from a udp:// tracker URL.
func UDPURLAddr(raw string) (string, error) {
	u, err := parseUDPURL(raw)
	if err != nil {
		return "", err
	}
	return u, nil
}

func parseUDPURL(raw string) (string, error) {
	if !strings.HasPrefix(raw, "udp://") {
		return "", fmt.Errorf("not a udp:// URL: %q", raw)
	}
	rest := strings.TrimPrefix(raw, "udp://")
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	host, port, err := net.SplitHostPort(rest)
	if err != nil {
		return "", err
	}
	p, err := strconv.Atoi(port)
	if err != nil || p <= 0 || p > 65535 {
		return "", fmt.Errorf("invalid tracker port %q", port)
	}
	// net.SplitHostPort already strips the brackets from "[::1]:port", and
	// net.JoinHostPort adds them back when the host contains a colon.
	return net.JoinHostPort(host, port), nil
}
