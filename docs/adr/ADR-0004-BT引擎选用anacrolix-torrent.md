# ADR-0004 BT 引擎进程内自研（anacrolix/torrent），不外包给 aria2

- **状态**：Accepted（2026-11-09 P0 里程碑评审批量批准）
- **日期**：2026-11-08
- **决策者**：架构组
- **关联**：`04` §5.2、`10` §3、`16` §2、`12` §3.1

## 背景

用户提出「aria2 已经实现了 BT，为什么不把 BT 也直接交给 aria2」。核实结论：**aria2 的 BT 并不弱，弱的是它只能通过 RPC 当黑盒使用**。上游 aria2 1.37.0（2023-11 至今未发新版）是完整 v1 客户端（DHT、PEX、MSE、磁力、WebSeed、LSD、UDP tracker、选择下载），但官方特性清单里没有 uTP/BEP29、BT v2/BEP52、Merkle、打洞、WebTorrent、智能封禁。

本项目的「视频网站 / 边下边播 / 跨平台含移动端 / 统一队列」恰好都依赖细粒度控制。

## 决策

1. BT / 磁力采用 **`anacrolix/torrent` v1.61+（Go / MPL-2.0）**，作为进程内 Transport 实现，由核心 Scheduler 统一支配。
2. 流式读取使用 `File.NewReader()`（`io.ReadSeekCloser`，支持 `SetReadahead` / `SetResponsive`），可边下边播、喂给解密器或自定义存储后端。
3. 追求极致性能时的升级路径是 **cgo 接 libtorrent-rasterbar 2.x 到进程内**（单独立项评估），**不绕回 aria2**。
4. 默认只使用 v1 / hybrid 种子；BT v2 纯种子因生态支持不整齐不作为默认。
5. `Resource` 必须承载 v1 infohash / v2 btmh / private flag / tracker 分层 / passkey ref / peer_id（ADR-0013）。

## 备选方案与对比

| 维度 | aria2 1.37.0 | anacrolix/torrent |
|---|---|---|
| 协议深度 | 完整 v1 | BEP 3/4/5/6/7/9/10/11/12/15/19/20/23/27/29/40/41/42/43/47/52/54/55 + MSE/PE、uTP、WebTorrent、WebSeed、v2 混合、打洞、智能封禁、IPv6、tracker 客户端+服务端、scrape |
| 流式读取 | 只能等文件落盘；RPC 无 `io.Reader`、无分片优先级、无存储注入 | `File.NewReader()` 直接可读 |
| 可观测性 | `tellStatus` hex bitfield + `getFiles`/`getPeers` + 6 类 WS 通知 | 每 piece/peer/tracker 事件进程内可见 |
| 状态与文件 | `.aria2` + `aria2.session`，自定 `dir` | 全在自有 SQLite，落盘逻辑统一 |
| 移动端 | iOS 禁止派生子进程；Android W^X 禁止 exec app data | 纯 Go 编译进主二进制，两端可跑 |
| 许可证 | GPL-2.0-or-later | MPL-2.0，静态链接商业版相对友好 |
| 运维 | 6800 端口、rpc-secret、崩溃按 GID 重建、每平台一个二进制 | 无额外进程与端口 |

| 方案 | 结论 |
|---|---|
| `librqbit`（Rust） | 性能好但需跨语言集成，与 Go 核心不匹配 |
| `libtorrent` 2.0.11（C++） | 最强，但 cgo + Boost 破坏纯 Go 交叉编译；留作可选升级 |
| 把 BT 单独外包给 aria2 | ❌ 双调度器（ADR-0002 方案 C） |

## 后果

**正面**：细粒度进度/优先级/限速、边下边播、移动端可用、统一状态源、MPL 许可友好。

**负面**：库未实现 **LSD（BEP 14，issue #248 自 2018 未合）** 与 **Super-seeding（BEP 16）**；局域网内 qBittorrent 不会主动发现我们，需自补几十行组播或依赖 tracker/DHT。默认 `Seed=false`，做种策略须自行实现（`16` §4）。

