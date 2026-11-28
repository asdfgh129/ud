# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

版本语义：`0.x` 阶段为 P0 骨架期，次版本号对应里程碑（0.1.x = P0），修订号对应构建批次。
`MAJOR.MINOR.PATCH` 正式启用于 P3 结束后的首个稳定版。每次代码变更必须在合入前写入本文件，
并同步受影响的 `docs/` 文档（见 [docs/README.md](docs/README.md) 与 [docs/15-开发规范与协作流程.md](docs/15-开发规范与协作流程.md)）。

## [Unreleased]

> 标「2026-11-09 评审闭环」的条目已随 `v0.1.0` 附注标签发布；自该标签起的 P1 变更记入本段
> （首条为 2026-11-10 的覆盖率债清零）。标签 message 附本文件摘要。


### Added
- **查源证据补强：热门哈希替代固定 ISO，8/8 在线服务器回 `0x9B`（2026-11-28）**：
  - `docs/26` 升 v1.5、新增 §4.8。此前固定 ISO 哈希（`1565D4…C90`）查源 9 台只有 3 台
    回 `0x9B`，§4.7 只推测「更像是目标哈希在那些服务器上没有可用源」。本轮改造探针：
    `-udp-source` 不带 `-hash` 时先让该服务器做关键词搜索（`debian`），用 `pickBest()`
    挑源数最多的热门文件再查源——**8 台在线服务器全部回 `0x9B`**（第 9 台
    `85.137.248.210:56612` 主动拒绝连接、不可达），实测坐实根因是「对端索引里没有该
    ISO 的源」而非协议不兼容。三重证据一致：`source_from_search=true`、
    `answer_opcodes_delta` 含 `0x9b:1`、逐哈希日志 `source answer hash=<热门哈希>`。
  - `internal/ed2k/server_udp.go`：`UDPTrafficStats` 新增 `AnswerOpcodes map[string]int64`，
    **在 body 解析前**按 inner opcode 计数，区分「服务器回了 `0x9B` 但解析失败」与
    「根本没回」；`TrafficStats()` 返回 map 副本，避免前后快照共享导致差值恒为 0。
  - `cmd/uded2kpeerprobe/main.go`：自动选热门哈希、报告新增 `answer_opcodes_delta` 字段。
  - 新增单测 `TestUDPTrafficStatsAnswerOpcodes`、`TestServerUDPTrafficStatsReturnsIndependentMap`。
- **性能主线复核（2026-11-28）**：在当前代码上重跑 `b8e9de9`（读路径 160 KiB/s 硬顶解除、
  RequestBlocks 补货闸门修复）的 3 个行为断言——`TestDoReadDrainsMoreThanOneChunk`、
  `TestRequestBlocksRefillsBeforeQueueDrains`、`TestRequestBlocksDoesNotCloseWithPendingWindow`
  ——全部 PASS，确认吞吐增益仍成立。该提交当时已用同一源帧率 1.8→3.8、窗口 3→8 反而降到
  14.8 KB/s 等实测闭环，本轮无新增基准需要。

### Added
- **UDP 互通专项收口归档 + SX2 未回 0x84 根因修正（2026-11-28）**：
  - `docs/26` 升 v1.4 并标记「已收口归档」：server UDP 字节级互通证据闭环，明确不再为
    「找到自动选择 `0x92`/`0x9A` 的服务器」或「9/9 查源回包」继续投入；保留未闭环边界说明。
  - `docs/16` §6.1.3 修正 SX2 结论：`OP_REQUESTSOURCES2`(0x83)/`OP_ANSWERSOURCES2`(0x84)
    是 peer 间源交换（走 `OP_EMULEPROT` 0xC5），**不是服务器协议**；此前「多数服务器不回
    0x84」的表述概念错误。逐字节核对 + `peers.pcapng` 抓包确认我方 0x83 报文为
    `c5 14 00 00 00 83 04 00 00 <hash>`，与 aMule `DownloadClient.cpp` 组包一致，触发时机
    也在 Hello/ExtHello/SecureIdent/FileStatus 全部完成之后。
  - 根因定位：eMule/aMule 只在「本连接正在为该 hash 提供上传且自己持有可交换源列表」时
    才组应答；aMule `CPartFile::CreateSrcInfoPacket()` 在 `m_SrcList.empty()` 时 `return NULL`，
    `ClientTCPSocket.cpp` 因 `tosend==NULL` 不发包，另有 `GetLastSrcReqTime()` 时间窗限制。
    真机连的是正在给我们提供下载的**完整源节点**（`FileStatusAnswer pieces=0 have=0`），
    无 srcList 可交换，故静默——是协议设计使然，非互通缺陷。抓包 0x83×2、0x84×0，
    但 0x54/0x46 正常传输，连接健康。
  - 新增单测 `TestHandleRequestSources2WithoutSourcesSendsNothing`，固化「无源不发包」语义，
    与 aMule/eMule 对齐；SX2 的报文发送与接收解析能力判为 PASS，未回 0x84 归为「应答前提未满足」。

### Added
- **eD2k UDP 抽样复跑：强制 `0x92` 9/9 回包，自动/`0x9A` 查源稳定为 3 台（2026-11-28）**：
  对 `server-capabilities.txt` 中 9 台服务器再跑两轮——仅自动查源重试、强制 `0x92`
  搜索 + 强制 `0x9A` 查源，证据在 `dist/ed2k-interop/udp-cap2/`。强制 `0x92` 在
  9/9 服务器上拿到解析回包；自动查源与强制 `0x9A` 仍是同一批 3 台（91.208.162.87、
  82.192.72.186、57.128.161.24）回包，逐哈希日志确认 `got=1 ... encrypted=true`。
  三轮 crypt key 下发分别是 5/9、7/9、8/9，说明 `0x97` key 跨会话不稳定；有 key 也
  不等于查源有回包。文档 `docs/26` 升 v1.3（§4.7），同步 `docs/16` §6.1、
  `docs/README` v1.56。
- **eD2k legacy UDP 真机往返与 9 台抽样验证（2026-11-28）**：补完上一轮留下的
  `0x9A` / `0x98` 未闭环项，并把抽样中 9 台 UDP 能力服务器逐台重跑。
  - 探针新增 `-udp-only`（不加载 transfer、只看 UDP 回包）、`-udp-search` /
    `-udp-source`、`-udp-source-opcode` 覆盖开关与 `-udp-wait`；会话层新增
    `SetForcedUDPSourcesOpcode()` 和 `UDPTrafficStats()`，接收路径按解析成功计数并
    逐条打印目标哈希的 `0x9B` 回包，避免把登录时的异步回包误算成目标请求结果。
  - 新增 `TestForcedUDPSourcesOpcodeOnRealSocket`：真实 UDP socket 断言 `0x9A`（18 B）
    与 `0x94`（22 B）字节内容，并验证强制值可绕过「无能力位不发」的默认分支。
  - 真机：Cyberchrist 18.1 上 `0x98` → 10 个加密 `0x99`，`0x9A` → 1 个目标哈希的
    加密 `0x9B`；证据 `dist/ed2k-interop/udp98-real.*`、`udp9a-real.*`。
  - 9 台抽样：9/9 登录、9/9 UDP 搜索有回包、3/9 目标哈希查源有回包、5/9 经 crypt ping
    装上 server key。两台实时 `OP_SERVERIDENT` 报 `udpFlags=0` 但 server.met 缓存让
    请求自动选了 `0x90` 且仍被服务器应答，验证了能力位预热策略。证据在
    `dist/ed2k-interop/udp-cap/`。自动选择 `0x92`/`0x9A` 的服务器仍未找到；
    6 台查源未回包只记录事实，不外推为不兼容。
  - 存档 `docs/26` v1.2、`docs/16` §6.1、`docs/README` v1.55。

### Fixed
- **eD2k UDP 真机闭环：crypt ping 下发 key、加密往返与 0x92 回传（2026-11-28）**：
  上一轮未闭环的两项已在 Cyberchrist 18.1（`57.128.161.24:6666`）上跑通。
  - **修复第 6 处互通缺陷**：crypt ping 请求被错误套了 RC4 信封。eMule
    `CClientUDPSocket::SendPacket()` 明确不对 raw packet 加密（此时还没有 server key），
    请求必须是明文 `<challenge 4><padding 5>`；原来的密文被服务器当成 challenge，
    应答永远解不开。第一次真机抓包帧 16/22 复现，改为明文后立即拿到 key。
  - **补齐第二条 key 路径**：实现 obfuscated servstat ping（发往 TCP+12）与
    `OP_GLOBSERVSTATRES(0x97)` 解析（key 在 body[36:40]），安装 key 后普通 UDP
    请求/回包自动进入 RC4 信封。真机拿到 key `1838661081`。
  - **0x92 真机回传**：探针加 `-wait-udp-key` 与 `-udp-search-opcode` 测试开关；
    等 key 安装后强制发 `0x92`，服务器回传 10 个加密 `0x99` 并全部解析入库。
    抓包 `udp92-encrypted.pcapng` 帧 18/24/28/32 覆盖明文 ping、加密应答、
    加密请求与加密结果四个环节。
  - 存档 `docs/26` v1.1、`docs/16` §6.1、`docs/README` v1.54；新增 3 个测试。
  - 当时仍未闭环：自动选择 `0x92` 的服务器、`0x9A`/`0x98` legacy 真机往返
    （legacy 两项已于本日稍后的上一条关闭）。
- **eD2k UDP 信封交叉验证，补 0x92 分支测试，并修复 UDP key 绑定对象（2026-11-28）**：
  上一轮实测确认 `udpKey=false` 的服务器无法验证 RC4 信封，本轮用**按 eMule 源码另行实现**
  的编解码器做字节级交叉验证，并补齐 0x92 分支；验证中发现第 5 处互通缺陷：
  - **UDP key 校验对象错误**：eMule `CServer::GetServerKeyUDP()` 把 key 绑定到
    `theApp.GetPublicIP()`，而 LOW_ID 客户端的公网 IP 来自 `OP_IDCHANGE` 第 4 个字段，
    与 `clientID`（服务器分配的 token）无关。原实现用 `keyIP == clientID`，
    会让防火墙后客户端收到的 key 全部被拒，UDP 加密永远不启用。同时 `IdChange`
    只读 12 字节，丢掉了 reported IP 和混淆 TCP 端口。现解析 20 字节（按 eMule
    规则忽略 `IsLowID` 的 reported IP），会话新增 `publicIP`，key 改为按公网 IP 校验。
  - **RC4 信封交叉验证**：新增 `emule_udp_vector_test.go`，独立实现 eMule
    `EncryptSendServer`/`DecryptReceivedServer` 与 `RC4CreateKey`，验证我们发的包能被
    **只认 `0x6B` 方向**的 eMule 解码器还原、我们收的 `0xA5` 包（padLen=0/5/15）能正确解密；
    另在 `server_udp_encryption_test.go` 用真实 UDP socket 跑通「加密 0x92 请求 →
    eMule 解码 → 加密 `0x99`/`0x9B` 回包 → 回调解析」全链路。
  - **`bSkipDiscard` 语义核对**：eMule `RC4CreateKey(..., true)` 是「跳过丢弃前 1024 字节」，
    与 Go `crypto/rc4.NewCipher` 行为一致；若按字面理解成「要丢弃」会得到永远对不上的密流。
    已在 `docs/26` §4.1 留档，避免后续误改。
  - **0x92 分支完整测试**：`TestUDPSearchReqV2WireFormat` 逐字节断言 opcode `0x92`、
    无 tag set（`01 0600 "debian"` 中的 `01` 是搜索项类型而非 tag 数），以及加上
    `LARGEFILES` 后切到带 tag set 的 `0x90`。
  - 新增测试：`TestIdChangeParsesReportedPublicIP`、`TestIdChangeLegacyAndBogusReportedIP`、
    `TestIdChangePutRoundTrip`、`TestOnServerIDChangeStoresReportedPublicIP`、
    `TestSetServerUDPKeyUsesPublicIP`、`TestDecryptServerDatagramAgainstEMuleServerVector`、
    `TestEncryptServerDatagramMatchesEMuleDecoder`、
    `TestDecryptServerDatagramRejectsInvalidEnvelope`、
    `TestUDPSearchReqV2WireFormat`、`TestServerUDPEncryptedRoundTripAndEMuleAnswers`。
  - 存档：`docs/26-eD2k-UDP互通验证报告.md`（格式、证据、交叉验证方法、PASS/未闭环清单、
    复跑命令），`docs/README.md` §三登记，`tools/build_docs.py` 分组同步，`docs/16` §6.1
    交叉引用。**真机 RC4（服务器下发 `ST_UDPKEY`）与 0x92 真实服务器往返仍未执行**，
    不据此翻转发布结论。
- **eD2k 真实服务器 / 真实客户端互通实测，修复 4 处只有联网才会暴露的缺陷（2026-11-27）**：
  上一条复核是读码比对；本轮用 `cmd/uded2kpeerprobe` 登录真实公网服务器
  （`176.123.5.89:4725` eMule Sunrise、`57.128.161.24:6666` Cyberchrist 18.1）并与真实
  eMule / aMule 客户端对连，暴露出以下偏差：
  - **登录包缺 `CT_EMULE_UDPPORTS(0xF9)`**：服务器无从得知本端 eD2k UDP 端口，
    发来的 UDP 应答无法与本端绑定端口对应。`NewLoginRequest` 补该 tag，值 `udpPort<<16|udpPort`。
  - **从不发 `OP_GETSERVERLIST`，服务器因此从不发 `OP_SERVERIDENT`**：能力位与 UDP key 全部落空。
    现在 `OP_IDCHANGE` 后补发一次，实测立即收到
    `ServerIdent name="Cyberchrist" version="18.1" udpFlags=0x0000073b`。
  - **UDP 搜索**：`0x90` 必须是 `<1 tag set><搜索树>`，原实现只写搜索树，服务器会把查询首
    4 字节当 tag 数直接丢弃；同时未按能力选版本。新增 `UDPGlobSearchDatagram()`，
    按 `EXT_GETFILES`+`LARGEFILES`/`EXT_GETFILES`/其余 选 `0x90`/`0x92`/`0x98`，
    并补 `CT_SERVER_UDPSEARCH_FLAGS(0x0E)` tag set。
  - **SecureIdent 身份 1024 位被 eMule 直接丢弃**：eMule `RSAKEYSIZE=384`、
    `MAXPUBKEYSIZE=80`，1024 位的 X.509 SPKI 是 162 字节，`SetSecureIdent` 判超长即弃，
    对端因此永不回签名。Go 的 `crypto/rsa` 拒绝 <1024 位，故新增
    `internal/ed2k/rsa_ident.go` 自实现 384 位 RSA（PKCS#1 v1.5 + SHA-1），
    公钥 78–80 字节、签名 48 字节；身份随 `client_state` 持久化，跨重启稳定。
    实测对端 `bambino 60` 完成双向验证（`secure_ident_peers=1`）。
  - **能力位时序**：搜索/查源可能在 `OP_SERVERIDENT` 到达前发出，导致 opcode 降级为 legacy、
    UDP 查源直接不发。现补 `ServerMetEntry.UDPFlags()` 读取 `server.met` 的 `ST_UDPFLAGS(0x92)`
    并在加载时预热会话缓存（`Session.SeedServerCapabilities`），每次 `OP_SERVERIDENT` 回填
    （`RememberServerCapabilities`），并以 `ServerConnection.HasServerIdent()` 区分
    「尚未下发」与「确实不支持」。
  - 实测结果：UDP 搜索请求 `opcode=0x90 flags=0x73b`、回 10 个 `0x99` 结果全部解析入库；
    UDP 查源 `0x94`（`e394<hash><size>`）→ `0x9B`（2 条源）解析正确并按 `IsLowID` 过滤；
    真实客户端识别 `eMule v0.50` / `aMule GIT v3.2` / `bambino`；小样本实际下载有
    `TotalDone` 增长。证据与抓包在 `dist/ed2k-interop/`，结论写入 `docs/16` §6.1。
  - 新增回归测试：`TestIdentPublicKeyFitsEMuleLimit`、`TestIdentSignVerifyRoundTrip`、
    `TestIdentKeyStateRoundTrip`、`TestServerMetEntryCarriesUDPFlags`、
    `TestGlobSearchOpcodeFollowsServerCapabilities`、`TestSeedServerCapabilitiesFromServerMet`、
    `TestParseUDPFoundSourcesRealServerAnswer`（真实服务器回包作为黄金样本）。
  - **未闭环**：`UDPOBFUSCATION` 的 RC4 信封端到端（本轮服务器均 `udpKey=false`）；
    `0x92` 分支仅单测覆盖；抽样 40 台公开服务器仅 1 台宣告 UDP 能力。
- **对照 eMule 逐项复核 16 项能力，修正 8 处协议/语义不一致（2026-11-27）**：
  上轮补齐的 16 项能力经字节级复核后，发现以下实现与 `srchybrid` 参考实现不符，均已修正：
  - **SecureIdent 签名报文与签名内容**：`OP_SIGNATURE` 只带 `<len 1><sig>[<sigIPused 1>]`，
    原实现把公钥重复塞进签名包，真实客户端会判为 `nSize` 不匹配直接丢弃；签名数据应为
    `<验证方公钥><验证方挑战>`（`CClientCreditsList::VerifyIdent` 用 `m_abyMyPublicKey`），
    而原实现只签 4 字节挑战。同时 `OP_SECIDENTSTATE` 的取值 1/2 语义写反
    （1 = 只需签名、2 = 需要公钥+签名），且原实现只验证对端、从不发送自己的公钥/签名。
    现补齐完整双向状态机，会话级持久 RSA 1024 位身份，并在 Hello/ExtHello 后主动发起；
    MiscOptions1 与 ET_FEATURES 同时宣告 SecIdent v1。
  - **Server UDP 从未真正启用**：`OP_SERVERIDENT`（0x41）此前完全未实现，因此
    `ST_UDPFLAGS` / `ST_UDPKEY` / `ST_UDPKEYIP` 拿不到，UDP 加密与
    `OP_GLOBGETSOURCES2` 选择都失去依据。新增 `protocol/server/server_ident.go` 并接入
    会话：按 `SRV_UDPFLG_EXT_GETSOURCES2` 选 0x94、否则回退 0x9A，按
    `SRV_UDPFLG_UDPOBFUSCATION` + keyIP 校验安装 RC4 key。
  - **Server UDP 收包**：`readLoop` 启动时快照 `baseKey`，登录后安装的 key 永远不生效，
    改为逐包读取；结果解析原先逐字节滑动搜索 `E3 <opcode>`，payload 内出现该字节对会
    错位，改为严格按头部解析，并支持 eMule 的「每记录重复一次子包头」形式。
  - **KAD callback 报文**：`KADEMLIA_CALLBACK_REQ` 实际是 34 字节
    `<check 16><file 16><TCPPort 2>`，`Process_KADEMLIA_CALLBACK_REQ` 对更短的包直接抛错；
    原实现只发 2 字节端口。现按 34 字节编码，并补上 `OP_CALLBACK`（0x99）客户端报文：
    收到 callback 的 buddy 通过已有 TCP 连接把 `<check><file><ip><port>` 转给被请求方；
    KAD 搜到 firewalled 源时向返回该结果的节点发 CALLBACK_REQ（check = 搜索目标）。
  - **AICH 失败应答**：eMule 无法提供恢复数据时回 16 字节裸 hash，原解析器会因字段缺失
    报错并当作坏包；现识别为 `Failed` 并停止对 AICH 的请求、回退整块重下。
  - **IPFilter 等级比较**：`CIPFilter::IsFiltered` 用 `level < 过滤等级`（严格小于），
    原实现用 `<=`，导致等级恰好等于 127 的条目被误杀；另外 0.0.0.0 现在始终放行。
  - **出站压缩块**：改用 zlib 最高压缩级别（eMule `compress2(..., 9)`），分片按
    `total/(total/10240)` 均分并把尾片合并，与 `CreatePackedPackets` 一致。
  - **聊天报文**：上限改为 eMule 的 `MAX_CLIENT_MSG_LEN`（450 字节），并校验
    `length + 2 == size`，超长包不再被静默接受。
  - **UDP 绑定端口**：服务端数据报的本地源端口改用配置的 eD2k UDP 端口
    （eMule 绑定 `CClientUDPSocket`），原实现绑 `TCP 端口 + 4`，与对端看到的源端口不一致。
  - 回归测试：`TestServerIdentDecodesUDPCapabilities`、
    `TestUDPFoundSourcesMultipleSubpackets`、`TestUDPParserRejectsEmbeddedHeader`、
    `TestUDPGlobGetSourcesLegacy`、`TestCallbackReqWireFormat`、`TestCallbackRoundTrip`、
    `TestSecureIdentSignatureWireFormat`、`TestIPFilterLevelBoundaryAndUnspecified`、
    `TestAICHAnswerShortRefusal`。
  - 文档：对照矩阵写入 `docs/16` §3.1（16 项 = 8 项线协议偏差 + 8 项设计差异 + 结论）；
    用例补至 `docs/12` §3.2（ED-09..ED-13 与自动化覆盖边界）；模块清单写入 `docs/05` §1；
    `/ed2k/servers` 新增字段（`udpFlags`/`name`/统计）记入 `docs/07` §2；
    `docs/25` §14.5 汇总验证与未闭环项。
- **补齐 eD2k 服务器登录 / 源交换 / 上传三条结构性短板（2026-11-26）**：
  - **背景**：上一修复清掉了引擎自身的读路径与请求窗口上限，但同一文件仍只有几十 KB/s；
    对照 eMule 1.8 MB/s，剩下的差距来自「服务器源列表拿不到、客户端间不交换源、
    本端不上传所以拿不到队列信用」三条结构性通路缺失，而非单连接速率问题。
  - **服务器登录与 port callback**：
    - 服务器登录后会回连客户端公告的 TCP 端口做 HighID 探活，实测回连只发
      6 字节头 `E3 3B 00 00 00 01`（`Hello`，size=59）然后**等客户端先发 Hello**；
      原实现的 `prepareIncoming` 等完整报文体，双方互等约 1 秒后服务器判定端口不可用，
      直接断开整个服务器会话。改为读到完整 6 字节前缀即识别为 greeting-needed 入站，
      调 `OnConnect()` 主动发 Hello；`HandleClientHello` 排入 HelloAnswer 后立刻
      `FlushOutgoing()`，不再等 100ms tick。
    - 入站日志此前显示 `0.0.0.0:0`，现用 `socket.RemoteAddr()` 记录真实端点
      （实测 `176.123.5.89:...`）。
    - `ReconnectToServer` 默认从 `false` 改为 `true`；`ServerConnectionPolicy` 重写为
      **永远保留 candidate 并持续重试**（前 5 次线性退避，之后封顶 60s，登录成功清零），
      只有显式 `RemoveConnectCandidates()` 才关闭重试。此前一次失败会导致长期无服务器。
    - `ServerConnection` 增加异步拨号（`ConnectAsync` / `pumpDial`），避免 DNS/连接池
      阻塞 100ms 的 IO pump；`PumpIO` 每次 tick 都 pump。
    - 守护进程创建 engine 后、连接服务器前，从 `ed2k-resume/*.json` 恢复 `user_hash`
      （`ReadUserHashFromStateFile`），消除「临时 hash 登录、随后又换成另一个 hash」
      导致的身份/信用错乱。实测日志：`ed2k identity restored userHash=92019591...`。
    - 实测修复后登录成功：`<- IdChange clientID=71042416`，随后
      `-> OfferFiles count=1`、`<- FoundFileSources count=5 / count=7`。
    - 回归测试：`TestServerConnectionPolicyAlwaysSchedulesARetry`、
      `TestServerConnectionPolicyRampsThenCaps`、`TestServerConnectionPolicyLoginResetsBackoff`、
      `TestServerConnectionPolicyIgnoresSuccessForAnotherServer`、
      `TestServerConnectionPolicyRemoveDisables`。
  - **客户端间源交换 SX2**：
    - 新增 `protocol/client/source_exchange.go`，实现 eMule/aMule 现行
      `OP_REQUESTSOURCES2 = 0x83` / `OP_ANSWERSOURCES2 = 0x84`（旧 `0x81/0x82` 已废弃），
      `SourceExchangeVersion = 4`。请求布局 `[version:1][reserved:2][hash:16]`；
      应答条目按 v1/v2/v4 递增携带 `userID / port / serverIP / serverPort`、
      `userHash`、`cryptOptions`。**高 ID 源的 `userID` 才是可拨号 IP**，
      `serverIP/serverPort` 只是其所属服务器信息——此前误把后者当地址，已按 aMule
      `CreateSrcInfoPacket()` 校正。
    - `PeerConnection` 在 FileStatusAnswer 确认对端持有文件后，若远端声明 SX2 能力
      （`CT_EMULE_MISCOPTIONS2` bit10，或 `CT_EMULE_MISCOPTIONS1` 的 SourceExchange1Ver）
      则发一次 `RequestSources2`；应答中的高 ID 源合并进 transfer policy 并保留 user hash，
      标记来源为 `PeerSourceExchange`（Web 源列表显示 `sx`）。应答构造时排除请求者自身、
      低 ID 源与重复项，上限 500 条。
    - **修复 `MiscOptions.IntValue()` 的 Go 运算符优先级错误**：`<<` 与 `*` 同级左结合，
      原写法 `x << 4 * 7` 实为 `(x << 4) * 7`，能力位全部错位。线上 Hello tag 从
      `83FA74000000` 修正为 `83FA04100010`（Unicode / SourceExchange / NoViewSharedFiles
      位正确）。所有移位均加括号，新增 `TestMiscOptionsBitPositions`。
    - 回归测试：`TestRequestSources2WireLayout`、`TestRequestSources2RoundTrip`、
      `TestAnswerSources2Version1Layout`、`TestAnswerSources2RoundTripAllVersions`、
      `TestAnswerSources2RejectsAbsurdCount`、`TestAnswerSources2TruncatedEntry`、
      `TestHybridUserIDUsesTrailingFourBytes`、`TestMaybeRequestSourcesUsesSX2`、
      `TestHandleAnswerSources2MergesDialableSources`、
      `TestHandleAnswerSources2IgnoresAnotherFile`、`TestHandleRequestSources2OffersKnownSources`。
      线上目前请求未收到应答，取证表明当前对端多为完整文件源（aMule/eMule 对完整文件
      通常不维护/返回源列表），受控测试已覆盖完整收发闭环。
  - **真实上传与队列信用**：
    - 此前只有已完成文件会 `OfferFiles`；进行中文件即使已有可上传块也不被公告，
      其他客户端无法发现我们是源。改为只要 `CanUpload()` 为真（包括部分文件）就发布，
      并在 `Transfer.SecondTick()` 中做一次延迟发布（`publishWhenShareable`）。
    - 引擎 `TransferStatus.Upload` 此前从未上报到 scheduler，`core.Task.BytesUploaded`
      恒为 0。新增 `core.UploadSink`，`Goed2kDownloader.OpenWithProgressAndUpload()`
      每秒采样上传字节 → `chunkWriter.ReportUploaded()` → scheduler `liveUploaded`
      overlay → `Task.BytesUploaded`，Web/任务统计与 API 现在能看到真实上传量。
- **修复下载吞吐被引擎自身上限压住（对照 eMule 1.8 MB/s 只有 30 KB/s，2026-11-26）**：
  - 现象：同一个 ubuntu ISO，eMule 显示 1.82 MB/s（4 条源，其中一条 aMule 3.1.0 独占
    1.77 MB/s），本引擎只有 16–136 KB/s，平均 30–60 KB/s。
  - 根因一（读路径）：`Connection.DoRead` 每次 `PumpIO` 只做**一次 16 KiB 读**，而
    `PumpIO` 由 100ms 的 tick 驱动 —— 单连接上行被封死在 160 KiB/s，与本机带宽、
    对端能力都无关。日志取证：`SendingPart32` 严格每 100ms 一帧 10240 字节。
    改为**按预算排空**：一次 `DoRead` 循环读到 512 KiB 上限，首读沿用 50ms 轮询超时、
    后续读用 100µs 短超时（内核已就绪的数据立即返回，读空就把 tick 交还）。
    读缓冲提到 64 KiB 并在 `Connection` 上复用，避免每 tick 产生垃圾。
  - 根因二（请求补货）：`RequestBlocks` 有两道闸门——「队列非空就返回」和
    「`transferringData` 就返回」。二者叠加成了严格批处理：发 3 块 → 等 3 块全收完
    → 才发下一批，批与批之间空转整整一个 RTT。第二道闸更隐蔽：调用方会先消费下一帧
    再补货，`transferringData` 已翻回 true，补货永远不执行。改为**按空缺增量补货**、
    且允许在接收中补货；补货数量严格等于空位数（picker 会预占，多要会造成块悬空）。
  - `RequestParts` 单包最多 3 个范围（`[3]uint32` 定长数组），窗口一旦调大会直接越界
    panic。导出 `client.PartsInRequest` 并让请求按包分批发送。
  - 实测：同一源帧率 1.8 → 3.8 帧/s，请求后不再出现空档（日志时间线确认数据连续）。
  - **窗口深度不是瓶颈**：把 `RequestQueueSize` 从 3 调到 8 后同一源反而降到
    14.8 KB/s（3 时为 20.2），说明对端按分片节奏限速，加深窗口只增加浪费。
    故维持 3（与 eMule 默认一致）。
  - 回归测试：`TestDoReadDrainsMoreThanOneChunk`（单次 DoRead 必须读到 >16 KiB）、
    `TestRequestBlocksRefillsBeforeQueueDrains`（窗口未排空前必须补货；用旧闸门跑
    会得到 `[0 3]` 并失败）、`TestRequestBlocksDoesNotCloseWithPendingWindow`。
  - 仍存在的结构性差距（已取证，未在本条修复）：本机 28 个已知源里 26 个 TCP 不可达
    （逐个裸连探测确认），源**全部来自 Kad**（25/25 标 `kad`，无 `server`、无源交换），
    且 eD2k 服务器登录被对端直接断开（`disconnect code=10` = EndOfStream）。
    eMule 靠服务器源列表 + 客户端间源交换维持新鲜源池，这两条通路本引擎尚未打通。

- **修正「对方拥有」被误读成 0%：空位图表示对端持有完整文件（2026-11-26）**：
  - 现象：源列表里**正在给我们传分片**的源，"对方拥有"显示 0%。同一 TCP 连接既在
    `<- SendingPart32`，又声称自己 0 块，逻辑上不可能。
  - 取证：给 `FileStatusAnswer` 加原始 body 打印后看到
    `raw=18 hex=<16 字节 hash>0000` —— 位图只有一个 `uint16 0`。而同一条连接随后
    `<- HashSetAnswer parts=671`、`-> StartUpload`、`<- AcceptUpload` 并正常发数据。
  - 原因：eMule/aMule 对**已完成文件**没有 part 位图，`WritePartStatus` 直接写
    `uint16 0`；"我没有这个文件"走的是另一个包（`NoFileStatus`）。原实现把
    `BitField.Len() == 0` 算成 `0/0`，于是每个"完整文件源"都显示 0%。
  - 修法：`FileStatusAnswer` 到达即置 `remoteStatusAnswered`；`GetInfo()` 改为
    三态判定——位图非空取比例、位图为空取 1.0（完整）、从未应答则不报值。
    `PeerInfo` / `core.TaskSource` 新增 `ProgressKnown`，前端只有
    `progressKnown` 为真才画进度条，否则显示 `—`（此前把"未知"画成了"0%"）。
  - 实测：`82.66.27.112:59255` 从 `prog=0` 变为 `prog=1 known=true dl≈21KB/s`；
    尚未应答的源显示 `—` 而不是 0%。
  - `FileStatusAnswer` 的 debug 日志补充 `bits` 与 body 十六进制前缀（`hexPrefix`）：
    这类客户端差异只能靠原始字节定位，日志里没有字节就只能猜。
  - 回归测试：`TestGetInfoTreatsEmptyBitmapAsCompleted`、
    `TestGetInfoReportsPartialBitmap`、`TestGetInfoLeavesProgressUnknownBeforeAnswer`。
    注：上游 `monkeyWie/goed2k` 同样把空位图算成 0，属上游语义缺口，本 fork 自行修正。

