package interop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/asdfgh129/ud/internal/core"
	bttransport "github.com/asdfgh129/ud/internal/transport/bt"
)

// LocalCases returns the fully automated loopback evidence cases. They are
// supplementary (Gate=false): a green local suite does NOT mean the L4 release
// matrix (docs/12 §3.1) has been executed.
func LocalCases() []Case {
	return []Case{
		local("L3-01", "BEP 3 HTTP tracker：announce / scrape / compact v4+v6 / 故障注入",
			caseHTTPTracker),
		local("L3-02", "BEP 15 UDP tracker：connect / announce / scrape / compact IPv4",
			caseUDPTrackerV4),
		local("L3-03", "BEP 15 UDP tracker：IPv6 compact peer list (18B)",
			caseUDPTrackerV6),
		local("L3-04", "HTTPS tracker + passkey + 主 tracker 故障后切备",
			caseHTTPSFailover),
		local("L3-05", "真实 BT 线协议端到端：独立 seeder + 自建 tracker + bttransport",
			caseEndToEndTracker),
		local("L3-06", "BT 握手抓包：诚实 PeerID（-UD0000-，非伪装）",
			caseHonestPeerID),
		local("L3-07", "纯磁力：仅 infohash 经 bootstrap peer 取元数据并完成下载",
			casePureMagnet),
		local("L3-08", "多文件种子选择下载：未选文件字节不进入 ChunkWriter",
			caseSelectiveDownload),
		local("L3-09", "流式读取：4KiB 分片首片可读时延",
			caseStreamingFirstChunk),
	}
}

func local(id, title string, run func(context.Context, Options) Outcome) Case {
	return Case{ID: id, Title: title, Criterion: "本地自动化证据（不替代 L4 发布门禁）",
		Scope: ScopeInProcess, Gate: false, Requires: "仅回环，零外部网络", Run: run}
}

// ---------------------------------------------------------------------------
// L3 cases
// ---------------------------------------------------------------------------

func caseHTTPTracker(ctx context.Context, _ Options) Outcome {
	peers4 := []CompactPeer{
		{IP: net.ParseIP("127.0.0.1"), Port: 6881},
		{IP: net.ParseIP("127.0.0.2"), Port: 6882},
	}
	peers6 := []CompactPeer{{IP: net.ParseIP("::1"), Port: 6883}}
	want := ScrapeStat{Complete: 7, Downloaded: 42, Incomplete: 3}
	tr := NewHTTPTracker(false, WithPeers(peers4...), WithPeers6(peers6...), WithStats(want))
	defer tr.Close()

	var ih [20]byte
	copy(ih[:], "ud-interop-infohash-01")
	resp, err := AnnounceHTTP(ctx, tr.Client(), tr.URL(), ih, "-UD0000-interop01", 51413, 4096)
	if err != nil {
		return fail("announce 失败：" + err.Error())
	}
	got4, err := DecodeCompactPeers4([]byte(resp.Peers))
	if err != nil {
		return fail("IPv4 compact peers 解码失败：" + err.Error())
	}
	got6, err := DecodeCompactPeers6([]byte(resp.Peers6))
	if err != nil {
		return fail("IPv6 compact peers 解码失败：" + err.Error())
	}
	if len(got4) != 2 || got4[0].Port != 6881 || got4[1].Port != 6882 {
		return fail(fmt.Sprintf("IPv4 peers = %+v", got4))
	}
	if len(got6) != 1 || got6[0].Port != 6883 || !got6[0].IP.IsLoopback() {
		return fail(fmt.Sprintf("IPv6 peers = %+v", got6))
	}
	scrape, err := ScrapeHTTP(ctx, tr.Client(), tr.ScrapeURL(), ih)
	if err != nil {
		return fail("scrape 失败：" + err.Error())
	}
	f, ok := scrape.Files[string(ih[:])]
	if !ok || f.Complete != want.Complete || f.Downloaded != want.Downloaded || f.Incomplete != want.Incomplete {
		return fail(fmt.Sprintf("scrape 统计不符：%+v", scrape.Files))
	}

	// Injected 5xx must be observable (tracker failure path, docs/12 §4).
	tr.SetFault(503, 0)
	if _, err := AnnounceHTTP(ctx, tr.Client(), tr.URL(), ih, "-UD0000-interop01", 51413, 0); err == nil {
		return fail("注入 503 后 announce 仍然成功，故障注入失效")
	}
	tr.SetFault(0, 0)
	ann := tr.Announces()
	if len(ann) != 1 || ann[0].InfoHash != ih || ann[0].Passkey != "" {
		return fail(fmt.Sprintf("announce 记录异常：%+v", ann))
	}
	return pass("BEP 3 announce/scrape、compact IPv4(2)/IPv6(1)、统计与 503 故障注入全部符合预期；"+
		"纯 Go 自建 fixture，未使用 chihaya / opentracker 二进制",
		Evidence{
			"ipv4_peers": len(got4), "ipv6_peers": len(got6),
			"scrape_complete": f.Complete, "scrape_downloaded": f.Downloaded,
			"scrape_incomplete": f.Incomplete, "announces_recorded": len(ann),
			"scrape_requests": tr.Scrapes(), "fault_injected": "503",
		})
}

