// Command uded2kpeerprobe is an evidence-collection-only interop probe for the
// 2026-11-27 protocol refresher. It logs into real eD2k servers from a
// server.met, optionally runs a keyword search, picks the file with the most
// sources, then connects to those sources and records what actually answered:
//
//   - which client software the peer reports (CT_EMULE_VERSION -> eMule/aMule),
//   - whether it completed our SecureIdent challenge (OP_PUBLICKEY /
//     OP_SECIDENTSTATE / OP_SIGNATURE),
//   - whether it is reachable as a High ID source and what it sends first.
//
// It never touches ud state or the daemon, and it refuses to invent a verdict:
// PASS only means "a real server and at least one real peer spoke to us".
// Run it on a host with real network access.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	goed2k "github.com/asdfgh129/gobitmule/internal/ed2k"
	"github.com/asdfgh129/gobitmule/internal/ed2k/protocol"
)

type serverEvidence struct {
	Address            string `json:"address"`
	ClientID           int32  `json:"client_id"`
	IDClass            string `json:"id_class"`
	HandshakeCompleted bool   `json:"handshake_completed"`
	UsersCount         int32  `json:"users_count,omitempty"`
	Name               string `json:"name,omitempty"`
	UDPFlags           uint32 `json:"udp_flags"`
	FilesCount         int32  `json:"files_count,omitempty"`
	LastMessage        string `json:"last_message,omitempty"`
}