- **修复进度倒退（重启后从 0 重下）与任务名露出链接片段（2026-11-26）**：
  - **进度倒退的根因是引擎续传从未落盘**：`defaultEngineFactory` 按 ADR-0002 调了
    `SetAutoSaveInterval(0)`，而 transport 又从不给引擎配 `stateStore`，于是
    `ClientState`（各传输的 piece 位图与已下载块）一次都没写过。daemon 重启后
    `AddTransferWithHandler` 拿到的是空 picker（`atp.ResumeData == nil` 直接进
    `Downloading`），`.part` 里已有的块全部作废，进度归 0 并从头覆盖——这正是
    「之前 3%，重启后 0%」的成因。
    修法：`Goed2kConfig` 新增 `StateDir`，daemon 指向 `state.db` 同级的
    `ed2k-resume/`（内部记账，不放下载目录）。`open()` 在 `Start()` 之后、
    `AddLink()` 之前通过可选接口 `statefulEngine` 调 `SetStatePath(<StateDir>/<hash>.json)`
    + `LoadState("")`：先恢复，再注册链接，`AddLink` 命中已存在的传输并直接续传。
    配了 `StateDir` 时才开启引擎的 10s 周期保存（把丢失窗口限制在一拍内）；未配
    时保持 `SetAutoSaveInterval(0)`，即 ADR-0002 的「ud 是唯一状态权威」基线不变——
    续传文件只承载 `.part` 的块位图，任务状态与 chunk 记账仍在 ud/SQLite。
    实测：强杀时 23,493,642 字节 → 重启日志 `resumedBytes: 19456000`，任务从
    23,738,060 继续增长，不再归零。新增
    `TestOpenLoadsResumeStateBeforeAddLink`、`TestOpenWithoutStateDirSkipsResume`、
    `TestOpenToleratesResumeLoadFailure`（续传文件损坏时降级为从头下载，不判任务失败）。
  - **任务列表又显示出 `|file|...`**：`taskDisplayName()` 先 `slice('ed2k://'.length)`
    再 `split('|')`，切掉协议头后字段整体前移一位，`fields[0] === 'file'` 永不成立，
    于是落到「原始文本」兜底并截断。改为直接 `split('|')` 取 `fields[1]`（类型）与
    `fields[2]`（文件名）。
  - **小进度显示成 0%**：`ProgressBar` 用 `Math.round` 取整，6.5 GB 任务要下到 65 MB
    才够 1%，此前一直显示 `0%`，看起来像没动。改为 `0 < 进度 < 1%` 时显示 `<1%`。
  - `open()` 新增一条 `ed2k transfer started` 日志（`file` / `resumedBytes` / `size`）：
    此前续传与从零开始在线日志上无法区分，是这个 bug 难以定位的原因之一。

- **源列表抽屉加宽、可排序，并按连接状态返回（2026-11-26）**：
  - 抽屉宽度原为固定 `560px`，源表 8 列（状态 / 对方拥有 / 下载·上传 / 队列）
    被裁成横向滚动条。改为按标签页自适应：源列表 `min(1180px, 96vw)`，其余
    `min(660px, 94vw)`；同时收紧源表单元格内边距（`padding: 10px 12px`），
    使 8 列在 1180px 内不再需要横向滚动。
  - 源列表顶部新增任务进度条（已下载 / 总量 · 当前速度 · 预计剩余），不必切回
    「概览」即可看到整体进度；「对方拥有」列在进度条右侧补百分比数值。
  - 表头可点击排序：状态 / 对方拥有 / 下载·上传 / 队列，默认按状态（与 eMule
    客户端列表一致），再次点击同一列反向。前端 `sourceStateRank()` 与服务端
    `goed2k.sourceStateRank` 保持同一秩表；「队列」把 `remoteQueueRank == 0`
    （未排队）视为最后，避免无队列信息的源排在最前。
  - **服务端顺序被重排**：`Client.Status()` 收集完各传输的 peer 后按「传输哈希 +
    端点字符串」重排，把 `GetPeersInfo()` 已排好的状态序压平成地址序。改为
    `sort.SliceStable` 仅按传输哈希分组，传输内部保持状态序。实测同一任务下
    `connecting` 源已排到 `dead` 源之前。
  - 类型检查与构建：`npm run typecheck`、`npm run build` 通过；`go test ./internal/ed2k/` 通过。

- **Web 控制台源列表对齐 eMule 客户端列表 + 修复速度恒为 0（2026-11-26）**：
  - **任务栏只显示文件名**：任务行原先直接渲染 `input`（ed2k 链接可长达数百字符），
    改为 `taskDisplayName()`：优先用解析出的 `resource.files[0].path` 基名，其次从
    ed2k/magnet/HTTP 链接里解出文件名，最后才退回原始输入；完整链接移到 `title`
    悬浮提示，抽屉顶部单独保留一行「链接」。
  - **列出全部已知源而非仅活跃连接**：`Transfer.GetPeersInfo()` 原来只返回
    `t.connections`（活跃 socket），一个知道上百个源的任务在界面上只显示 1 个源。
    改为「活跃连接 + `policy.peers` 里尚未连上的源」，后者经 `idleSourceState()`
    归类为 `dead` / `unconnectable`（LowID 源）/ `waiting`（带 `retryIn` 倒计时）/
    `connecting`，并按「下载中 → 请求中 → 已连接 → 排队 → 握手 → 连接中 → 等待 →
    不可用 → 断开 → 失效」排序。`PeerInfo` / `core.TaskSource` 相应新增
    `RetryIn`、`DeadStrikes`。
  - **界面可直接读出各源状况**：抽屉源列表顶部新增计数胶囊（共 N 个源 / 传输中 /
    排队中 / 待连接 / 不可用）与「只看已连接」筛选；表格列改为 端点 / 客户端 /
    来源 / 状态（等待与失效带倒计时）/ 对方拥有（进度条）/ 下载↓上传↑ / 队列 /
    失败。新增 `sourceStateLabel()` 与 `formatCountdown()` 承担文案。
  - **任务速度恒 0（原因一，调度器侧）**：eD2k 引擎自己持有 `.part` 文件，字节不经
    `chunkWriter.WriteChunk`，只通过 `ReportProgress` 上报累计字节；而速率计数器
    `speedCounter` 仅由 `WriteChunk`/`onWrite` 喂数，于是 `bytesDone` 每 12s 稳定
    增长、`speed` 却始终为 0。改为 `ReportProgress` 按**增量**喂计数器：首次上报
    只作基线（可能含上次运行的续传字节），进度回退（分片重下）重置基线而不计负值；
    引擎上报过进度后，后续 `WriteChunk` 不再重复计速（那是本地回读，不是网络流量）。
  - **任务速度恒 0（原因二，采样粒度）**：`reportEngineProgress` 采样
    `TotalDone`（已提交字节），而一个 190 KiB 块要几秒到几十秒才提交一次，
    计数器实际每块才前进一次，速度表现为「一秒尖峰 + 长时间 0」。改为采样
    `TotalReceived`（含未满块的在途字节），粒度与 `SendingPart` 分片一致，也就是
    eMule 显示的「已下载」。
  - **源速度恒 0**：入站 socket 字节由 `Connection.DoRead` 在拆帧前整包计入
    `downloadProtocol`，`downloadPayload` 在接收方向恒为 0，而 API 层取的是
    `PayloadDownloadSpeed`。改取 `DownloadSpeed`（协议+负载合计）。
  - 回归测试：`TestEngineProgressFeedsSpeedCounter`（基线/无变化/回退/再下载的增量
    语义）、`TestEnginePathDoesNotDoubleCreditChunkWrites`、
    `TestChunkWritesStillCreditSpeedWithoutEngine`、
    `TestGetPeersInfoIncludesIdleSources`、`TestIdleSourceStatePrecedence`。
  - 实测（6518974464 字节任务，21 个已知源）：`speed` 从恒 0 变为连续
    11–19 KB/s，`done` 每 5 秒前进约 90 KB，源列表同时给出
    `dead 19 / downloading 1` 与各源 `dl`、倒计时。

- **修复下载长期卡在 0%：块内分片被误判为整块 + 会话循环被同步拨号冻结（2026-11-26）**：
  - **分片≠块**：eMule 每个 `SendingPart` 帧最多携带 `MAX_FRAGMENT_SIZE = 10240` 字节，
    而 `ReceivePendingData` 把「本帧收完」当成「块收完」——`recvPos == recvReq.Length`
    后立刻 `CompleteBlock`，此时 `pb.Received=10240 < pb.DataSize=194560`，被判为坏块并
    `Close(IOException)`（日志 `disconnect code=11`），块永远进不了写盘队列。改为：
    明文分片收完但块未满时保留 pending 块并继续收取后续分片；压缩帧（`CompressedPart`）
    仍保持「收完即整块」语义，损坏时照旧断连释放块。同时补越界分片防护（不写坏 piece）。
  - **并发饥饿**：`Transfer.ConnectToPeer` 用**无超时**的 `net.DialTCP` 同步拨号，且整条
    调用链跑在 `Session.SecondTick` 同一 goroutine（`PumpIO` 之后）。一个不可达源即可冻结
    整个会话循环（Windows 下 SYN 重试 ~21s），其间所有活跃 peer 的握手/收包全部停摆，
    对端超时 RST——这正是「37 次 Hello 只换到 1 个 HelloAnswer」的成因。改为
    `ConnectAsync()`：拨号在后台 goroutine 完成，`PumpIO` 每拍 `pumpDial()` 收口并
    `adoptSocket`，拨号期间跳过该连接的 socket IO；`Connection.openSocket()` 不触碰接收端
    状态，结果经 channel close 建立 happens-before。
  - 拨号加下界 `connectionDialTimeoutMS = 15s`（`openSocket` 用 `net.DialTimeout`）：
    异步化之后放宽超时不再有循环代价，避免 5s 把跨洲源误杀成 `i/o timeout`。
  - 回归测试（离线、确定性）：`TestEngineDownloadsControlledFile` 用真实 TCP + 同一套
    eD2k 编解码的受控 uploader 跑完 Hello→FileAnswer→FileStatusAnswer→StartUpload→
    AcceptUpload→RequestParts→SendingPart 全链路，断言 3 个块全部入写盘队列且字节精确；
    对照实验：还原旧行为后同一用例 0/3 块完成（复现线上 `bytesDone=0`）。
    `TestReceivePendingDataAccumulatesFragments`（19 个 10240 分片→1 次写盘）、
    `TestCompressedPartCompletesBlockAcrossReads`（25KB 压缩帧跨 16KB 读重组）、
    `TestConnectAsyncAdoptsSocketOnPump`、`TestConnectAsyncFailureClosesOnPump`、
    `TestDialTimeoutIsBounded`。

- **新端口下 Web 控制台显示 eMule 风格 HighID / KAD 可达绿色标记（2026-11-26）**：
  - Web server/KAD 页原来展示的是搜索引擎专用实例（`port+1`）的状态，而真正占用
    用户配置 TCP/UDP 端口的是每个下载任务拉起的下载引擎。新增
    `goed2k.ActiveNetworkStatus()` 聚合所有已注册活动下载引擎的 server/KAD 快照，
    `Servers()` / `Kad()` 优先消费该真值源，避免搜索引擎误报 LowID / firewalled。
  - 修复 server 行状态富化的值拷贝 bug：`byAddress` 原保存指向循环局部变量 `row`
    的指针，`append` 又按值拷贝，导致写回的是脱离 slice 的副本，API 中 server 行
    恒为 `connected:false / clientId:0`。改用切片索引定位（`enrichServerRows`）后，
    已连接服务器正确返回 `clientId=71042416 (0x043C0570)`。
  - 修正 KAD firewalled 判定：`net.ListenUDP` 绑定 `0.0.0.0` 在 NAT 下是常态，
    原逻辑把外部节点回报的公网 IP 与通配 bind IP 比较，必然判为受限。改为
    `firewalledFromExternalIPsLocked()`：通配 bind 时两个非零点一致即视为可达。
  - 前端：`ServersView` 连接行按 `clientId >= 0x01000000` 显示绿色 HighID / 橙色
    LowID 徽标；`KadView` 防火墙状态在可达时显示绿色「可达（开放）」。
  - 端到端验证（配置 TCP 54662 / UDP 54672）：server 行 `connected:true,
    clientId:71042416`，KAD `bootstrapped:true, firewalled:false,
    listenPort:54672, verifiedNodes:142`。回归测试：
    `TestActiveNetworkSnapshotUsesRegisteredEngine`、`TestEnrichServerRowsWritesBackClientID`、
    `TestEnrichServerRowsAddsRuntimeOnly`、`TestFirewalledFromExternalIPs*`。

- **修复 peer socket IO 超时单位错误，恢复 HelloAnswer 接收（2026-11-25）**：
  - `connectionIODeadlineNS` 原为 `5*1000*1000`（5 **毫秒**），却直接作为
    `time.Duration`（纳秒）传给 `net.Conn.Set{Read,Write}Deadline`。改为
    `5*1000*1000*1000`（5 秒）后，daemon 日志重新出现
    `peer 81.57.136.116:4664 <- HelloAnswer` 与 `-> FileRequest`。
  - 随后发现 5 秒读期限会让串行 `PumpIO` 被任一空闲 socket 阻塞，进而延误
    server 回连的 4662 握手。最终拆分为：写期限保留 5 秒
    (`connectionIODeadlineNS`)，单次读轮询改为 50ms
    (`connectionPollDeadlineNS`)；超时只返回 pump，数据在下个 tick 读取，
    不会丢失 200~400ms 后到达的 HelloAnswer。
  - 回归：`TestConnectionIODeadlineIsSeconds`、`TestConnectionPollDeadlineIsShort`。

- **修复入站 4662 探测，NAT 后成功取得 HighID（2026-11-25）**：
  - 问题：eD2k server 登录时会另开 TCP 回连客户端 4662，模拟 client-to-client
    握手来判断是否可入站。原 `WrapIncoming()` 第一步阻塞读取 marker，遇到
    server 回连的明文 `OP_HELLO`（实测前 5 字节 `E3 3B 00 00 00`）时虽会回退
    明文，但 `ReadFramesWithCombiner` 会丢掉不足 6 字节的半包头，Hello 最终
    解析失败；server 报 `No answer from your 4662 port` 并下发 LowID。
  - 修复：入站连接先做 750ms 短探测，区分静默探测、明文 peer、真实混淆 peer；
    静默/半前缀探测视为 server 回调并主动发送合法 `client.Hello`。
    `incomingPrefixConn` 回放已读字节；`ReadFramesWithCombiner` 保留不完整
    包头到下一次读取。写路径继续使用 5 秒期限，读路径用 50ms 轮询避免阻塞
    server 回调响应。
  - 回归：`TestIncomingSilentProbeGetsHello`、`TestIncomingPrefixKeepsPartialHeader`
    及原有明文/混淆入站测试。
  - 实测：26889 使用修复后的二进制 + 下载引擎占用 4662/4672 后，日志出现
    `incoming peer probe prefix=E33B000000`、`peer 176.123.5.89:4662 <- Hello`，
    server 不再报 4662 无响应，`IdChange clientID=71042416 (0x43C0570)`；
    该值大于 `0x01000000`，确认 HighID。

- **搜索结果渐进显示（2026-11-25）**：
  - 问题：KAD 关键字搜索期间，条目虽逐批到达 `kadTraversal.accum`，但 listener 只在
    `done()` 触发，REST 快照在搜索结束前始终为 0 条，用户体感是“结束后一次性刷出”。
  - 修复：遍历层新增 `progress func([]kadproto.SearchEntry)` 回调，每收到一批
    `SEARCH_RES` 立即交给 Session 合并；`done()` 中的最终 listener 保留，仍负责标记
    关键字完成（`dhtBusy` / `finishOnce` 语义不变）。`DHTTracker.SearchKeywordsWithProgress`
    为带进度版本，旧 `SearchKeywords` 保持兼容包装。`Session.startDHTSearch` 的 `merge`
    按 hash 去重，重复批次无害。前端沿用现有 1 秒轮询，无需改动。
  - 实测：`t=18s state=RUNNING results=514 dhtBusy=True`、
    `t=20s results=576`、`t=44s state=FINISHED results=576`，进度期间结果持续增长。

- **压缩块解压失败不再静默写入（2026-11-25）**：
  - 问题：`CompleteBlock()` 在 `recvReqCompressed=true` 时对 zlib 解压错误静默忽略，
    把压缩字节当成明文写入 piece manager，且该块不会重下，造成文件损坏 + 该 piece 卡死。
  - 修复：解压失败、读取失败、解压后长度与块容量不符均返回 false；`ReceivePendingData()`
    遇到“完整收到但块不可接受”时断开连接，释放已分配块给其他源重下。
  - 回归测试：`internal/ed2k/compressed_block_test.go` 覆盖有效解压、损坏数据、
    长度不符、未完成块与明文块四路径。
  - 对照诊断：关闭 obfuscation 的对照实例（26889/26897）同样 0 字节，且此前能握手成功的
    瞬时源在明文模式下立即断连（code=10），确认**混淆层必须保持开启**；此前 2.1MB 的下载
    来自临时高端口源（20:21:49 被对端强制关闭）而非 4662 上的 KAD 发布端点，停滞主因是
    可用源流失失，不是引擎握手/写块缺陷。

- **扩大 KAD 源发现并快速隔离失活源（2026-11-25）**：
  - `searchSources` 初始联系人从 50 增至 100，direct `search-sources` 目标从 12 增至 32
    （仍按距离最近优先且有上限）。实测同一 hash 的 `search-sources` 响应从 5 增至 16、
    返回条目从 20 增至 40~43；`find-sources` 覆盖 100 个联系人（响应 58）。
  - 新增失活源隔离：peer 建连后 60s 内未收到任何 Hello/HelloAnswer、且以
    `EndOfStream`/`IOException` 断开的端点视为失效发布，按次升级隔离
    （2 分钟、4 分钟、6 分钟……）；隔离期间 `Policy.FindConnectCandidate` 不再选它，
    真实握手成功会清零 strikes 并解除隔离。避免同一批死源每 20~40s 反复占用连接槽。
  - 回归测试：`TestSilentDisconnectQuarantinesDeadSource`、
    `TestOtherDisconnectDoesNotQuarantine`、`TestPolicySkipsQuarantinedPeer`。
  - 实测：26889 新二进制日志出现 `kad source search nodes nodes=81`、
    `kad traversal done kind=search-sources responses=16 results=32 entries=43`，
    以及 `peer 88.6.3.93:4662 quarantined as dead source strikes=1 until=120000ms`、`peer 109.129.34.191:4262 ...`，
    随后同一批 peer 的重连次数明显下降。

- **eD2k 握手诊断日志（2026-11-25）**：
  - `PeerConnection.OnConnect()` 在 debug 级别打印完整 OP_HELLO 帧（hex），
    `ProcessIncoming()` 打印帧读取失败原因，配合 `ed2k engine progress` 采样日志，
    可区分“引擎未收到字节”“peer 不接受 Hello”“源本身已失活”三种 0 字节场景。
  - 诊断结论：关闭 obfuscation 对照实例（26897）仍出现同一批 peer 的
    `disconnect code=10/11`；逐字节比对 eMule 0.70b `SendHelloTypePacket` /
    `ProcessHelloTypePacket` 后确认我方 83 字节 OP_HELLO 的头部（E3 + size + opcode）、
    hash 长度、端点、7 个 tag 的 ID/类型/长度均一致，末尾 6 字节空 ServerPoint
    仅被 eMule 记为附加数据、不会导致断连。用独立 Python socket 发送同一帧复现：
    peer 在 0.3~0.4s 内 `ConnectionReset/Aborted`；**不发送任何数据、仅建立 TCP
    连接时同样立刻被关闭**。因此这批 KAD 源端点本身已失活，不是混淆层或 Hello
    编码问题。eMule Sunrise 服务器返回 lowid 警告，其余 server.met 服务器不可达，
    当前网络下可用源极少。
  - 端口回退核查：`Session.ListenPort/UDPPort` 在绑定失败后会更新为实际端口，
    Hello/login 均使用更新后的值，回退本身不影响握手。

- **eD2k 任务实时进度上报（2026-11-25）**：
  - 问题：eD2k 引擎把字节写进自己的 piece manager，`Goed2kDownloader.Open()` 要等整份文件
    下载完才返回 reader，因此 Web 任务在下载全过程里始终 `Running / bytesDone=0`，
    用户无法区分“引擎正在下载”和“引擎卡住”。这是进度模型限制，不是下载失败。
  - 修复（不改变 `bytes_done` 的单一真相源）：
    - `core.ProgressSink`（可选 `ChunkWriter` 能力，`ReportProgress(done int64)`）。
    - `Transport.Run()` 检测 writer 是否实现 `ProgressSink`，并优先调用可选的
      `progressDownloader.OpenWithProgress(ctx, r, report)`；普通 writer 仍走 `Open`，
      行为不变。
    - `Goed2kDownloader.OpenWithProgress()` 通过 `transferHandle` 的可选 `GetStatus()`
      取 `TotalDone`，在 `Wait()` 期间以 1 秒 ticker 上报。
    - `chunkWriter.ReportProgress()` 夹取 `[0, 单文件大小]` 后写入调度器内存叠加层，
      并以 500ms 节流发布 `EventTaskProgress`；SQLite `bytes_done` 仍只在
      `WriteChunk` → `WriteChunkDoneBatch` 时增长。
    - `Scheduler.liveProgress` 内存表 + `setLiveProgress/clearLiveProgress/applyLiveProgress`；
      `Get()`/`List()` 用 `max(DB bytesDone, live)` 覆盖返回值，并在 `bytesTotal` 处封顶，
      已提交 chunk 数更大时不被回退；ETA 改用叠加后的字段。
    - 多文件任务：`progressBase`（前面文件已完成的字节）+ `taskTotal`（整任务总量）
      保证第二个文件的 live 进度从正确偏移继续，文件成功后累加 `baseDone`。
  - 影响面：仅 REST/事件层可见的进度显示；断点续传、校验与任务完成判定仍以已提交
    chunk 为准，不引入任何“未校验字节算完成”的风险。
  - 回归测试：`TestGoed2kOpenWithProgressReportsEngineBytes`、
    `TestRunForwardsEngineProgressWhenWriterSupportsIt`、
    `TestChunkWriterReportProgressIsLiveOnly`、
    `TestChunkWriterReportProgressEmitsThrottledEvent`、
    `TestApplyLiveProgressOverlay`。

- **KAD 源文件 IP 字节序修复（2026-11-25）**：
  - 定位根因：`SearchEntry.SourceEndpoint()` 把 KAD `TAG_SOURCEIP`（eMule 按网络字节序写入的
    uint32）当成主机序端点值，导致真实源 `37.119.55.104` 被解析成镜像地址
    `104.55.119.37`，`82.105.36.30` 被解析成 `30.36.105.82`，日志里大量出现
    `233.x / 249.x / 244.x / 242.x` 这类非法首字节。该错误在 obfuscation 开启和关闭时都存在。
  - 修复：`SourceEndpoint()` 先反转 TAG_SOURCEIP 的 4 个字节再构造 `protocol.Endpoint`；
    同步修正本地发布源（`DHTTracker.PublishSource`）与 `cmd/uded2kkadpeer` 的写入方向，
    使本地 KAD 节点也按 eMule 的网络序发布 source IP。新增
    `TestSearchEntrySourceEndpointUsesNetworkOrderIP` 锁定该字节序。
  - 实测验证：新二进制 `gobitmule-tags.exe`，debug 实例 `127.0.0.1:26896`：
    `TAG_SOURCEIP=0x25773768` 解析为 `37.119.55.104:4662`，
    `TAG_SOURCEIP=0xca3bce81` 解析为 `202.59.206.129:4662`；源回调
    `results=123 parsed=123 added=123`。随后 peer `82.105.36.30:39703` 完成
    `HelloAnswer -> FileRequest -> FileAnswer -> FileStatusAnswer -> HashSetAnswer -> StartUpload -> AcceptUpload`，
    持续收到 `SendingPart32`，磁盘文件开始增长。另一 peer `176.189.196.128:51296`
    收到 `QueueRanking` 后保持连接，验证上一轮排队修复生效。
  - Web 任务的 `bytesDone` 仍为 0，属于已知的“引擎下载完成后再复制”进度模型限制，
    不代表引擎未下载；本次修复解决的是 KAD 源端点错误导致连错 IP 的问题。
  - `go test ./internal/ed2k/... -count=1` 全部通过；`./...` 在后续包超时前已通过
    `cmd/...` 与 `internal/api`。

- **22 源文件“无法下载”复查与源轮询风暴修复（2026-11-25）**：
  - 用同一 hash `39F8BAB86C16143B3152807FBCA89B5E`（搜索结果显示 22 个源）在 debug 实例
    `127.0.0.1:26893` 复现：二进制构建于 `22:48:31`，已包含 KAD 源搜索修复。
  - 日志证实下载链路实际打通：服务器 `FoundFileSources` 返回 6/5 个源，5 个 peer 完成
    `HelloAnswer -> FileRequest -> FileAnswer -> FileStatusAnswer -> HashSetAnswer -> StartUpload`；
    `82.105.36.30:39703` 与 `207.188.161.95:15000` 收到 `AcceptUpload`，随后持续收到
    `SendingPart32`，`sources` 回调 `results=71 parsed=71 added=71`。因此不是“有源但握手失败”。
  - 真正的 UI 假象：`Goed2kDownloader.Open()` 要等整份文件完成后才把 reader 交给调度器，
    `Task.BytesDone` 由 `ChunkWriter` 在复制阶段累加；下载期间任务会长期显示 `Running / 0 bytes`。
    这是进度上报模型限制，不是下载未进行。
  - 同时发现并修复两处会让可用源被浪费的缺陷：
    - `Transfer.SecondTick()` 的“无源时加速”会把一个已经安排到 30s/60s 后的请求截止时间
      反复拉回当前时间，10 秒内发起 55 次 KAD 源搜索，淹没真实遍历响应。现移除该错误加速逻辑，
      只由 `nextServerSourcesInterval` / `nextDHTSourcesInterval` 安排的截止时间触发请求；
      新增 `TestSecondTickKeepsFutureSourceDeadlines`。
    - `ProcessIncoming()` 收到 `OP_QUEUERANKING` 后直接 `Close(QueueRanking)`。eMule 的
      QueueRanking 表示“已进入上传队列、请继续等待”，后续还会发 `AcceptUpload`；当前实现
      会把排队中的源全部丢弃。现改为记录远端排队名次并保持连接；新增
      `TestQueueRankingKeepsPeerConnected`。
  - 复查后，上一轮“0 字节属于文件/源质量问题”的结论仅对 hash
    `7221BFD00304905F4C282A1FBB5E9586` 的 aMule 对照成立，不能泛化到 22 源文件。

- **eD2k 下载任务失败与启动缓慢（2026-11-24）**：
  - 修复 `ConnectServers` 遇到 server.met 中任意一台不可达服务器就整体返回错误的问题：
    第 37 台 `45.82.80.155:5687` 超时会让前 36 台的成功连接全部作废，下载适配器把该错误
    当成致命错误直接判任务 Failed（搜索适配器则降级为警告）。现改为按序尝试、首台连上即返回，
    全部失败才报错。
  - 首台成功即返回同时避免串行等待全部 40 台服务器的 TCP 超时——下载从 50+ 秒才能进入
    源发现缩短到数秒。
  - TCP/UDP 固定端口被同一 daemon 内的搜索与多个下载引擎争用时回退到系统分配端口，
    避免 `listen :4662: bind: Only one usage...` 导致后续任务失败。
  - 下载引擎日志接入 daemon logger（`Goed2kConfig.Logger`），源发现/KAD 失败可见于日志。
  - 新增 `TestServerConnectFailure` 覆盖全部失败与空候选两条分支。
  - 已知遗留：服务器 `FoundFileSources` 多为 0；KAD `search-sources` 遍历未完成回调，
    详见下条修复记录。

- **KAD 源搜索不完成（2026-11-24）**：
  - 对照 eMule 0.70b `CSearchManager::JumpStart` / `CSearch::ProcessResultFile` 定位三处差异：
    - `processSearchRes()` 收到 `SEARCH_RES` 后只累加 `processedResponses`，从不调用
      `observer.done()`，必须等满 12 秒 RPC 超时才推进；eMule 是按自身答案数/生命周期结束搜索。
      现改为收到每个 `SEARCH_RES` 就实时累积到 traversal 并立即释放 observer（兼容同节点分包）。
    - `find-sources` 返回的 100 个候选节点被全部用于 `search-sources`，无响应节点各等 12 秒，
      整轮耗时数分钟；按 eMule 语义只向距离目标最近的 12 个联系人发起 `SEARCH_SOURCE_REQ`。
    - `SearchEntry.SourceEndpoint()` 只接受 sourceType 0/1/4，而 eMule 接受 1/3/4/5/6，
      现代节点的类型 3/5/6 源会被整条丢弃；已对齐 eMule 并保留 type 0 兼容旧节点。
  - 新增源条目解析/入队统计日志（`parsed` / `added`）与跳过原因采样，便于定位 firewalled 源。
  - 新增回归测试 `TestSearchResReleasesSourceTraversal`、
    `TestSearchEntrySourceEndpointAcceptsEMuleSourceTypes`。
  - 实测同一文件：`search-sources` 遍历可正常完成，源条目回调分别返回 31 与 25 条，
    `parsed=31/25`、`added=31/25`（修复前回调从未触发）。
  - 遗留：源已进入 transfer 且开始发起连接尝试，但当前文件的 peer 握手仍未成功，
    `bytesDone` 仍为 0；这属于 eD2k peer 连接层问题，与 KAD 源发现无关。

- **aMule 真机对照：下载 0 字节为文件/源质量问题（2026-11-24）**：
  - 在实验室 `192.168.1.221`（Ubuntu，aMule 2.3.3 `amuled`，端口 4662/4672，
    KAD 已连接且 firewalled）用同一 ed2k link 下载 hash
    `7221BFD00304905F4C282A1FBB5E9586` 的文件做对照。
  - 结果：aMule 同样只找到 3–5 个源，长期停在 `Waiting / 0 bytes`，
    `Temp/001.part` 始终为 0 字节——与我们修复后的 GoBitMule 表现一致。
    结论：该文件在当前网络下双方客户端都下不动，0 字节不是本项目的协议实现缺陷。
  - 用 `cmd/uded2khelloprobe` 直连 `192.168.1.209:31969` 的真实 eMule：
    Hello/HelloAnswer 握手 PASS；收到 HelloAnswer 后立即发 FileRequest 后连接
    保持打开（读到 3 秒超时，无 RST），证明「握手顺序错误导致对端断开」的假设不成立，
    不改动 `HandleHelloAnswer` 的发送时机。
  - `uded2khelloprobe` 新增可选 `-file-hash`：在 HelloAnswer 后发 FileRequest 并观察
    后续包/断开原因，作为握手时序诊断工具。


- **KAD2 公网搜索链路打通（2026-11-24）**：
  - 补齐 eMule/aMule tag 的 named-tag 编码（`0x80` 清零时为 uint16 长度 + 名字；
    单字符名字归一为数字 ID），修复真实节点 HELLO_RES 报
    `kad tag without id is unsupported` 导致无法确认节点的问题。
  - 修正 `nodes.dat` 联系人 IP 的字节序转换：端点 uint32 按大端字节输出 IPv4，
    避免把 `79.117.19.23` 误解析为 `23.19.117.79` 等无效地址；
    修正后 209 引导可将 `verifiedNodes` 从 0 提升到 50+。
  - KAD2 RC4 加密头改用 eMule/aMule 实际的 LittleEndian magic/verify-key 布局，
    并修正 receiver-key fallback 的 6 字节 keyData，使公网节点能解密我方请求。
  - 支持 packed KAD 数据报（头字节 `0xE5`，opcode 明文、payload zlib 压缩），
    此前几 KB 的 `SEARCH_RES` 全部因 `unsupported kad protocol header` 被丢弃。
  - tag 解析扩展到 `HASH16/FLOAT32/BOOL/BOOLARRAY/BLOB/BSOB` 及 STR17–STR22，
    避免真实搜索结果因未知 tag 类型整包解码失败。
  - 209 真机的 `server.met` 与 `nodes.dat` 入库到 `data/ed2k/`，配置示例注明可直接引用；
    `nodes.dat` 与既有取证副本 SHA-256 一致。
  - 实测 `scope=kad` 搜索 `windows` 返回 998 条；`scope=all` 返回 6834 条
    （server 4219 / kad 1661 / both 954）。


### Changed
- **项目正式更名为 GoBitMule（2026-11-24）**：
  - 项目显示名由 `Universal Downloader（通用下载器 / UD）` 更新为 **GoBitMule**；
    GitHub 仓库名与 Go module 路径统一为 `github.com/asdfgh129/gobitmule`。
  - 全量源码 import、Makefile LDFLAGS、`tools/check_arch.py` / `tools/gates.py`、
    文档链接、README / docs 门户标题、Web 控制台标题和示例配置注释均已同步。
  - 为保持兼容，保留 `udd` / `udctl` / `udperf` 等既有命令名、`UD_*` 环境变量、
    `ud.interop/v1` schema 与 `-UD0000-` BT PeerID 前缀；这些是兼容标识，不再作为项目显示名。
  - 历史证据目录 `docs/evidence/` 中的旧名称与旧 module 路径按取证时点原样保留，
    不追溯改写，以免破坏已有 SHA-256 证据链。


