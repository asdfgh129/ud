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