func caseUDPTrackerV4(ctx context.Context, _ Options) Outcome {
	peers := []CompactPeer{{IP: net.ParseIP("127.0.0.1"), Port: 7001},
		{IP: net.ParseIP("127.0.0.3"), Port: 7002}}
	want := ScrapeStat{Complete: 3, Downloaded: 11, Incomplete: 5}
	tr, err := NewUDPTracker(false, WithUDPPeers(peers...), WithUDPStats(want))
	if err != nil {
		return fail("启动 UDP tracker 失败：" + err.Error())
	}
	defer tr.Close()
	addr, err := UDPURLAddr(tr.URL())
	if err != nil {
		return fail(err.Error())
	}
	var ih [20]byte
	copy(ih[:], "ud-interop-udp-hash-01")
	connID, err := UDPConnect(ctx, addr)
	if err != nil {
		return fail("connect 失败：" + err.Error())
	}
	if connID != tr.ConnectionID() {
		return fail("connect 返回的 connection id 与 tracker 不一致")
	}
	resp, err := UDPAnnounce(ctx, addr, connID, ih, "-UD0000-interop02", 51413, 4096, false)
	if err != nil {
		return fail("announce 失败：" + err.Error())
	}
	if len(resp.Peers) != 2 || resp.Peers[0].Port != 7001 || resp.Peers[1].Port != 7002 {
		return fail(fmt.Sprintf("compact peers = %+v", resp.Peers))
	}
	stats, err := UDPScrape(ctx, addr, connID, ih)
	if err != nil {
		return fail("scrape 失败：" + err.Error())
	}
	if len(stats) != 1 || stats[0].Seeders != 3 || stats[0].Completed != 11 || stats[0].Leechers != 5 {
		return fail(fmt.Sprintf("scrape 统计不符：%+v", stats))
	}
	if _, err := UDPAnnounce(ctx, addr, connID+1, ih, "-UD0000-interop02", 51413, 0, false); err == nil {
		return fail("过期 connection id 未被拒绝")
	}
	connects, scrapes := tr.Counters()
	return pass("BEP 15 connect/announce/scrape、6B compact IPv4 peers、统计与 stale connection id 拒绝全部符合预期",
		Evidence{"connects": connects, "scrapes": scrapes,
			"peers": len(resp.Peers), "recorded_announces": len(tr.Announces()),
			"interval_s": resp.Interval})
}