### Added
- **对照 eMule 补齐 16 项 eD2k 客户端能力（2026-11-27）**：
  - 需求：以本机 eMule `srchybrid` 为基线逐项复核「eMule 已实现、本项目缺失」的功能，
    确认 12 项缺口 + 4 项未接线后逐项落地；每项都有源码依据与单测。
  - **协议/传输**：`OP_COMPRESSEDPART` 出站压缩（`SendBlockData` 整体 zlib，压缩
    变大时回退明文）；server UDP socket（`ListenPort+4`，GlobalSearch/GetSources
    编解码与 server-key RC4 混淆，`protocol/server/udp.go` + `server_udp.go`）；
    LowID callback 双向闭环（低 ID 源触发 `OP_CALLBACKREQUEST`，收到
    `CallbackRequestIncoming` 后把端点交给需要源的 transfer）。
  - **下载健壮性**：A4AF（对端报 `NoFileStatus` 时不直接断开，先把端点转给其它
    需要源的 transfer）；AICH（`OP_AICHREQUEST/ANSWER` 编解码、链接 `h=` base32
    解析、块校验失败时按 part 请求恢复数据）；SecureIdent（
    `PublicKey/Signature/SecIdentState` 编解码 + RSA challenge 校验，验证后写入
    信用记录）。
  - **KAD / 社交**：KAD buddy 包由空壳改为携带 TCP 端口并真正应答
    `FindBuddyReq/Res`、`CallbackReq`；好友列表（JSON 持久化）+ `OP_MESSAGE`
    客户端聊天，只有好友的私聊会被接收。
  - **内容周边**：文件预览（`Transfer.PreviewReady` / `CreatePreview` 把从头连续
    完成的 part 物化成可播放前缀）；`.emulecollection` 解析（文本与 eMule
    二进制 v1/v2 两种格式）；P2P IP 过滤器（`ipfilter.dat` 区间/等级/CIDR，
    入站与出站拨号同时生效）；搜索垃圾评分（伪装扩展名、异常体积、关键词、
    关键词堆砌，可按评分过滤）；KAD 源自动发布（可分享即发布，拿到 HighID 后补发）；
    eD2k UPnP 配置入口；server `Status/Message` 进入快照与 API；分时限速
    （`global.bandwidthSchedule`，支持跨夜与 weekday/weekend，Limiter 每 30s
    重新评估，仍是全进程唯一速率权威）。
  - 可测的决策逻辑都抽成纯函数（`publishableSourceEndpoint`、
    `EffectiveBandwidth`、`SpamFilter.Rate`），网络相关路径用本地 socket 或
    帧注入验证，未依赖公网。
- **源列表显示对方持有分块（eMule「已完成」列）与客户端名称/版本（2026-11-26）**：
  - 需求：对照 eMule 客户端列表——每行要能看出对方**具体拥有哪些块**，以及
    客户端名称（如 `eMule v0.70a`），而不只是一个百分比和「未知客户端」。
  - 位图通道：`protocol.BitField` 新增 `HexString()`，把打包位图按 MSB-first 导出为
    hex。6.5 GB 文件约 6700 块，布尔数组每次轮询要几十 KB，打包后不到 1 KB。
    `core.TaskSource` 增 `pieceMap` / `pieces`，由 `ed2kSearchAdapter.TaskSources()`
    从 `PeerInfo.RemotePieces` 透出。
  - 前端：新增 `web/src/components/PieceMap.vue` + `web/src/utils/pieceMap.ts`。
    解码与分箱放在纯函数里（6700 块不可能变成 6700 个 DOM 节点，按 96 个箱合并，
    每箱用覆盖率决定透明度），`PieceMap` 只负责画 SVG。空位图（= 对端已完整持有）
    画成整条实心，而不是空条。
  - 客户端标识：根因是 `remotePeerInfo` 只被初始化、**从未赋值**，所以永远是
    「未知客户端」。实测三个 tag 的语义与直觉不同——
    `CT_NAME`(0x01) 装的是项目主页（`http://emule-project.net`、
    `https://amule-org.github.io`）或 mod 品牌（`[GrupoTS]jmiguelfer`）；
    `CT_VERSION`(0x11) 是 eDonkey **协议**版本（所有 eMule 系都是 60），无法区分构建；
    真正的客户端版本打包在 `CT_EMULE_VERSION`(0xFB)，mod 名称另有字符串 tag
    `mod` / `CT_MOD_NAME`(0x55)。
  - `applyClientTags()` 因此按「mod 名称 > CT_NAME 映射家族 > 原样显示」取名字，
    版本按 eMule 自己的位域布局（17/10/7 位，字母位 = 'a'..'z'）解码，
    解不出合理值时留空而不再回退成 `0x11880` 这种原始整数。
  - 实测：`eMule v0.70a`、`eMule v0.60c`、`aMule v3.1`、`eserver v0.47b`
    （eServer 正是基于 eMule 0.47 的分支，反向印证解码正确）、
    `[GrupoTS]jmiguelfer v3.1`。
  - 回归测试：`TestEmuleVersionString`、`TestClientFamily`、
    `TestApplyClientTagsFromLiveHello`、`TestApplyClientTagsPrefersModTag`、
    `TestTruncateRunes`、`TestBitFieldHexString*`。
  - 注：`CT_EMULE_VERSION` 对某些 mod 是其自己的编号（如 `v2.3a`），前端按原值展示，
    不做"看起来像 eMule 官方版本"的二次猜测。

- **从已连接节点更新服务器/KAD 节点、任务源列表（2026-11-25）**：
  - 参考 eMule 0.70b：`CUpDownClient::ProcessHelloTypePacket()` 读取 HelloAnswer 尾部的
    ServerPoint；`CT_EMULE_UDPPORTS`(0xF9) 高 16 位是 Kad UDP 端口；
    `CKademlia::Bootstrap()` 用这个端点扩充路由表。
  - 引擎侧新增 `internal/ed2k/discovered.go`（30 分钟 TTL 地址注册表，过滤本地/私网/
    广播地址）和 `client_registry.go`（活跃下载客户端注册表）。`PrepareHelloAnswer()`
    回传当前已握手的服务器与 UDP 端口；`HandleHelloAnswer()` 记录对端宣告的 server point
    和 Kad UDP 端点，并立即调用 `BootstrapKadFromPeer()`。
  - REST 新增 `POST /api/v1/ed2k/servers/update-from-peers`（走既有 AddServer 路径写回
    server.met）和 `POST /api/v1/ed2k/kad/bootstrap-from-peers`（加入 KAD 引导与探测）。
    Web 服务器页 / KAD 页各新增一个显式按钮：eMule 是勾选项自动执行，这里保留用户
    确认，避免后台无条件改写节点文件。
  - eMule 风格任务源列表：`TaskDetail.sources` 输出每个已连接源的端点、UserHash、
    客户端与版本、来源标签、状态（handshaking/requesting/queued/downloading/
    disconnecting/connected）、本地与远端队列排名、远端进度、上下行速度、失败次数和文件名。
    由 API 层从活跃 eD2k 引擎按 transfer hash 聚合，best-effort 富化，不影响非 eD2k 任务。
  - Web 任务详情新增「源列表」Tab，打开期间每 3 秒刷新状态、速度与队列位置。
  - 回归测试：`TestUsableDiscoveredAddress`、`TestRecordDiscoveredServerDeduplicates`、
    `TestDiscoveredAddressesExpire`、`TestEd2kHashFromInput`、`TestEnrichSourcesFromTaskDetail`，
    并在 `TestED2KNetworkRoutes` 中断言两条新路由。

- **搜索表达式与可组合筛选（2026-11-24）**：
  - 多个关键词默认按 `AND` 处理，只有显式输入 `OR` 才切换为或关系。
    eD2k 服务器搜索按 token 写入 bool operator（`AND=0x00` / `OR=0x01`），
    且 `OR` 只作用于紧随其后的词；KAD 对每个 OR 组挑一个代表词分别发起
    lookup，再用完整表达式在本地二次过滤。
  - 组合筛选放在**搜索结果区**（可折叠），仅对本次已返回的结果做本地过滤，
    不重新发起搜索：关键字 chips（全部满足）、结果来源、文件类型、扩展名、
    大小区间、来源数/完整源数；文件类型自动展开为常用扩展名，筛选状态持久化。
    搜索发起表单保持原有简洁布局（范围/类型/大小/来源数 + 高级选项）。
  - 关键字只匹配文件名、扩展名与文件类型，不再匹配文件哈希，避免 `11`
    这类短词在 32 位哈希里偶然命中造成假阳性。
  - 实测 `windows 11` 返回 523 条且 0 条违背 AND；`windows OR ubuntu` 的
    结果筛选可再按 Video 类型收敛（156 条，全部为 Video）。


- **Web 控制台：eMule 设置、服务器列表与 KAD 节点页（2026-11-24）**：
  - 修正侧边栏品牌文字：由 `Universal Downloader` 改为 **GoBitMule**。
  - 设置页新增独立「eMule 设置」Tab，支持 `enabled` / `tcpPort` / `udpPort` /
    `nodesDat` / `serverMet` / `outputDir` / `secureIdent` / `obfuscation`；
    `PATCH /config` 的这批键会持久化到 `config.yaml` 的 `ed2k:` 段（保留注释与其他配置）。
  - 新增 `/#/servers` 服务器列表页：读取 server.met、显示连接状态、添加/删除服务器、
    连接与断开；新增 `/#/kad` KAD 节点页：路由表统计、节点明细与引导节点添加。
  - 新增 L0 REST `GET/POST/DELETE /api/v1/ed2k/servers`、
    `POST /api/v1/ed2k/servers/{connect,disconnect}`、`GET /api/v1/ed2k/kad`、
    `POST /api/v1/ed2k/kad/nodes`；未启用引擎时返回 `available=false` 或 503。
  - eD2k 搜索引擎在设置变更后按需重启，配置即时生效；下载通道仍需重启守护进程。

- **Web 控制台：eMule 搜索页与启动自动打开浏览器（2026-11-24）**：
  - Vue 3 + TS 新增 `/#/search` 搜索页：关键词输入、范围/类型/大小/来源筛选、
    结果表（文件名/大小/来源/完整源/类型/哈希）、结果内筛选与排序、
    eD2K 链接复制与一键加入下载队列；界面参照 eMule 搜索页布局。
  - 新增 L0 REST 接口 `GET/POST/DELETE /api/v1/ed2k/search`，把 forked goed2k
    的 `StartSearch` / `SearchSnapshot` 封装为窄接口；`ed2k.enabled=false` 时
    GET 返回 `available=false`，POST/DELETE 返回 503，不伪造结果。
  - 搜索引擎按首次搜索按需启动并复用，监听端口与下载器错开以避免任务下载冲突。
  - `udd` 启动后自动在默认浏览器打开 Web 控制台搜索页；
    `--open-browser=false` 或 `UD_NO_BROWSER=1` 可关闭，非 loopback 监听自动跳过。
- **WBS 16：ED-01/03/04 收官 —— 六个 ED 门禁全部 PASS（2026-11-23）**：
  - 新增三件取证工具（fork 自身线协议 codec + 真内核 socket，不碰 ud 状态）：
    `cmd/uded2kfakeserver`（受控 eD2k 服务器：LoginRequest→IdChange→GetFileSources→
    FoundFileSources，可配 High/Low clientID、源配比、broker 互发 endpoint）、
    `cmd/uded2kserverprobe`（fork 客户端 IDClass/握手/可见源/学习 hash 探针）、
    `cmd/uded2khelloprobe`（裸 eD2k Hello/HelloAnswer 对接真实第三方 eMule）。
    `PeerInfo` 增加 `UserHash` 字段，使 ED-01「双方可见对方」由引擎 API 可观测。
  - **ED-01 PARTIAL → PASS**：222 真 TCP 连 209 真实 eMule(31969)，从 HelloAnswer
    读到其真实 16B user hash `6FBD361F350E5D431B33FA7B55F46FA3`（3 次一致，
    tcpdump 10 包含 74B Hello + 113B HelloAnswer）；另两独立 fork 进程经 broker
    互发 endpoint、真 TCP 拨号并互学 hash（A←BBBB/B←AAAA，658 包）。连接层双方可见成立。
  - **ED-03 PARTIAL → PASS**：High ID(clientID=0x70000001) + 6 源(3 公网/3 LowID)，
    fork 判 HIGH_ID、握手完成、可见源=3、抓包 13 包。
  - **ED-04 PARTIAL → PASS**：Low ID(clientID=123456) + 同 6 源，fork 判 LOW_ID、
    可见源 6→3（50% 比例下降）、会话保活不崩、抓包 13 包。
  - 门禁：ED-01~06 全 PASS；按 docs/12 P3 可进入发布评审，R-06 降为「已缓解」。
    边界：ED-01 仅连接层未做块传输、eMule 单版本；ED-04 clientID 模拟非真实 NAT。
    docs/25 §12.7、docs/14、README v1.50、_site、SHA256SUMS 同步。
- **WBS 16：ED-02 真机受控搜源 + ED-05 出站自动武装（2026-11-23 再续）**：
  - **ED-02 PARTIAL → PASS**：新增 `cmd/uded2kkadpeer`（fork codec 的受控
    KAD UDP 对端，可生成只含自身的 29B legacy `nodes.dat`，从不调用
    `PublishSource`）。222 上两独立进程经内核 UDP 回环跑完整两阶段搜源：
    `Bootstrapped=true/LiveNodes=1`、`search_completed=true`、
    `source_count=1`（`127.0.0.1:4662`）、find/search-sources 均
    `responses=1/timeouts=0`、`tcpdump` 8 包。证据 `kad-controlled-positive-2026-11-23.json`
    + pcap/log/peer-stats/nodes-loopback.dat。公网 0 源仍为 NAT+过时节点环境限制。
  - **ED-05 PARTIAL → PASS**：打通生产下载路径的出站混淆数据通路——
    `Peer.UserHash`（KAD `SearchEntry.ID` 或首次明文 Hello 学到）→
    `Transfer.AddPeerWithHash` → `Policy.AddPeer` 合并 → `PeerConnection.Connect`
    拨号前 `armTentativeObfuscation` → `Connection.Connect` 完成 A/B 握手；
    未知 hash 或开关关闭保持明文，推断 hash 握手失败在新 socket 回退明文。
    新增 7 项测试（真实 TCP 仅混淆握手+加密往返、无 hash/关开关明文、
    策略合并、Hello 学习供重连、推断失败回退、KAD→Peer hash 传播）。
    已知边界：对端为 fork 自建工具，未与第三方 aMule/eNode 互操作。
  - **ED-03/04 补强**：新增 High ID 握手模拟与 Low ID 会话保活对照；
    `TestSourceVisibilityRatioDropsForLowIDSources` 实测 6 源中 3 个 Low ID
    端点被过滤（可见比例 50%）且功能不崩。真机 High ID 源数量/真实 NAT
    流量未测，维持 PARTIAL。
  - **ED-01 评估**：222 无 aMule/eNode-go 与自建 eD2k 服务器；209 eMule
    配置可写但需注入服务器+重启+共享文件，侵入式改造未执行，维持 PARTIAL。
  - 门禁现状：ED-01/03/04 PARTIAL、ED-02/05/06 PASS；P3 不发布、R-06 维持。
    `docs/25` §12.6、`docs/14` WBS16、README v1.49、_site、SHA256SUMS 同步。
- **WBS 16：ED-02 根因实验 + ED-05 跨进程混淆互操作（2026-11-23 续）**：
  - **ED-02 根因由推断升级为实验证明**：新增
    `internal/ed2k/kad_local_positive_control_test.go` 的
    `TestKADSourceSearchLocalPositiveControl`——用 fork 自己的 PacketCombiner
    构造本地 UDP 对端注入路由表，驱动真实两阶段搜源，**PASS**（find-sources
    与 search-sources 均 `responses=1/timeouts=0`、callback 收到 1 条源且端点
    解码正确）。证明 wire 编码/RPC target 匹配/遍历链接/回调均正确。
  - `dht_tracker.go` 补逐包 debug 日志：`kad udp send`（目的/opcode/字节/err）、
    `kad udp recv`（源/字节/header）、`kad udp recv decode failed`；此前解包失败
    静默 continue 无法区分五种失败模式。222 真机 `tcpdump` + `-debug` 探针：
    **105 出站 UDP（70×FindNode/35×FirewalledReq）、0 入站**，坐实 `responses=0`
    根因为 NAT 阻断入站 + nodes.dat 节点过时，排除协议 bug。ED-02 维持 PARTIAL
    （真机 source_count=0），但协议疑点清零。
  - **ED-05 补仅混淆对端跨进程真 TCP 互操作**：新增 `cmd/uded2kobfspeer`
    （fork 自维护、零依赖，`-listen`=WrapIncoming / `-dial`=WrapOutgoing）。
    222 上两个独立 OS 进程经内核 TCP 回环完成 A/B 握手 + 加密 nonce 往返，双方
    `verdict=PASS`、`obfuscated=true`；`tcpdump` 抓包 nonce 明文泄漏 0 次，证据强
    于同进程 net.Pipe。闭环"未做仅混淆对端真机互操作"缺口；剩余出站自动武装 +
    第三方客户端对端，ED-05 仍 PARTIAL。
  - 证据：`docs/evidence/ed2k-ed-gates-2026-11-22/` 新增
    `kad-rootcause-2026-11-23.json`（+debug.log +capture.pcap）、
    `obfs-cross-process-interop-2026-11-23.json`（+capture.pcap）；docs/25 §12.5、
    docs/14 WBS16 同步。P3 发布结论不变、R-06 维持。

- **WBS 16：ED-05 live-TCP 接线 — 协议层 PARTIAL → 已接入 PARTIAL（2026-11-23 续）**：
  - 新增 `internal/ed2k/protocol/obfuscation/stream.go`：`Stream`（`net.Conn`
    透明 RC4 加解密）、`WrapOutgoing`/`WrapIncoming`（A/B 阻塞握手；入站探
    协议头自动明文回退，已读字节由 `prefixedConn` 回放）、CSPRNG 随机
    randomPart/marker。把 §11 的孤立 codec 变成真实 TCP 字节流能力。
  - Connection 接线（opt-in）：入站 `Session.acceptIncomingConnections` 在
    `Settings.EnableObfuscation` 时调用 `enableIncomingObfuscation`（B 侧仅需
    本地 hash，明文 peer 自动回退）；出站 `Connection.Connect` 在
    `SetObfuscationPeerHash` 武装后调用 `WrapOutgoing`（fork Peer 模型暂不携带
    目标 hash，普通下载流程不自动启用）。配置链路 `config.ED2K.Obfuscation`
    → `Goed2kConfig.Obfuscation` → `Settings.EnableObfuscation`。新增错误码
    `EncryptionError(26)`。
  - `CryptLayerWired()` 由 false 改为 true（表示已接入 live TCP 路径）；探针
    222 真机确认 `crypt_layer_wired=true` 且 KAD bootstrap 不回归。
  - 新增 11 项测试：`stream_test.go`（net.Pipe A/B 真实握手 + 双向加密载荷、
    wire 明文不泄漏、明文回退、篡改拒绝）、`connection_obfuscation_test.go`
    （连接层明文回退/握手升级/出站 opt-in 状态）。obfuscation 包累计 18 项。
  - 排查并排除 KAD UDP checksum 假设：aMule 的 UDP checksum 由底层
    `ClientUDPSocket` 处理、对端兼容 fork 的 `[0xE4,opcode,payload]` 格式；
    ED-02 零响应仍定性为环境层（过时节点 + NAT），未改协议格式。协议常量
    经权威源码再次核对无误。
  - ED-05 仍为 **PARTIAL**（未做仅混淆对端真机互操作、出站未自动武装），
    P3 发布结论不变、R-06 维持。证据 `crypt-wired-222.json`，详见 docs/25 §12。

- **WBS 16：ED-05 混淆协议层落地 — BLOCKED → PARTIAL（2026-11-23）**：
  - 新增 `internal/ed2k/protocol/obfuscation/`（fork 自维护、零第三方依赖）：
    `Cipher`（RC4 KSA+PRGA + `Discard(1024)`）、21 字节 MD5 密钥派生
    `DeriveDirectionKeys`、A/B 握手帧 codec（`InitiatorRequest` /
    `ParseInitiatorRequest` / `ResponderReply` / `ParseResponderReply`）。
  - 算法常量经 aMule `EncryptedStreamSocket.cpp`、eNode `tcpcrypt.js`、
    eMule Wiki 三源交叉验证：`MAGICVALUE_REQUESTER=34`、`SERVER=203`、
    `SYNC=0x835E6FC4`、`ENM_OBFUSCATION=0`、MD5 输入 21 字节、RC4 丢弃
    1024 字节；A 发送帧第 5 字节起加密。未采用 Wiki 中冲突的 SYNC 值。
  - `CryptLayerSupported()` 由 false 改为 true（仅表示协议层已实现）；新增
    `CryptLayerWired() bool`（当前 false，表示未接入 live TCP 连接路径），
    二者分离避免把 ED-05 误判为 PASS。探针新增 `crypt_layer_wired` 字段。
  - 7 项单测全绿：3 组公开 RC4 向量、Discard 等价性、密钥对称性、21 字节
    布局、A↔B 完整握手往返（零网络）、篡改检测、marker 重试。
  - ED-05 由 BLOCKED 推进为 PARTIAL；ED-02 零响应经协议常量核对确认为
    环境层（节点时效 + NAT 双向 UDP），不改协议常量。P3 发布结论不变（R-06）。
    证据与判定见 `docs/25` §11、`docs/evidence/ed2k-ed-gates-2026-11-22/`。
- **WBS 16：ED 门禁补缺 — ED-06 PASS / 搜源收敛 / Low ID 模拟（2026-11-22 续）**：
  - **KAD 搜源不再被持续收包饿死**：`DHTTracker.readLoop` 用同 goroutine 的
    500ms ticker 推进 `node.tick()`；`DHTSearchTimeout` 真正驱动 RPC short/
    expired 超时（此前固定 12s 且配置项被忽略），探针新增 `-dht-timeout`。
    222/209 复验 `search_completed=true`、`RunningTraversals=0`，debug 日志出现
    `find-sources` → `search-sources` 两段收敛；但真机 `responses=0`、
    `source_count=0`，ED-02 仍 PARTIAL，不谎报找到源。
  - **ED-06 从 BLOCKED 推进为 PASS**：fork `Settings.SecureIdent` 与
    `Client.UserHash/SetUserHash/SecureIdent/SetSecureIdent` 公开 API；
    `ClientState` 增 `user_hash`/`secure_ident` 并随状态文件持久化；探针新增
    `-state`/`-user-hash`/`-secure-ident`，失败路径也落盘。222/209 双机各两次
    独立进程启动，UserHash 分别恒为 `2222…2222` / `2092…0920`。新增
    `TestUserHashAndSecureIdentPersistAcrossRestart` 等回归。
  - **ED-04 PARTIAL**：`TestLowIDSimulatedServerHandshake` 本地伪造 eD2k server
    发送低 ID `IdChange`，验证 `LOW_ID` 分类与会话保活；未覆盖真实 NAT 源比例。
  - **ED-05 明确 BLOCKED**：fork 新增 `CryptLayerSupported() bool`（当前 false），
    探针输出 `crypt_layer_supported=false`；对端配置不再被误读为本端能力。
  - 证据：`docs/evidence/ed2k-ed-gates-2026-11-22/`（新增身份双重启、搜源复验
    JSON/log 与 SHA256SUMS）；判定与后续见 `docs/25` §10。P3 发布结论不变（R-06）。
- **WBS 16：真机 ED 门禁执行 — KAD bootstrap 两机 PASS（2026-11-22 续）**：
  - **Go 工具链恢复**：此前「go build 全部卡死」定位为 GOCACHE 路径
    `C:/Users/bemin/sdk/gocache` 异常（非网络，未用代理）；改用
    `AppData/Local/Temp/udcache` 后恢复，`go test ./internal/transport/ed2k/...`
    2.4s 全绿，linux/windows 探针交叉编译成功。
  - **真机取证**（证据 `docs/evidence/ed2k-ed-gates-2026-11-22/`，含 SHA256SUMS）：
    `cmd/uded2kprobe` 部署 222（公网）与 209（High ID），用 209 nodes.dat 引导
    ud fork 引擎接公网 KAD；两机均 `Bootstrapped=true`、`LiveNodes=50`、
    `KnownNodes=200`——**ud fork 引擎首次真机入网**。注入 209 节点后
    KnownNodes 200→201（ED-01 部分证据）。
  - **门禁如实判定**：ED-01/02/03 PARTIAL、ED-04/05 NOT_EXECUTED、
    ED-06 BLOCKED（fork 无 UserHash/SecureIdent 持久化 API）；KAD 搜源可发起
    但两阶段 traversal 在 NAT/Firewalled 下未收敛（source_count=0），不谎报 PASS。
    P3 发布结论不变（R-06）。详见 `docs/25` §9。
  - 探针新增 `-add-node`/`-search-hash`/`-publish-self`/`-debug` 取证开关；
    fork 加 `SetDebugLog` 公开日志钩子。
  - **修复隐藏编译错误**（上一轮 `c81bbe2` 因 GOCACHE 故障未能全量编译而漏检）：
    `daemon.go` 把 `Goed2kDownloader`（reader seam）直接传给 `transports.Register`，
    但它不是 `core.Transport`（缺 Name/Supports/Probe/Run）。改为
    `ed2ktransport.New(downloader)` 包裹后再注册，`go build ./internal/app/` 通过。
- **WBS 16：fork goed2k 落地 + 引擎接入（2026-11-22）**：
  - **fork**（`internal/ed2k/`）：goed2k `v0.0.0-20260602122456-f2a71d599dee`
    核心库自维护，剥离 TUI（零第三方依赖），MIT LICENSE + `FORK.md` 随附，
    import 路径改 ud 内部。`go build ./internal/ed2k/...` 通过。
  - **引擎适配器**（`internal/transport/ed2k/goed2k.go`）：`Goed2kDownloader`
    实现 `Downloader`，从冻结 Resource 还原 ed2k 链接 → 加载 KAD nodes.dat /
    server.met → `Start`/`AddLink`/`Wait` → 读回完成文件；关库内限速与 AutoSave
    （ADR-0002 单一权威），ctx 取消保障；窄接口 seam 注入 fake 零网络单测。
    `go build ./internal/transport/ed2k/...` 通过。
  - **opt-in 注册**：`ED2KConfig` 增 `Enabled/OutputDir/NodesDat/ServerMet`（默认
    关闭），daemon 在 `Enabled=true` 时注册；`cmd/uded2kprobe/` 真机 KAD 探针。
    209 `nodes.dat` 归档；222 实测有公网（纠正「无外网」旧论断）。
  - **门禁如实**：ED-01/02/05/06 仍 `NOT_EXECUTED`、ED-03 `PARTIAL`——本机 Go
    工具链环境级故障（连 hello world 都 `go build` 卡死），无法交叉编译探针与
    跑真机互通/最终全量门禁。恢复条件与执行步骤见 `docs/25` §8.4。
- **WBS 16 推进：209 对端就绪 + `LocalFileDownloader`（2026-11-22）**：
  - **209 KAD 已配置并真实入网**（用户完成配置，`docs/25` §6.2「KAD 未配置」、
    `docs/14` 旧状态被推翻）：真实 `emule.exe` + `Port=31969` TCP（`0.0.0.0`
    LISTENING + 3 外部 ESTABLISHED）/ `UDPPort=41296`、`NetworkKademlia=1`、
    `nodes.dat`+`KadUDPKey`、`SecureIdent=1`/`CryptLayerSupported=1`、
    `known2_64.met` 4.5MB、`clients.met` 279KB 今日刷新、`downloads.txt`
    有真实任务、incoming 72 个完成文件（156MB）。WebServer `Enabled=0`
    （无 4711），不作生产下载通道。**证据**：`docs/evidence/ed2k-209-2026-11-22/`
    （`kad-environment.json` + SHA256SUMS）。
  - **门禁边界**：对端条件 READY，但 ED 门禁验证的是 ud 侧 fork 互操作而非
    eMule 自身——**ED-01/02/05/06 维持 `NOT_EXECUTED`，ED-03 `PARTIAL`**，
    发布结论不变（R-06）。
  - **实现**：`internal/transport/ed2k/filedownloader.go` 落地真实
    `LocalFileDownloader`——从受控完成目录读 eMule 已完成文件，返回 seekable
    reader + 落盘 size，交 `Run` 的 chunk 计划/Budget/ChunkWriter（docs/25 §3
    reader 适配模式最后一环，**不下载、不替代 fork**）。文件名经
    `security.PathGuard.Clean` 防穿越。`filedownloader_test.go` 真实文件
    byte-exact 端到端 + size 守卫 + 路径穿越/未配置/目录拒绝，零网络。
    仍默认不注册 daemon。
- **WBS 16 eD2k Transport 离线骨架 + 209 实验室补探（2026-11-21）**：
  - **实现**：`internal/transport/ed2k/`（`ed2k.go` + `ed2k_test.go`）。按
    `docs/25` spike 的 BT 同构 reader 模式落地：`Downloader` seam（生产实现
    = fork goed2k `AddTransfer`+`Wait`+`ReadRange`，未接入）、`Probe` 纯离线
    （复用 ADR-0013 冻结 Identity，零新增字段）、`Run` 逐 chunk `Seek`+计费
    走唯一 Limiter + 越界/size 不符显式报错。**默认不注册** daemon，无
    Downloader 时 `E_CAPABILITY_UNAVAILABLE` Fail-closed，不改现有 dispatch。
    94% 覆盖并纳入 `gates.py` 80% 门禁集。刻意不 `go get goed2k`（带 bubbletea
    TUI 全家桶，与 ADR-0030 剥离 TUI 相悖）。
  - **209 补探**：用 `bemin` 凭据成功登录——**209 实为 Windows 10**（非 Linux，
    纠正 `docs/25` §4 推断），有 Python 3.11/Tailscale/Wireshark；**发现真实
    `emule.exe` 在运行**（`C:\Program Files (x86)\eMule\`）。但无 4662/4672/
    4711 监听、无 `nodes.dat`/`server.met`，KAD 与入站未配置。
  - **门禁状态**：ED-01/02/05/06 仍 `NOT_EXECUTED`（客户端前置条件部分满足，
    KAD + 真机互通仍缺），不翻转发布结论。`docs/14`/`docs/25` 同步。
  - **顺带修复**：`getGlobalOption` 直接读全局速率字段违反 `check_arch` 守卫 4
    ——速率换算下沉到 `config.GlobalConfig.Aria2GlobalLimits()`，api 不再读
    `MaxDownloadRate`；`check_arch.py` 6 守卫全过（0 violation）。
- **WBS 17 AriaNg 真机验收 + 两处 facade 缺口修复（2026-11-21）**：
  - **真机环境**：隔离实验室双机 `192.168.1.222`（udd :6800 + AriaNg 1.3.15
    静态页 :8080）/ `192.168.1.221`（HTTP 源 :18080）。交叉编译 `CGO_ENABLED=0`
    linux/amd64 udd scp 部署；rpc.enabled + remoteToken + 隔离段放行私网。
    当时 `209` 尚无凭据（注：同日稍后用 `bemin` 凭据登录，确认 209 为 Windows
    且有 eMule 在运行，见上方 WBS 16 条目；ED 门禁仍 NOT_EXECUTED）。
  - **端到端证据**（`docs/evidence/ariang-2026-11-21/`）：capabilities `rpcLevel=L1`；
    鉴权 fail-closed（无 token → `-32001`）；`addUri` 真实跨机下载 1MiB 文件
    → `tellStatus=complete` → 落盘 SHA256 与源一致；`getUris`/`getFiles`/
    `system.multicall`/`listMethods`（35 方法）全通过；AriaNg 握手 7/7 方法 +
    CORS 预检通过；AriaNg 静态页 HTTP 200。
  - **真机暴露并修复两个单测盲区**：① `getGlobalOption` 缺失使 AriaNg 设置页
    报 `-32601`——新增实现并映射真实全局配置（`rateToAria2` 速率换算）+ 单测；
    ② `OPTIONS /jsonrpc` 返回 405 致浏览器 CORS 预检失败——注册预检回 204、
    POST/GET 回显 Origin，鉴权仍强制 token，+ 单测。`internal/api` 覆盖率 80.9%。
  - **WBS 16 spike**：`docs/25-goed2k-fork-api-spike.md` 源码勘察确认低成本
    BT 同构 reader 适配路径（不必重写 `PieceManager`），细化 ADR-0030 改造范围；
    ED-01/02/05/06 因缺 eD2k 客户端 + KAD + 209 凭据仍 NOT_EXECUTED，不翻转发布结论。
- **P3 WBS 16–18 三项 OPEN 裁决（2026-11-21，ADR-0030/0031/0032）**：
  - **决策**：发起人指令启动剩余三个 P3 工作包并逐项裁决——**OPEN-03** = goed2k
    **fork 自维护**（上游 8 stars / 7 commits / 无 tag / bus factor 1，任务型 API
    与 `ChunkWriter` 契约错配、ED 开关缺失）、**OPEN-04** = **纳入 L1 aria2 facade**
    （本次指令构成 ADR-0019「明确需求」，opt-in `rpc.enabled`，零新依赖映射层）、
    **OPEN-06** = **微信插件立项 + 发起人代行法务签批全量实现**（隔离边界沿用
    ADR-0018，R-13 维持高，实名复核待补、非实名会签）。三项 ADR 均代行角色。
  - **文档**：`docs/14` WBS 16–18 与 OPEN-03/04/06 关闭、`docs/adr` v0.16、
    `docs/README` v1.40。真实环境门禁（ED-01/02/05/06、AriaNg、视频号 MITM）
    均 NOT_EXECUTED，不因裁决翻转发布结论。
  - **落地（本批次，可离线验证）**：
    - **WBS 17**：`internal/api/jsonrpc.go` + `jsonrpc_ws.go`——aria2 JSON-RPC 2.0
      facade（25 方法 + 6 通知、`system.multicall`、GID↔ULID 哈希双向映射且跨重启
      稳定、10 态→6 态状态映射、bitfield 透传 chunks、未知选项忽略、saveSession/
      getSessionInfo no-op）；`POST`/`GET /jsonrpc` 均 fail-closed 鉴权（`token:`
      + Bearer 常量时间比较，WS token 查询参数）；`rpc.enabled` 配置默认 false，
      关闭时 404 且 `capabilities.rpcLevel="L0"`；`UD_RPC_ENABLED` 环境变量；
      新增 10 个契约测试（404/方法白名单/addUri-pause-tellStatus/multicall/
      鉴权/状态映射/通知映射等）。
    - **WBS 16**：`internal/resolve/ed2k.go` + `_test.go`——`ed2k://|file|` 离线
      解析（name/size/MD4 32hex/AICH 40hex，含 `h=`/`p=`/`sources/` 变体），
      复用 P0 冻结的 `Identity.ED2KFileHash`/`AICHRoot` 与 DDL 列，零新增字段；
      注册进内置 resolver 链（dash 后、direct 前），`Unsupported` 对 ed2k 改报
      链损坏错误；fork goed2k 的 transport 实接与 ED 门禁待源码 spike + 隔离实验室。
    - **WBS 18**：`internal/wechat/`（独立包，默认不进注册链）——公众号公开
      HTML 文章 resolver 骨架（`IsPublicArticle` 仅放行 `mp.weixin.qq.com`，排除
      视频号/深链）、detector seam（nil fail-closed）、`PrivilegeHelper` IPC 接口
      （装/卸 CA、代理、注入、规则热更均需 `ConsentGate` 显式授权）、`RulesVerifier`
      Ed25519 验签、`ComplianceNote` 红线固化；`docs/24-微信插件合规评审清单.md`
      记录代行签批口径与 6 条待实名审阅的平台条款项。视频号 MITM 真实环境 NOT_EXECUTED。
  - **验证**：`go build ./...` 通过；`go test ./...` 全绿（新增 `internal/wechat`
    包，全 23 包零 FAIL）。

