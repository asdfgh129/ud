package goed2k

// #2: eD2k server UDP transport. eMule binds the server UDP socket at TCP
// port + 4 and uses it for global searches and glob source requests, with an
// optional RC4 envelope keyed by the server's UDP key. This file owns that
// socket and feeds decoded results back into the session.

import (
	"fmt"
	"net"
	"sync"

	"github.com/asdfgh129/gobitmule/internal/ed2k/internal/logx"
	"github.com/asdfgh129/gobitmule/internal/ed2k/protocol"
	serverproto "github.com/asdfgh129/gobitmule/internal/ed2k/protocol/server"
)

// UDPTrafficStats counts server-UDP datagrams that were actually decoded.
// Probes use it as evidence that a legacy opcode round-tripped with a real
// server instead of merely being written to a socket.
type UDPTrafficStats struct {
	SearchDatagrams  int64 `json:"search_datagrams"`
	SearchEntries    int64 `json:"search_entries"`
	SourcesDatagrams int64 `json:"sources_datagrams"`
	SourcesEndpoints int64 `json:"sources_endpoints"`
	// AnswerOpcodes counts every decoded datagram by its inner opcode, before
	// body parsing. It separates "server answered but we could not parse the
	// body" from "server never answered", which a parsed-only counter hides.
	AnswerOpcodes map[string]int64 `json:"answer_opcodes,omitempty"`
}

func (s UDPTrafficStats) recordAnswerOpcode(op byte) UDPTrafficStats {
	if s.AnswerOpcodes == nil {
		s.AnswerOpcodes = make(map[string]int64)
	}
	s.AnswerOpcodes[fmt.Sprintf("0x%02x", op)]++
	return s
}

// ServerUDP holds the server-facing UDP socket. Send uses the connected
// server's UDP endpoint; baseKey==0 means plaintext (server did not supply a
// UDP key), matching eMule's IsServerCryptLayerUDPEnabled()==false path.
type ServerUDP struct {
	mu        sync.Mutex
	conn      *net.UDPConn
	baseKey   uint32
	stopCh    chan struct{}
	once      sync.Once
	onResults func(protocol.Hash, []protocol.Endpoint)
	onSearch  func([]serverproto.SharedFileEntry)
	// onServerKey is called with a key learned from an OP_GLOBSERVSTATRES
	// crypt-ping answer. eMule installs those with SetServerKeyUDP(), which
	// binds the key to our current public IP without extra validation.
	onServerKey      func(uint32)
	pendingChallenge uint32
	stats            UDPTrafficStats
}

func NewServerUDP() *ServerUDP {
	return &ServerUDP{stopCh: make(chan struct{})}
}

// SetUDPKey installs the server's UDP key (0 disables encryption).
func (u *ServerUDP) SetUDPKey(key uint32) {
	u.mu.Lock()
	u.baseKey = key
	u.mu.Unlock()
}

// TrafficStats returns decoded server-UDP counters. The counters are kept on
// the receive path so an opcode can be proven to have produced a parseable
// answer even when no transfer or search handle consumes the entries.
func (u *ServerUDP) TrafficStats() UDPTrafficStats {
	u.mu.Lock()
	defer u.mu.Unlock()
	stats := u.stats
	if u.stats.AnswerOpcodes != nil {
		stats.AnswerOpcodes = make(map[string]int64, len(u.stats.AnswerOpcodes))
		for op, n := range u.stats.AnswerOpcodes {
			stats.AnswerOpcodes[op] = n
		}
	}
	return stats
}

// HasUDPKey reports whether a server key is currently installed. Tests and
// diagnostics use it to distinguish requests sent before and after the
// crypt-ping answer installed the envelope key.
func (u *ServerUDP) HasUDPKey() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.baseKey != 0
}

// SetServerKeyHandler installs the callback that receives a key learned from
// a crypt ping (OP_GLOBSERVSTATRES). It may be set after Start.
func (u *ServerUDP) SetServerKeyHandler(fn func(uint32)) {
	u.mu.Lock()
	u.onServerKey = fn
	u.mu.Unlock()
}

// SendCryptPing sends the obfuscated OP_GLOBSERVSTATREQ probe used by eMule
// before a plain status request. The answer is encrypted with the challenge
// (not the server key, which we do not have yet) and is the second source of
// ST_UDPKEY material, alongside OP_SERVERIDENT.
func (u *ServerUDP) SendCryptPing(addr *net.UDPAddr, challenge uint32, paddingLen int) bool {
	wire, err := serverproto.CryptPingDatagram(challenge, paddingLen)
	if err != nil {
		return false
	}
	u.mu.Lock()
	conn := u.conn
	if conn == nil || addr == nil {
		u.mu.Unlock()
		return false
	}
	u.pendingChallenge = challenge
	u.mu.Unlock()
	_, err = conn.WriteToUDP(wire, addr)
	if err != nil {
		u.mu.Lock()
		u.pendingChallenge = 0
		u.mu.Unlock()
	}
	return err == nil
}

// UDPPort returns the bound port or 0 when not started.
func (u *ServerUDP) UDPPort() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.conn == nil {
		return 0
	}
	if addr, ok := u.conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.Port
	}
	return 0
}