func caseUDPTrackerV6(ctx context.Context, _ Options) Outcome {
	tr, err := NewUDPTracker(true, WithUDPPeers(CompactPeer{IP: net.ParseIP("::1"), Port: 7003}))
	if err != nil {
		return Outcome{Status: StatusSkipped, Reason: "IPv6 回环不可用，跳过 IPv6 compact 用例：" + err.Error()}
	}
	defer tr.Close()
	addr, err := UDPURLAddr(tr.URL())
	if err != nil {
		return fail(err.Error())
	}
	connID, err := UDPConnect(ctx, addr)
	if err != nil {
		return fail("IPv6 connect 失败：" + err.Error())
	}
	var ih [20]byte
	copy(ih[:], "ud-interop-udp6-hash-1")
	resp, err := UDPAnnounce(ctx, addr, connID, ih, "-UD0000-interop03", 51413, 0, true)
	if err != nil {
		return fail("IPv6 announce 失败：" + err.Error())
	}
	if len(resp.Peers) != 1 || resp.Peers[0].Port != 7003 || !resp.Peers[0].IP.IsLoopback() {
		return fail(fmt.Sprintf("IPv6 compact peers = %+v", resp.Peers))
	}
	return pass("18B IPv6 compact peer list 解析正确（udp6 回环）",
		Evidence{"peers": len(resp.Peers), "peer_ip": resp.Peers[0].IP.String(),
			"peer_port": resp.Peers[0].Port})
}

func caseHTTPSFailover(ctx context.Context, _ Options) Outcome {
	const pk = "pk-interop-2026"
	primary := NewHTTPTracker(true, WithPasskey(pk), WithFault(503, 0))
	defer primary.Close()
	secondary := NewHTTPTracker(true, WithPasskey(pk),
		WithPeers(CompactPeer{IP: net.ParseIP("127.0.0.1"), Port: 6999}),
		WithStats(ScrapeStat{Complete: 2, Downloaded: 9, Incomplete: 1}))
	defer secondary.Close()

	var ih [20]byte
	copy(ih[:], "ud-interop-tls-hash-01")
	// Primary tier is down: the harness must fall back to the next tracker tier.
	if _, err := AnnounceHTTP(ctx, primary.Client(), primary.URL(), ih, "-UD0000-interop04", 51413, 100); err == nil {
		return fail("主 tracker 注入 503 后仍返回成功")
	}
	resp, err := AnnounceHTTP(ctx, secondary.Client(), secondary.URL(), ih, "-UD0000-interop04", 51413, 100)
	if err != nil {
		return fail("切备后 announce 失败：" + err.Error())
	}
	got, err := DecodeCompactPeers4([]byte(resp.Peers))
	if err != nil || len(got) != 1 || got[0].Port != 6999 {
		return fail(fmt.Sprintf("备用 tracker peers = %+v, err=%v", got, err))
	}
	recs := secondary.Announces()
	if len(recs) != 1 || recs[0].Passkey != pk {
		return fail(fmt.Sprintf("passkey 未随路径传递或记录：%+v", recs))
	}
	if !strings.HasPrefix(secondary.URL(), "https://") {
		return fail("HTTPS tracker URL 不符合预期：" + secondary.URL())
	}
	return pass("自签 HTTPS tracker + URL 路径 passkey 校验通过；主 tier 503 后备用 tier 接管并回传 compact peers。"+
		"注意：这是套件自身的分层降级逻辑，anacrolix 内建多 tracker 降级仍属 L4 待验证",
		Evidence{"tls": true, "passkey_path": true, "primary_status": 503,
			"secondary_announces": len(recs), "fallback_peers": len(got)})
}