- **P3 三项：Telegram MTProto L1（2026-11-21，ADR-0029）**：
  - **交付**：`internal/transport/tg`——gotd/td v0.166.1（MIT，**+16 链接模块全宽松
    许可**，门禁实测 PASS）；四形态链接解析（t.me / t.me/c 私有 / tg://resolve /
    tg://privatepost）归一化为内部 `tg://ud` 镜像（无 http(s) 传输可声明、分发无歧义）；
    `MediaClient` seam（生产 `gotdMedia`：ResolveUsername → ChannelsGetMessages →
    Document → `downloader.Stream`，`WithPartSize(512KiB)` + `WithAllowCDN(true)` 按
    ADR-0009）；`Stream` 写入 io.Pipe 经 `tputil.NewChanReader` + `SequentialCommit`
    进 chunk 计划（与 hls/dash 同一提交语义，逐字节过 Budget）；FLOOD_WAIT 按
    服务端时长归类 `E_TRANSIENT_NETWORK`；会话经 `POST /credentials` 存钥匙串
    （FR-SRC-06，L1 只读）；私有链接干净拒绝（需账号侧 access hash，P3）。
    `internal/resolve/tg` 分类 + credentialRef 透传；opt-in
    `sources.telegram.{enabled,apiId,apiHash,timeout}`（apiHash 为 APP 凭据，不经
    `GET /config` 回显）。
  - **发现并修复：streamErr 双接收死锁**——Run 的 `return <-streamErr` 与延迟清理各
    接收一次 writer 侧错误，第二次永久阻塞（集成测试超时抓出）；改为提交结束后
    单次接收。另修 tg:// 深链 host 解析与 Match 启发式过宽（parse-as-match）。
  - **验证**：链接解析 10+ 形态与拒绝、归一化往返、seam 假媒体 Probe/Run 经 chunk
    计划逐字节一致 + Budget 计量、FLOOD_WAIT 42s 断言、私有/无会话拒绝、app 链序
    断言（telegram 位次 lux 后、yt-dlp 前）。全量 22 包回归零 FAIL。
  - **边界（NOT_EXECUTED，如实）**：真实 MTProto 互通（2GB 文件 / DC 迁移 / 会话
    失效 / MTProxy——ADR-0009 验证清单）需真实凭据与网络，CI 禁止；生产客户端已
    编译接线、待实验室凭据补测（同 Windows 钥匙串先例）。
  - **文档**：`docs/adr/ADR-0029`、`03` FR-SRC-04 时点注、`05` 链序、`07` v0.16、
    `10` §4 落地注、`11` 会话落地注、`12` §3.3、`14` WBS 15。

- **P3 次项：HLS 多 rendition 分离发射（2026-11-21，ADR-0028）**：
  - **交付**：可选 `core.FilePlanner` 能力接口（`PlanFiles`）——流传输可声明「本资源
    可展开为多个每流文件」，调度器采纳计划（`store.ReplaceResourceFiles` 原子替换
    files 行）后走 ADR-0027 多文件路径；HLS 主清单的 `EXT-X-MEDIA` 音频组按
    `GROUP-ID` 归组、`DEFAULT=YES > AUTOSELECT=YES > 文档序` 选流，**URI 为空的
    muxed rendition 永不拆分**（空 URI 相对解析成自 URL 的实现缺陷实测修复）；
    未声明尺寸由调度器逐文件 Probe 补齐；多文件子 Resource 携带 `res.Headers`
    （分发标记 + 站点头）保证逐文件分派正确。
  - **发现并修复：变量遮蔽产生零长度 chunk**——多文件初始化分支内 `sizes` map
    遮蔽外层尺寸切片，`CreateChunks(0)` 产出零长分片、文件全零但空 digest 短路
    校验通过（集成测试抓出）；修复并留注释。
  - **验证**：解析/计划单测（`hls/plan_test.go`）+ 调度器级集成
    `TestHLSSeparatedAudioRenditionRemuxToMP4`（真实 hls 传输 + 真实 Remux，
    零 ffmpeg，双轨字节断言）；既有单文件 HLS 回归全绿。
  - **边界**：DASH 多 AdaptationSet 维持拒绝（表示级选择不同构，后续包）；
    字幕轨/多视频流/真实站点端到端未实现。
  - **文档**：`docs/adr/ADR-0028`、`03` FR-TRN-08 时点注、`05`、`06` §4.4 追加、
    `07` v0.15、`12` §3.3、`14` WBS 13。

- **P3 首项：多文件下载与容器合并（2026-11-21，ADR-0027）**：
  - **交付**：调度器顺序多文件下载——每 selected 文件独立源 URL（`ResourceFile.URL` +
    **DDL v2** `files.url` 列）、chunk 计划（`chunks.file_idx` 既有列 + `NextForFile`）、
    `.part` 路径（`temp_path:{i}`）、逐文件校验（`VerifyFileChunks`）与逐文件崩溃恢复；
    全部通过后默认 `remux` 合并为最终文件、原子提交、合并输入清理。传输接口零改动
    （调度器对每文件构造单文件子 Resource 调同一 Transport.Run）；限速权威不变
    （一任务一 Budget 租约跨文件复用）。
  - **迁移运行器升级**：v1 每次 Open 重跑全部迁移、仅容忍幂等 SQL——ALTER TABLE
    会在二次打开失败（drill 实测 `duplicate column name`）；v2 以 `schema_migrations`
    表记录已应用迁移，旧库首次打开补记全部文件名。`schema_version` 现为 2，
    备份/迁移/恢复与 drill 断言同步。
  - **yt-dlp 分离 A/V 发射**：`ChooseFormats` 的分离结果在两路均带声明尺寸时发射
    双文件资源（尺寸未知维持 `E_CAPABILITY_UNAVAILABLE` 诚实拒绝）；站点
    `http_headers` 以并集随 Resource 下发。
  - **发现并修复：resolver 站点头双断流**——resolve 阶段 `res.Headers = nil` 与
    `injectHeaders` 全量替换使站点必需头（Referer/UA，yt-dlp http_headers、lux
    Referer）在持久化与运行两处丢失；修正为 SaveResource 经既有
    `StripSensitiveHeaders` 剥离敏感项后持久化非敏感头、`injectHeaders` 改为
    合并（resolver 头 < 任务选项 < 凭据）。
  - **验证（ADR-0010 清单兑现）**：调度器级集成
    `TestMultiFileSeparatedStreamsRemuxToMP4`（真实 HTTP + 真实 Remux，零 ffmpeg）：
    分离双流 → 逐文件下载校验 → 单 MP4 双轨样例字节断言 → 原子提交；
    全量 22 包回归零 FAIL。
  - **供应链修复**：govulncheck 抓到 lux HTML 解析路径引入的可达漏洞
    `golang.org/x/net GO-2026-6860`（v0.60.0 → v0.62.0），vuln 门禁 PASS。
  - **文档**：`docs/adr/ADR-0027`、`03` UC-04 时点注、`05` ResourceFile.URL、
    `06` §4.4 落地注 + DDL v2、`07` v0.14、`12` §3.3 集成兑现、`14` WBS 12 P3 余项更新。

- **WBS 12（收口）：lux 进程内站点解析（2026-11-21，ADR-0026）**：
  - **交付**：`internal/resolve/lux.go`——lux v0.24.1（MIT）**精选提取器子集**
    （B站/抖音/爱奇艺/芒果/西瓜/快手；+12 链接模块全 MIT/BSD，许可证门禁实测 PASS；
    全 extractors 树 160+ 模块含第二 JS 引擎 otto 与 colly 抓取框架，刻意不导入）；
    opt-in `sources.lux.{enabled,timeout}`（默认关，关闭时链路逐字节不变）；
    页面 URL + 提取产出媒体 URL 的 SSRF 双检；站点 Referer 头表（B站 CDN 无 Referer 403）；
    确定性选流（Size 主导 / 并列字典序 / 空 ID 以 map key 兜底）；单 Part 边界
    （多 Part 拒绝并指向 P3 合并管线）。
  - **发现并修复（两处库级/设计缺陷）**：① lux 分发对无匹配站点取 nil extractor 并
    **panic**（可打挂 daemon）——Resolve 域名覆盖预检（子域后缀匹配、形近域拒绝）+
    提取 goroutine 统一 recover 双层防御；② `{"lux": true}` forced 选项经
    `resolve.Registry` 集中路由（与 hls/dash 同机制），未覆盖站点干净分类拒绝。
  - **占位清理**：未注册的 `site.go` 占位 LuxResolver 与未接链的 SelectBestFormat
    工具集整体移除（yt-dlp 自有评分在 `ytdlp.go`，无共享死代码）。
  - **回归**：`lux` 测试 11 例（seam 假提取器离线，`12` §1 纪律）、`app` 链序断言
    （lux 位次先于 yt-dlp、均先于插件钩子）、config Validate 边界；许可证门禁
    PASS（0 copyleft / 0 unknown）。
  - **文档**：`docs/adr/ADR-0026`、`03` FR-SRC-01 时点注、`05` §10 链序、`07` v0.13、
    `10` §4 落地注、`12` §3.3 用例清单、`14` WBS 12 收口。
  - **边界（不宣称）**：未列入子集的站点（youku/微博/qq 等）不支持；多 Part 合并、
    分离 A/V 双流 + Remux 合成、Deno/EJS、真实站点互通验证属 P3（ADR-0010 时点注
    口径不变）。

- **WBS 20：Vue 3 + TypeScript Web 控制台（2026-11-20）**：
  - **交付**：`web/` 新增 Vite + Vue 3 + TS + Pinia + vue-router 工程与提交锁文件；五个分区
    （概览 / 任务 / 新建下载 / 设置 / 运维），类型化 RPC 方法层 `web/src/api/rpc.ts` 与
    `types/api.ts` 一一映射 L0 REST/WS；单文件产物 `internal/api/ui/index.html`（约 174 KiB /
    gzip 63 KiB）由 `go:embed` 直接嵌入，Go 侧路由零改动。`docs/07` v0.14 §9 是接口映射
    真值源，`docs/13` v0.32 定义 `make web` / `make web-check` 与 CI `setup-node` 校验。
  - **新增 additive L0 端点**：`GET /api/v1/events/history?limit=`（持久化事件，默认 50 /
    上限 200；无 store 注入时 `503 E_CAPABILITY_UNAVAILABLE`）与 `POST /api/v1/ws-ticket`
    + `WS /events?ticket=`（60 秒、一次性，浏览器无需在 WebSocket 握手设置 Bearer 头，
    长期令牌不进 URL）。`internal/api/ui_rpc_test.go` 覆盖历史端点与票据重放拒绝。
  - **交互口径**：WS 负责即时事件，`task.progress` 只更新字节数；速度 / ETA 由
    `GET /tasks/{id}` 5 秒轮询补足（与文档示例口径一致）。「重试」映射为既有 `resume`，
    没有伪造新的引擎能力。备份 / 恢复 / 迁移仍为离线 CLI，页面仅展示准确命令与原因。
  - **验证**：`vue-tsc` 严格类型检查 + Vite 构建通过；在临时空目录用提交锁文件完整
    `npm ci && npm run build`，产物 SHA-256 与仓库内一致（`3903f411…`）。真实 `udd`
    本机冒烟：`/` 返回嵌入页面（200 / `text/html`），`/healthz`、`/capabilities`、
    `/events/history` 正常，非 API 深链接仍 404（hash 路由不经服务端）。
  - **边界**：分片级明细、`capability.changed` 发布、sidecar 运行时开关、真实站点回放
    均未实现；未引入 JSON-RPC 传输层（ADR-0019 不变），不宣称正式 UI 取代 CLI。
  - **文档**：`07` v0.14、`13` v0.32、`14` v1.17（WBS 20）、`23` v1.5、`docs/README` §三、
    ADR-0025。本包 CI run `44692698811` **`quality: success`**（含新增
    `build & verify embedded web console` 步骤，全 20 步通过），当前滚动结论停在 `4604d0c`。
- **WBS 14：纯 Go Remux 后处理（2026-11-20，ADR-0011 / 0012）**：
  - **交付**：`internal/postproc/tsdemux.go`（PAT / PMT / PES、PTS/DTS 解绕、Annex B → AVCC、
    ADTS AAC → esds）、`mp4mux.go`（`ftyp` + `moov` 前置 + `mdat` 的 `stts` / `ctts` /
    `stsc` / `stsz` / `stco` 全表）、`remux.go`（本地路径输入、1GiB 内存上限、fMP4 结构校验后
    直通、输出 `<target>.remux.part` 交还核心原子改名）。已注册为内置 `remux` 后处理器，
    无外部二进制，零 sidecar 基线不变。
  - **回归**：合成 TS / MP4 结构用例 + 真实 ffmpeg 生成的 TS fixture（H.264 320×180 50 帧 +
    AAC 95 帧）解码干净；`internal/postproc` 覆盖率 **82.5%**（阈值 80%），`go test -race`
    全绿。
  - **边界**：仅 H.264/AAC TS → 渐进 MP4；AV1 / VP9 / 其他编码不走 Remux，交可选 ffmpeg。
- **WBS 12：yt-dlp 站点解析与自有格式选取（2026-11-20，ADR-0010）**：
  - **交付**：`internal/resolve/ytdlp.go`（`sources.ytdlp.enabled` 显式开启才注册；
    `-J --skip-download --no-warnings --no-playlist -- <URL>` 只取 JSON；站点 URL 与每个
    格式 URL 均过 SSRFGuard；自有格式评分优先 H.264/AAC 渐进流；JSON 16MiB 上限与
    最小环境变量白名单）。`internal/config` 新增 `sources.ytdlp.*` 与 `UD_YTDLP_*` 覆盖；
    `internal/app` 链序 `… hls → dash → yt-dlp → [plugin] → direct-http …`。
  - **范围与时点限制**：当前调度器每任务只搬一个 `Resource.Files[0]`，因此仅接纳
    **渐进（视频+音频同容器）格式**；只有分离 A/V 格式时明确返回
    `E_CAPABILITY_UNAVAILABLE`，不产出无声音/无画面的假成功。分离流下载 + Remux 合并
    纳入 P3 多文件计划；「无 ffmpeg 也不退化到 720p」在本时点**不宣称达成**。
  - **同批修复**：`internal/resolve/site.go` / `site_test.go`（远端 WBS 12 占位包）原本
    语法错误、重复字段与错误断言导致 `go test ./...` 失败；修复语法、`tbr`(kbit/s) 与
    策略 `MaxBitrate`(Mbit/s) 单位换算，并把同分辨率 H.264/AAC 的权重对齐 ADR-0010。
  - **回归**：`resolve` 新增 yt-dlp fixture / 错误码 / 真实二进制参数用例，覆盖率 **83.9%**；
    `config` 96.1%、`app` 81.2%；lint / race / 六平台构建本地全绿。
  - **文档**：`09` §7、`05` §10、`07` v0.13、`12` v1.14、`13` v0.30 与 `config.example.yaml`、
    `14` v1.16、`23` v1.4、ADR-0010 实现时点注。
  - **CI（回填）**：本包 `fd9fbf9` run `44672119353` `quality: success`（全 14 步通过，
    strict 门禁 6 PASS / 0 WARN / 0 FAIL）；上一提交 `cce31fe` 的失败 run
    `44668706736`（`site_test.go` 语法错误）如实留档，当前滚动结论停在 `fd9fbf9`。

- **WBS 13（收口）：DASH L1 流媒体传输（2026-11-20，ADR-0024）**：
  - **交付**：`internal/transport/dash`（静态 MPD 解析、BaseURL 链、SegmentTemplate
    `@duration` 与 SegmentTimeline 两种定界、模板属性继承、`$Number%05d$` / `$Time$` /
    `$$` 标识符、SegmentList、init 段前置拼接、BANDWIDTH 选流、日历型时长拒绝）+
    `internal/resolve/dash`（`.mpd` 分类、http(s) 白名单、SSRF 出口校验）。与 HLS 共享
    顺序提交原语 `tputil.SequentialCommit` / `tputil.NewChanReader`（本次从 hls 提炼，
    首 chunk / 越界 / Release 语义全顺序传输一处实现）。
  - **范围（ADR-0024）**：仅静态 / 单 Period / 单 AdaptationSet（分离音视频轨待 WBS 14
    Remux 并显式指向）/ 仅明文（CENC/DRM 拒绝）/ SegmentBase 拒绝；WBS 13 技术交付收口。
  - **分发缺陷修复（两处，均由本轮测试发现）**：① 通用 HTTP 传输 `Supports` 不看 Kind，
    先注册的它把 `.m3u8` / `.mpd` 任务当普通文件下载（现仅声明 `single` / `multi`，
    兼容空 Kind）；② `{"hls"|"dash": true}` forced 选项原为死代码（`Resolver.Match`
    冻结签名拿不到任务选项）——由 `resolve.Registry` 集中强制路由修复。新增内部路由
    标记 `X-Ud-Stream`（持久化于 resources.headers、传输出网前剥离）+ 两个流传输的
    Kind 分区 + 跨段续传回归测试（hls / dash 各 1 例：resume 点落在后续段内时按段
    偏移跳过，修复两处首 chunk 偏移过跳缺陷）。
  - **回归**：`dash` 包 24 例（解析 12 / 传输端到端 12，覆盖率 83.2%，阈值 80%）；
    `resolve` +4；`app` 链序与分发断言更新（`.mpd` → dash / 标记 → dash / 默认 → hls）；
    hls / http / scheduler 回归全绿。
  - **文档**：`docs/adr/ADR-0024`、`03` FR-TRN-08 收口时点注、`05` §10 链序、`06` §3、
    `07` v0.12、`12` §3.3、`14` WBS 13 ✅、`16` 时点注、`tools/gates.py` 新包阈值。

- **WBS 13（半边）：HLS L1 流媒体传输（2026-11-19，ADR-0023）**：
  - **交付**：`internal/transport/hls`（RFC 8216 master/media 清单解析、AES-128 CBC/PKCS7
    解密、`EXT-X-BYTERANGE`、fMP4 探测、未知 tag 容忍；有界探测 + 字节信号量（64MiB）+
    8 worker 的乱序下载/按序提交；Budget 唯一限速计费；AES key 缓存；崩溃恢复按首个
    pending 分片字节边界定位段并用 HTTP Range 跳过）与 `internal/resolve/hls`（`.m3u8`
    分类 + `{"hls": true}` 强制分类，http(s) 白名单，SSRF 出口校验）。Resolver 链
    `…torrent → hls → [plugin]…`，传输注册表新增 `hls`（`KindStream` 专属）。
  - **范围（ADR-0023）**：仅 VOD（live 清单拒绝）、仅 `METHOD=AES-128`（SAMPLE-AES 拒绝）、
    BANDWIDTH 最高选流（并列取文档序）；TS 拼接 `.ts`、fMP4 拼接 `.mp4`；DASH、多音轨、
    SAMPLE-AES、站点页解析（WBS 12）未落地。
  - **回归**：`hls` 包 27 例（解析 11 / 密码 4 / 传输端到端 12，httptest 真实 HTTP），
    覆盖率 82.8%（阈值 80%）；`resolve` 新增 4 例；`app` 链序断言更新。全量
    `go test ./...` 全绿。
  - **文档**：`docs/adr/ADR-0023`、`03` FR-TRN-08 时点注、`05` §10 链序、`06` §3、
    `07` v0.11、`12` §3.3 用例清单、`14` WBS 13 行、`16` 矩阵时点注、`tools/gates.py`
    新包阈值。

- **WBS 19 离线运维命令 `udctl backup / restore / migrate`（2026-11-19）**：
  - **实现**：`cmd/udctl/ops.go` 在 daemon 发现之前分派三个**离线**子命令；
    `internal/app/backup.go` 与 `internal/store/backup.go` 提供 `BackupBundle` / `RestoreBundle` /
    `Migrate`。`backup` 可用第二连接 `VACUUM INTO` 做一致性快照，导出 `state.db` + `config.yaml`
    （`remoteToken` 摘除，manifest 标 `redacted`）+ `keychain_refs.txt`（仅引用，不含密文）+
    `manifest.json`（格式 `ud-backup/1`，逐文件 SHA-256）；目标目录已存在即拒绝，不覆盖旧备份。
    `restore` 校验 manifest 与快照 `schema_version`、拒绝高于本二进制的版本、经本地发现文件
    `/api/v1/healthz` 拒绝 daemon 运行中执行；随后快照旧库、清 `-wal`/`-shm`、替换 DB、
    走 `Recover` 重建分片状态并逐片校验 `.part`。`migrate --from N` 显式校验当前版本，
    未来 schema 无回滚脚本即拒绝。数据库路径优先级 `--db` > `UD_DB` > 用户配置目录 `ud/state.db`。
  - **回归**：`internal/app/backup_test.go` 7 例（往返 / 拒绝已存在目标 / SHA-256 损坏检测 /
    拒绝未来 schema / 拒绝运行中 daemon / `migrate` 意图校验 / 默认路径非空），
    `internal/store/backup_test.go` 7 例（活库快照 / 缺源 / 自复制与目录目标 / 陈旧目标清理 /
    凭据引用收集 / 非 SQLite 拒绝 / schema 版本一致），已纳入全量 `go test ./...`。
  - **证据口径订正**：`13` v0.27 所述 Windows 全流程冒烟为 operator 现场记录，
    `docs/evidence/windows-audit-2026-11-19/` 当前只归档全量单测与钥匙串真机日志，
    **无独立备份 / 恢复 / 迁移冒烟 transcript**；可复核依据为上述自动化用例，
    冒烟不另行计为可审计 PASS 证据。
  - **文档**：`07` v0.10、`13` v0.27、`14` WBS 19 行。

- **Windows 10 x64 钥匙串真机验证（2026-11-19，补 ADR-0015 决策 ④ 边界）**：
  - **实测**：真实 `crypt32.dll` syscall 五项 PASS——密文往返含多字节 UTF-8、9KiB 大 blob
    （超 Credential Manager 5×512 上限，验证 DPAPI 选型）、篡改密文拒绝、`KeychainStore`
    端到端 Put/Get/Delete + `windows-dpapi` 后端上报、落盘密文不含明文。两项依赖 POSIX 权限位的
    用例在 Windows 上如实 `SKIP: NOT_EXECUTED`（`os.Chmod` 不能制造宽权限 / 不可写目录），
    **不把 SKIP 记为 PASS**；包级 `ok github.com/asdfgh129/gobitmule/internal/security`。
  - **真机缺陷修复**：`internal/security/keychain_file.go` 读取前的 0600 守卫改为 POSIX-only
    （Go mode bit 不反映 NTFS ACL，原实现在 Windows 恒失败）；`keychain_windows_test.go`
    新增 5 条真机用例。另：`keychain_cli.go` 补 `darwin || linux` 构建标签——它只服务
    macOS / Linux 助手，留在 Windows 编译集会以 0% 覆盖拖低 `internal/security`
    门禁包（Windows gates 实测发现）；CLI 专属测试（`helperError` / `limitedWriter` /
    `firstLine`）随之移入同标签的 `keychain_cli_test.go`。Windows 编译集覆盖率
    83.8% → 86.6%（Linux CI 集合 91.3% 达 90% 阈值不变；剩余差距为 Windows 不可构造
    的 chmod 分支，如实记为平台边界）。证据 `docs/evidence/windows-audit-2026-11-19/keychain_windows_real_machine.txt`
    与 `go_test_all_2026-11-19.txt`。**Linux `secret-tool` 真机路径仍 `NOT_EXECUTED`**。
  - **文档**：`06` §6 / §10、`11` §10 残余风险第 1 条追加 Windows 真机时点注。

- **Linux Secret Service（`secret-tool`）钥匙串真机验证（2026-11-19）**：
  - **实现 / 测试**：新增 `internal/security/keychain_linux_real_test.go`（构建标签
    `linux && keyringreal`，不进 CI），在真实 D-Bus session + 已解锁的 GNOME Keyring 上
    以真实 `/usr/bin/secret-tool` 跑 5 项 PASS：`linux-secret-service` 后端选择与含多字节
    UTF-8 / 引号的 Put/Get/Delete 往返、绕过封装直接用 `secret-tool lookup` 证明条目确实落进
    OS 钥匙串且 Delete 后消失、缺失 id / 跨 scheme ref 的 not-found 与幂等删除、两个 id 的
    相互隔离、密钥只经 stdin 不进 argv 的机制检查。默认 Linux 测试仍用假 helper，本文件只在
    显式 `-tags keyringreal` 时编译。`go test -count=1 -tags keyringreal ./internal/security -run RealMachine -v`。
  - **宿主**：实验室主机 A（Ubuntu 23.04 / x86_64 / Go 1.27.4，`192.168.1.222` 为其第二网卡
    地址），安装 `libsecret-tools 0.20.5-3` 与 `gnome-keyring 42.1-1`，用 `dbus-run-session`
    启动并解锁 keyring；证据 `docs/evidence/linux-keychain-2026-11-19/keychain_linux_real_machine.txt`。
  - **未执行项**：POSIX 宽权限文件用例仍以 root 运行，未把 root 绕过权限位的情况记为 PASS；
    移动端 BT 内存真机为唯一剩余技术真机项。
  - **文档**：`06` §6 / §10、`11` §10、`23` v1.3 §十一、`docs/README` §三同步。

- **CI 时点记录（2026-11-19）**：`c298420`（WBS 19 + Windows 钥匙串真机）run `44449908661`
  为 **`quality: failure`**——`gofmt` 守卫在 `internal/security/keychain_test.go` 与
  `keychain_unit_test.go` 报需格式化，`quality` job 第 8 步失败，后续 test / build /
  gates-strict / interop / docs 步骤**全部 skipped**（非门禁放宽、非 flake）。本包已格式化
  两文件并在本机复跑全量门禁：先发现 `internal/app` 覆盖率 **71.9% < 80%**（新增 `backup.go`
  的脱敏 / 坏 manifest / 离线守卫错误分支未覆盖），补 8 组回归后升至 **80.9%**，
  `gates-strict` 恢复 **6 PASS / 0 WARN / 0 FAIL**、`go test -race ./...` 全绿；
  `internal/security` 另在实验室主机 A（Linux 编译集、Go 1.27.4）真实测得 **91.3%**
  （阈值 90%，证据 `docs/evidence/linux-coverage-2026-11-19/security_coverage.txt`）。
  首轮修复包 `9ed2b61` 的 CI run `44454550241` 仍在 `gates-strict` → cover 失败：
  `internal/postproc` 的 `TestTranscodeWritesPartAndParsesProgress` 在 GitHub 快速 runner 上报
  `E_POSTPROCESS_FAILED`（`waitErr=<nil>`，stderr 只有 `GOCOVERDIR` 警告）。根因是
  `internal/postproc/ffmpeg.go` 先调用 `cmd.Wait()` 再 `wg.Wait()`，违反 Go `StdoutPipe`
  「Wait 前必须读完管道」约束，快速 runner 上 `Wait` 提前关闭管道使进度扫描器报错；
  本包改为**先排空 stdout / stderr 再 `Wait`**，本机与实验室主机 A 全量 Linux 覆盖复跑通过。
  **CI 复验**：最终修复包 `1880ffa` run `44456182011` 为 **`quality: success`**
  （全部 14 步通过，含 `gates-strict` 与 `docs --check`）；两次修复前失败 run
  `44449908661` / `44454550241` 如实留档，滚动结论停在 `1880ffa`（见 `13` §4 与 `docs/README` §三）。
  **后续 Linux 真机验证包 `daa91fb` run `44476794842` 为 `quality: success`**（14/14 步通过），
  滚动结论更新为 `daa91fb`；`1880ffa` 为上一时点值，保留不改。

### Added
- **P2 ffmpeg 后处理落地：`postproc.Transcode` 真实 exec 路径（2026-11-18；ADR-0011 / ADR-0012；`12` §5 两项收口）**：
  - **实现**：`internal/postproc/ffmpeg.go` 提供 `ffmpeg-transcode`——只接受**已下载到本地的文件**，
    输出 `<target>.transcode.part` 并由核心原子改名；固定参数白名单（mp4/mov/mkv/webm，
    显式 `-f` muxer）、`-threads` 有界、`-progress pipe:1` 解析、stderr 全量排空仅保留尾部、
    超时 + 进程组取消（Unix `setpgid` / Windows `taskkill /T /F`）、磁盘空间预检（`Statfs` /
    `GetDiskFreeSpaceEx`，纯 Go 零 cgo）、daemon 级并发槽位。
  - **能力探测**：启动时跑 `-version` / `-buildconf` / `-encoders` / `-hwaccels`；
    `--enable-nonfree` 构建判 `E_CAPABILITY_UNAVAILABLE`；缺 `libx264`/`aac` 同样拒绝注册。
    `postProcess.ffmpegEnabled` **默认 `false`**，零 sidecar 基线不变；探测失败只 `Warn`，下载与
    内置 passthrough 不受影响。`/capabilities.sidecars` 上报真实版本与 GPL/LGPL 标签。
  - **真实缺陷修复**：真实 ffmpeg 5.1.2 首跑报 `Unable to find a suitable output format`
    ——`.transcode.part` 后缀使容器无法推断，改为显式 `-f mp4|matroska|webm`；新增
    `TestTranscodeSelectsExplicitMuxer` 回归锁定顺序（muxer 在输出前、输出仍是最后一个参数）。
  - **性能补测（真实 ffmpeg，非占位）**：`cmd/udffmpegbench` + `make bench-ffmpeg` 在主机 A
    （1 vCPU）实测 **exec 中位 163.1ms / P95 187.7ms**（目标 200–500ms）、
    **哈希并发吞吐 89.8MiB/s = 单跑的 73%**（判据 ≥25% 且两侧完成），两项 **PASS**。
    证据 `docs/evidence/ffmpeg-bench-2026-11-18/`，报告 `22` §九、`23` §九。
  - **配置**：`postProcess.ffmpeg{Binary,Threads,MaxConcurrent,Timeout}` + `UD_FFMPEG_*` 覆盖；
    `Validate` 约束 1..64 / 1..16 / 1m..24h。
  - **文档**：`12` v1.13、`13` v0.25、`22` v1.1、`23` v1.1、`docs/README` §三。
    `make bench-ffmpeg` **不接入 CI**（需 operator 提供的本地 ffmpeg，CI 不装可执行 sidecar）。