// Start binds the UDP socket. port<0 disables it (offline tests).
func (u *ServerUDP) Start(port int) error {
	if port < 0 {
		return nil
	}
	u.mu.Lock()
	if u.conn != nil {
		u.mu.Unlock()
		return nil
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
	if err != nil && port > 0 {
		// Falling back to an ephemeral port keeps search alive but breaks the
		// contract that the source port seen by the server equals the port we
		// advertised in CT_EMULE_UDPPORTS, so say so out loud.
		logx.Debug("server udp: bind failed, falling back to ephemeral", "port", port, "err", err.Error())
		conn, err = net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	}
	if err != nil {
		u.mu.Unlock()
		return err
	}
	logx.Debug("server udp listening", "addr", conn.LocalAddr().String(), "requested", port)
	u.conn = conn
	onResults, onSearch := u.onResults, u.onSearch
	u.mu.Unlock()
	go u.readLoop(conn, onResults, onSearch)
	return nil
}

func (u *ServerUDP) readLoop(conn *net.UDPConn,
	onResults func(protocol.Hash, []protocol.Endpoint),
	onSearch func([]serverproto.SharedFileEntry)) {
	buf := make([]byte, 8192)
	for {
		select {
		case <-u.stopCh:
			return
		default:
		}
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-u.stopCh:
				return
			default:
				continue
			}
		}
		datagram := append([]byte(nil), buf[:n]...)
		if n >= 2 {
			logx.Debug("server udp <- datagram", "bytes", n, "opcode", datagram[1], "from", addr.String(), "encrypted", datagram[0] != protocol.EdonkeyHeader)
		}
		// Read the key per datagram: OP_SERVERIDENT installs it after the
		// socket is already running, so a startup snapshot would leave every
		// encrypted answer undecryptable for the whole session.
		u.mu.Lock()
		baseKey := u.baseKey
		u.mu.Unlock()
		plain, decrypted := serverproto.DecryptServerDatagram(datagram, baseKey)
		if !decrypted {
			// Plaintext when no key, encrypted when we could decrypt.
			plain = datagram
			// A crypt-ping answer is keyed by the challenge we just sent,
			// because the server key has not been installed yet. eMule does
			// the same in CUDPSocket::ProcessServerPacket.
			u.mu.Lock()
			challenge := u.pendingChallenge
			u.mu.Unlock()
			if challenge != 0 {
				if decoded, ok := serverproto.DecryptServerDatagram(datagram, challenge); ok {
					plain = decoded
				}
			}
		}
		if len(plain) >= 2 {
			u.mu.Lock()
			u.stats = u.stats.recordAnswerOpcode(plain[1])
			u.mu.Unlock()
		}
		// Handle both the encrypted and the (fallback) plaintext form of the
		// server status answer, which carries the UDP key at body offset 36.
		if stat, ok := serverproto.ParseGlobServStatRes(plain); ok {
			u.mu.Lock()
			challenge, onServerKey := u.pendingChallenge, u.onServerKey
			u.mu.Unlock()
			if challenge != 0 && stat.Challenge == challenge {
				u.mu.Lock()
				u.pendingChallenge = 0
				u.mu.Unlock()
				debugPeerf("server udp <- crypt ping answer challenge=%d key=%d flags=0x%x",
					stat.Challenge, stat.UDPKey, stat.UDPFlags)
				if stat.UDPKey != 0 && onServerKey != nil {
					onServerKey(stat.UDPKey)
				}
			}
		}
		foundSources := serverproto.ParseUDPFoundSources(plain)
		if len(foundSources) > 0 {
			endpoints := 0
			for _, list := range foundSources {
				endpoints += len(list)
			}
			u.mu.Lock()
			u.stats.SourcesDatagrams++
			u.stats.SourcesEndpoints += int64(endpoints)
			u.mu.Unlock()
		}
		for hash, endpoints := range foundSources {
			debugPeerf("server udp source answer hash=%s got=%d from=%s encrypted=%t",
				hash.String(), len(endpoints), addr.String(), decrypted)
			if onResults != nil {
				onResults(hash, endpoints)
			}
		}
		if entries, err := serverproto.ParseUDPSearchResults(plain); err == nil && len(entries) > 0 {
			u.mu.Lock()
			u.stats.SearchDatagrams++
			u.stats.SearchEntries += int64(len(entries))
			u.mu.Unlock()
			if onSearch != nil {
				onSearch(entries)
			}
		}
	}
}

// SendTo sends a plaintext datagram (optionally RC4-wrapped) to addr.
func (u *ServerUDP) SendTo(addr *net.UDPAddr, datagram []byte) bool {
	u.mu.Lock()
	conn := u.conn
	baseKey := u.baseKey
	u.mu.Unlock()
	if conn == nil || addr == nil {
		return false
	}
	wire := datagram
	if baseKey != 0 {
		encrypted, err := serverproto.EncryptServerDatagram(datagram, baseKey)
		if err != nil {
			return false
		}
		wire = encrypted
	}
	_, err := conn.WriteToUDP(wire, addr)
	return err == nil
}

func (u *ServerUDP) Close() {
	u.once.Do(func() { close(u.stopCh) })
	u.mu.Lock()
	if u.conn != nil {
		_ = u.conn.Close()
		u.conn = nil
	}
	u.mu.Unlock()
}