func caseEndToEndTracker(ctx context.Context, _ Options) Outcome {
	content := []byte("WBS11 interop end-to-end payload over a real BitTorrent wire connection -- deterministic loopback seed 0123456789")
	infoBytes, ih, err := makeSingleFileInfo(content, "interop.bin")
	if err != nil {
		return fail(err.Error())
	}
	tracker := NewHTTPTracker(false, WithStats(ScrapeStat{Complete: 1, Downloaded: 0, Incomplete: 1}))
	defer tracker.Close()

	mi := &metainfo.MetaInfo{
		InfoBytes:    bencode.Bytes(infoBytes),
		Announce:     tracker.URL(),
		AnnounceList: metainfo.AnnounceList{{tracker.URL()}},
	}
	raw, err := bencode.Marshal(mi)
	if err != nil {
		return fail("marshal metainfo: " + err.Error())
	}
	seed, err := startSeeder(mi, map[string][]byte{"interop.bin": content})
	if err != nil {
		return fail("启动独立 seeder 失败：" + err.Error())
	}
	defer seed.Close()
	tracker.SetPeers(CompactPeer{IP: net.ParseIP("127.0.0.1"), Port: portOf(seed.addr)})

	res := &core.Resource{
		Kind:     core.KindTorrent,
		Files:    []core.ResourceFile{{Path: "interop.bin", Selected: true}},
		Identity: core.ProtocolIdentity{TorrentMeta: raw, Private: true},
	}
	cw := newChunkWriter(int64(len(content)), int64(len(content)))
	transport := bttransport.New(nil, noopCreds{}, bttransport.WithBootstrapPeers(seed.addr))
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := transport.Run(runCtx, res, cw, nil); err != nil {
		return fail("bttransport.Run 失败：" + err.Error())
	}
	if !bytes.Equal(cw.Bytes(), content) {
		return fail("下载字节与 seeder 原文不一致")
	}
	announced := waitFor(12*time.Second, func() bool {
		for _, a := range tracker.Announces() {
			if a.InfoHash == ih {
				return true
			}
		}
		return false
	})
	if !announced {
		return fail("下载完成，但在 12s 窗口内未观测到 anacrolix 向自建 tracker 发起 announce")
	}
	return pass("经真实 BT 线协议从独立 seeder 下载完成，sha256 一致；自建 HTTP tracker 收到 infohash 匹配的 announce。"+
		"seeder 与 leecher 均为 anacrolix 客户端，不等价于与 qBittorrent / Transmission 互通",
		Evidence{
			"bytes": len(content), "sha256": hex.EncodeToString(sha256sum(content)),
			"announce_observed": announced, "announces": len(tracker.Announces()),
			"tracker_scheme": "http", "peer_discovery": "tracker + bootstrap",
		})
}

func caseHonestPeerID(ctx context.Context, _ Options) Outcome {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fail("启动握手捕获监听失败：" + err.Error())
	}
	defer ln.Close()

	type capture struct {
		handshake []byte
		attempts  int
		err       error
	}
	ch := make(chan capture, 1)
	go func() {
		// anacrolix prefers an MSE-obfuscated header first and, when that
		// handshake fails, retries with a plaintext BT handshake. Closing the
		// obfuscated connection therefore drives the client to the plaintext
		// dialect we can inspect on the wire.
		tl, _ := ln.(*net.TCPListener)
		attempts := 0
		for i := 0; i < 5; i++ {
			if tl != nil {
				_ = tl.SetDeadline(time.Now().Add(3 * time.Second))
			}
			conn, err := ln.Accept()
			if err != nil {
				continue
			}
			attempts++
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			buf := make([]byte, 68)
			_, readErr := io.ReadFull(conn, buf)
			_ = conn.Close()
			if readErr == nil && buf[0] == 19 && string(buf[1:20]) == "BitTorrent protocol" {
				ch <- capture{handshake: buf, attempts: attempts}
				return
			}
		}
		ch <- capture{attempts: attempts,
			err: fmt.Errorf("未捕获到明文 BT 握手（共 %d 次连接，客户端可能始终使用 MSE 混淆头）", attempts)}
	}()

	// A pure magnet with only an infohash makes the client dial bootstrap peers
	// immediately; metadata will never arrive, which is expected here.
	res := &core.Resource{
		Kind:     core.KindTorrent,
		Identity: core.ProtocolIdentity{InfoHashV1: "0102030405060708090a0b0c0d0e0f1011121314"},
	}
	transport := bttransport.New(nil, noopCreds{}, bttransport.WithBootstrapPeers(ln.Addr()))
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	probeDone := make(chan struct{})
	go func() {
		defer close(probeDone)
		_, _ = transport.Probe(probeCtx, res)
	}()

	c := <-ch
	// Metadata can never arrive from a bare handshake listener; cancel as soon
	// as the wire evidence is captured instead of burning the full timeout.
	cancel()
	<-probeDone
	if c.err != nil {
		return fail(c.err.Error())
	}
	h := c.handshake
	peerID := string(h[48:68])
	if len(peerID) != 20 || !strings.HasPrefix(peerID, "-UD0000-") {
		return fail(fmt.Sprintf("PeerID 不诚实：%q", peerID))
	}
	for _, forbidden := range []string{"-qB", "-TR", "-LT", "-UT", "-AZ"} {
		if strings.HasPrefix(peerID, forbidden) {
			return fail("伪装为第三方客户端：" + forbidden)
		}
	}
	return pass("从真实 TCP BT 握手第 48..68 字节捕获 PeerID，前缀 -UD0000- 且未伪装任何主流客户端；"+
		"客户端先尝试 MSE 混淆头、失败后回退明文握手。"+
		"这是回环抓包等价证据，公网 L4 抓包仍属 BT-14 待办",
		Evidence{"peer_id": peerID, "handshake_bytes": len(h),
			"protocol": string(h[1:20]), "source": "tcp loopback",
			"connections_until_plaintext": c.attempts})
}