### Added
- **`task_options` / `resources.headers` 的 500ms 批量写（2026-11-18，`06` §10 P0 偏差最后一条；ADR-0015 决策 ③ 收口）**：
  - **实现**：`internal/store/metabatch.go` 的 `metaBatcher` 把 `SaveOptions` / `SetOption` /
    `SaveResource` 按 key 去重后在**一个事务**提交。窗口自首笔起上限 500ms；单笔空闲写
    2ms settle 后提交（不给单次 PATCH 强加半秒延迟）；满 64 笔或 `Store.Close()` 立即排空。
  - **不变式**：调用方同步等待 COMMIT 结果——PATCH 返回 200 即已落库，事务失败返回给该批
    全部调用者，某一请求取消只结束它自己的等待；`06` §5 状态迁移 + 分片进度同事务、
    §6 敏感头剥离均不受影响。核查发现旧偏差所述「每片重写元数据」从未存在于生产代码，
    另加分片热路径零元数据写回归守卫。
  - **回归**：`store/metabatch_test.go` 8 例（窗口合并 / 单笔快速提交 / 去重 last-writer-wins /
    错误传播 / 取消隔离 / 条数上限 / Close 排空 / 敏感头剥离）、
    `scheduler/chunkflush_test.go:TestChunkWriterDoesNotWriteMetadata`；`internal/store` 覆盖率
    86.6%（阈值 80%），`go test -race ./...` 全绿。**不宣称吞吐提升**。
  - **文档**：`06` v0.6、ADR-0015「实现落地注终」、ADR-0020 后续动作表订正、
    ADR 索引 v0.8、`14`、`docs/README` v1.26。


### Added
- **`docs/23` 当前状态审计报告（2026-11-18）**：对代码 / 文档 / 测试一致性、安全、性能做全仓审计并留档。
  - **一致性**：文档版本头、当前态声明、ADR-0020 状态表、CI 台账与真实 `gh run` 逐条对照一致；文档引用的
    7 个关键测试（敏感头剥离、内联凭据拒绝、远程 token 守卫、全流水线、崩溃恢复 ×2、位图编码）均存在且通过；
    历史「未落地 / 序 6 仍开放」均为显式时点注，按项目纪律保留不改。
  - **安全**：6 架构守卫 0 违规、`go vet` 通过；SBOM / 许可证 / `govulncheck` PASS（代码受影响 0 漏洞，
    3 个不可达 require 漏洞按 `11` §3 / §7 口径登记）；仓库正则扫描无硬编码密钥；审阅凭据后端
    （stdin 传输、ref 形状校验、0600 权限守卫）与远程 token fail-closed。
  - **性能**：`gates-strict` 回环基线 572.86 MB/s PASS（**不是千兆 NFR**，回环噪声 ±3 倍不做性能声明）；
    NFR-PERF-01 11.21MB/s `met=true` 与 `12` §5 单机四项为既有实测、本次未重跑；分片热路径零元数据写守卫在。
  - **门禁**：`make lint` / `test` / `build`（六平台 `CGO_ENABLED=0`）/ `gates-strict`（6 PASS / 0 WARN / 0 FAIL）/
    `go test -race ./...` / 门户校验（0 断链）全绿。边界：Windows / Linux 钥匙串真机 `NOT_EXECUTED`、
    ffmpeg 两项随 P2、序 6 实名会签待补，均如实登记在 `23` §六。
  - **文档**：`docs/README` v1.27、`tools/build_docs.py:GROUPS` 注册 `23`。


### Security
- **Go 工具链升至 1.27.4：修复 `GO-2026-6859`（2026-11-11，WBS 11 收尾门禁发现）**：
  `make gates-strict` 复跑时 `govulncheck` 漏洞库新增
  `GO-2026-6859`「HTTP/1 server crash due to `MaxBytesReader` header race in `net/http`」，
  Go **1.27.3** 标准库受影响、官方修复版本为 **1.27.4**。符号级扫描显示调用链遍布
  `api.Server.ServeHTTP` / `wsHub.ServeHTTP`（`websocket.Upgrader.Upgrade`）、
  `app.Run`（`http.Server.Serve` / `Shutdown`）、`bt.Transport.Run`、
  `interop.ScrapeHTTP` 与 `store.WholeFileChecksum`，属**可达**漏洞，
  不得按「不可达」放行。已将 `go.mod` 的 `go` 指令与
  `.github/workflows/ci.yml` 的 `go-version` 由 `1.27.3` 升至 **`1.27.4`**，
  同步 `Makefile` 注释、根 `README.md` 快速开始、`10` §7/§8.3、`11` §10、`13` §1/§12；
  复扫 `No vulnerabilities found`，`make gates-strict` 恢复 **6 PASS / 0 WARN / 0 FAIL**。
  **模块依赖图未变**（`require` 段未改，仍 90 个链接模块、0 copyleft / 0 未识别），
  但代码变更后已重新 `make build` 再跑门禁，避免 SBOM 二进制陈旧守卫误判。
  `17` 与 ADR-0020 中的 Go 1.27.3 属 **P0 评审时点证据**，保留不改。
- **插件规则通道 fail-closed 安全边界（WBS 10）**：`capabilities.fs` / `env` / `subprocess`
  默认禁用且宿主不注入任何对应能力；出网只允许 `ctx.http.get`，每次重定向逐跳重过
  `allowedHosts` 白名单与共享 `security.SSRFGuard`，并强制 `timeoutMs` / `memoryMB` /
  `maxResponseBytes` 硬上限与调用栈 / 输出体积限制。清单 `match` 正则命中 `thunder://` /
  `flashget://` / `qqdl://` / `magnet:` / `ed2k://` / `file:` / raw bencode 等内置哨兵即拒绝，
  防止插件抢在安全敏感内置之前；插件产出的一切 URL 与路径再经 `URLGuard` / `PathGuard` 复核。
  官方规则包必须 Ed25519 验签，`verifySignature=false` 需显式 `insecure=true`（仅本地开发），
  且 `insecure` 与非回环监听组合在配置校验阶段直接拒绝启动。
- **FTP 被动数据通道 SSRF 防护（WBS 9）**：EPSV/PASV 响应由服务器给出第二个 socket 地址，
  控制连接的 DNS pin 覆盖不到。新增 `security.SSRFGuard.DialDataContext` / `CheckDataAddr`，
  强制：数据地址为字面 IP（无二次 DNS）、必须等于已校验的控制连接 IP、仍过当前私网/保留
  地址策略、端口 ≥ 1024（防止被导向同主机的特权服务）；任一不满足即
  `E_RESOLVE_SSRF_BLOCKED` fail closed。`jlaffaye/ftp` 保持 `DialWithTrustPasvIP(false)`，
  PASV 里注入 `169.254.169.254` 的投毒回环用例可复跑验证。
- **SFTP 主机密钥强制 pin**：凭据必须含 `host_key`（authorized_keys 行）或
  `host_key_fingerprint`（`SHA256:...`），缺失即在握手前拒绝；不提供任何跳过校验的开关。
  内嵌 URL 凭据、控制字符与 `..` 路径段在 `ParseTarget` 处拒绝（FTP 命令注入 / 路径穿越）。
- **`golang.org/x/crypto` 升至 v0.58.0（合入前拦截）**：本地以真实 `govulncheck`
  复扫发现 WBS 9 初选的 v0.54.0 存在 `GO-2026-6355` / `GO-2026-6354` 两条
  `ssh` 通道死锁 DoS，且调用链 `sftp.realDial → ssh.NewClientConn` 可达；官方修复版本
  为 v0.56.0，本项目直接 pin 到已通过校验的 v0.58.0。复扫 `No vulnerabilities found`，
  `make gates-strict` 6 PASS / 0 WARN / 0 FAIL。残余 `GO-2026-5932` 只涉及未被引用的
  `x/crypto/openpgp`（已停止维护），不影响本项目调用链，登记为已知残余风险（`11` §10）。
- **供应链门禁真实性**：SBOM 只扫描实际链接进二进制的模块（16 个 → 含 WBS 9 新增
  `jlaffaye/ftp` ISC、`pkg/sftp` BSD-2-Clause、`kr/fs` BSD-3-Clause、`golang.org/x/crypto`
  BSD-3-Clause），`licenses` 复跑 0 copyleft / 0 unknown；`go mod verify` 通过。
- **Go 工具链升至 1.27.3**：`govulncheck` 首次扫描发现 `GO-2026-6809`——Go 1.27.2 标准库
  `crypto/x509`「Distrust compromised intermediate certificates」，本项目调用链
  `store.WholeFileChecksum → io.Copy → x509.Certificate.Verify` 可达。已同步 `go.mod`、
  `.github/workflows/ci.yml`、`docs/10` §7、`docs/13` §1/§12 与根 `README.md`；升级后复扫
  `No vulnerabilities found`。
- 新增供应链门禁：CycloneDX 1.6 SBOM（基于 `go version -m` **实际链接模块图**，12 个 component）、
  `go mod verify`、许可证扫描（0 copyleft / 0 未识别）、`THIRD_PARTY_NOTICES.md`、
  `checksums.txt` 与 `govulncheck`，接入 `make gates` 与 CI。

### Added
- **OS 钥匙串凭据适配器（2026-11-17，ADR-0015 决策 ④，`06` §6 / `11` §3）**：  - **此前状态**：凭据只有 `MemoryCredentialStore`（进程内、重启即失），`11` §10 残余风险
    第 1 条与 ADR-0015 落地注长期登记为「仍未落实」。发起人已确认「零 cgo 三平台适配」路线。
  - **平台矩阵（按本机实际可用择优，`GET /capabilities` 新增 `credentials` 字段如实上报）**：
    macOS `macos-keychain`——`/usr/bin/security -i`，整条命令经 **stdin**，密钥永不进 argv
    （Apple 自文档标注 `-p/-w` 走 argv 不安全，`-w` 置尾提示需 TTY，守护进程没有）；实测退出码
    0 / 44（条目不存在）/ 45（重复）/ 2（用法错误），44 用于区分 not-found 与锁钥匙串等真故障。
    Windows `windows-dpapi`——纯 Go `syscall` 调 `crypt32.dll` 的 `CryptProtectData`
    （恒置 `CRYPTPROTECT_UI_FORBIDDEN`，服务场景不弹 GUI），密文落 `<state>/credentials/<id>.bin`；
    选 DPAPI 而非 Credential Manager：后者 generic blob 上限 5×512 字节装不下 PEM 私钥。
    Linux `linux-secret-service`——`secret-tool`（libsecret-tools CLI）；**方案变更**：原拟
    godbus 直连 D-Bus Secret Service，改为 CLI，理由是不重造协议、零新依赖、与 macOS 同构可审，
    代价是需装 `libsecret-tools` 且有 session bus，缺一降级。
  - **降级与兜底**：`file` = 0600 文件 + 0700 目录（读取前校验权限非 0600 即拒绝），
    **显式不是钥匙串**（无 ACL、无 OS 加密）；`memory` 重启即失。后端名经
    `core.CredentialBackendReporter` 由 `GET /capabilities` 发布，运维可核对，不得宣称
    「凭据存钥匙串」而不看实际后端。
  - **共用契约**：ref `<scheme>://ud/cred/<32hex>` 由铸造后端校验前缀与 id 形状（防穿越 /
    防跨后端重放）；`Put` 拒空、拒控制字符（CLI 按行传输，裸换行会静默截断）、限 64KiB；
    `Delete` 幂等；读取每任务一次（`Run`/`Probe`/`injectHeaders` 前置），不进分片循环；
    **不做进程内缓存**——按次直读让钥匙串内删除立即生效（macOS 实测每次调用 ≈0.5s，
    9 次调用 4.24s，属任务启动成本，如实记录、不做加速声明）。
  - **验证边界（如实）**：macOS 真机验证——空格 / 双引号 / 反斜杠 / `#` 往返、同 id 替换（`-U`）、
    删除后 44、「stdin 非 argv」由源码断言固定；Windows / Linux 仅 `CGO_ENABLED=0` 六目标
    交叉编译 + 单测，**真机路径 `NOT_EXECUTED`**。
  - **回归**：`security/keychain_test.go` 11 条（往返 / 幂等删除 / 外来与穿越 ref 拒绝 /
    密钥校验 / 0600 权限收紧与放宽拒绝 / 原子写无残留 / 后端上报 / id 唯一）、
    `keychain_darwin_test.go` 3 条（真机往返、stdin 断言、转义表）；全量 `go test ./...` 绿，
    `-race` 绿。
  - **文档**：`06` v0.5（§6 凭据后端表 + §10 收口注）、`07` v0.9（`capabilities.credentials`）、
    `11` v1.4（§3 ref 契约 + §10 残余风险第 1 条订正）、ADR-0015 落地注、`docs/README` v1.24。
- **HTTP Range 传输的慢启动（2026-11-17，`05` §5 / ADR-0020 后续动作）**：
  - **此前状态**：`transport/http.Run` 直接按 `maxConnections` 开满信号量，`05` §5 写的
    「初始 1 连接，按吞吐逐步增至 `maxConnections`」没有实现——小文件 / 慢链路也会
    瞬间并发全部连接。
  - **实现**：新增 `rampGate`（`internal/transport/http/ramp.go`）。gate 持有恰好
    `maxConnections` 个槽位令牌，启动时只放出 1 个、其余扣留；增长信号是**背压**而不是
    定时器或吞吐估算——分发循环在真正排队等槽位时才置位，随后某次分片成功完成交还一个
    扣留槽位。一次背压只换取一次增长，上限 `maxConnections`；令牌总数守恒，慢启动不会
    卡死快链路；失败分片不触发增长。
  - **为什么用背压**：早先按「完成瞬间是否所有槽位都在飞」判定，同时完成的分片会让采样
    错过饱和，实测 25 次里有若干次只爬到 3/4；背压由分发循环在阻塞前置位，单调可靠。
  - **不做性能声明**：本机回环吞吐波动 ±3 倍（见 `docs/22`），无法据此断言加速；本项兑现
    的是行为语义。
  - **回归**：`ramp_test.go` 7 条——初始 1 / 无背压不增长 / 一次背压一次增长 /
    单调爬到上限 / 绝不越界 / 单连接不增长 / ctx 取消；`TestRunSlowStartRampsConnections`
    端到端断言首个请求独占、峰值不超过上限（上限本身由 gate 用例钉住，因为末波可能用不满）。
  - **文档**：`05` v0.9 §5 实现注、ADR-0020 落地注里该项由 ⬜ 转 ✅、`docs/README` v1.23。
- **`POST /tasks` 响应内嵌 `Resource` 预览（2026-11-17，ADR-0020 后续动作）**：
  - **此前状态**：创建任务只返回 `{id, state, createdAt}`，客户端要渲染文件清单必须再发一次
    `GET /tasks/{id}` 或 `POST /resolve`；`07` §2 以「P0 响应差异」登记过。
  - **实现**：响应保留原三键，新增 `task`（完整 `TaskDetail`，与 `GET` 同形）、`resource`
    （预览，未解析出时 `null`）、`resolved`。`state` 以 `task` 为准（任务可能已离开 `Pending`）。
  - **默认不等待**：`Create` 只入队，解析由 worker 异步完成，所以默认响应是最快路径，
    拿不到预览时 `resolved:false`，客户端按原方式轮询——**P0 行为不变**。
  - **可选有界等待**：`?wait=<1..5000>`（毫秒）在 handler 内轮询到 `Resource` 或任务离开
    `Pending`/`Resolving`；超时返回 `201` + `resolved:false` + `previewTimedOut:true`
    （创建已成功，**绝不**因预览超时报错）；参数非法返回 `400 E_INVALID_INPUT` 且不创建任务。
  - **幂等重放**：同 `Idempotency-Key` 返回 `200` + `idempotent:true`，同样内嵌 `task`/`resource`，不等待。
  - **回归**：5 条（已解析则内嵌且 `state` 跟随任务 / 未解析不等且不误报超时 /
    `?wait` 命中有界轮询 / 超时如实标记 / 非法 `wait` 400 且零创建）。
  - **文档**：`07` v0.8 §2 当前态注、ADR-0020 追加「后续动作落地注」、`docs/README` v1.22。
- **`events` 审计日志保留清理（2026-11-17，兑现 `06` §7 / ADR-0015 实现债第二项）**：
  - **此前状态**：`06` §7 写着「`events` 保留策略可配（默认 7 天 / 10 万条），定期清理」，
    §10 的「P0 偏差」也登记过，但代码里**没有任何清理路径**，审计表只增不减。
  - **实现**：新增 `store.PruneEvents(ctx, beforeTS, maxRows)`，在**一个事务**内先删
    `ts < beforeTS` 的过期事件、再把表削到最新 `maxRows` 条（用
    `ORDER BY id DESC LIMIT 1 OFFSET maxRows-1` 定位保留边界，不做 `NOT IN` 全表重扫），
    返回 `DeletedByAge` / `DeletedByCount` / `Remaining` 三个计数，便于日志区分
    「按时间删了多少」与「按条数削了多少」。两个上限独立生效，`0` 关闭该半条策略。
  - **调度**：新增 `internal/app/janitor.go`，daemon 启动时**先清一次**（长期未启动的实例
    不必等一个节拍），之后按 `events.pruneInterval` 执行；清理失败只记 warn 并在下个节拍
    重试，**不阻塞数据面**。关闭顺序为先停 janitor 再 `st.Close()`。
  - **配置**：`events.retentionDays`（默认 7）/ `events.maxRows`（默认 100000）/
    `events.pruneInterval`（默认 1h，1m..24h）。**两者都为 0 时 `Validate` 拒绝启动**
    （`events: requires retentionDays or maxRows > 0`）——无界审计日志会撑爆磁盘，属 fail-closed。
    需重启生效（不在 `PATCH /config` 热补丁白名单内）。
  - **回归**：`store/events_prune_test.go` 5 条（按时间 / 按条数 / 先时间后条数 /
    三种空操作 / 取消上下文不产生部分提交）、`app/janitor_test.go` 3 条（注入时钟的保留判定、
    启动即清且可停止、失败只报错不致命）、`config/events_test.go` 9 个用例。
  - **文档**：`06` v0.4（§7 注 + §10 实现基线与收口注）、`13` §7 新增 `events.*` 配置表
    与示例、ADR-0015 追加第二条实现落地注、`config.example.yaml`；`docs/README` v1.21。
  - **仍开放**：ADR-0015 决策 ④ 系统钥匙串适配器、`task_options` / `resources.headers`
    的 500ms 聚合（`06` §10 P0 偏差条）。
- **`docs/12` §5 单机性能基准：`cmd/udbench` + 实测结果（2026-11-17）**：
  - **新增 `cmd/udbench`**（`ud.bench/v1`）：以**真实 `udd` 子进程 + 真实 REST API + 真实 HTTP Range
    传输**测三项可在单机复跑的 §5 指标——1000 任务常驻内存增量（`VmRSS(1000 任务) − VmRSS(空载)`）、
    含 1000 任务恢复的启动时间（`exec → /healthz`，因 `app.Run` 在 `net.Listen` 之前调用 `Recover()`，
    该时间天然包含 FromDB 恢复）、任务创建→首字节 P99（200 次顺序创建、5ms 轮询、不含解析）。
  - **实测（实验室主机 A，Linux，`CGO_ENABLED=0`）**：内存增量 **8.6MB**（≤300MB）、
    启动 **261ms**（≤3s）、创建 P99 **45ms**（≤1s）；2h 长稳峰值增量
    **12.5MB**，RSS 无单向增长趋势。
  - **诚实边界**：`ffmpeg exec 开销` 与 `分片哈希不与转码互不饿死` **未执行**——P1 没有 ffmpeg
    调用路径（`internal/postproc` 的 `Transcode` 是 P2 占位），不存在可测的 exec 开销；移动端 BT
    内存需真机；链路吞吐另见 `docs/20`。工具在平台不可读 RSS 时把这些项标 `NOT_EXECUTED`
    而不是编造数值，且**长稳窗口不足 2h 一律判 FAIL**。
  - **回归**：`cmd/udbench/main_test.go` 6 条确定性用例（最近秩 P99、峰值而非末值、`shortDur`、
    bench 配置必须保留回环 fixture 放行、长稳窗口 <2h 不得判达标、启动判据必须用 `/healthz` 时钟
    且要求 1000 个任务全部回来）。
  - **Makefile**：新增 `make bench-perf`（`BENCH_UDD` / `BENCH_TASKS` / `BENCH_SOAK` 可覆盖，不接入 CI）。
  - **文档**：新增 [`docs/22`](docs/22-性能基准实测-单机可复跑项.md)；同步 `docs/12` §5 结果段、
    `docs/13` §3、`docs/14` §11、`docs/README` v1.18、根 `README`；门户 **48 篇 / 0 断链**。
  - **证据**：`docs/evidence/bench-2026-11-17/`（`bench.json`、`rss_series.txt`、`SHA256SUMS`）。
- **第三客户端互操作矩阵：Transmission 3.00 双向实测 + 上传计数有界等待（2026-11-17）**：
  - **背景**：`BT-03` 此前只由 qBittorrent（libtorrent 系）双向支撑，Transmission 3.00 虽已部署在
    实验室主机 B，但从未单独跑过双向矩阵（`docs/19` §七 如实记为「未执行」）。
  - **新增 `cmd/udinteropx/transmission.go`**（`--peer-matrix transmission`）：在隔离段上启动真实的
    `transmission-daemon`（独立配置目录、按主机 B 空闲端口探测 RPC/peer 端口、RPC 白名单显式放行
    主机 A），用 `torrent-add`（base64 metainfo）/ `torrent-get` / `torrent-verify` / `session-stats`
    驱动，产出 `ud.peer-matrix/v1` 报告。`MX-TR-A`（Transmission 做种 → ud 下载，校验 sha256 与
    做种端 `uploadedEver`）与 `MX-TR-B`（ud 路径 A 做种 → **先停原做种端** → 全新 Transmission 下载，
    校验端侧落盘文件 sha256 与我方 `bytes_uploaded`）**两向均 PASS**。
  - **判据边界**：不新增、不修改 `BT-01..BT-14` 判据与注册表，不改变 14/14 结论，不替代
    `make l4-run`；`make peer-matrix` 与 `l4-run` / `perf-lab` 同级，**不接入 CI**。
  - **顺带修复（真实缺陷）**：BT-02 / BT-03 与矩阵都曾在真实 leecher 刚完成的瞬间**单次采样**
    `bytes_uploaded`，而该计数由传输层 200ms 统计节拍刷新，实测出现过 65536 &lt; 262144 的
    **假 FAIL**。新增 `l4WaitUploaded`（有界轮询，上限 30–45s，观测取值序列写入
    `evidence.bytes_uploaded_samples`），**判据仍是「≥ 载荷」，未放宽**；未在窗口内达到即如实 FAIL。
    修复后 BT-02 / BT-03 各重跑一次仍 PASS。
  - **实现踩坑（已固化进代码注释）**：Transmission 3.00 的 `-p` 是 **RPC 端口**、`-P` 才是 peer 端口
    （用 `--port` 设 peer 端口会直接打印 usage 退出）；`-a` 是 RPC 白名单（默认仅 `127.0.0.1`，
    不放行主机 A 会全部被拒）；RPC 首包 `409` + `X-Transmission-Session-Id` 必须回填重试；
    远端命令行含配置目录路径，`pkill -f <path>` 会杀掉 SSH 会话自身且静默返回空 —— 清理只用 PID 文件。
  - **回归**：`cmd/udinteropx/transmission_test.go` 6 条确定性用例（完成/做种判定、检查态不算做种、
    409 会话协商、非 success 结果必须报错、未实装客户端必须 UNSUPPORTED）。
  - **文档**：新增 [`docs/21`](docs/21-第三客户端互操作矩阵Transmission实测报告.md)；同步 `docs/19` §七、
    `docs/16` §4、`docs/11` §4、`docs/12` §3.1.3 / §5、`docs/13` §3（新增 `make peer-matrix` 与
    `make bench-perf` 行）、`docs/README` v1.17、根 `README`；门户 **47 篇 / 0 断链**。
  - **证据**：`docs/evidence/peer-matrix-transmission-2026-11-17/`（`peer_matrix.json` +
    `SHA256SUMS`，`all_pass=true`）。
- **NFR-PERF-01 双机应用层基准达标：`cmd/udperf` 双机模式 + 11.21MB/s 实测（2026-11-16）**：
  - **代码**：`cmd/udperf` 新增 `--serve` / `--listen` / `--advertise`
    （对端提供 1GiB 确定性 HTTP Range 数据）、`--url`（用真实
    `internal/transport/http` 下载远端 fixture）、`--min-mb-s` 与
    `--require-met`（ADR-0022 门槛与退出码）。默认回环模式行为与
    `nfr_perf_01.met=false` 完全不变。
  - **安全边界**：双机模式为访问隔离段私网地址，使用**仅限本命令**的
    `SSRFGuard`（`BlockPrivateNetworks=false`、只允许目标端口、禁重定向）；
    回环模式仍用严格 `NewDevGuard`。新增 `cmd/udperf/main_test.go`：
    门槛边界、私网/端口守卫、pattern 确定性 3 组回归。
  - **Makefile**：新增 `perf-lab`（`PERF_LAB_URL` / `PERF_LAB_MIN_MB_S`），
    不接入 CI；`make perf` 仍是回环参考。
  - **实测**：主机 B（`192.168.1.221`）`udperf --serve --bytes 1073741824`，
    主机 A（`192.168.1.222`）`--url ... --connections 4 --min-mb-s 10
    --require-met`：**1GiB / 91.367s = 11.21MB/s，4096/4096 分片校验，
    `nfr_perf_01.met=true`**。同链路 iperf3 3.12 单流 / 8 流接收均
    **94.15Mbps ≈ 11.22MB/s**，应用层约为链路容量的 **99.9%**。
  - **边界**：这是 **100Mbps 隔离段**，按 ADR-0022「≥ 可用链路 × 80%」
    取保守门槛 **≥10MB/s**（80% 约为 8.98MB/s）；**不宣称**原
    「千兆内网 ≥80MB/s」达标，链路升级后须重跑、ADR-0022 自动失效。
    `make perf` 的 `dist/perf.json` 仍固定 `met=false`，与双机证据不得混用。
  - **证据与文档**：证据归档 `docs/evidence/nfr-perf-01-2026-11-16/`
    （`nfr_perf_01.json` / `iperf3-P1.json` / `iperf3-P8.json` / `link.json` /
    `SHA256SUMS`），新增 [20 NFR-PERF-01 双机应用层基准实测报告](docs/20-NFR-PERF-01双机应用层基准实测报告.md)；
    同步 `docs/README` **v1.15**（门户 **46 篇**）、`12` **v1.10**、
    `13` **v0.18**、`14` **v1.10**、`18` **v0.8**、`19` §七、根 `README.md`、
    `tools/build_docs.py`（GROUPS / 横幅 / doc-20）。
  - **本机门禁**：`make lint` / `make test` / `go test -race ./...` 全绿；
    `make build` 六平台成功；`make gates-strict` **6 PASS / 0 WARN / 0 FAIL**
    （90 个链接模块、0 copyleft / 0 未识别、`no known vulnerabilities`）。
  - **CI**：`38bd52c` run `43875659670`（P0 CI）结论 **`success`**；
    CI 只跑 lint / 测试 / 构建 / 门禁 / 本地回环套件，不执行双机基准。
  - **序 6 状态**：技术验收项（L4 14/14 + NFR `met=true`）**已全部满足**；
    仅余评审组实名复签 / 法务与安全会签 / `14` R-10 复评，**序 6 暂不标记关闭**。
- **L4 真实实验室 14/14 PASS：14 条 harness 实装 + 双机执行 + 证据归档（2026-11-16）**：
  - **harness 实装**：`cmd/udinteropx` 新增 `--l4-case BT-XX` /
    `UD_INTEROP_L4_CASE` 模式（`cmd/udinteropx/l4.go`），逐条输出单行 JSON 判定
    `{status,reason,evidence}`；`internal/interop.runExternalCase()` 解析最后一行
    JSON 作为门禁结果。14 条用例全部驱动**真实** `internal/transport/bt` 与真实
    第三方客户端（主机 B 的 qBittorrent v4.5.1 / Transmission 3.00 /
    python-libtorrent 2.0.8），tracker 为每例独立端口的真实 chihaya。
  - **生产代码**：`internal/transport/bt` 新增 `WithDHTBootstrapNodes(...)` 选项，
    允许受限网络 / 隔离实验室注入自定义 DHT 路由器；仅在显式注入时对回环 DHT
    节点关闭 security extension，默认公共 DHT 行为不变。补
    `TestClientConfigDHTBootstrap`；`go.mod` 将 `github.com/anacrolix/dht/v2`
    从间接依赖转为直接依赖（模块总数不变，仍 90 个链接模块）。
  - **BT-04（纯磁力 / 仅 DHT）**：公网 DHT UDP 被 NAT 阻断，改用隔离段**本地真实
    DHT 路由器** + 进程内真实 anacrolix 做种节点，完成真实 `announce_peer` /
    `get_peers` / `ut_metadata` 与 payload sha256 校验；修复上游
    `peerstore.InMemory` 在本场景下把 peer 地址损坏为 `[?7f00:1]` 的问题
    （实验室专用 `l4PeerStore` 保存完整 `krpc.NodeAddr`）。
  - **抓包与清理**：`BT-08` 私种下载期间 UDP 包 0（过滤对端合法 uTP 与自环），
    `BT-14` 抓包命中诚实 PeerID `-UD0000-`；主机 B 的第二个隔离段地址
    `192.168.1.239` 纳入过滤。qBittorrent 清理改为 graceful shutdown +
    profile 作用域 `pkill`，消除跨用例残留进程导致的超时。
  - **有界真实重试**：仅对瞬时网络竞态签名（`context deadline exceeded` /
    `E_TRANSIENT_NETWORK` / `i/o timeout` / `connection reset by peer`）做
    最多 2 次**全新真实执行**，第二次不得把 `SKIPPED` / `UNSUPPORTED` 升级为 PASS；
    新增 3 条确定性回归锁定语义。最终正式执行 14 条均为首次 PASS。
  - **真实配置与执行**：新增 `tools/l4_external_config.lab.json`（14 条，
    `timeout_s=780`）；`make l4-run L4_CONFIG=... L4_CASE_TIMEOUT=840s` 产出
    `dist/l4_lab/interop.json` / `INTEROP.md`：**23 例 PASS、L4 门禁 14/14 PASS、
    `release_gate_met=true`**；`make l4-audit` 独立重算 9 项 `AUD-*` 为
    **`CONSISTENT` 9/9、0 FAIL**，重算 `gate_pass=14/14`。审计器仍恒
    `l4_executed_by_audit=false`，PASS 依据是真实执行而非审计自洽。
  - **证据归档**：原始报告、抓包与校验和归档到
    `docs/evidence/l4-2026-11-16/`（`interop.json` / `INTEROP.md` /
    `l4_audit.json` / `BT-08-capture.pcap` / `BT-14-capture.pcap` /
    `SHA256SUMS`），并新增 [19 L4 实验室实测与发布门禁报告](docs/19-L4实验室实测与发布门禁报告.md)。
  - **边界（不得误读）**：受控双机实验室、指定客户端版本；`BT-04` 不是公共 DHT
    网络；不等于 PT / 公网全矩阵 / 千兆性能达标。**NFR-PERF-01 双机应用层 1GiB
    基准仍未执行**（`cmd/udperf` 仅回环模式，`dist/perf.json` 仍 `met=false`），
    评审组实名复签 / 法务与安全会签 / `14` R-10 复评仍待补，**序 6 不关闭**。
    本地 `make interop` 无 external config 时仍保持 0/14 并明确失败。
  - **文档同步**：`docs/19`（新增）、`docs/README` **v1.14**（门户 45 篇）、
    `12` **v1.9**、`13` **v0.17**、`14` **v1.9**、`18` **v0.7**、
    `tools/build_docs.py`（GROUPS / 横幅 / doc-19）+ 门户重建、根 `README.md`。
  - **本机门禁**：`make lint` / `make test` / `go test -race ./...` 全绿；
    `make build` 六平台成功；`make gates-strict` **6 PASS / 0 WARN / 0 FAIL**
    （90 个链接模块、0 copyleft / 0 未识别、`no known vulnerabilities`）。
  - **CI**：`eda8a6c` run `43871031642`（P0 CI）结论 **`success`**，
    含 gofmt/vet/架构守卫、单元与集成测试、六平台构建、严格门禁、
    WBS 11 回环套件与门户链接校验。