type searchHit struct {
	Hash     string `json:"hash"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Sources  int    `json:"sources"`
	Complete int    `json:"complete_sources"`
}

type peerEvidence struct {
	Endpoint     string `json:"endpoint"`
	State        string `json:"state,omitempty"`
	Client       string `json:"client,omitempty"`
	Version      int    `json:"version,omitempty"`
	ModVersion   string `json:"mod_version,omitempty"`
	UserHash     string `json:"user_hash,omitempty"`
	SourceFlag   int    `json:"source_flag"`
	Verified     bool   `json:"secure_ident_verified"`
	Progress     string `json:"progress,omitempty"`
	DownloadRate int    `json:"download_bps,omitempty"`
}

type udpEvidence struct {
	SearchRequested  bool                   `json:"search_requested"`
	SourceRequested  bool                   `json:"source_requested"`
	SearchOpcode     string                 `json:"search_opcode"`
	SourceOpcode     string                 `json:"source_opcode"`
	SourceSent       bool                   `json:"source_sent,omitempty"`
	SourceFromSearch bool                   `json:"source_from_search,omitempty"`
	ServerUDPKey     bool                   `json:"server_udp_key"`
	Before           goed2k.UDPTrafficStats `json:"before"`
	After            goed2k.UDPTrafficStats `json:"after"`
	SearchDatagrams  int64                  `json:"search_datagrams"`
	SearchEntries    int64                  `json:"search_entries"`
	SourcesDatagrams int64                  `json:"sources_datagrams"`
	SourcesEndpoints int64                  `json:"sources_endpoints"`
	// AnswerOpcodesDelta is After.AnswerOpcodes minus Before.AnswerOpcodes:
	// every datagram received during the wait window, keyed by inner opcode,
	// regardless of whether its body parsed. "0x9b>0 but sources_endpoints=0"
	// therefore means "answered, body unusable" rather than "never answered".
	AnswerOpcodesDelta map[string]int64 `json:"answer_opcodes_delta,omitempty"`
}

type report struct {
	Timestamp    string           `json:"timestamp"`
	Servers      []serverEvidence `json:"servers"`
	SearchQuery  string           `json:"search_query,omitempty"`
	SearchHits   []searchHit      `json:"search_top,omitempty"`
	ChosenFile   *searchHit       `json:"chosen_file,omitempty"`
	VisiblePeers int              `json:"visible_sources"`
	ActiveConns  int              `json:"active_connections"`
	Peers        []peerEvidence   `json:"peers,omitempty"`
	ClientCounts map[string]int   `json:"client_counts,omitempty"`
	VerifiedPeer int              `json:"secure_ident_peers"`
	IdentKeyLen  int              `json:"secure_ident_key_bytes,omitempty"`
	Downloaded   int64            `json:"downloaded_bytes"`
	UDP          *udpEvidence     `json:"udp,omitempty"`
	Verdict      string           `json:"verdict"`
	BlockReason  string           `json:"block_reason,omitempty"`
}

func main() {
	os.Exit(run())
}

func run() int {
	met := flag.String("met", "data/ed2k/server.met", "server.met to pick servers from")
	server := flag.String("server", "", "explicit server address (host:port); overrides -met")
	query := flag.String("search", "debian", "server-side keyword search used to find a live file")
	searchWait := flag.Int("search-wait", 25, "seconds to wait for search results")
	hashArg := flag.String("hash", "", "skip the search and use this file hash (32 hex)")
	sizeArg := flag.Int64("size", 0, "file size for -hash")
	tcp := flag.Int("tcp", 4662, "TCP listen port")
	udp := flag.Int("udp", 4672, "UDP (server datagram + KAD) port")
	duration := flag.Int("duration", 45, "seconds to observe peer connections")
	secureIdent := flag.Bool("secure-ident", true, "advertise and verify SecureIdent")
	obfuscation := flag.Bool("obfuscation", false, "accept/offer CryptLayer obfuscation")
	statePath := flag.String("state", "", "engine state file (persists UserHash + RSA identity)")
	debug := flag.Bool("debug", false, "emit engine debug logs to stderr")
	waitUDPKey := flag.Bool("wait-udp-key", false, "wait for the server's crypt-ping key before searching")
	udpSearchOpcode := flag.String("udp-search-opcode", "", "force UDP search opcode (hex, e.g. 0x98) for interop testing")
	udpSourceOpcode := flag.String("udp-source-opcode", "", "force UDP source opcode (hex, e.g. 0x9A) for interop testing")
	udpOnly := flag.Bool("udp-only", false, "only collect UDP search/source evidence; skip the peer swarm")
	udpSearch := flag.Bool("udp-search", true, "in -udp-only mode, send a global search")
	udpSource := flag.Bool("udp-source", false, "in -udp-only mode, send a global source request (needs -hash/-size)")
	udpWait := flag.Int("udp-wait", 8, "seconds to wait for UDP answers in -udp-only mode")
	out := flag.String("out", "", "write the JSON report to this path")
	flag.Parse()

	r := report{Timestamp: time.Now().UTC().Format(time.RFC3339), Verdict: "NOT_EXECUTED"}
	if *debug {
		goed2k.SetDebugLog(os.Stderr)
	} else {
		goed2k.SetDebugLog(io.Discard)
	}

	st := goed2k.NewSettings()
	st.ListenPort = *tcp
	st.UDPPort = *udp
	st.SecureIdent = *secureIdent
	st.EnableObfuscation = *obfuscation
	st.EnableDHT = false // server + peer interop only; KAD has its own probe

	c := goed2k.NewClient(st)
	c.SetAutoSaveInterval(0)
	defer c.Close()
	if *statePath != "" {
		c.SetStatePath(*statePath)
		if err := c.LoadState(""); err != nil {
			r.BlockReason = "load state: " + err.Error()
			printReport(r, *out)
			return 2
		}
	}
	if err := c.Start(); err != nil {
		r.BlockReason = "client Start: " + err.Error()
		printReport(r, *out)
		return 2
	}

	// 1) log in to real servers.
	addrs := []string{}
	if *server != "" {
		addrs = append(addrs, *server)
	} else {
		entries, err := c.LoadServerMet(*met)
		if err != nil {
			r.BlockReason = "load server.met: " + err.Error()
			printReport(r, *out)
			return 2
		}
		for _, e := range entries {
			addrs = append(addrs, e.Endpoint.String())
		}
	}
	if len(addrs) == 0 {
		r.BlockReason = "no server candidate"
		printReport(r, *out)
		return 2
	}
	// ConnectServerMet already tries candidates in order; use it so a dead
	// server does not abort the run.
	if *server == "" {
		if err := c.ConnectServerMet(*met); err != nil {
			r.BlockReason = "connect server.met: " + err.Error()
			printReport(r, *out)
			return 2
		}
	} else {
		// -server overrides the candidate list, but server.met still carries the
		// ST_UDPFLAGS bits for that endpoint; loading it seeds the capability
		// cache so the immediate search below picks the datagram opcode the
		// server can actually parse.
		_, _ = c.LoadServerMet(*met)
		if err := c.Connect(*server); err != nil {
			r.BlockReason = "connect server: " + err.Error()
			printReport(r, *out)
			return 2
		}
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && !anyHandshake(c) {
		time.Sleep(250 * time.Millisecond)
	}
	// OP_SERVERIDENT (which carries the UDP capability bits) arrives a few
	// hundred milliseconds after OP_IDCHANGE; collect it before sampling.
	for i := 0; i < 20 && !anyServerIdent(c); i++ {
		time.Sleep(150 * time.Millisecond)
	}
	if *udpSearchOpcode != "" {
		value, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(*udpSearchOpcode), "0x"), 16, 8)
		if err != nil {
			r.BlockReason = "bad -udp-search-opcode: " + err.Error()
			printReport(r, *out)
			return 2
		}
		c.Session().SetForcedUDPSearchOpcode(byte(value))
		fmt.Fprintf(os.Stderr, "probe: forcing UDP search opcode 0x%02x\n", value)
	}
	if *udpSourceOpcode != "" {
		value, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(*udpSourceOpcode), "0x"), 16, 8)
		if err != nil {
			r.BlockReason = "bad -udp-source-opcode: " + err.Error()
			printReport(r, *out)
			return 2
		}
		c.Session().SetForcedUDPSourcesOpcode(byte(value))
		fmt.Fprintf(os.Stderr, "probe: forcing UDP source opcode 0x%02x\n", value)
	}
	if *waitUDPKey {
		for i := 0; i < 40 && !c.Session().HasServerUDPKey(); i++ {
			time.Sleep(250 * time.Millisecond)
		}
		fmt.Fprintf(os.Stderr, "probe: server UDP key installed=%t\n", c.Session().HasServerUDPKey())
	}
	r.Servers = collectServers(c)
	if !anyHandshake(c) {
		r.Verdict = "FAIL"
		r.BlockReason = "no server completed the login handshake"
		printReport(r, *out)
		return 2
	}

	// UDP-only interop mode: prove that a specific request opcode really
	// produced a parseable answer from the live server. No transfer is added,
	// so peer state cannot mask a missing UDP reply.
	if *udpOnly {
		ev := udpEvidence{
			SearchRequested: *udpSearch,
			SourceRequested: *udpSource,
			SearchOpcode:    forcedOpcodeLabel(*udpSearchOpcode),
			SourceOpcode:    forcedOpcodeLabel(*udpSourceOpcode),
			ServerUDPKey:    c.Session().HasServerUDPKey(),
		}
		ev.Before = c.Session().UDPStats()
		if *udpSearch {
			r.SearchQuery = *query
			r.SearchHits = runSearch(c, *query, time.Duration(*searchWait)*time.Second)
		}
		if *udpSource {
			var hash protocol.Hash
			var size int64
			if *hashArg == "" {
				// 不指定固定哈希时，先在本服务器搜一个热门文件再查源。避免把
				// "这台服务器没有我们那个固定 ISO 的源"误判为协议不兼容。
				if len(r.SearchHits) == 0 {
					r.SearchHits = runSearch(c, *query, time.Duration(*searchWait)*time.Second)
				}
				best := pickBest(r.SearchHits)
				if best == nil {
					r.Verdict = "FAIL"
					r.BlockReason = "-udp-source auto hash: search returned no usable file"
					printReport(r, *out)
					return 2
				}
				var err error
				hash, err = protocol.HashFromString(best.Hash)
				if err != nil {
					r.Verdict = "FAIL"
					r.BlockReason = "bad search hash: " + err.Error()
					printReport(r, *out)
					return 2
				}
				size = best.Size
				r.ChosenFile = best
				ev.SourceFromSearch = true
			} else {
				if *sizeArg <= 0 {
					r.Verdict = "FAIL"
					r.BlockReason = "-udp-source with -hash also needs -size"
					printReport(r, *out)
					return 2
				}
				var err error
				hash, err = protocol.HashFromString(*hashArg)
				if err != nil {
					r.Verdict = "FAIL"
					r.BlockReason = "bad -hash: " + err.Error()
					printReport(r, *out)
					return 2
				}
				size = *sizeArg
				r.ChosenFile = &searchHit{Hash: hash.String(), Size: size, Name: "(from -hash)"}
			}
			fmt.Fprintf(os.Stderr, "probe: source hash=%s size=%d from_search=%t\n", hash.String(), size, ev.SourceFromSearch)
			ev.SourceSent = c.Session().SendUDPSourcesRequest(hash, size)
		}
		if *udpWait > 0 {
			time.Sleep(time.Duration(*udpWait) * time.Second)
		}
		ev.After = c.Session().UDPStats()
		ev.SearchDatagrams = ev.After.SearchDatagrams - ev.Before.SearchDatagrams
		ev.SearchEntries = ev.After.SearchEntries - ev.Before.SearchEntries
		ev.SourcesDatagrams = ev.After.SourcesDatagrams - ev.Before.SourcesDatagrams
		ev.SourcesEndpoints = ev.After.SourcesEndpoints - ev.Before.SourcesEndpoints
		for op, n := range ev.After.AnswerOpcodes {
			if delta := n - ev.Before.AnswerOpcodes[op]; delta > 0 {
				if ev.AnswerOpcodesDelta == nil {
					ev.AnswerOpcodesDelta = make(map[string]int64)
				}
				ev.AnswerOpcodesDelta[op] = delta
			}
		}
		r.UDP = &ev
		missing := []string{}
		if *udpSearch && ev.SearchDatagrams == 0 {
			missing = append(missing, "no UDP search answer")
		}
		if *udpSource {
			if !ev.SourceSent {
				missing = append(missing, "source request not sent")
			}
			if ev.SourcesDatagrams == 0 {
				missing = append(missing, "no UDP source answer")
			}
		}
		if len(missing) > 0 {
			r.Verdict = "FAIL"
			r.BlockReason = strings.Join(missing, "; ")
			printReport(r, *out)
			return 2
		}
		r.Verdict = "PASS"
		printReport(r, *out)
		return 0
	}

	// 2) find a file that real peers are actually sharing.
	var chosen *searchHit
	if *hashArg != "" && *sizeArg > 0 {
		chosen = &searchHit{Hash: strings.ToUpper(*hashArg), Size: *sizeArg, Name: "(from -hash)"}
	} else {
		r.SearchQuery = *query
		r.SearchHits = runSearch(c, *query, time.Duration(*searchWait)*time.Second)
		chosen = pickBest(r.SearchHits)
	}
	r.ChosenFile = chosen
	if chosen == nil {
		r.Verdict = "FAIL"
		r.BlockReason = "server search returned no usable file (no hash+size)"
		printReport(r, *out)
		return 2
	}

	// 3) join the swarm and watch who answers.
	h, err := protocol.HashFromString(chosen.Hash)
	if err != nil {
		r.BlockReason = "bad hash: " + err.Error()
		printReport(r, *out)
		return 2
	}
	tmp, err := os.CreateTemp(os.TempDir(), "ud-peerprobe-*.part")
	if err != nil {
		r.BlockReason = "temp file: " + err.Error()
		printReport(r, *out)
		return 2
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	if _, err := c.AddTransfer(goed2k.NewAddTransferParamsFromFile(h, goed2k.CurrentTimeMillis(), chosen.Size, tmp, false)); err != nil {
		r.BlockReason = "AddTransfer: " + err.Error()
		printReport(r, *out)
		return 2
	}

	handle := c.FindTransfer(h)
	everConnected := false
	accum := map[string]peerEvidence{}
	stop := time.Now().Add(time.Duration(*duration) * time.Second)
	for time.Now().Before(stop) {
		if handle.IsValid() {
			status := handle.GetStatus()
			r.VisiblePeers = status.NumPeers
			r.ActiveConns = handle.ActiveConnections()
		}
		if r.ActiveConns > 0 {
			everConnected = true
		}
		if r.ActiveConns > 0 {
			// Keep sampling: SecureIdent and the client tag arrive a few
			// packets after the handshake, and a peer that reconnects starts
			// a fresh connection whose identity is not known yet.
			mergePeers(accum, handle)
			if countVerified(accum) > 0 {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if handle.IsValid() {
		status := handle.GetStatus()
		r.Downloaded = status.TotalDone
		r.VisiblePeers = status.NumPeers
		r.ActiveConns = handle.ActiveConnections()
		mergePeers(accum, handle)
	}
	r.Peers, r.ClientCounts, r.VerifiedPeer = finalizePeers(accum)
	r.IdentKeyLen = c.IdentKeyPublicDERLen()

	switch {
	case !everConnected && r.ActiveConns == 0:
		r.Verdict = "FAIL"
		r.BlockReason = "no peer connection was established"
	case len(r.Peers) == 0:
		r.Verdict = "FAIL"
		r.BlockReason = "peers connected but none reported a client identity"
	default:
		r.Verdict = "PASS"
	}
	printReport(r, *out)
	if r.Verdict == "PASS" {
		return 0
	}
	return 2
}

func forcedOpcodeLabel(value string) string {
	if value == "" {
		return "auto"
	}
	return strings.ToLower(value)
}

func anyHandshake(c *goed2k.Client) bool {
	for _, s := range c.ServerStatuses() {
		if s.HandshakeCompleted {
			return true
		}
	}
	return false
}

// anyServerIdent reports whether any connected server already told us what it
// can parse (OP_SERVERIDENT), which is what the UDP opcode choice depends on.
func anyServerIdent(c *goed2k.Client) bool {
	for _, s := range c.ServerStatuses() {
		if s.HasServerIdent {
			return true
		}
	}
	return false
}

func collectServers(c *goed2k.Client) []serverEvidence {
	out := make([]serverEvidence, 0, 4)
	for _, s := range c.ServerStatuses() {
		out = append(out, serverEvidence{
			Address:            s.Address,
			ClientID:           s.ClientID,
			IDClass:            s.IDClass(),
			HandshakeCompleted: s.HandshakeCompleted,
			UsersCount:         s.UsersCount,
			Name:               s.Name,
			UDPFlags:           s.UDPFlags,
			FilesCount:         s.FilesCount,
			LastMessage:        s.LastMessage,
		})
	}
	return out
}

func runSearch(c *goed2k.Client, query string, wait time.Duration) []searchHit {
	handle, err := c.StartSearch(goed2k.SearchParams{Query: query, Scope: goed2k.SearchScopeServer})
	if err != nil {
		return nil
	}
	defer func() { _ = handle.Stop() }()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		snap := handle.Snapshot()
		switch snap.State {
		case goed2k.SearchStateFinished, goed2k.SearchStateFailed, goed2k.SearchStateStopped:
			return topHits(snap.Results)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return topHits(handle.Snapshot().Results)
}

func topHits(results []goed2k.SearchResult) []searchHit {
	hits := make([]searchHit, 0, len(results))
	for _, res := range results {
		if res.FileSize <= 0 || res.Hash.Equal(protocol.Invalid) {
			continue
		}
		hits = append(hits, searchHit{
			Hash:     res.Hash.String(),
			Name:     res.FileName,
			Size:     res.FileSize,
			Sources:  res.Sources,
			Complete: res.CompleteSources,
		})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Sources > hits[j].Sources })
	if len(hits) > 10 {
		hits = hits[:10]
	}
	return hits
}

func pickBest(hits []searchHit) *searchHit {
	best := -1
	for i, h := range hits {
		if best < 0 || h.Sources > hits[best].Sources {
			best = i
		}
	}
	if best < 0 {
		return nil
	}
	chosen := hits[best]
	return &chosen
}

// mergePeers folds one sample into the accumulator. A peer keeps "verified"
// once it proved possession of its key, even if that connection later closes.
func mergePeers(accum map[string]peerEvidence, handle goed2k.TransferHandle) {
	for _, pi := range handle.GetPeersInfo() {
		if pi.ModName == "" && pi.UserHash.Equal(protocol.Invalid) && !pi.Verified {
			continue // still handshaking, nothing learned yet
		}
		key := pi.Endpoint.String()
		ev, seen := accum[key]
		if !seen {
			ev = peerEvidence{Endpoint: key, SourceFlag: pi.SourceFlag}
		}
		if pi.ModName != "" {
			ev.Client = pi.ModName
		}
		if pi.Version != 0 {
			ev.Version = pi.Version
		}
		if pi.StrModVersion != "" {
			ev.ModVersion = pi.StrModVersion
		}
		if !pi.UserHash.Equal(protocol.Invalid) {
			ev.UserHash = pi.UserHash.String()
		}
		if pi.State != "" {
			ev.State = pi.State
		}
		if pi.ProgressKnown {
			ev.Progress = fmt.Sprintf("%.1f%%", pi.Progress*100)
		}
		if pi.DownloadSpeed > ev.DownloadRate {
			ev.DownloadRate = pi.DownloadSpeed
		}
		if pi.Verified {
			ev.Verified = true
		}
		accum[key] = ev
	}
}

func countVerified(accum map[string]peerEvidence) int {
	n := 0
	for _, ev := range accum {
		if ev.Verified {
			n++
		}
	}
	return n
}

// finalizePeers turns the accumulator into the report slice plus counters.
func finalizePeers(accum map[string]peerEvidence) ([]peerEvidence, map[string]int, int) {
	peers := make([]peerEvidence, 0, len(accum))
	counts := map[string]int{}
	verified := 0
	for _, ev := range accum {
		peers = append(peers, ev)
		if ev.Client != "" {
			counts[ev.Client]++
		}
		if ev.Verified {
			verified++
		}
	}
	sort.SliceStable(peers, func(i, j int) bool { return peers[i].Endpoint < peers[j].Endpoint })
	return peers, counts, verified
}

func printReport(r report, out string) {
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
	if out != "" {
		if err := os.WriteFile(out, append(b, '\n'), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write report: %v\n", err)
		}
	}
}