func casePureMagnet(ctx context.Context, _ Options) Outcome {
	content := []byte("pure magnet interop payload: metadata fetched over BEP 9 from a loopback bootstrap peer 9876543210")
	infoBytes, _, err := makeSingleFileInfo(content, "magnet.bin")
	if err != nil {
		return fail(err.Error())
	}
	mi := &metainfo.MetaInfo{InfoBytes: bencode.Bytes(infoBytes)}
	hash := mi.HashInfoBytes().HexString()
	seed, err := startSeeder(mi, map[string][]byte{"magnet.bin": content})
	if err != nil {
		return fail("启动 seeder 失败：" + err.Error())
	}
	defer seed.Close()

	res := &core.Resource{
		Kind:     core.KindTorrent,
		Files:    []core.ResourceFile{{Path: "magnet.bin", Selected: true}},
		Identity: core.ProtocolIdentity{InfoHashV1: hash},
	}
	cw := newChunkWriter(int64(len(content)), int64(len(content)))
	transport := bttransport.New(nil, noopCreds{}, bttransport.WithBootstrapPeers(seed.addr))
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := transport.Run(runCtx, res, cw, nil); err != nil {
		return fail("纯磁力 Run 失败：" + err.Error())
	}
	if !bytes.Equal(cw.Bytes(), content) {
		return fail("纯磁力下载字节不一致")
	}
	return pass("仅凭 40 位 infohash（无 .torrent、无 DHT）经 BEP 9 从 bootstrap peer 取回元数据并完成下载，字节一致。"+
		"BT-04 要求的「仅公网 DHT」路径未验证",
		Evidence{"infohash": hash, "bytes": len(content),
			"sha256": hex.EncodeToString(sha256sum(content)), "metadata_source": "BEP 9 bootstrap peer"})
}

func caseSelectiveDownload(ctx context.Context, _ Options) Outcome {
	a := []byte("SELECTED-FILE-A-" + strings.Repeat("a", 512))
	b := []byte("UNSELECTED-FILE-B-" + strings.Repeat("b", 1024))
	info := metainfo.Info{
		Name:        "bundle",
		PieceLength: 16384,
		Files: []metainfo.FileInfo{
			{Path: []string{"a.bin"}, Length: int64(len(a))},
			{Path: []string{"b.bin"}, Length: int64(len(b))},
		},
	}
	pieces, err := metainfo.GeneratePieces(bytes.NewReader(append(append([]byte{}, a...), b...)), 16384, nil)
	if err != nil {
		return fail("生成 piece hash 失败：" + err.Error())
	}
	info.Pieces = pieces
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return fail(err.Error())
	}
	mi := &metainfo.MetaInfo{InfoBytes: bencode.Bytes(infoBytes)}
	seed, err := startSeeder(mi, map[string][]byte{
		filepath.Join("bundle", "a.bin"): a,
		filepath.Join("bundle", "b.bin"): b,
	})
	if err != nil {
		return fail("启动多文件 seeder 失败：" + err.Error())
	}
	defer seed.Close()
	raw, err := bencode.Marshal(mi)
	if err != nil {
		return fail(err.Error())
	}

	res := &core.Resource{
		Kind:     core.KindTorrent,
		Files:    []core.ResourceFile{{Path: "a.bin", Selected: true}, {Path: "b.bin"}},
		Identity: core.ProtocolIdentity{TorrentMeta: raw, Private: true},
	}
	cw := newChunkWriter(int64(len(a)), int64(len(a)))
	transport := bttransport.New(nil, noopCreds{}, bttransport.WithBootstrapPeers(seed.addr))
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := transport.Run(runCtx, res, cw, nil); err != nil {
		return fail("选择下载 Run 失败：" + err.Error())
	}
	got := cw.Bytes()
	if !bytes.Equal(got, a) {
		return fail(fmt.Sprintf("选中文件字节不一致：got %d bytes, want %d", len(got), len(a)))
	}
	if bytes.Contains(got, b[:32]) {
		return fail("未选中的 b.bin 字节泄漏进 ChunkWriter")
	}
	return pass("多文件种子仅下载被选中的 a.bin，字节一致且未混入 b.bin。"+
		"注意：冻结的 core.Resource 仍描述全部文件，是否落盘由调度器决定；BT-09 的「未选文件不落盘」需 L4 复核",
		Evidence{"selected_bytes": len(a), "unselected_bytes": len(b),
			"chunk_bytes": len(got), "sha256_selected": hex.EncodeToString(sha256sum(a))})
}