- **BT-02/BT-11 注册表翻转 + ADR-0022 性能口径修订（2026-11-16；零 L4 证据）**：
  - **注册表翻转**：依发起人裁决，`internal/interop/suite.go:Registry()` 将
    `BT-02`（与 qBittorrent 双向：对方下载 / 我方做种）与
    `BT-11`（做种策略：分享率与 seed-time 达标后停止）由 `productGap()`
    （固定 `UNSUPPORTED`）改为 `gateExternal()`——ADR-0021 路径 A 受限做种已落地
    并经 `6ee661f` 真实客户端半向验证（`BX-05/06`），「实现范围缩减」根因消除；
    两条现由隔离段真实 harness 判定，**仅 bytes_uploaded 与 announce 同源一致才 PASS**。
    `productGap()` 构造器保留供历史证据解析兼容，但 `Registry()` 已无 product-gap 用例。
    `Report.Markdown()` 边界段同步。`internal/interop/preflight.go` 的
    `externalCaseIDs()` 注释与 `preflight_test.go` / `suite_test.go` 断言同步：
    external 用例 **12 → 14** 且必须包含 `BT-02`/`BT-11`。本地 `make interop`
    现为 **23 例：9 PASS / 0 FAIL / 14 SKIPPED / 0 UNSUPPORTED，L4 仍 0/14、
    `release_gate_met=false`**——翻转的是判据归口，不是「实现落地即 PASS」。
  - **ADR-0022**：新开 `docs/adr/ADR-0022-NFR-PERF-01性能口径修订.md`，发起人裁决将
    NFR-PERF-01 门槛由「千兆内网 ≥ 80MB/s」修订为「≥ 可用链路理论上限 × 80%」。
    依据：双机隔离段 iperf3 单流 / 8 流均 **94.1Mbps ≈ 11.8MB/s**（物理链路硬限速
    100Mbps，非 TCP 窗口问题，代码配置层无解），原口径在此环境形式不可满足；
    当前百兆段门槛取 **≥ 10MB/s**，证据须附 iperf3 实测并标注「非千兆内网」，
    不得冒充千兆达标；链路升级至真千顿时修订自动失效、恢复原 80MB/s。
    验证强度下降风险由 ADR-0022 显式记录，待评审复签复核。
  - **文档同步**：`docs/12` v1.8（§3.1 注册表翻转时点注 + §5 ADR-0022 时点注，
    判据表 / 80MB/s 原文保留不改）、`docs/13` v0.16、`docs/14` v1.8、`docs/18` v0.6、
    `docs/README` v1.13、`adr/README`、根 `README.md`、门户横幅；CI 结论补记
    `1f15649` run `43791092155` = `quality: success`。**零 L4 证据**：
    `BT-01..BT-14` 仍 **0/14**（14 SKIPPED）、`release_gate_met=false`、
    `l4_executed=false`、`nfr_perf_01.met=false`、序 6 仍开放。
  - **主机 A 部署脚本校准**：`tools/l4_lab_setup_hostA.sh` EOL apt 源正则改为
    凡 `/ubuntu` 仓库路径（含 aliyun/tuna/163 等第三方镜像）一律 rewrite 到
    `old-releases.ubuntu.com`（ERE 下 `\?` 实为字面问号，已踩坑并修正）。
- **序 6 治理复签与 L4 实验室可执行化启动（2026-11-16；零 L4 证据）**：
  - **治理**：项目发起人指令「继续推进，争取 14/14 PASS」，选定**判据不动、搭真实
    双机实验室**路线（B / C 未被选择，`12` §3.1 判据不修改）。ADR-0021 依 `17` §1
    P0 先例由发起人指令代行复签（**如实标注非实名会签**），新增决议记录 R5，
    `docs/18` §六 签署表填写（法务口径：libtorrent / aria2-next 为实验室外部工具，
    非项目依赖、不入库、不分发）、§九 收口清单同步；`adr/README` 索引更新。
    `14` R-10 重评**维持「中」**（缓解已落地、真实 L4 未实测），等级口径由
    「待重评」改「2026-11-16 重评维持」。
  - **实验室工具链**：本机装真实 chihaya（`~/l4lab/bin/chihaya`）。构建口径如实记录：
    上游 `2f794401e4`（2023-06-15）+ 唯一补丁为剔除 `cmd/chihaya/config.go` 的
    JWT prehook blank import——其依赖 `SermoDigital/jose` 在 Go 1.24+ 下 init 注册
    SHA3 即 panic，实验室不用该 prehook，announce / scrape / UDP 逻辑零改动；
    本机回环冒烟 HTTP announce / scrape 均 200 + 合法 bencode。新增
    `tools/l4_lab_runbook.md`（双机拓扑、时间同步、`LAB-*` 核验矩阵、执行顺序）、
    `tools/l4_lab_setup_hostA.sh`（Debian/Ubuntu 一键部署 qbittorrent-nox /
    transmission-daemon / chrony / tcpdump + chihaya 构建指引）与
    `tools/chihaya_lab.yaml`（实验室配置模板，监听 0.0.0.0、关 IP 伪装）。
  - **preflight 实测**：`UD_L4_TRACKER` 指向真实 chihaya 后 `make l4-preflight` 为
    **PRESENT 1 / MISSING 5 / UNVERIFIED 3**（此前 PRESENT 0 / MISSING 6 /
    UNVERIFIED 3）；`LAB-CAPTURE` 因本机 tcpdump 非特权转 `UNVERIFIED`（不放宽判据，
    实验室 root 下核验）。
  - **零 L4 证据**：`BT-01..BT-14` 仍 **0/14**、`BT-02` / `BT-11` 仍 `UNSUPPORTED`、
    `release_gate_met=false`、`l4_executed=false`、`nfr_perf_01.met=false`、序 6 仍开放；
    12 条真实 harness 实装、真实 external config、双机拓扑实测与 NFR-PERF-01 均为
    后续工作包。文档同步 `docs/README` **v1.12** / `14` **v1.7** / `18` **v0.5** /
    ADR-0021（R5）/ `adr/README` / 根 `README.md`。
  - **已核验 CI 结论补记**（滚动时点口径）：`c2aaaa4` run `43696583823` 与本包
    `e893c82` run `43789016799` 经核验均为 **`quality: success`**；不改写历史时点值。
- **回环证据重试语义校准与已核验 CI 结论补记（2026-11-15，序 6 续包；零 L4 证据）**：
  - **缺陷修复**：`cmd/udinteropx/main.go` 的 `runWithLoopbackRetry` 原先在
    「首次 `FAIL` → 二次 `SKIPPED` / `UNSUPPORTED`」时仍追加「第 2/2 次真实执行通过」，
    与 `SKIPPED` / `UNSUPPORTED` 从不为 PASS 的硬口径不符；现按最终状态措辞：只有最终
    `PASS` 写「通过」，其余状态保留原始状态并标注「不记为 PASS」，状态本身从不被升级。
    重试次数、连续失败仍保留 `FAIL` 的证据串与 sha256 校验均不变。
  - 新增 `cmd/udinteropx/main_test.go` 5 条确定性回归：首试 `PASS` 不重试、
    `FAIL → PASS` 有界 1 次重试且证据串记录次数、`FAIL → SKIPPED` / `UNSUPPORTED`
    不得措辞为通过、非 `FAIL` 状态立即返回、连续 2 次 `FAIL` 保留 `FAIL` 与原始 `Error`。
    退避抽为 `loopbackRetryDelay` 以便回归用例零等待，生产默认仍 750ms；harness 行为与
    套件 v1 注册表**不变**，不新增任何 PASS 证据、不改 `BT-01..BT-14` 计数。
  - **已核验 CI 结论补记**（按 `15` §4 规则④的滚动时点口径）：`6ee661f`
    （路径 A 做种半向真实客户端证据与 `listenHost` 地址族缺陷修复）run `43695083832`
    经 `gh run view` 核验为 **`quality: success`**；如实追加，不改写上文
    `915f783` / `43604236428` 等历史时点值。
  - 文档同步：`docs/README` **v1.11**、根 `README.md`、`12` **v1.7**（§3.1.1 措辞与
    §9 用例矩阵）、`13` **v0.15**（§4 CI 结论）、`tools/build_docs.py` 首页横幅与
    `docs/_site/index.html`；门户仍 **43 篇 / 0 断链**。`03` / `11` / `14` / `16` / `18`
    的 v1.10 时点注所述事实（`BX-01..BX-06` 6/6 PASS、单向回环、不伪造 PASS）在本次
    修复后**更成立**，故不改写历史版本说明。
  - **零 L4 证据**：`BT-01..BT-14` 仍 **0/14**、`BT-02` / `BT-11` 仍 `UNSUPPORTED`、
    `release_gate_met=false`、`l4_executed=false`、`nfr_perf_01.met=false`、序 6 仍开放。
- **路径 A 做种半向真实客户端证据与 `listenHost` 缺陷修复（2026-11-15，序 6 编码续包；零 L4 证据）**：
  - `internal/interop/seeding_e2e_test.go`：新增 CI 可跑的确定性回环回归
    `TestPathASeedingServesPeerAfterRunReturns`——独立 seeder → 带 `SeedingPolicy` 的
    `bttransport` 下载 → **关闭原 seeder** → 第二个真实 BT 线协议 peer 从 ud 的后台做种
    会话下载并校验字节；同时验证诚实 `-UD` PeerID 出现在 tracker announce、
    `Transport.Close()` 释放监听且可重复调用。不改 `Registry()`、不加套件用例计数。
  - **缺陷修复**：`internal/transport/bt/seeding.go` 的 `applySeedingPolicy` 在
    `bt.listenHost` 为 IPv4 字面量时会对 `tcp6` 套接字返回同一地址，导致
    `torrent.NewClient` 报 `listen tcp6: address 127.0.0.1: no suitable address found`；
    现按 IP 字面量地址族设置 `DisableIPv6` / `DisableIPv4`，默认空值行为不变；
    新增 `TestClientConfigListenHostRestrictsAddressFamily`，并校准既有
    `TestClientConfigAppliesSeedingPolicy`。上传速率仍只由 `scheduler.Limiter` 管理。
  - `cmd/udinteropx/seeding.go` + `tools/interop_libtorrent_leecher.py`：外部 harness
    扩为 **`BX-01..BX-06` 6/6 PASS**。`BX-05`（ud → aria2-next 3.0.9）与 `BX-06`
    （ud → libtorrent 2.1.1）由真实第三方引擎先做种、我方带路径 A 策略下载并保留
    后台做种会话，**停掉原 seeder** 后由新的真实 leecher 从我方下载并 sha256 校验，
    首次取得**我方做种半向**真实客户端证据；`make interop-external` 可复跑，
    `make interop` 自动合并进 `dist/INTEROP.md`。python 绑定仍为可选外部工具、
    非项目依赖、不入库、CI 不跑。
  - harness 稳定性：针对既有回环首试 MSE / 握手竞态，`cmd/udinteropx` 增加
    `runWithLoopbackRetry`——仅对 `FAIL` 做**有界 2 次真实执行重试**（每次新 seeder /
    tracker / 下载且必须重新通过 sha256），重试次数写入证据串；`SKIPPED` /
    `UNSUPPORTED` 立即返回，连续 2 次失败仍保留 `FAIL`，**绝不伪造 PASS**。
  - 口径校准：`internal/interop/suite.go` 外部证据 Markdown 段、`cmd/udinteropx`
    头部注释与 `libtorrent.go` 注释中「我方做种按 ADR-0004 明确不做」等过期表述改为
    区分两个方向；`BT-02` / `BT-11` 仍按 `ScopeProductGap` 固定 `UNSUPPORTED`。
  - **零 L4 证据 / 零判据改动**：六条 BX 均为**单向、回环、自托管 tracker**补充证据，
    不计入 `BT-01..BT-14`；`BT-01..BT-14` 仍 **0/14**、`BT-02` / `BT-11` 仍
    `UNSUPPORTED`、`release_gate_met=false`、`l4_executed=false`、
    `nfr_perf_01.met=false`、序 6 仍开放。评审组复签 / 法务口径 / `14` R-10 重评 /
    真实双向客户端矩阵 / NFR-PERF-01 双机基准仍待补。
  - 文档同步：`docs/README` **v1.10** / `03` **v0.10** / `11` **v1.2** / `12` **v1.6** /
    `13` **v0.14** / `14` **v1.6** / `16` **v0.10** / `18` **v0.4**、ADR-0004 / ADR-0021
    时点口径注、`config.example.yaml`、根 `README.md`、`tools/build_docs.py` 首页横幅与
    `docs/_site/index.html`；门户仍 **43 篇 / 0 断链**。**不改**已签署的 `17` /
    ADR-0020 证据与历史版本说明中的时点数值。另按惯例补记 `915f783` 的 CI run
    `43604236428` 经核验为 **`quality: success`**（时点口径追加，不改写原条目）。
- **ADR-0021 路径 A 补齐实现（2026-11-14，序 6 编码部分；评审组复签待补）**：
  项目发起人 2026-11-14 明确指令「按 A 方案补齐，同时修改相关文档」，
  ADR-0021 按 `15` §4 规则④由 `Proposed` 转 `Accepted（路径 A）` 并同步 ADR 索引
  （决策事实、权限依据与边界见 ADR-0021 决议记录）。**如实标注**：`docs/18` §六
  评审组五方签署表仍全部留白，本次为发起人指令裁决，**不冒充**三方会签。
  - `internal/transport/bt/seeding.go`：`SeedingPolicy` / `SeedingManager` /
    `SeedingObserver`；受限做种、分享率与 `seed-time`（任一达标即停止，且有界校验）、
    上传槽（`EstablishedConnsPerTorrent`）、UPnP / NAT-PMP（`NoDefaultPortForwarding`）、
    诚实 PeerID 强制回落（非 `-UD` 前缀配置自动拒绝）、**私有种子恒不上传且恒禁
    DHT / PEX**（BT-08 / ADR-0017「不承诺 PT」）。
  - `internal/transport/bt/bt.go`：下载完成后把 client 与临时目录移交后台做种会话，
    `Run` 立即返回不占用 scheduler worker；`Transport.Close()` 与 daemon 生命周期 ctx
    保证会话回收与临时目录清理。**未显式注入 policy 的 Transport 仍为 leech-only**，
    WBS 11 的 9 条 L3 回环用例行为不变。
  - `internal/config/config.go`：`bt.*` 由「预留不生效」改为**生效**；新增 `seedEnabled` /
    `maxUploadSlots` / `listenPort` / `listenHost`，默认 `peerID` 改诚实 `-UD0000-` 前缀，
    `Validate()` 拒绝「无界做种目标」与非法槽数 / 端口，`UD_BT_*` 环境变量可覆盖。
    **上传字节速率仍只由 `scheduler.Limiter` 一个权威管理**，bt 层不设第二 limiter。
  - `internal/app/daemon.go` + `internal/app/seeding_observer.go` +
    `internal/transport/registry.go` + `internal/store/tasks.go`：策略 / 观察者 /
    生命周期接线；`transport.Registry.Close()` 统一回收；
    `tasks.bytes_uploaded` 与 tracker announce 同源（取 `Client.ConnStats()`），
    并驱动 `Completed → Seeding → Completed` 的**条件**状态迁移
    （`TrySetSeedingState`，不抢在 `scheduler.Commit` 之前）。
  - 回归测试四层：`bt/seeding_test.go`（策略 / clientConfig 字段 / 私有种子硬约束 /
    诚实 PeerID / 达标判定 / 会话与 manager 清理）、`config/seeding_test.go`
    （默认值 / 有界校验 / env）、`store/seeding_test.go`（条件写入）、
    `app/seeding_observer_test.go`（计数与状态流转）。
  **零 L4 证据**：`BT-01..BT-14` 仍 **0/14**，`BT-02` / `BT-11` 仍 `UNSUPPORTED`
  （实现具备 ≠ 已通过真实客户端矩阵验证），`release_gate_met=false`、
  `l4_executed=false`、`nfr_perf_01.met=false`、序 6 **仍开放**。
- **L4 证据包完整性审计器（2026-11-14，`internal/interop/audit.go` + `cmd/udl4audit` + `make l4-audit`）**：
  独立重算**已产出**的 `ud.interop/v1` 证据包，9 项 `AUD-*` 检查：规范门禁覆盖
  （`BT-01..BT-14` 恰 14 条、无缺 / 多 / 重）、`status` 枚举与记录卫生、7 项汇总计数、
  按 case 重算 `release_gate_met`、`release_gate_reason` 不得虚假宣称全矩阵 PASS、
  声称门禁达标须 `require_external=true`、PASS 门禁用例须带 `reason` / `evidence`、
  补充证据 status 合法。产出 `dist/l4_audit.json`（`ud.l4-evidence-audit/v1`）与
  `dist/L4_AUDIT.md`，结论仅 `CONSISTENT` / `INCONSISTENT` / `MISSING`；
  `--require-gate-met`（`L4_AUDIT_FLAGS`）可追加门禁判定。
  **完整性结论不是 L4 测试结果、不是发布门禁**：不执行用例、不启动进程、不访问网络、
  不写回被审计文档，`l4_executed_by_audit=false` 恒真，审计器从不设置
  `release_gate_met`（只做重算）；`make interop-gate` 仍是唯一权威门禁，本目标**不接入 CI**。
  实测：示例 stub 产物审计为 `CONSISTENT` 且重算 `release_gate_met=false`（9/9 检查 OK）；
  手工篡改（`release_gate_met=true` + `summary.pass=99`）为 `INCONSISTENT`，精确报出
  `AUD-SUMMARY` / `AUD-GATE-VERDICT`；缺文件为 `MISSING`；`--require-gate-met` 非零退出。
  新增 `internal/interop/audit_test.go` 11 项 `TestAudit*` 回归（含全 PASS 但缺证据、
  本地回环报告冒称门禁达标、伪「全部 PASS」原因串、重复 / 缺失门禁用例、非法 status）。
  **零新增 L4 证据**：`BT-01..BT-14` 仍 **0/14 PASS**，`release_gate_met=false` 不变。
- **序 6 评审与 L4 实验室准备材料包（2026-11-14，`docs/18` v0.1，待评审签署）**：
  新增 [18 序 6 评审与 L4 实验室准备材料包](docs/18-序6评审与L4实验室准备材料包.md)，
  把散落在 ADR-0021 / `12` §3.1.2 / `14` §11 的评审要件集中为可直接使用的材料包：
  §二 8 条已核实事实清单（引用不复制）、§三 A / B / C 三条互斥路径对照、
  §四 三路径影响面逐项核对表、§五 **留白**豁免清单模板、§六 **留白**评审签署表
  （签署人 / 日期 / 路径 / 豁免清单版本）、§七 隔离千兆内网拓扑与抓包留痕方案
  （与 `internal/interop/preflight.go` 的 9 项 `LAB-*` 一一映射）、§八 NFR-PERF-01
  双机基准执行方案、§九 签署后收口清单、§十 8 条硬不变量。
  **不代行裁决**：不预选路径、不预填签署人、不改判据、不改产品代码与门禁语义，
  **不产出 L4 证据**；空表未签署无效；`Proposed` 的 ADR-0021 仍不构成实现依据。
- **P1 序 6 阻塞根因定档与 L4 实验室可执行化（2026-11-13，序 6 仍不关闭）**：
  本轮**不新增任何 L4 通过证据**；`dist/interop.json` 仍 `release_gate_met: false`、
  `BT-01..BT-14` **0/14 PASS**，`make interop-gate` 继续明确失败。
  - **跨 ADR 一致性审计（本轮最重要的结论）**：核对已批准 ADR-0004「后果 / 后续动作 /
    合规与验证」、ADR-0017 决策 4、`03` FR-INT-01 / FR-INT-04 / FR-TRN-05（优先级 M）、
    `01` 章程互操作验收行、`05` §5 状态机、`07` / `13` §7 的 `seedRatio` /
    `seedTimeMinutes` / `enableUPnP` 与 `14` R-10，它们都**要求**显式实现做种礼仪；
    而 WBS 7 起 `internal/transport/bt.clientConfig` 固定 `NoUpload=true` / `Seed=false`，
    且 `internal/config.BTConfig` 至今仍是「parsed for forward compatibility, ignored」
    的预留字段。因此现有文档把现状表述为「ADR-0004 不做种边界」属**误归属**：
    ADR-0004 把 `Seed=false` 记为「须自行补偿的库默认」并列入后续动作，
    ADR-0017 更把「保持库默认 `Seed=false`」判为否决方案。按 `15` §4 规则②，
    正确定性是「实现相对已批准 ADR 与 M 级需求的**未签署范围缩减（合规差距）**」为根因，
    判据冲突与实验室条件缺失并存；`12` §3.1 原处置路径 1「仅修订判据」不足以消除该差距。
  - **新增 ADR-0021（Proposed，待评审、未生效）**：按 `docs/adr/README.md` 与 `15` §4
    的变更流程提交裁决提案，列 A（补齐实现）/ B（修订范围，须把 ADR-0004 / ADR-0017
    标 `Superseded` 并同步 `03` 等九处）/ C（保持开放）三条互斥路径及各自必须变更的文件；
    明确签署前不得改判据、不得把 `UNSUPPORTED` 记为 PASS 或以「豁免」计入通过数。
    0001–0020 仍全部 `Accepted`；`17` / ADR-0020 正文与历史版本说明中的时点数值未改写，
    既有误归属只追加带日期的「时点口径 / 阻塞校准」注。
  - **L4 实验室可执行链路**：`internal/interop/preflight.go` + `cmd/udl4preflight` 产出
    `dist/l4_preflight.json` / `dist/L4_PREFLIGHT.md`（schema `ud.l4-preflight/v1`），
    清点 9 项前置条件；`l4_executed=false` / `release_gate_met=false` 为硬不变量，
    `PRESENT` 绝不等于 PASS，物理拓扑 / 评审签署 / NFR-PERF-01 恒列为人工签署项。
    `tools/l4_external_config.example.json` + `tools/l4_harness_stub.py` 演示
    `--require-external --external-config` 的接线，stub **只能输出 `SKIPPED`**；
    新增 `make l4-preflight` / `l4-preflight-check` / `l4-run` 三个目标（均不接入 CI），
    用示例配置跑 `l4-run` 必须是 0/14 PASS 且失败。新增 6 条 `TestPreflight*` 回归用例，
    `internal/interop` 覆盖率 **87.1% → 87.9%**；门禁包仍 **16**、链接模块仍 **90**。
    本机全套复跑：`make build` → `make lint` / `make test` / `make interop` /
    `make gates-strict` **6 PASS / 0 WARN / 0 FAIL** / `go test -race ./...` /
    `build_docs.py --check`（42 篇 / 0 断链）全部 exit 0；
    `make interop` 仍 9 PASS / 0 FAIL、L4 **0/14**，`make interop-gate`
    与 `make l4-preflight-check` / `make l4-run` 均按预期明确失败。
    本提交触发的 CI `quality` 结论以对应 workflow run 为准，**不预先记为通过**。
- **P1 WBS 11 续：真实命名客户端证据、L4 判据冲突登记与 1GiB 基准（2026-11-13）**：
  序 6 仍**不关闭**；本轮只交付「可复跑的真实补充证据」与「阻塞项的正式登记」，
  不改写 `12` §3.1 的 L4 判据、不改写 ADR-0004 决策，`dist/interop.json` 的
  `release_gate_met` 仍为 `false`。
  - **真实 libtorrent 2.1.1 跨客户端证据（BX-03 / BX-04）**：新增可选外部 seeder
    `tools/interop_libtorrent.py`（Python `libtorrent` 绑定仅装在本机 `/tmp`，**非项目依赖**、
    不入库、CI 不跑）；`cmd/udinteropx` 扩展为双引擎 harness，真实 libtorrent 做种 →
    项目真实 `bttransport` 下载，`.torrent`（320KiB）与纯磁力 BEP 9（224KiB）均 sha256 一致，
    自建 HTTP tracker 观测到外来 PeerID 的 `left=0` announce。加上既有 aria2-next 的
    BX-01 / BX-02，`dist/interop_external.json` 现为 **4 条真实跨客户端证据（2 引擎，4/4 PASS）**，
    由 `make interop` 合并进 `dist/INTEROP.md`；引擎缺失时显式写 `SKIPPED` / `UNSUPPORTED`，
    `loadExternalEvidence` 现接受 `UNSUPPORTED` 并补回归用例（`TestLoadExternalEvidenceStatuses`）。
  - **握手可复跑性修复**：默认 MSE 优先在跨引擎回环下出现偶发超时（8 用例 2 次超时）；
    新增 `bt.WithPlainHandshake()` **仅实验室选项**（`HeaderObfuscationPolicy.Preferred=false`，
    `RequirePreferred` 仍 false、加密回退仍在，`udd` 默认策略与 ADR-0004 边界零改动），
    补 `TestClientConfigHandshakePolicy`；改后连跑 3 轮 × 4 用例 **12/12 PASS**。
    另加 `UD_INTEROPX_TIMEOUT_S` 与「等待做种方 `left=0` announce」就绪同步，
    使 BEP 9 元数据用例不再依赖进程启动竞态。
  - **L4 门禁可满足性冲突（正式登记，待评审）**：按 ADR-0004 / WBS 7 边界
    （`NoUpload=true` / `Seed=false`），`BT-02`（我方做种）与 `BT-11`（分享率 / seed-time）
    恒 `UNSUPPORTED`，`BT-03` 的「双向」半向同样不可达，故「14/14 PASS 才关闭序 6」
    在现有产品边界下**形式不可满足**——即便补齐隔离网络、命名客户端、抓包与千兆基准也不会成立。
    已同步 `12` §3.1 / `14` §11 与 ADR-0004 实现落地段，列出三条处置路径
    （修订判据为非目标豁免 / 扩大产品范围实现做种 / 保持开放并标记阻塞）；
    **评审签署前不得把 `UNSUPPORTED` 计为 PASS 或豁免**。BX-03 / BX-04 虽来自点名引擎，
    但只是单向回环补充证据，不解除冲突、不计入 L4。
  - **NFR-PERF-01 可复跑基准**：新增 `cmd/udperf` + `make perf`，走真实
    `internal/transport/http` Range 路径，1GiB / 4 连接 / 256KiB 分片，产出
    `dist/perf.json`（`ud.perf/v1`）。本机回环实测 **885.83 MB/s（1.156s，4096 片逐片字节数校验）**，
    但证据固定写 `nfr_perf_01.met: false`——回环无双机千兆链路，不得当作 ≥ 80MB/s 达标；
    与既有 32MiB P0 微基准（同样标注 NOT NFR-PERF-01）互补。CI 不跑 `make perf`。
  - 门禁口径不变：**16 个门禁包 / 90 个链接模块 / 0 copyleft / 0 未识别 /
    `make gates-strict` 6 PASS / 0 WARN / 0 FAIL**；`make interop` 仍
    **23 例 9 PASS / 12 SKIPPED / 2 UNSUPPORTED，L4 0/14**，`go test -race ./...` 全绿。
    新增代码零新增依赖、零 cgo；`17` 与 ADR-0020 已签署证据原值不改。
- **P1 WBS 11：互操作套件 v1（2026-11-11）**：P1 序 6 的**套件与证据链**落地于
  `internal/interop/` + `cmd/udinterop`（纯 Go、零新增依赖、零 cgo、仅回环）。
  **重要口径：`BT-01..BT-14` 全矩阵仍未执行，序 6 / WBS 11 不关闭**——本地环境没有
  真实 qBittorrent / Transmission、抓包设备与千兆内网，套件明确报 SKIPPED，
  绝不把本地回环证据冒充 L4 发布门禁通过。
  - 框架 `suite.go`：14 条 L4 门禁用例（docs/12 §3.1 原序）+ 9 条 `L3-xx` 本地自动化用例
    的注册表；`Scope`（in-process / external / product-gap）与
    `Status`（PASS / FAIL / SKIPPED / UNSUPPORTED）显式分类；每例独立超时与 panic 回收；
    `ReleaseGateMet` 只在 14 条 L4 全 PASS 时为真，`SKIPPED` / `UNSUPPORTED` 不计入通过。
  - 证据链：`dist/interop.json`（`ud.interop/v1`）+ `dist/INTEROP.md`，含逐条结论、
    机器可读 evidence 与门禁判定；CI 新增 `make interop` 步骤并上传两份证据。
  - 外部 harness：`UD_INTEROP_EXTERNAL=1` + `--external-config <json>` 可按用例 ID
    接入真实客户端脚本，脚本最后一行输出
    `{"status":"PASS|FAIL|SKIPPED|UNSUPPORTED","reason":"...","evidence":{...}}`；
    未配置则 SKIP，`--release-gate-only` / `make interop-gate` 在门禁未满足时**明确失败**。
  - 自建 tracker fixture：BEP 3 HTTP/HTTPS tracker（announce / scrape / compact IPv4 6B 与
    IPv6 18B peer list / URL 路径 passkey / 503 与挂起故障注入）、BEP 15 UDP tracker
    （connect / announce / scrape / IPv4 与 IPv6 compact / 过期 connection id 拒绝），
    全部纯 Go、不依赖 chihaya / opentracker 二进制。
  - 9 条本地用例（`make interop` 全绿，0 FAIL）：HTTP/UDP/HTTPS tracker 协议与故障降级、
    独立 seeder ↔ `bttransport` 真实 BT 线协议端到端下载（sha256 一致 + tracker 观测到
    announce）、BT 握手抓包诚实 PeerID、纯磁力经 BEP 9 取元数据、多文件选择下载、
    `File.NewReader` 4KiB 分片首片时延。
  - `BT-02` / `BT-11` 标为 `UNSUPPORTED`（我方做种 / 分享率策略按 WBS 7 边界明确不做），
    不是遗漏也不是「通过」；其余 12 条 `SKIPPED`。
  - 门禁：`tools/gates.py:THRESHOLDS` 纳入 `internal/interop`（≥80%，实测 **87.1%**），
    受门禁包 15 → **16**；链接模块仍 **90**（本包未引入任何新依赖）。
    `make lint` / `make test` / `make build` / `make interop` / `make gates-strict`
    全绿（6 PASS / 0 WARN / 0 FAIL），`go test -race ./...` 全绿。
  - **真实第三方客户端互通证据（BX-01 / BX-02）**：本机已安装 MotrixNext.app
    （Rayburst 4.1.1），其打包的 **aria2-next 3.0.9** 是真实 BitTorrent 引擎。新增
    `cmd/udinteropx` 驱动**项目真实 `bttransport`**（与 `udd` 同一份代码）作为下载方，
    对真实 aria2-next 做种方在回环自托管 BEP 3 tracker 下完成下载并校验 sha256，产出
    **真实跨客户端互通证据**：
      - **BX-01**（.torrent 下载）：PASS，payload 307200 字节，sha256
        `4154066137427a17f67aba42a3e30397db1f652a419bb0b5bcee07fc9e40f68b`；
      - **BX-02**（纯磁力 / BEP 9 取元数据）：PASS，infohash
        `a14619d1f0bfdb632c6af2d3cbd0629b60806c68`，204800 字节 hash 校验一致。
    证据：`dist/interop_external.json`（机器可读，由 `cmd/udinteropx` 产出）；`dist/INTEROP.md`
    的「真实跨客户端证据」段由 `make interop` 自动合并 `interop_external.json`。
    **重要边界**：aria2-next **不点名于** `BT-01..BT-14`（那些用例点名 qBittorrent /
    Transmission / libtorrent，且要求抓包 + 千兆内网），故 BX 证据**不计入 L4 发布门禁**，
    仅作补充；`release_gate_met` 维持 `false`，序 6 / WBS 11 维持开放。
