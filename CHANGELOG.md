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
  `app/daemon_test.go`（0% → 86.5%，真实起停守护进程、discovery 生命周期、配置错误）、
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
- **Go module 路径与仓库地址统一为 `github.com/asdfgh129/ud`**：首次推送 GitHub 时把 `go.mod`、
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

[Unreleased]: https://github.com/asdfgh129/ud/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/asdfgh129/ud/releases/tag/v0.1.0