**后续动作**：P1 建互操作回归套件（真实 qBittorrent/Transmission 双向做种；自建 chihaya/opentracker 覆盖 UDP/HTTP/IPv6/passkey/private flag）；显式实现分享率/时长、上传槽、UPnP/NAT-PMP；announce 统计统一取 `tasks.bytes_*`。

## 合规与验证

- `12` §3.1 BT-01..BT-14 全矩阵通过方可发布 P1。
- 抓包验证 `private=1` 任务无 DHT/PEX 流量（BT-08）、PeerID 诚实（BT-14）。
- 升级 `anacrolix/torrent` 必须重跑互操作套件（`15` §7）。

## 实现落地（2026-11-11 补记，不改写上述决策）

> 本节只记录实现事实与能力边界；上文「决策 / 备选方案 / 后果 / 合规与验证」保持原样，
> 尤其「**BT-01..BT-14 全矩阵通过方可发布 P1**」这条**仍然有效且尚未满足**。

**P1 WBS 7（2026-11-10）** 落实决策 1/2/4/5：`anacrolix/torrent v1.62.0`（MPL-2.0，
纯 Go、零 cgo）作为进程内 `internal/transport/bt` Transport，由 Scheduler 的 `Budget` 统一计费；
`File.NewReader()` 流式读取接入 `ChunkWriter`；`core.Resource` 承载 v1 infohash / private flag /
tracker 分层 / passkey ref，冻结字段未改（ADR-0013）。`resolve/magnet.go` +
`resolve/torrent.go` 处理磁力与 `.torrent`；私有种子强制 `NoDHT` / `DisablePEX` /
`NoUpload` / `Seed=false`；诚实 PeerID 前缀 `-UD0000-` + 6 随机字节（ADR-0017）。
**MPL-2.0 修改清单为空**（仅依赖，未改上游文件）。

**P1 WBS 11（2026-11-11）** 落实「后续动作」中的**套件基建**部分：
`internal/interop/` + `cmd/udinterop` 建立 `BT-01..BT-14` L4 门禁注册表、自建 BEP 3
HTTP/HTTPS 与 BEP 15 UDP tracker fixture（BEP 23 compact 6B/18B、passkey、多 tracker 分层、
故障注入）、9 条 `L3-xx` 纯回环自动化用例（独立 seeder ↔ `bttransport` 真实线协议、
诚实 PeerID 握手抓包、纯磁力 BEP 9 元数据、选择下载、流式首片时延），产出
`dist/interop.json` / `dist/INTEROP.md` 并由 CI 跑 `make interop`。
本轮 `go test -race ./...` 还发现并修复了 `waitInfo` 的生产数据竞态
（改为等待 `Torrent.GotInfo()`，补 `TestWaitInfoWaitsForGotInfo`）。

**落地时确认的能力边界（避免被误读为「全矩阵通过」）**：

1. `BT-01..BT-14` 是**发布门禁判据**，本次全部 `SKIPPED`；`BT-02`（我方做种）与
   `BT-11`（分享率 / seed-time）按 WBS 7 产品边界标 `UNSUPPORTED`，属明确不做。
   当前 **0/14 PASS，`release_gate_met: false`**，`make interop-gate` 明确失败。
2. 自建 tracker 是**纯 Go 回环 fixture**，不是 `chihaya` / `opentracker` 二进制；
   真实 qBittorrent / Transmission / libtorrent 客户端、抓包设备与千兆内网矩阵**未执行**。
3. 「后果 / 负面」仍然成立：LSD（BEP 14）与 Super-seeding（BEP 16）库未实现；
   默认 `Seed=false`；分享率 / 上传槽 / UPnP-NAT-PMP 与统一 announce 统计**仍为待办**。
4. 决策 3 的 cgo/libtorrent 升级路径与决策 4 的 v1/hybrid 默认策略**未改变**；
   升级 `anacrolix/torrent` 仍须重跑互操作套件（`15` §7）。
5. 产品文案维持 `16` §7 口径，只写「兼容标准 BitTorrent 网络，可与主流客户端互通」，
   不得写「已与主流客户端实测互通」「全矩阵通过」「千兆内网性能达标」。