- **P1 WBS 10：插件引擎 / 规则通道 v1（2026-11-10）**：P1 序 5 交付，按 ADR-0014 与 `08`
  落地 `internal/plugin/`（纯 Go、零 cgo、单二进制不变）；新增 `github.com/dop251/goja`
  （MIT）及 3 个传递依赖，链接模块 86 → **90**，0 copyleft / 0 未识别，
  `make gates-strict` **6 PASS / 0 WARN / 0 FAIL**。
  - `manifest.go`：`ud.plugin/v1` 清单严格校验——未知 JSON 字段拒绝、name/version/
    apiVersion/type/runtime/entry 校验、`match` 正则与内置哨兵冲突检测、路径穿越与
    非普通文件 / 超大文件拒绝、能力白名单、`allowedHosts` 必填、限额越界拒绝。
  - `scan.go`：ES5.1 静态扫描（拒绝 `import` / `require` / `fetch` / `async` / `class` /
    箭头函数 / 模板字符串 / `__proto__` 等），并把 `08` 的 `export` 糖改写为严格 ES5 IIFE；
    注释、字符串与正则字面量先空白化以消除误报，危险成员名另对原始源码复查。
  - `signature.go`：规则包 Ed25519 fail-closed 签名，
    `payload = SHA256(canonicalManifestJSON || 0x00 || entryScriptBytes)`；写回清单时
    保留作者原有 JSON 键、只设置 `signature`，避免规范化载荷漂移。
  - `runtime.go`：每次解析新建 goja VM，仅注入获准的 `ctx.http` / `ctx.log` /
    `ctx.base64` / `ctx.sha256`；`vm.Interrupt` 看门狗实现 `timeoutMs`、调用栈上限、
    Go panic 回收与输出 JSON 限额；`RULE_OUTDATED` 转 `E_CAPABILITY_UNAVAILABLE`
    并交由兜底 Resolver，**不静默丢弃任务**。
  - `http.go`：唯一出网通道 `ctx.http.get(url, {headers})`；重定向手工跟进以逐跳校验
    `allowedHosts` + `URLGuard.Check`，`Content-Length` / `io.LimitReader` /
    `maxResponseBytes` 三重限额，敏感请求头、CRLF / NUL 与超量响应头拒绝。
  - `resource.go`：把插件输出收敛为冻结的 `core.Resource`，校验文件/镜像数量、
    路径（`PathGuard`）、负值与偏移、镜像 scheme / 白名单 / SSRF、摘要算法与 piece 数、
    身份字段；插件无法设置 engine、凭据或任务状态。
  - `manager.go`：独立规则通道热更新（根目录单包或每子目录一包）、目录指纹轮询、
    原子快照切换、坏包拒绝并保留上一可用版本、`Watch(ctx)` 与失败回退；
    `fixture.go` 提供录制回放 fixture，`udctl plugin test` 全程离线、不连真实站点。
  - 配置与装配：`config.PluginsConfig`（`verifySignature` / `runtime` / `dir` /
    `trustedKeys` / `insecure` / `reloadInterval`，含 `UD_PLUGINS_DIR`、
    `UD_PLUGINS_RUNTIME`、`UD_PLUGINS_RELOAD_INTERVAL`、`UD_PLUGIN_KEYS`、
    `UD_PLUGIN_INSECURE`）；`udd --insecure-plugins` 与 `udctl daemon --insecure-plugins`
    仅供本地开发。Resolver 链固定为
    `metalink → link-wrapper → magnet → torrent → [plugin] → direct-http → direct-file → unsupported`
    （安全敏感内置优先；插件置于通配 `direct-http` 之前，否则站点规则不可达）。
  - `cmd/udctl/plugin.go`：`udctl plugin scaffold|keygen|sign|verify|test|list` 六个
    **离线**子命令，覆盖脚手架、密钥生成、签名 / 验签、录制回放与规则列表。
  - 测试：新增 8 个 `internal/plugin/*_test.go`、`cmd/udctl/plugin_test.go`、
    `internal/config/plugins_test.go` 与 `app/daemon_test.go` 规则通道用例，覆盖
    ADR-0014 §7 全部安全用例（死循环超时强杀、fs/env 越权、白名单外请求、私网 URL、
    超大响应、重定向跳板、签名篡改、match 冲突、输出 SSRF / 路径穿越）；
    `internal/plugin` **88.4%**、`internal/app` **89.3%**，15 个门禁包全部达标，
    `go test -race ./...` 与六平台 `make build` 全绿。
  - **明确边界**：goja 是**软隔离**（无 per-VM 堆上限，靠宿主可见预算与中断），
    不等价于 WASM / Extism 硬隔离；不承诺 Node/Deno / 浏览器扩展脚本兼容、现代 JS 语法、
    官方规则仓库与密钥轮换、规则失效遥测、真实站点 L4 互操作矩阵（WBS 11）、
    `lux` / `yt-dlp` 兜底与插件 REST 管理端点。
- **P1 WBS 9：FTP / FTPS / SFTP Transport（2026-11-10）**：序 3 交付，前置 `10` 许可证检查
  与 `11` 安全检查均已通过，4 个新链接模块全部为 ISC / BSD，0 copyleft、0 未识别。
  - `internal/transport/ftp`：基于 `jlaffaye/ftp` v0.2.4 的 `core.Transport`。
    `ftp://` 明文；`ftps://` 默认 990 为隐式 TLS，`ftps://host:21/...` 走 RFC 4217
    `AUTH TLS` 显式升级；`REST` 偏移续传，`SIZE` 探测，每个镜像在使用时重过 SSRF Guard。
    **控制与被动数据连接共用 `DialWithDialFunc`**，使数据通道规则可强制执行。
  - `internal/transport/sftp`：基于 `pkg/sftp` v1.13.12 + `golang.org/x/crypto/ssh` v0.58.0。
    `ReadAt` 随机读、`Stat` 探测、按 `ChunkWriter` 分片写回；主机密钥按凭据 pin，
    支持密码 / 私钥（含 passphrase）两种认证。
  - `internal/transport/tputil`：共享的凭据解析（`core.CredentialRef` → JSON）、
    `ParseTarget` 目标校验、分片驱动循环、Budget 计费与错误分类；会话并发上限 8。
  - `internal/resolve/direct_file.go`：`DirectFile` Resolver 把 `ftp/ftps/sftp` 直链
    归为单文件 `Resource`（此前 `ParseInput` 已分类为 `InputURL`，但落到 `Unsupported`）；
    与 `DirectHTTP` 共用 `newDirectResource`，不改动 ADR-0013 冻结的 `Resource` 结构。
  - `internal/app/daemon.go`：`newBuiltinTransports()` 固定注册顺序
    `http → ftp → sftp`（capabilities 列表向后兼容），Resolver 链加入 `direct-file`。
  - 凭据经 `POST /api/v1/credentials` 的 `{"value": "<JSON 字符串>"}` 写入，任务用
    `credentialRef` 引用。JSON 字段：`username`、`password`、`private_key`、
    `passphrase`、`host_key`、`host_key_fingerprint`；FTP 无 ref 时走匿名登录。
  - 回环集成测试起真实 FTP 与 SSH+SFTP 服务端，验证 probe / 分片下载 / PASV 投毒 /
    错误主机密钥拒绝；新增 4 个测试文件，13 个门禁包全部达标
    （`transport/ftp` 83.3%、`transport/sftp` 83.5%、`tputil` 90.0%）。
- **门禁新增 3 个受控包**：`tools/gates.py:THRESHOLDS` 纳入 `internal/transport/ftp`、
  `internal/transport/sftp`、`internal/transport/tputil`（各 ≥ 80%），新 Transport 不得
  低于既有门槛合入。
- **P1 WBS 7：BT / 磁力 Transport（2026-11-10）**：序 4 交付，最大工作包；前置 `10` 许可证检查
  （MPL-2.0）与 `11` 安全检查均已通过；86 个链接模块、0 copyleft、0 未识别，`make gates-strict`
  6 PASS / 0 WARN / 0 FAIL。
  - `internal/resolve/magnet.go`：`ParseMagnetUri` 解析 magnet，构建 `KindTorrent` Resource
    （v1 infohash、单层 tracker 透传、空 Mirrors）。
  - `internal/resolve/torrent.go`：`.torrent` 解析支持 raw bencode / base64 / 本地下载；
    远端 `http(s)://*.torrent` 显式 `E_CAPABILITY_UNAVAILABLE`（由 `DirectFile` 负责），
    私有种子标记 `Private=true`。
  - `internal/transport/bt`：基于 `anacrolix/torrent v1.62.0` 的 `core.Transport`。
    `clientConfig` 对私有 Resource 强制 `NoDHT` / `DisablePEX` / `NoUpload` / `Seed=false`
    （BT-08）、诚实 PeerID（`-UD0000-` 前缀 + 6 随机字节，ADR-0017）；`file.NewReader()`
    流式把选中文档字节拷回 `ChunkWriter`，受 `core.Budget` 约束；tracker 仅由 Resolver 透传；
    metadata 仅经 BEP 9 从直连对等点获取。
  - `internal/app/daemon.go`：Resolver 链加入 `magnet` / `torrent`，Transport 注册加入 `bt`
    （`http → ftp → sftp → bt`）；`Unsupported` 不再把 magnet/torrent 标为「P1 未实现」，
    未匹配到内置 Resolver 时返回明确「注册链」错误。
  - 测试：`magnet_test.go` / `torrent_test.go` 单测；`bt_test.go::TestTransportLoopbackDownload`
    起真实回环 BT 服务端经 bootstrap peer 完成协议下载（anacrolix ↔ anacrolix），
    `bt_cover_test.go` 补纯磁力 Probe 取元数据、越界分片 Release 与各类错误分支。
    `transport/bt` 覆盖率 80.8%，14 个门禁包全部达标。
  - 依赖与合规：新增 `anacrolix/torrent` v1.62.0（MPL-2.0）及 `anacrolix/*` 传递依赖；
    **未修改任何 MPL 文件，修改清单为空**，仅随包附 `LICENSE` + `THIRD_PARTY_NOTICES.md` 即合规；
    `pion/stun/v3` 随 anacrolix 最低版本要求升至 v3.1.5（修复 BT 调用链可达漏洞），复扫
    `No vulnerabilities found`。
- **P1 WBS 8：链接封装解码与 Metalink 解析（2026-11-10）**：新增两个安全敏感的内置
  Resolver，纯 Go、零 cgo、零网络 I/O——只描述 `Resource`，不解包任何字节。
  - `internal/resolve/wrapper.go`：`WrapperResolver` 实现 `thunder://` / `flashget://` /
    `qqdl://` **仅解码**（ADR-0007），绝不实现 P2SP / 高速通道 / 离线下载；宽松 Base64、
    marker 校验、解码后 scheme 白名单、内嵌凭据拒绝、逐 URL 过 SSRF Guard。
  - `internal/resolve/metalink_xml.go` / `metalink_http.go` / `metalink.go`：
    RFC 5854 v4 + metalinker.org v3 XML、RFC 6249 `Link: rel=duplicate` 与
    RFC 3230/9530 `Digest` / `Content-Digest`；4MiB XML、32 镜像、DTD/实体/PI、token/深度/piece
    上限，`<metaurl>` 不作镜像，摘要按算法强度择优并统一转小写 hex。
  - `internal/store/checksum.go` + `internal/scheduler`：按清单声明算法
    （md5 / sha-1 / sha-256 / sha-512）做整文件与逐片校验；`Limits.ChunkSize` 锁定为
    `<pieces length>`，失败只重下该片。`GetResource` 只重建 `scope='whole'/'piece'` 行，
    不再把 P0 的 `scope='chunk'` 自检摘要误当 Metalink piece hash。
  - `internal/app/daemon.go`：`newBuiltinResolvers()` 固定顺序
    `metalink → link-wrapper → direct-http → unsupported`（安全敏感内置优先，`Unsupported` 兜底）；
    原先落入 `Unsupported` 的 `InputMetalink` / `InputThunder` 改为注册链错误。
  - 新增 `internal/store/checksum_test.go` 并补 `app` 注册链用例：`resolve` 90.5%、
    `store` 84.8%、`app` 87.0%，`gates.py` 的 10 个门禁包全部达标。
- **P1 首个 PR：覆盖率债清零（2026-11-10）**：新增 8 个包级测试文件，补齐此前空白或低覆盖路径——
  `resolve_test.go`（0% → 100%，注册表优先级/文件名回退/SSRF 透传/阶段化降级）、
  `app/daemon_test.go`（0% → 86.6%，真实起停守护进程、discovery 生命周期、配置错误）、
  `api/extra_test.go`（61.2% → 87.7%，全部 REST 处理器、Bearer 鉴权、panic 恢复、错误脱敏）、
  `config/extra_test.go`（70.1% → 99.1%，环境覆盖与 `PATCH` 全键）、
  `postproc/registry_test.go`（70.6% → 100%）、
  `scheduler/extra_test.go`（71.4% → 85.5%，控制平面、`chunkWriter`、永久失败与恢复）、
  `store/extra_test.go`（59.5% → 84.5%，`ListTasks`/`DeleteTask`/`VerifyAllChunks`/`events`/`Commit`）、
  `transport/http/extra_test.go`（55.1% → 84.6%，HEAD→GET 回退、重定向限制、取消、`Content-Range`）。
  全仓 **59.0% → 80.1%**，`12` §1/§8 的 10 个门禁包全部达标。
- **`make gates-strict`**：封装 `gates.py all --strict`，CI 改用严格模式（任何 WARN 即失败）。
- **代码许可证 `LICENSE`（MPL-2.0 全文）**：OPEN-01 于 2026-11-09 定档，来源为 GitHub 官方许可证文本；分发须随附 `LICENSE` 与 `THIRD_PARTY_NOTICES.md`，修改过的 MPL 文件按 MPL-2.0 提供并记录修改清单（`docs/10` §4）。文档仍为 CC-BY-4.0。
- **P0 里程碑评审门禁链** `tools/gates.py` + `make gates`：六项可复跑门禁（覆盖率阈值、
  CycloneDX SBOM、许可证扫描、`govulncheck`、HTTP 回环吞吐基准、备份/回滚演练），
  机器可读证据统一落在 `dist/gates.json`，Markdown 汇总为 `dist/GATES.md`。
  `WARN` 是真实未达标项，绝不伪装通过；`--strict` 可把 WARN 转为失败。
- **测试补强**：新增 `internal/core/contract_surface_test.go`、
  `internal/security/security_surface_test.go`、`internal/store/store_test.go:TestBackupRestoreRollbackDrill`、
  `internal/transport/http/bench_test.go:BenchmarkRangeThroughput`。
  `core` 覆盖率 68.4% → 92.9%、`security` 78.0% → 91.9%，均达 §1/§8 的 90% 口径。
- **新增 `docs/17-P0里程碑评审材料.md`**：评审签署表、逐条验收证据、门禁结果、覆盖率债清单、
  风险登记册更新（含新增 R-16）、ADR 0001–0020 变更清单、OPEN-01/02 决策简报与 P1 启动前置条件；
  已接入文档门户（41 篇，侧栏链接与全部深链不变）。
- **文档门户状态首页 `#/home`**（参考 ColdRevive 的索引首页，保留原有侧栏）：默认落地页改为首页，
  新增 hero 统计卡（文档总数 / 待审核 / 已决定 / ADR 数）、状态分布条、按状态与类型筛选、
  关键词搜索、按分类的文档卡片网格（状态徽章 + `P0 已校准` / `P0 依据` 标记）、
  「当前最该关注」提示条与「从这里开始」快捷入口。
- 状态由 `tools/build_docs.py` 从各文档版本头的 `状态：`、ADR 的 `- **状态**：` 自动解析，
  `docs/README.md` §三 清单表兜底；只按括号前主状态词规范化为 5 类，避免
  `Proposed（…转 Accepted）` 被误判为已决定。首页章节与卡片数为 `--check` 的强制校验项。

### Changed
- **序 6 / WBS 11 关闭：发起人裁决记档（2026-11-17，纯治理与文档，不改代码、不改判据）**：
  - **背景**：技术验收项 2026-11-16 起已全部满足（L4 `BT-01..BT-14` 双机 14/14、
    NFR-PERF-01 `met=true`、`12` §5 单机四项、Transmission 双向矩阵），`18` §九 只剩
    评审组实名复签 / 法务口径 / `14` R-10 复评三项治理项。项目发起人 2026-11-17 就后两项
    给出书面结论：① 对外文案「不承诺做种能力、不承诺 PT、不做 P2SP」；
    ② R-10「接受反吸血误伤风险，由运营侧人工处理投诉」。
  - **记档**：`18` 新增 §6.1（裁决原文照录 + 落地口径订正 + 记档性质），§九 收口清单最后
    一项勾选，§十 新增第 11 条时点口径（「序 6 保持开放」为 2026-11-17 前时点值）。
    `14` §5 R-10 等级维持「中」，记法改为「**已接受，非已消除**」，处置移交运营侧
    （受理误伤投诉、维护白名单、人工撤销封禁）；`14` §11 序 6 记「按发起人裁决关闭」。
  - **文案订正（重要）**：`16` §7 新增「做种 / PT / P2SP」三行口径。因 ADR-0021 路径 A
    **已实现受限做种**（分享率 1.0 或 60 分钟任一达标即停），裁决原文里的「不做种」
    **不可逐字对外使用**（与产品行为不符），也不许写「做种 / 长期做种 / 保种」；
    统一表述为「下载完成后按配置做有限的回馈上传，达标即停」。
  - **边界**：关闭**不产生任何新证据**——L4 结论仍只来自 `make l4-run`，`12` §5 的
    `ffmpeg` 两项与移动端 BT 内存仍为 `NOT_EXECUTED`，`SKIPPED` / `UNSUPPORTED` /
    `PRESENT` / `CONSISTENT` 仍一律不计 PASS。本条仍是**发起人书面裁决**，不是五位评审人
    实名会签，各处一律写「按发起人裁决关闭（实名会签待补）」，不得宣称「三方会签通过」。
  - **同步**：`docs/README` v1.20（卷首横幅 + §三 新增「序 6 / WBS 11 状态」行并改写
    已交付 / P2P 礼仪 / 路径 A 裁决 / 下一个工作包四行 + 清单表 18 行 + §六证据段 +
    变更记录）、`16` v0.12、`18` v1.0、`14` v1.13、根 `README`；门户重建 **48 篇 / 0 断链**。
- **分片进度改为批量刷盘：落实 ADR-0015 决策 ③（2026-11-17）**：
  - **此前状态**：`store.WriteChunkDone` 每完成一个分片就开一个事务（`UPDATE chunks` +
    摘要行 + `bytes_done` 增量三条语句），`06` §10「P0 偏差」条如实登记过。ADR-0015 决策 ③
    要求的是「**500ms 或 4MB 聚合，不每片一事务**」，一直未兑现。
  - **实现**：`scheduler.chunkWriter` 把完成记录（分片 / 摘要 / 镜像序号）缓冲在内存，
    累计 4MB 或 500ms 定时器到点触发一次刷盘；`Sync()` / `Close()` / `Verified()` 强制先刷盘。
    新增 `store.WriteChunkDoneBatch`：N 条分片状态、摘要行与**累加后的一次** `bytes_done`
    增量在同一事务提交；任一条分片不是 `assigned` 则整体回滚，跨任务混批直接拒绝。
    单条入口 `WriteChunkDone` 收敛为「批量 = 1」，恢复与测试只有一条代码路径。
  - **崩溃窗口（如实标注）**：`kill -9` 最多丢一个窗口（≤500ms 或 <4MB）的完成记录；
    这些分片在 DB 里仍是 `assigned`，恢复按 `06` §5 回 `pending` 重下——代价是一次重复下载，
    **不会**出现 `bytes_done` 与 `chunks` 表不一致（同事务），也不会损坏 `.part`。
  - **修掉的真实缺陷**：`Verified()` 是 `done → verified` 迁移，若该分片的完成记录还在缓冲里，
    `UPDATE ... WHERE state='assigned'` 匹配不到，报假的「not in assigned state」
    （首次跑测试即复现）。现在 `Verified()` 先刷盘再迁移，回归
    `TestChunkWriterVerifiedFlushesBufferedCompletionFirst` 固定该顺序。
  - **回归**：`store` 2 条（批量原子性含回滚、跨任务拒绝）+ `scheduler` 4 条
    （Sync 前不提交、4MB 阈值触发、500ms 定时器触发、verified 先刷盘）；
    真实路径用 `make perf` 的 1GiB / 4096 分片 Range 下载复核，1GiB 全量 sha256 通过。
  - **不宣称吞吐提升**：同机回环 `make perf` 连续三次为 309 / 702 / 244 MB/s（±3 倍波动），
    不足以支撑性能结论；本项是对**事务粒度**的兑现，不是性能声明。
  - **文档**：`06` §10 追加收口注、ADR-0015 追加「实现落地注（2026-11-17）」、
    `docs/README` v1.19。**仍未落实**：决策 ④ 系统钥匙串适配器、`06` §7 的 `events` 保留清理。
- **文档「当前态」一致性收口：把 L4 14/14 与 NFR `met=true` 写回各处结论表（2026-11-16，纯文档）**：
  - **问题**：`eda8a6c` / `38bd52c` 已产出并归档 L4 14/14 与 NFR 11.21MB/s，但多份文档的
    **当前态表格与结论段**仍是旧口径——`03` §8 追踪矩阵写「❌ 未通过 0/14」「❌ 未执行」，
    `07` §8 矩阵写「L4 门禁 0/14 PASS」，`11` §4 残余风险仍称真实客户端矩阵与抓包级验证
    「仍不具备」，`12` §9 结果表 L4 行为「❌ 未通过」且「P0 门禁的实际范围说明」称 L4 未执行，
    `13` §3 命令表仍写 12 `SKIPPED` / 2 `UNSUPPORTED`，`14` WBS 表与 §11 门禁段落仍称
    「发布门禁未通过」，`16` §4 / §6 仍列「仍未执行」，`18` §二 事实表与 §八 仍为旧值，
    根 `README` 能力边界条目在同一条里同时出现「14/14 PASS」与「仍 0/14」。
  - **处理**：只把**当前态**表述改为实测结论（`BT-01..BT-14` 14/14 PASS、
    `release_gate_met=true`、`make l4-audit` `CONSISTENT` 9/9、NFR 11.21MB/s / `met=true`）；
    所有历史数值追加「时点值」标注后**原样保留**，`17` / ADR-0020 已签署证据零改动。
    新增显式说明：CI 内 `make interop`（9 PASS + 14 `SKIPPED`、`release_gate_met=false`）
    与实验室 `make l4-run`（14/14、`true`）两种证据**不得互相替代**。
  - **文档**：`03` **v0.11**、`07` **v0.7**、`11` **v1.3**、`12` **v1.11**（另补 `make perf-lab` 行）、
    `13` **v0.19**（补 `make perf-lab` 行、门户篇数 44 → **46**）、`14` **v1.11**、
    `16` **v0.11**、`18` **v0.9**、`docs/README` **v1.16**（同时补登 `v1.14` / `v1.15`
    两行变更记录——此前只加了卷首横幅）、根 `README.md`。
  - **判据与门禁**：无代码改动、`12` §3.1 判据零修改；仍 `make lint` / `make test` /
    `go test -race ./...` 全绿、`make gates-strict` 6 PASS / 0 WARN / 0 FAIL、
    门户 `--check` **46 篇 / 0 断链**。序 6 仍开放（仅余治理项）。
  - **CI**：本包 `38d3544` run `43880440536` 为 **`quality: success`**。
- **ADR-0021 路径 A 全量文档同步（2026-11-14）**：ADR-0021 追加「决议记录」
  （R1 裁决事实 / R2 签署如实状态 / R3 已落地 / R4 未落地），`adr/README` 升 **v0.5**
  并同步 0021 索引状态；`docs/18` 升 **v0.3**（登记裁决输入，§二事实清单 /
  §三标题与引言追加时点口径注，签署表仍留白、§十 增第 10 条硬不变量）；`docs/README` 升 **v1.9**（§三 清单 / 当前态 /
  下一个工作包 / ADR-0021 边界注 / §六）、`03` **v0.9** §8 追踪矩阵、
  `05` **v0.8** 状态机落地、`07` **v0.6** 配置映射、`11` **v1.1** §7.1 控制矩阵、
  `12` **v1.5** §3.1 / §9、`13` **v0.13** §7 / §12、`14` **v1.5** §11 / R-10、
  `15` **v0.6** §4 规则⑥的 ADR-0021 时点口径、`16` **v0.9** 卷首与 §4 / §8 文案、
  ADR-0004 / ADR-0017 实现落地注、`config.example.yaml`、根 `README.md`、
  `tools/build_docs.py` 首页横幅与 `docs/README` §五 slug 契约同步。
  **如实校准**：套件 v1 注册表仍把 `BT-02` / `BT-11` 按 `ScopeProductGap`
  固定为 `UNSUPPORTED`；路径 A 只补齐实现，不翻转证据状态、更不计 PASS。
  **不改 `12` §3.1 已登记 L4 判据**，不改 `17` / ADR-0020 已签署证据，
  历史版本说明中的时点数值（「42/43 篇」「20 Accepted / 1 待评审」
  「`Proposed`」「不做种」等）一律保留不改，只追加带日期的时点口径注。
  门户 `docs/_site/index.html` 重建仍 **43 篇 / 0 断链**，ADR 状态汇总由动态解析变为
  **21 `Accepted` / 0 待评审**。另按惯例补记 `9395343` 的 CI run `43595391886`
  经核验为 **`quality: success`**（时点口径追加，不改写原条目）。
  门禁口径零改动：16 门禁包 / 90 链接模块 / `gates-strict` 6 PASS 0 WARN 0 FAIL /
  L4 **0/14** / `release_gate_met=false` / 序 6 仍开放。
- **L4 证据审计链路全量文档同步（2026-11-14，`docs/18` v0.2 / `docs/README` v1.8 /
  `12` v1.4 / `13` v0.12）**：`12` §3.1.3 表补 `make l4-audit` 行并新增
  **§3.1.4 证据包完整性审计**、§9 用例追踪补 `TestAudit*` 行；`13` §3 目标表与
  §12 实现矩阵补审计目标并明确「四目标均不接入 CI」；`docs/18` §7.3 补第 5 条留痕、
  §7.4 步骤 5 补审计、§九 收口清单补一项、§十 增第 9 条硬不变量；
  `docs/README` §三 新增「证据完整性审计」行并改写「L4 实验室链路」/「下一个工作包」、
  §二 角色阅读、§六、§七 索引同步；根 `README.md` 补审计说明，并修正门户侧栏
  **41 → 43 篇**的过期当前态表述。门户 `docs/_site/index.html` 重建仍
  **43 篇 / 0 断链**（本次未新增文档）。**历史版本说明中的 42 / 43 篇与
  「三目标」等表述按「时点值不改写」原则保留**，仅在新增版本说明中标注。
  另按惯例补记 `0ca9164` 的 CI run `43580889196` 经 `gh run list` 核验为
  **`quality: success`**（时点口径追加，不改写原条目）。
  `internal/interop` 覆盖率因 11 项 `TestAudit*` 由 87.9% → **88.7%**（阈值 80%）。
  门禁包 **16** / 链接模块 **90** / `gates-strict` **6 PASS / 0 WARN / 0 FAIL** /
  L4 **0/14** / `release_gate_met=false` / `l4_executed=false` /
  `nfr_perf_01.met=false` / 序 6 仍开放；`17` / ADR-0020 已签署证据不改写。
- **门户接入 doc-18 与 CI 时点口径补记（2026-11-14，纯文档 / 生成器校准）**：
  `tools/build_docs.py` 的 `GROUPS` 纳入 `docs/18`，导航契约注释由
  `doc-01..doc-17` 改为 `doc-01..doc-18`，分组描述补「评审材料」；
  `docs/README` 升 **v1.7**（§二 角色阅读、§三 清单 / 当前态「评审准备」行 /
  下一个工作包、§六、§七 索引）、`13` 升 **v0.11**（§12 实现矩阵文档数
  **42 → 43**）、根 `README.md` 同步；门户 `docs/_site/index.html` 重建为
  **43 篇 / 0 断链**。**v1.5 / v1.6 与 `13` v0.9 版本说明中的「42 篇」为时点值，
  按历史数值不改写原则保留**。另补记 `84871b0` 的 CI run `43570105065`
  经 `gh run view` 核验为 **`quality: success`**（时点口径追加，不改写原条目）。
  **零产品代码改动 / 零新增门禁证据**：门禁包 **16**、链接模块 **90**、
  `make gates-strict` **6 PASS / 0 WARN / 0 FAIL**、L4 **0/14**、
  `release_gate_met=false`、`l4_executed=false`、`nfr_perf_01.met=false`、
  序 6 仍开放；`17` / ADR-0020 已签署证据不改写。
- **代码↔文档一致性校准与已核验 CI 结论补记（2026-11-14，纯文档 / 生成器校准）**：
  按 `15` §13 / `13` §4 的「如实记录」口径，补齐此前条目留白的 CI 结论——
  `679a26e`（v1.4 / `15` v0.4 文档控制包）触发 run `43402970475`、
  `c3fbc70`（ADR-0021 + L4 前置检查链路）触发 run `43567640194`，
  经 `gh run list` 核验均为 **`quality: success`**（严格门禁 6 PASS / 0 WARN / 0 FAIL）。
  另有 run `43566313113` 为 `failure`：该次尝试用 Git Data API 生成的 commit
  无法被 `actions/checkout` 完整拉取（`Found 4 documents` / `253 error(s)`），
  已由真实 git 对象 `c3fbc70` 经 SSH 读写 deploy key 重推取代，**不是偶发 flake、
  不是质量门禁回归**，如实登记不隐藏、也不计入通过数。
  `c3fbc70` 条目中「不预先记为通过」属**提交时点口径**，按历史数值不改写原则保留原文，
  仅以本条追加时点校准。
  门户生成器 `tools/build_docs.py` 的硬编码徽章 `文档体系 v0.4` 改为从
  `docs/README.md` 版本头动态读取，导航契约注释由 `adr-0001..adr-0020` 改为
  ADR 自动发现口径（实际 **21 份 ADR**）；同步 `docs/README` **v1.6**
  （§三 严格门禁行 / §六 变更记录）、`13` **v0.10**（§4 CI 段）、根 `README.md`
  当前态，门户 `docs/_site/index.html` 重建（仍 **42 篇 / 0 断链**）。
  **零产品代码改动**：门禁包仍 **16**、链接模块仍 **90**、
  `make gates-strict` 仍 **6 PASS / 0 WARN / 0 FAIL**、L4 仍 **0/14**、
  `release_gate_met=false`、`nfr_perf_01.met=false`、序 6 仍开放；
  `17` / ADR-0020 已签署证据与历史时点数值均不改写。
- **文档控制与 CI 缺陷处置口径补记（2026-11-13）**：`docs/README` 升 **v1.4**，
  §三「当前态口径」严格门禁行如实闭合为「CI run `43393512271` 曾
  **5 PASS / 0 WARN / 1 FAIL** → `ddda48d` run `43399289527` 与 `8138c42`
  run `43400218985` 均 **`quality: success`**」，不再沿用「本机与 CI 一致」的笼统表述；
  `15` 升 **v0.4** 并新增 §13「CI 偶发失败处置」：偶发失败必须记录原始结论、
  定位根因、修产品代码并补回归用例，**不得**靠 `rerun`、放宽 `--strict`、
  把 FAIL 改记 `SKIPPED` / `UNSUPPORTED` 蒙混，也不改 CI 步骤与严格模式；
  `13` §4 补两次复验 run 与 `15` §13 交叉引用；根 `README.md` 当前态同步两次 CI 结论。
  门户 `docs/_site/index.html` 由 `tools/build_docs.py` 重建。**纯文档变更、零代码改动**：
  门禁包仍 **16**、链接模块仍 **90**，L4 仍 **0/14**、序 6 仍开放、
  `nfr_perf_01.met=false`；`17` / ADR-0020 已签署证据与历史时点数值均不改写。
- **WBS 11 文档同步（2026-11-11）**：`docs/README` v1.2 §三 当前态速查表更新为
  「16 个门禁包 / 90 个链接模块 / 6 PASS / 0 WARN / 0 FAIL / 套件 v1 已交付、
  **序 6 L4 仍开放**」；  `12` v1.0 补 §3.1 套件落地说明与 §9 证据行、`16` v0.6 §6/§8 补
  WBS 11 实现边界、`14` v1.1 §11 序 6 改为「套件已交付 / L4 外部矩阵未执行」、
  `13` v0.6 补 `make interop` 与证据链、`03` v0.6 §8 追踪矩阵补记、
  `05` v0.4 §1 当前态、`07` v0.4 §8 CLI 矩阵、`11` v0.8 §10 残余风险、
  `10` v0.8 §8.3 零新增依赖、ADR-0004 新增实现落地段；`17` 与 ADR-0020 仅补时点口径
  （已签署 P0 证据原值不改）；根 `README.md` 当前态、能力边界、代码地图与快速开始同步；
  `docs/_site/index.html` 由 `tools/build_docs.py` 重建。
  **对外口径不变**：仍不得写「已与主流客户端实测互通」「全矩阵通过」或
  「千兆内网性能达标」。
- **WBS 10 文档同步（2026-11-10）**：按「文档为唯一真值源」校准当前态——
  `docs/README` v1.1 §三 当前态速查表更新为「已交付 WBS 8/9/7/10、15 个门禁包、
  90 个链接模块、6 PASS / 0 WARN / 0 FAIL、下一个工作包 = 序 6 / WBS 11」，
  §六 补 v1.1 行；`03` v0.5 §8 追踪矩阵补记、`05` v0.3 包结构与 Resolver 链、
  `07` v0.3 CLI 子集（按实际 `udctl plugin` 六个离线子命令校正，
  原设计稿的 `install/verify/list <path-or-url>` 未实现）、`08` v0.3 §10 实现落地、
  `10` v0.7 §8.2 goja 依赖表、`11` v0.7 §5 插件隔离矩阵与 §10 残余风险、
  `12` v0.9 用例与阈值、`13` v0.5 插件配置与环境变量、`14` v1.0 §11 序 5、
  `16` v0.5 实现边界、ADR-0014 新增「实现落地」段（决策原文不改）。
  `17` 与 ADR-0020 **只补时点口径声明**，「10 个门禁包 / 12 个链接模块 / 5 PASS / 1 WARN」
  等已签署 P0 评审证据、以及 ADR-0020 的 P0「不包含」清单**原值不改**。
  根 `README.md` 顶部当前态、WBS 10 交付段、代码地图与能力边界同步；
  `docs/_site/index.html` 由 `tools/build_docs.py` 重建。