func caseStreamingFirstChunk(ctx context.Context, _ Options) Outcome {
	content := bytes.Repeat([]byte("ud-interop-stream-"), 4096) // ~72 KiB
	infoBytes, _, err := makeSingleFileInfo(content, "stream.bin")
	if err != nil {
		return fail(err.Error())
	}
	mi := &metainfo.MetaInfo{InfoBytes: bencode.Bytes(infoBytes)}
	seed, err := startSeeder(mi, map[string][]byte{"stream.bin": content})
	if err != nil {
		return fail("启动 seeder 失败：" + err.Error())
	}
	defer seed.Close()
	raw, err := bencode.Marshal(mi)
	if err != nil {
		return fail(err.Error())
	}
	const chunkSize = int64(4096)
	res := &core.Resource{
		Kind:     core.KindTorrent,
		Files:    []core.ResourceFile{{Path: "stream.bin", Selected: true}},
		Identity: core.ProtocolIdentity{TorrentMeta: raw, Private: true},
	}
	cw := newChunkWriter(int64(len(content)), chunkSize)
	transport := bttransport.New(nil, noopCreds{}, bttransport.WithBootstrapPeers(seed.addr))
	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := transport.Run(runCtx, res, cw, nil); err != nil {
		return fail("流式 Run 失败：" + err.Error())
	}
	total := time.Since(start)
	first := cw.FirstWrite().Sub(start)
	if first <= 0 || first > total {
		return fail(fmt.Sprintf("首片时延异常：first=%v total=%v", first, total))
	}
	if !bytes.Equal(cw.Bytes(), content) {
		return fail("流式分片重组后字节不一致")
	}
	return pass("File.NewReader 按 4KiB 分片供给，首片可读早于整文件完成，重组字节一致。"+
		"这是回环时延测量，不是 BT-12 的边下边播 SLA 或公网首字节阈值",
		Evidence{"bytes": len(content), "chunk_size": chunkSize,
			"chunks": cw.Writes(), "first_chunk_ms": first.Milliseconds(),
			"total_ms": total.Milliseconds()})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func sha256sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

func makeSingleFileInfo(content []byte, name string) ([]byte, [20]byte, error) {
	var ih [20]byte
	info := metainfo.Info{Name: name, PieceLength: 16384, Length: int64(len(content))}
	pieces, err := metainfo.GeneratePieces(bytes.NewReader(content), 16384, nil)
	if err != nil {
		return nil, ih, fmt.Errorf("generate pieces: %w", err)
	}
	info.Pieces = pieces
	raw, err := bencode.Marshal(info)
	if err != nil {
		return nil, ih, fmt.Errorf("marshal info: %w", err)
	}
	mi := metainfo.MetaInfo{InfoBytes: bencode.Bytes(raw)}
	h := mi.HashInfoBytes()
	copy(ih[:], h[:])
	return raw, ih, nil
}

// seedFixture is an independent anacrolix client that serves real BT pieces.
type seedFixture struct {
	addr net.Addr
	cl   *torrent.Client
	dir  string
}

func (s *seedFixture) Close() {
	s.cl.Close()
	_ = os.RemoveAll(s.dir)
}

func startSeeder(mi *metainfo.MetaInfo, files map[string][]byte) (*seedFixture, error) {
	dir, err := os.MkdirTemp("", "ud-interop-seed-")
	if err != nil {
		return nil, err
	}
	for rel, data := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
	}
	cfg := torrent.NewDefaultClientConfig()
	cfg.NoDHT = true
	cfg.DisablePEX = true
	cfg.NoUpload = false
	cfg.Seed = true
	cfg.ListenHost = func(network string) string {
		if strings.HasSuffix(network, "6") {
			return "::1"
		}
		return "127.0.0.1"
	}
	cfg.ListenPort = 0
	cfg.DefaultStorage = storage.NewFile(dir)
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	spec := &torrent.TorrentSpec{InfoBytes: mi.InfoBytes, InfoHash: mi.HashInfoBytes()}
	tor, _, err := cl.AddTorrentSpec(spec)
	if err != nil {
		cl.Close()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	tor.DownloadAll()
	info, err := mi.UnmarshalInfo()
	if err != nil {
		cl.Close()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	nPieces := len(info.Pieces) / 20
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		complete := true
		for i := 0; i < nPieces; i++ {
			if !tor.PieceState(i).Complete {
				complete = false
				break
			}
		}
		if complete && nPieces > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var addr net.Addr
	for _, a := range cl.ListenAddrs() {
		if a != nil && strings.Contains(a.String(), "127.0.0.1") {
			addr = a
			break
		}
	}
	if addr == nil {
		for _, a := range cl.ListenAddrs() {
			if a != nil {
				addr = a
				break
			}
		}
	}
	if addr == nil {
		cl.Close()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("seeder has no listen address")
	}
	return &seedFixture{addr: addr, cl: cl, dir: dir}, nil
}

func portOf(a net.Addr) int {
	if ta, ok := a.(*net.TCPAddr); ok {
		return ta.Port
	}
	_, p, _ := net.SplitHostPort(a.String())
	n, _ := net.LookupPort("tcp", p)
	return n
}

// noopCreds satisfies bttransport's constructor without holding secrets.
type noopCreds struct{}

func (noopCreds) Get(context.Context, core.CredentialRef) (string, error) { return "", nil }
func (noopCreds) Put(context.Context, string) (core.CredentialRef, error) {
	return core.CredentialRef("mem://interop"), nil
}
func (noopCreds) Delete(context.Context, core.CredentialRef) error { return nil }

// chunkWriter drives the scheduler-side chunk plan and records timing.
type chunkWriter struct {
	mu         sync.Mutex
	data       []byte
	total      int64
	chunkSize  int64
	next       int64
	writes     int
	firstWrite time.Time
}

func newChunkWriter(total, chunkSize int64) *chunkWriter {
	if chunkSize <= 0 {
		chunkSize = total
	}
	return &chunkWriter{total: total, chunkSize: chunkSize}
}

func (w *chunkWriter) Next(context.Context) (core.Chunk, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.next >= w.total {
		return core.Chunk{}, false, nil
	}
	length := w.chunkSize
	if w.next+length > w.total {
		length = w.total - w.next
	}
	c := core.Chunk{Start: w.next, Length: length}
	w.next += length
	return c, true, nil
}

func (w *chunkWriter) WriteChunk(_ context.Context, c core.Chunk, p []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if int64(len(w.data)) < c.Start+int64(len(p)) {
		nd := make([]byte, c.Start+int64(len(p)))
		copy(nd, w.data)
		w.data = nd
	}
	copy(w.data[c.Start:c.Start+int64(len(p))], p)
	w.writes++
	if w.firstWrite.IsZero() {
		w.firstWrite = time.Now()
	}
	return nil
}

func (w *chunkWriter) Verified(context.Context, core.Chunk, core.Hash) error { return nil }
func (w *chunkWriter) Release(context.Context, core.Chunk, error) error      { return nil }

func (w *chunkWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.data...)
}

func (w *chunkWriter) FirstWrite() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.firstWrite
}

func (w *chunkWriter) Writes() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writes
}

func waitFor(d time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fn()
}