**P1 WBS 11 续包（2026-11-13 补记，同样不改写上述决策）**：

- `internal/transport/bt` 新增 `WithPlainHandshake()`，**仅由 `cmd/udinteropx` 外部实验室
  显式开启**：把出站 `HeaderObfuscationPolicy.Preferred` 置为 false，消除第三方引擎 MSE
  首试协商竞态（默认策略下 8 个用例出现 2 次偶发超时，改后连跑 3 轮 12/12 PASS）。
  `RequirePreferred` 仍 false（加密回退保留），`udd` 默认 MSE 优先不变；
  `NoUpload` / `Seed=false` / private flag / 诚实 PeerID 等本 ADR 决策与 ADR-0017 控制
  **零改动**，`TestClientConfigHandshakePolicy` 作回归。
- 新增 `cmd/udinteropx` 双引擎 harness 与可选 `tools/interop_libtorrent.py`，取得真实
  `libtorrent 2.1.1` 的 `BX-03` / `BX-04` 单向回环证据（python 绑定为本机外部工具，
  非项目依赖、不进 SBOM、CI 不跑），连同 aria2-next 的 `BX-01` / `BX-02` 共 4/4 PASS。
  libtorrent 点名于 `BT-03`，但该判据要求双向（我方做种明确不做），故**不计入 L4**。
- **新发现的阻塞**：`BT-02` / `BT-11` 因决策中的 `Seed=false` 恒 `UNSUPPORTED`，
  `BT-03` 双向半向不可达，因此「BT-01..BT-14 全矩阵通过方可发布 P1」在现有产品边界下
  **形式不可满足**。三条处置路径（修订判据豁免 / 修订本 ADR 扩大产品范围实现做种 /
  保持开放）见 `12` §3.1，**须经评审签署**；签署前 `release_gate_met` 维持 `false`，
  `UNSUPPORTED` 不得改写为 PASS 或以豁免计入通过数。
- `make perf` 新增 1GiB 回环参考基准（固定 `nfr_perf_01.met=false`），不代表千兆内网
  NFR-PERF-01 达标；决策 3 的 cgo/libtorrent 升级路径与决策 4 的 v1/hybrid 默认策略
  **仍未改变**。

**阻塞根因二次校准（2026-11-13，ADR-0021 提案待评审；上文决策 / 后果 / 后续动作
原文不改）**：上文 WBS 11 续包中「`BT-02` / `BT-11` 因决策中的 `Seed=false` 恒
`UNSUPPORTED`」及「按 ADR-0004 不做种边界」属**误归属**。本 ADR「后果 / 负面」写的是
「默认 `Seed=false`，**做种策略须自行实现**」，「后续动作」更明确要求 P1 显式实现
分享率 / 时长、上传槽、UPnP/NAT-PMP 与统一 announce 统计；ADR-0017 决策 4 同样要求
「必须自行实现 P2P 礼仪」。因此 WBS 7 起 `internal/transport/bt.clientConfig` 固定
`NoUpload=true` / `Seed=false`、`internal/config.BTConfig` 至今仍为预留字段，
是**相对已批准决策的未签署范围缩减（按 `15` §4 规则②视为缺陷）**，
而不是本 ADR 授权的产品边界。序 6 阻塞应定性为「合规差距（根因）+ 判据冲突 +
实验室条件缺失」三者并存；仅修订 `12` §3.1 判据不足以消除合规差距。
已按 `docs/adr/README.md` 与 `15` §4 提交
[ADR-0021](ADR-0021-L4门禁与P2P礼仪范围冲突裁决提案.md)（`Proposed`，**未生效**），
列 A（补齐实现）/ B（修订范围，须把本 ADR 与 ADR-0017 标 `Superseded` 并同步 `03` 等九处）
/ C（保持开放）三条须评审签署的路径。签署前本 ADR 仍为 `Accepted`、
「`BT-01..BT-14` 全矩阵通过方可发布 P1」仍有效且未满足、
`release_gate_met` 维持 `false`、`UNSUPPORTED` 不得计 PASS 或以豁免计入通过数。