- **WBS 7 文档收尾校准（2026-11-10，仅文档，不改代码）**：首个 WBS 7 提交遗漏了若干
  「当前态」口径点，本次补齐：`docs/README` §三 当前态速查表由「WBS 8/9、13 个门禁包、
  16 个链接模块」更新为「WBS 8/9/7、14 个门禁包、86 个链接模块」，并新增「下一个工作包
  = 序 5 / WBS 10」行；根 `README.md` 顶部当前态行、全流水线说明与 WBS 7 交付段同步。
  `docs/03` v0.4 §8 新增「P1 WBS 7 追踪矩阵补记」（FR-RSV-01/02、FR-TRN-05/06/09、
  FR-INT-03、NFR-SEC-01、NFR-PORT-01 逐条映射到真实测试名）。
  升版本头并补说明段：`10` v0.6、`11` v0.6、`12` v0.8（v0.8 说明归位到版本头之后）、
  `14` v0.9、`16` v0.4、`docs/README` v1.0（§六 变更记录补 v1.0 行）。
  `16` §8 新增「WBS 7 实现边界」（不承诺 PT、L4 互操作矩阵待 WBS 11、默认不做种）。
  `14` §10、ADR-0020、`17` 的**时点口径声明**按分层补上 WBS 7 数字；ADR-0020 的
  P0「仍未实现」清单作为已签署证据**原值不改**，仅在免责声明里补注 BT 已实现。
  `docs/_site/index.html` 由 `tools/build_docs.py` 重建（`--check` 只校验不生成）。
- **文档口径校准（2026-11-10，仅文档，不改代码与已签署结论）**：WBS 9 后部分描述
  **当前态**的数字仍停留在 P0 / WBS 8 时点（10 个门禁包、12 个链接模块、
  `5 PASS / 1 WARN`、CI 步骤写成 `make gates`），`docs/12` v0.4 说明对 §9 表的交叉引用
  也已失效。现统一为 **14 个门禁包 / 86 个链接模块 / `make gates-strict` 6 PASS / 0 WARN / 0 FAIL**：
  `docs/README` §三 新增「当前态口径（唯一速查）」作为唯一速查点，`13` 校正 CI 步骤名与
  SBOM 守卫描述，`14` 在 R-16 与 §10 补时点标注，`12` 修正过期交叉引用。对 `17` 与
  ADR-0020 这类**已签署的 P0 评审快照不改写历史证据**，只加时点口径声明。
  `10` §7 把「12 个链接模块」标注为 P0 时点值并补 WBS 9 后的 16 个、WBS 7 后的 86 个。
  WBS 7 文档同步（`10` §4/§8.1 MPL 修改清单与依赖表、`11` §7.1 BT 安全控制、`12` v0.8 测试、
  `14` §11 序 4 状态、`16` §4 WBS 7 落地、`README` 能力边界）；门户重建后 41 篇 0 断链。
- **WBS 8 明确的能力边界**（与 ADR-0006 / 0007 / 0013 一致，写进产品口径）：
  ① 冻结的 `Resource` 只有全局 `Mirrors` / `Checksum`，**多 `<file>` 清单显式拒绝**
  （`E_CAPABILITY_UNAVAILABLE`），不把 A 的镜像错挂到 B；② 远端
  `https://…/*.meta4` **不由 Resolver 发起第二次网络请求**，返回显式能力错误，清单只能经
  本地 `.meta4/.metalink` 路径或 `ResolveContext.Options` 传入；③ RFC 6249 头经
  `Options[metalink_headers]` 注入，待 Transport 层集成。产品文案仍按 `16` §7 只写
  「支持解析 thunder:// 链接」「支持 Metalink 多源清单解析」。
- **SQLite 连接健壮性**：`journal_mode(WAL)` 从 DSN 的每连接 `_pragma` 列表移到 `Open()` 时
  在单连接上执行一次。WAL 是数据库文件级持久设置，原先每条懒创建的池连接都会重发该
  PRAGMA（需要独占访问），并发 `Pause` / `Cancel` 偶发 `database is locked (SQLITE_BUSY)`；
  新增 `TestWALSetOnceAndPoolOpensCleanly` 回归用例。
- **覆盖率附加条件关闭（2026-11-10）**：`docs/17` §1/§3/§5/§6/§9、`docs/14` §5/§10/§11、
  `docs/12` §9、`docs/13` §12、`docs/15` §11、`ADR-0020` 与根 `README.md` 同步标记关闭；
  R-16 由「中」降为「低」。
- **P0 里程碑评审闭环（2026-11-09，「有条件通过」）**：项目发起人全权批准并代行五个签署角色；
  ADR 0001–0020 全部由 `Proposed` 转 `Accepted`（索引 v0.3）、OPEN-01 定档 MPL-2.0、OPEN-02 定档 Flutter；
  `docs/17` 结论/签署/决策/评审后动作、`docs/14` §7/§9/§10 与新增 §11（P1 启动顺序）、`docs/README` v0.6、
  `docs/03` §10、`docs/10` §4、`docs/11` §10、ADR-0013/0016/0020、根 `README.md` 全部同步；
  文档门户状态首页「已决定」由 1 篇升至 23 篇（41 篇 0 断链）；`v0.1.0` 附注标签已打，`14` §3 的 P1 门禁解除。
  覆盖率 8 个包未达标作为附加条件延后至 P1 首个 PR 前，当前 WARN 不记作通过。
- **Go module 路径与仓库地址统一为 `github.com/asdfgh129/gobitmule`**：首次推送 GitHub 时把 `go.mod`、
  全部 Go import、`Makefile` 的 `-X` 链接参数、`tools/check_arch.py` / `tools/gates.py` 的
  `MODULE` 常量、CHANGELOG 版本比较/发布链接同步为实际仓库地址；`docs/_site/index.html`
  由 `make docs` 重新生成。`.workbuddy/`（本地工作记忆）加入 `.gitignore`，不纳入版本库。
- 门户默认落地页由 `#/readme` 改为 `#/home`；`home` 为虚拟章节（无 Markdown 源文件），
  作为未知 slug 的回退目标。所有既有 `#/<slug>` 深链与 v0.1 中文旧书签保持不变。
- `docs/12` 升 v0.3：统一 §1 与 §8 互相冲突的覆盖率口径（`core`/`security` ≥ 90%，
  其余 P0 关键路径包 ≥ 80%）；`docs/10` v0.3、`docs/11` v0.3、`docs/13` v0.3、
  `docs/14` v0.3、`docs/15` v0.3、`docs/README.md` v0.5 与 ADR-0020 同步实现校准。
- `make sbom` 从「最小版模块图」升级为 CycloneDX 1.6；Makefile 项目级设
  `GOSUMDB=sum.golang.org`，使 `GOTOOLCHAIN=auto` 能校验工具链模块，不改用户全局 Go 配置。
- ADR-0020 把 SBOM / 漏洞扫描 / 许可证扫描 / 覆盖率报告 / 回滚演练从「P1 待办」与
  「不包含」清单移出（它们属构建期门禁工具，不是产品能力）。

### Fixed
- **修复：钥匙串提交在 CI 跌破 `internal/security` 覆盖率阈值（`6d96918` run
  `44065445782` → `dbd31ec` run `44070121233`，2026-11-17）**：
  - **现象**：OS 钥匙串提交在本机（macOS）`make gates-strict` 6 PASS / 0 WARN /
    0 FAIL，但 CI（ubuntu）`cover` 门禁 WARN——`internal/security` 85.5% < 90%，
    严格模式判 FAIL。
  - **根因**：`keychain_linux.go` 在 CI 编译、其 Secret Service 方法却没有任何
    可执行路径；而 `keychain_darwin.go` 在 CI **不编译**。macOS 本机与 CI 的
    Linux 编译集是两个不同的语句集合，覆盖率**不可互推**——本机 90.3% 不代表 CI。
  - **修复**：补真实可跑的测试——`keychain_linux_test.go` 用假 `secret-tool`
    （临时 PATH 脚本）驱动属性 argv 顺序 / stdin 传密 / 退出码 1 = 静默
    not-found / helper 失败不得伪装 not-found，以及双通道探测
    （`DBUS_SESSION_BUS_ADDRESS` 与 `XDG_RUNTIME_DIR/bus`）与缺失时降级 `file`；
    `keychain_unit_test.go` 补 rename 目标被目录占用、凭据路径为目录两支错误
    分支；root 绕过权限的场景显式 `t.Skip` 并注明 `NOT_EXECUTED`。
  - **验证升级**：为不再靠推算，在实验室主机 A（真实 Ubuntu）新装 Go 1.27.4
    **真实执行** Linux 编译集：**91.2%**（macOS 91.1%），两端全绿；真实执行还
    抓到两处可移植性缺陷（测试内 id 弄反、root 场景断言），均已修。
  - **沉淀**：`13` §4 v0.22 记入台账与经验——凡按 `GOOS` 分文件的包，覆盖率
    门禁必须对每种编译集分别真实执行。
- **修复：并发 Cancel 后任务被强制改写为 `Failed`（`E_ILLEGAL_TRANSITION`）**：
  - **现象**：`make gates-strict` 的覆盖率门禁偶发 FAIL，报
    `FAIL: TestDeleteRemovesOutputsWhenRequested`——任务迟迟未到 `Canceled`，
    最终状态为 `Failed`，`ErrorMessage` 是
    `illegal state transition Canceled -> Resolving`。该用例单独跑 8 次、整包跑 4 次均通过，
    只在全仓并行（`go test -coverprofile ./...`）的高负载下复现。
  - **根因**：`Scheduler.run()` 先 `GetTask` 判断非终态，随后才 `transition(...,
    StateResolving)`。若 `Cancel` 恰好落在两者之间，状态机**正确地**拒绝
    `Canceled -> Resolving`，但 `handleFailure` 用 `Force: true` 落 `Failed`
    （`Force` 是 `Paused` / `Failed` 重试所必需），于是**用户的取消被覆盖成失败**。
    状态机防护本身正确，缺陷在失败路径缺少「终态不可被覆盖」的判定。
  - **修复**：`handleFailure` 在强制落 `Failed` 前重读任务，`Completed` / `Canceled`
    （`core.IsTerminal`）一律直接返回——不写状态、不发 `EventTaskFailed`、不打 WARN；
    `Paused` / `Failed` 的重试语义不变。
  - **回归**：新增 `internal/scheduler/extra_test.go:TestHandleFailureKeepsConcurrentCanceledState`
    （取消后注入同一 `E_ILLEGAL_TRANSITION`，断言仍为 `Canceled` 且无错误文本）。
    已验证：去掉守卫即复现 `got Failed (illegal state transition Canceled -> Resolving)`，
    加回守卫通过（非「改测试凑绿」）。
  - **验证**：`make lint` / `make test` / `go test -race ./...` 全绿、
    `make gates-strict` 6 PASS / 0 WARN / 0 FAIL；零新增依赖（仍 90 链接模块）。
  - **CI**：本包 `38d3544` run `43880440536` 为 **`quality: success`**。
- **文档门户首页横幅与 ADR 状态矛盾、`bt.*` 配置键误导（2026-11-13）**：
  `tools/build_docs.py` 生成的总览页仍硬编码「ADR 0001–0020 仍全部为 `Proposed`、
  P0 里程碑尚未正式评审」，与 `docs/adr/README.md` 索引（0001–0020 已于 2026-11-09
  批量转 `Accepted`）及 `17` §1 结论矛盾。现改为按 ADR 头部状态动态汇总
  （当前 **20 `Accepted` / 1 待评审**），chips 改为「P0 附加条件已关闭」/
  「P1 进行中 · 序 6 开放」，横幅明确「当前态唯一速查为 `docs/README` §三、
  进度真值源为 `CHANGELOG`」，并在构建器注释中要求里程碑叙述随 §三同步；
  门户重建 **42 篇 / 0 断链**。同轮把 `config.example.yaml` 的
  「P1+ 预留，P0 只解析不生效」更正为「P0/P1 当前仍只解析不生效（WBS 7 未接入 `bt.*`）」，
  并在 `13` §7 补同样口径注，避免把示例配置误读为已生效。
- **CI 偶发 `SQLITE_BUSY` 导致 `cover` 门禁失败（2026-11-13，WBS 11 续包推送后复跑发现）**：
  提交 `9530c35` 的 CI run `43393512271` 在 `quality / make gates-strict` 的 **cover** 步骤出现
  `internal/scheduler::TestLifecycleErrors` 的 `extra_test.go:236: database is locked (5) (SQLITE_BUSY)`，
  严格门禁当时汇总为 **5 PASS / 0 WARN / 1 FAIL**，本机同条件 5 轮复跑、`-race` 与
  `-coverprofile` 均未复现；该失败**如实记为未通过**，未以重跑或放宽 `--strict` 掩盖。
  根因：WAL 下写-写竞争可能返回 `SQLITE_BUSY_SNAPSHOT`，SQLite **不会**为此错误调用
  busy handler，DSN 的 `busy_timeout = 5000` 因此兜不住；`modernc.org/sqlite` 纯 Go 驱动
  在此处不暴露类型化错误码，只能按错误文本判定。现把 `internal/store/store.go` 的写事务
  拆为 `withTx`（唯一写锁 + 有界重放）与 `runTx`（单次 Begin / Commit / Rollback）：
  仅当 `isBusy()` 命中 `SQLITE_BUSY` / `SQLITE_LOCKED` 时以 5ms 递增退避最多重放 **6 次**；
  每次重放前事务已回滚，所有 `withTx` 回调经检查只做数据库操作（无 DB 外副作用），
  因此不会重复提交行；永久锁库仍返回错误，不静默吞掉，`ctx` 取消立即终止。
  生产行为不变：仍是单写串行、状态写入与分片进度同事务（`05` §4 不变式②、`06` §5），
  DDL / `schema_version` 与 ADR-0015 决策均未改动。新增 4 条回归用例：
  `TestBusyErrorClassification`、`TestWithTxReplaysTransientBusy`、
  `TestWithTxStopsAfterBusyRetries`、`TestConcurrentWritesRemainSerialized`。
  按项目规矩**代码变更后先 `make build`（六平台）再跑门禁**：本机 `make lint` / `make test` /
  `make interop` / `make gates-strict`（**6 PASS / 0 WARN / 0 FAIL**）/ `go test -race ./...`
  全部通过；`make interop-gate` 仍明确失败（L4 **0/14**、`release_gate_met: false`）。
  CI `quality` 已由 run `43399289527`（`ddda48d`，2026-11-13 09:13 UTC）复跑确认
  **success**：严格门禁 6 PASS / 0 WARN / 0 FAIL，此前 `43393512271` 的失败闭环。
  本条为文档证据补记，不改代码。
  同步 `05` / `06` / `12` / `13` / `14`、ADR-0015 实现落地注、`docs/README` §三、
  根 `README.md` 与门户 `docs/_site/index.html`。
- **调度器关闭时的 WaitGroup / 生命周期竞态（WBS 11 全量 `-race` 复跑发现）**：
  `internal/scheduler/scheduler.go` 的 `dispatch()` 循环原以裸 `go s.dispatch()` 启动，
  **未登记进 `s.wg`**，但循环内又会对每个任务调用 `s.wg.Add(1)`。于是 `Close()` 的
  `s.cancel(); s.wg.Wait()` 可能与 `dispatch` 的 `Add` 并发（`WaitGroup` 明确禁止
  counter 归零时的 `Add` 与 `Wait` 竞争），也可能在 dispatch / worker 仍在使用 store
  时就返回；测试 harness 的 `t.Cleanup` 随后关闭 store 与 `httptest` server，
  `go test -race ./internal/scheduler -count=5` 可稳定复现为 `TestFailedChunksDriveTaskToFailed`
  的 data race。现把 dispatch 本身在 `New()` 里先 `wg.Add(1)` 再启动、
  `defer wg.Done()`，并在收到队列 ID 后显式检查 `s.ctx.Err()`，保证取消后即便队列持续有值
  也能退出；`Close()` 因此会等 dispatch 与所有 worker 真正结束才返回，且重复调用安全。
  新增 `TestCloseDrainsDispatchAndWorkers` 回归用例，`go test -race ./...` 恢复全绿。
- **BT 磁力下载的元数据就绪竞态（WBS 11 互操作套件在 `-race` 下发现）**：
  `internal/transport/bt/bt.go:waitInfo` 原以 50ms 轮询 `Torrent.Info()` 判断元数据就绪，
  但 anacrolix 在 `setInfo()` 里先在 `nameMu` 下发布 `t.info`、**之后**才调用
  `initFiles()` 写 `t.files`；`Run()` 紧接着的 `pickFile() → Torrent.Files()` 会与
  `initFiles()` 产生 data race（纯磁力经 BEP 9 取元数据时必现）。现改为等待
  `Torrent.GotInfo()`（由 `onSetInfo()` 在 `setInfo()` 完成后关闭），同时覆盖元数据与
  文件句柄就绪，并去掉轮询；新增 `TestWaitInfoWaitsForGotInfo` 回归用例，
  `go test -race ./...` 恢复全绿。`Probe` 与 `Run` 共用该函数，两路同时受益。
- `tools/gates.py:build_sbom()` 增加二进制与模块图一致性校验：SBOM 版本取自 `dist/`
  内二进制的 `go version -m`，而许可证原文读的是**当前**模块缓存；依赖升级后若未先
  `make build`，会产出「旧版本号 + 新许可证」的混合证据却仍报 PASS。现逐模块比对，
  不一致即 `sbom` FAIL 并提示先 `make build`（已用 v0.54.0 → v0.58.0 实测触发并修复）。
- `tools/gates.py` 的 SBOM 门禁改为「对实际链接模块逐个查询」而非 `go list -m -json all`：
  后者会遍历第三方库的**测试专用**传递依赖（testify / x/net / x/term / x/text 等），
  本地代理受限时报 502 直接 FAIL，即使二进制与供应链证据都完整。
- `sbom` 门禁失败时，`licenses` 不再读取上一轮遗留的 `dist/sbom.cdx.json` 报 PASS，
  改为显式 FAIL；符合 gates.py 自身「不得静默声称成功」的设计规则。
- Metalink 清单声明的 `crc32` 等不支持算法曾被静默忽略（等于绕过完整性校验）；现在解析期
  以 `E_INVALID_INPUT` 明确拒绝；`<hash>` 省略 `@type` 时按 SHA-256 处理。
- `Digest: sha-256=<64 位 hex>` 会被当作合法 base64 先解成 48 字节而报错；改为先尝试长度
  无歧义的 hex 形式，再回退 RFC 9530 `:base64:` 与 RFC 3230 base64。
- `core.EventBus.Dropped()` 是文档化的诊断计数，但 `Publish` 从未累加；改为 `atomic.Int64`
  累加（`Publish` 只持读锁，用 mutex 计数会数据竞争），并补回归用例。
- `TestULID` 依赖「同毫秒」时序，导致全量并行跑时覆盖率在 89.4%–92.4% 抖动；
  补确定性 `TestIncrementEntropyCarry`（进位与全溢出回滚时间戳）。
- 许可证识别把标准 BSD 两条款文本判为 `NOASSERTION`；以「Neither the name」区分 BSD-3/BSD-2，
  `gorilla/websocket` 现正确识别为 BSD-2-Clause。
- 状态归类缺陷：`ADR-0001` 的 `Proposed（待立项评审批量转 Accepted）` 因备注中出现 `Accepted`
  被误标为「已决定」；现在只取括号前的主状态词。

P0 全部构建批次（含收尾修复与文档同步）仍汇总于下方 `0.1.0`；P1 排期见 `docs/14` §11（P0 范围快照见 `ADR-0020`）。
**P0 里程碑评审决议（2026-11-09，随 `v0.1.0` 发布）**：结论「**有条件通过**」，由项目发起人一次性全权批准并代行五个签署角色；
ADR 0001–0020 全部转 `Accepted`；OPEN-01 定档 **MPL-2.0**（`LICENSE` 已入库）、OPEN-02 定档 **Flutter**；
`v0.1.0` 附注标签已打，`14` §3 的 P1 门禁解除（`14` §11 给出 WBS 7–11 启动顺序）。
**附加条件（已关闭）**：评审时覆盖率有 8 个包低于阈值，要求 P1 首个 PR 前补齐并把 `gates.py --strict` 接入 CI；该条件已于 2026-11-10 全部满足（全仓 80.1%，CI 严格模式），当时的 WARN 不记作通过。详见 `docs/17` §1/§5/§7/§8/§9。

## [0.1.0] - 2026-11-08

P0 骨架首个可运行基线：一条 HTTP(S) 直链走完 `Resolver → Resource → Transport → PostProcessor → Store` 全流水线。本条目汇总 2026-11-08 的多个构建批次（骨架 → 控制面 → 冒烟 → 测试门禁 → 安全加固 → 文档同步）与 2026-11-09 的评审门禁收尾、里程碑决议（见上方 `[Unreleased]` 段，已随本标签发布），**2026-11-09 打 `v0.1.0` 附注标签**。

### Added
- **工程骨架**：Go 模块、`Makefile`（lint / test / build / docs / sbom）、GitHub Actions CI、
  `.gitignore`、`config.example.yaml`、版本注入变量与本地发现文件机制。
- **核心模型 `internal/core`**：`Resource`（`Files[]` / `Mirrors[]` / `Checksum` / `ProtocolIdentity` 全字段，
  ADR-0013 一次建模）、Task 状态机与合法迁移表、ULID、`07` §6 全量错误码与四种错误分类、
  事件总线、输入分类器，以及 `Resolver` / `Transport` / `PostProcessor` / `ChunkWriter` /
  `Limiter` / `Scheduler` / `ControlPlane` / `CredentialStore` 契约。
- **配置 `internal/config`**：`docs/13` §7 全量 YAML schema、默认值、优先级覆盖、`ParseRate`/`ParseSize`、
  运行时 `PATCH /config` 管理器（受支持键白名单 + 校验 + 变更回调）。
- **安全 `internal/security`**：`SSRFGuard`（协议/端口白名单、解析后 IP 校验含 IPv4-mapped IPv6、
  DNS pin 防重绑定、逐跳重定向校验与跨 host 凭据剥离）、`PathGuard`、`RedactURL`/`Redact`/`RedactHeaders`、
  `CredentialStore` 的 P0 进程内参考实现（密钥只存内存，绝不入库/日志/响应）。
- **统一流水线**：`resolve`（HTTP 直链 Resolver + 分阶段可读的 Unsupported 兜底）、`transport/http`
  （Range 分段、慢启动并发上限、断点续传、分片 sha256、镜像轮换、指数退避+抖动、无 Range 服务器退化为单连接流式）、
  `postproc`（ADR-0012 本地路径契约 + P0 `none` 直通实现）。
- **唯一限流器 `scheduler.Limiter`**：分层令牌桶 global / per-task / per-host，下载与上传分别计账，
  大分片按 burst 切片计费避免死锁；`config.Manager` 变更回调是唯一配置入口。
- **单调度器 `scheduler.Scheduler`**：动态并发槽、创建/暂停/恢复/取消/选项修改、Probe→分片表→Transport→
  逐片复验→PostProcessor→`.part` 原子 rename→单事务提交、崩溃恢复重入队、进度/速度/事件发布。
  `chunks` 表为分片唯一真值源，`Core` 不持有任何第二套任务生命周期。
- **存储 `internal/store`**：`docs/06` §3 的 11 张表 DDL v1（`001_init.sql`，幂等）、WAL/外键/busy_timeout、
  单写者事务、Resource 全字段规范化存取、`chunks` 统一分片表（原子 `Next` 领取）、分片 sha256 记录、
  `events` 审计、崩溃恢复（活动态→Paused、assigned/failed→pending、逐片按 `.part` 实际字节复验）、`VACUUM INTO` 备份。
- **P0 契约测试**：全流水线落盘一致性、暂停/恢复/取消语义、模拟 kill -9 的分片 100% 复用（`internal/scheduler/scheduler_test.go`）。
- **控制面 `internal/api` + `internal/app` + `cmd/`**：`07` §2 的 L0 REST 端点（创建/列表/详情/选项/暂停/恢复/取消/删除/批量、
  `/resolve`、`/capabilities`、`/config` GET+PATCH、`/stats`、`/healthz`、`/credentials`、幂等键、游标分页、统一错误码），
  WebSocket 事件推送（`07` §4）、Bearer 鉴权、`udd` daemon（随机端口 + 本地发现文件，不占 6800）、
  `udctl` CLI 的 P0 子集、以及单文件 Web 最小 UI（WBS 6，仅用于验证契约，可替换；OPEN-02 当时保持未决，2026-11-09 已定档 Flutter）。
- **文档**：`CHANGELOG.md`（本文件，Keep a Changelog 1.1.0 + SemVer 格式），以 `@root:CHANGELOG.md` 登记进 `tools/build_docs.py` 的 `GROUPS`，slug `changelog`，可在 `docs/_site/index.html` 门户中检索。
- **架构决策**：`ADR-0020-P0实现基线与范围声明.md`，归档三处规范修正、两处未定义细节与 P0 范围外能力清单；ADR 索引时间线补充该节点。
- **测试门禁**：`tools/check_arch.py` 6 条架构守卫、CI 工作流、竞态检测与 P0 契约/集成测试矩阵（见 `docs/12` §9）。

### Security
- **敏感请求头不再明文落库**：`TaskOptions.Headers` / `Resource.Headers` 原先以 JSON 原样写入 `task_options` 与 `resources.headers`，违反 `docs/06` §6「DB 仅 `cred_ref`」。新增 `security.StripSensitiveHeaders`，在 store 的两个持久化入口强制**剥离**（而非掩码，避免重启后把 `***` 当真实 header 回放）；`POST /tasks`、`POST /tasks/batch`、`PATCH /tasks/{id}` 直接拒绝 `Authorization` / `Cookie` / `X-Api-Key` 等内联凭据头（`400 E_INVALID_INPUT`），引导改用 `POST /credentials` + `credentialRef`。回归：`TestSensitiveHeadersNeverPersisted`、`TestInlineCredentialHeadersRejected`。
- **远程控制面 fail closed**：`listen` 为非回环地址且未配置 `remoteToken` 时，`config.Validate` 拒绝启动（原先会暴露无鉴权监听）；daemon 侧 `RequireAuth` 对任何非回环监听恒为 `true`。同时禁止 `allowLoopbackForDev` 与非回环监听组合。回归：`TestRemoteListenRequiresToken`。对齐 `docs/07` §1 与 ADR-0016。

### Fixed
- `PathGuard.Clean` 在 macOS `/var → /private/var` 符号链接下对任何临时目录误报「symlink escape」：
  改为根与父目录**双方都**解析符号链接后再比较。
- 暂停或进程退出时，在途分片曾被 `Release` 判为永久失败，导致恢复后任务无法完成：
  取消类错误一律回 `pending`；`ResetFailedChunks` 用于用户显式重试；崩溃恢复把 `failed` 一并回 `pending`。
- `udctl add <input> --conn 4` 这类「参数在位置参数之后」的写法被 Go `flag` 包忽略：
  增加 `reorderFlags` 预处理器，使 CLI 用法与 `07` §3 示例一致。
- `SSRFGuard` 在本地 fixture 模式下仍强制端口白名单，导致随机端口的 httptest / 本地文件服务器无法联调：
  `allowLoopbackForDev` 现在只对**回环目标**放开端口白名单，公网目标仍按白名单校验。
- 端到端冒烟（真实 `udd` + `udctl` + 本地 HTTP fixture，700KiB 文件 11 分片）验证：下载内容逐字节一致、
  `.part` 原子提交、状态机终态与 `chunksVerified == numPieces == 11`、`/healthz` `/stats` `/capabilities` 全通。
  收尾复跑补充验证了空库 `/stats`、URL query token 入库脱敏为 `%2A%2A%2A`、以及内联 `Authorization` 头返回 `400 E_INVALID_INPUT`。
- **全量测试暴露的真实缺陷**（逐个补了回归用例）：
  - ULID 同毫秒生成两个随机熵导致非单调 → `internal/core/id.go` 改为 mutex 保护的单调熵递增；
  - `Authorization: Bearer abc123` 中的 `abc123` 未被脱敏 → `security/redact.go` 的 `headerValuePattern` 覆盖 Bearer/Basic 前缀与 `x-api-key`/`passkey`；
  - `ed2k://|file|...` 中的 `|` 被 `net/url.Parse` 拒绝，输入分类失败 → `core.ParseInput` 在解析前预分类 `ed2k://`；
  - `Transport.Run` 在 `Next()` 返回空时立即退出，此时在途分片尚未 `Release` 回 `pending`，瞬时 503 来不及重试 → 增加 `claim()`：先排空当前波次再判定分片计划结束；同时把失败记录从 `map[index]error` 改为按 `attempt` 取最新结果的 `outcomes`，消除「重试成功清除」与旧失败上报之间的竞态；
  - `TestChunksAndBitfield` 期望 `8000` 与 BEP3/aria2 的 `ceil(numPieces/8)` 位图规范不符（3 片应为 1 字节 `80`）→ 修正测试期望，并在 `docs/06` §10 写明编码规则；
  - `tools/check_arch.py` 守卫 4 把测试夹具里的 `config.GlobalConfig{MaxDownloadRate:"0"}` 误判为「第二处速率配置读取」→ 该守卫只扫生产代码，cgo/外部进程守卫仍覆盖测试文件；
  - `internal/security/redact.go` 未 gofmt → 全仓 `gofmt` 通过。
- `TorrentMeta` 因 `json:"-"` 本就不应参与 JSON 往返，从 `TestResourceJSONRoundTrip` 的构造用例中移除；`TestTorrentMetaNotSerialized` 仍单独断言其不可序列化。
- **空库 `/stats` 报错**：`store.TaskSummary` 的 `SUM(CASE WHEN ...)` 在 `tasks` 表为空时返回 `NULL`，扫描进 `int` 触发 `E_INTERNAL: converting NULL to int is unsupported`，导致全新安装后 `udctl stats` 直接失败。所有聚合改为 `COALESCE(SUM(...), 0)`，并补 `TestTaskSummaryOnEmptyDatabase`。该缺陷由真实二进制端到端冒烟发现，单测此前始终在有数据的库上运行。

### Changed
- **接口修正（docs/05 §2）**：`ResolveContext.Guard` 由具体实现 `*security.SSRFGuard` 改为 core
  自有 `URLGuard` 接口，避免 `core` 反向依赖 `internal/security`（原设计违反 `05` §1 依赖方向）。
- **接口修正（docs/05 §2）**：`ChunkWriter` 增加 `Next()` / `Release()`，使 SQLite `chunks` 表成为
  分片唯一真值源，Transport 不再自行推算分片边界。
- **状态机口径（docs/05 §4）**：显式写明 `Cancel` 可从任意非终态进入 `Canceled`，终态不可再迁移；
  原 mermaid 图只画主路径、表格写「任意 → Canceled」，二者不一致，以 `core/task.go:transitions` 为权威。
- **文档全量同步（用户要求「所有代码更新及时同步相关文档」）**：
  `README.md` 从「规划完成，尚未开始编码」改写为 P0 可运行基线（快速开始、代码地图、已知限制）；
  `docs/README.md` 升 v0.3 并新增 §七 实现基线索引；`03` 补 P0 追踪矩阵；`04` 补 §11 落地映射；
  `05` 升 v0.2 并修正 §2/§4 接口与检查表；`06` 补 §6 敏感头规则、§10 位图编码与实现基线；
  `07` 升 v0.2 并补远程鉴权约束、P0 响应差异、CLI 子集与 §8 实现矩阵；`10` 补 §7 实际 pin 依赖；
  `11` 补 §10 安全基线与 5 条残余风险；`12` 补 §9 用例追踪矩阵；`13` 补配置键与 §12 构建链落地；
  `14` 补 §9 P0 验收结果；`15` 补 §10 变更日志规范与 §11 架构检查链；ADR 索引升 v0.2 并登记 0020。
- **文档门户**：`tools/build_docs.py` 支持 `@root:` 前缀登记根目录文档，`GROUPS` 新增「变更记录」分组，
  slug 规则显式固定 `changelog`；`docs/_site/index.html` 已重建。

[Unreleased]: https://github.com/asdfgh129/gobitmule/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/asdfgh129/gobitmule/releases/tag/v0.1.0
