# Universal Downloader（通用下载器）

跨平台的统一下载引擎：把 **BT / 磁力 / ed2k / HTTP(S) / FTP(S) / SFTP / Metalink / 迅雷链接 / 视频网站 / Telegram / 微信** 等来源，收敛到一条流水线、一个调度器、一份状态。

> **当前阶段：P0「有条件通过」的附加条件已于 2026-11-10 全部关闭，P1 进行中（WBS 8 / 9 / 7 / 10 已交付，WBS 11 互操作套件 v1 已交付；L4 真实客户端矩阵未执行、判据与不做种边界冲突待评审，序 6 仍开放）。** 当前态：**16 个门禁包**全部达标、**90 个链接模块** 0 copyleft / 0 未识别，`make gates-strict` 本机 **6 PASS / 0 WARN / 0 FAIL**（CI 于 2026-11-13 的 `cover` 步骤曾因 WAL 偶发 `SQLITE_BUSY` 失败，已在 `internal/store` 以有界事务重放修复，CI run `43399289527` 复跑 `quality: success`），`make interop` 本地 9 例全绿；本机 **aria2-next 3.0.9 + 真实 libtorrent 2.1.1 的 BX-01..BX-04 跨客户端证据（4/4 PASS，sha256 校验，单向回环补充、不计入 L4 门禁）**；`make perf` 产出 1GiB 回环参考基准（固定 `nfr_perf_01.met=false`）；唯一速查见 [`docs/README` §三](docs/README.md)，进度真值源为 [`CHANGELOG`](CHANGELOG.md) 与 [`docs/14` §11](docs/14-项目计划与风险管理.md)。
> 仓库已包含可运行的 Go 代码：HTTP(S) / FTP(S) / SFTP 直链与 BT / 磁力均能走完
> `Resolver → Resource → Transport → PostProcessor → Store` 全流水线。
> **P1 WBS 8 已交付（2026-11-10）**：`thunder://` / `flashget://` / `qqdl://` 解码与 Metalink
> v3/v4 + RFC 6249 清单解析已作为**内置、纯 Go、零网络 I/O** 的 Resolver 落地，含逐 URL
> SSRF 校验与按清单算法的整文件/逐片哈希校验。明确边界：多 `<file>` 清单、远端 `.meta4`
> 抓取、迅雷 P2SP / 高速通道 / 离线下载均**不支持**。
> **P1 WBS 9 已交付（2026-11-10）**：FTP / FTPS（隐式与 `AUTH TLS` 显式）/ SFTP
> 三个 Transport 落地，纯 Go、零 cgo、零新增 sidecar；FTP 被动数据连接逐条过 SSRF 数据通道
> 规则，SFTP 主机密钥必须 pin。凭据经 `POST /api/v1/credentials` 写入后以 `credentialRef`
> 引用，明文不进 URL / 数据库 / 日志 / 事件。**P1 首版每个分片独立建会话（并发上限 8）**，
> 连接池与 FTPS 数据通道 TLS session 复用属后续优化；不承诺完整 FTP/SFTP 生态。
> **P1 WBS 7 已交付（2026-11-10）**：基于 `anacrolix/torrent v1.62.0` 的 BT / 磁力
> Transport 落地（MPL-2.0，未修改任何 MPL 文件）；magnet 与 `.torrent`（raw / base64 /
> 本地文件）作为内置 Resolver 接入，magnet 经 BEP 9 从直连对等点取元数据。做种默认关闭
> （`Seed=false`）、私有种子强制禁 DHT/PEX、诚实 PeerID（`-UD0000-` 前缀）；`File.NewReader`
> 流式拷回 `ChunkWriter` 并受统一 Budget 约束。明确边界：**不承诺 PT / 私有站准入**，
> 真实公网 BT 客户端互操作矩阵在 WBS 7 时点列为 WBS 11 待办、证据为回环 fixture
> （WBS 11 套件 v1 交付后该矩阵**仍未执行**，见下文）。
> **P1 WBS 10 已交付（2026-11-10）**：基于 `goja` 的 ES5.1 插件规则引擎落地于
> `internal/plugin/`——manifest 严格校验、静态扫描、Ed25519 规则包签名、能力裁剪
> （`fs`/`env`/`subprocess` 默认禁用）、`ctx.http` 逐跳 SSRF Guard 与白名单、目录指纹热更新、
> `RULE_OUTDATED` 兜底；`udctl plugin scaffold|keygen|sign|verify|test|list` 为离线工具链。
> 规则包在 `direct-http` 之前接入 Resolver 链，安全敏感内置仍优先。明确边界：
> goja 是**软隔离**（无 per-VM 堆上限），不承诺 WASM / Extism 硬隔离、Node/Deno 脚本兼容、
> 官方规则仓库与真实站点规则包；插件 REST 管理端点未实现。
> **P1 WBS 11 互操作套件 v1 已交付（2026-11-11）**：`internal/interop/` + `udinterop`
> 建立 `BT-01..BT-14` L4 门禁注册表与 9 条纯回环自动化用例——自建 BEP 3 HTTP/HTTPS 与
> BEP 15 UDP tracker（compact IPv4/IPv6、passkey、故障注入）、独立 seeder ↔ `bttransport`
> 真实线协议下载、BT 握手抓包诚实 PeerID、纯磁力 BEP 9 取元数据、多文件选择下载与流式首片；
> `make interop` 产出 `dist/interop.json` / `dist/INTEROP.md`，并自动合并
> `dist/interop_external.json` 的真实跨客户端证据段。**明确边界**：
> `BT-01..BT-14` 需真实 qBittorrent / Transmission、抓包与千兆内网，本次全部记为
> `SKIPPED`（`BT-02` / `BT-11` 为产品明确不做的 `UNSUPPORTED`），**发布门禁未通过**、
> 序 6 不关闭；NFR-PERF-01 千兆内网基准同样未执行。
> 补充证据（2026-11-13 扩容）：`cmd/udinteropx` 支持双引擎，驱动项目真实 `bttransport`
> 对 **aria2-next 3.0.9**（MotrixNext，`BX-01` / `BX-02`）与真实 **libtorrent 2.1.1**
> （`BX-03` / `BX-04`，可选 `tools/interop_libtorrent.py` seeder，非项目依赖、CI 不跑）
> 做种方完成下载并 sha256 校验，取得 **4/4 PASS 真实互通证据**，`make interop-external` 可复跑。
> 但四条都是**单向（对方做种 → 我方下载）、回环、自托管 tracker**：aria2-next 不点名于
> `BT-01..BT-14`；libtorrent 虽点名于 `BT-03`，该判据要求双向（我方做种按 ADR-0004 明确不做），
> 且仍缺抓包、隔离千兆内网与 `chihaya` / `opentracker` 二进制，故**均不计入 L4 门禁**。
> 另已登记「14/14 PASS」收口条件与不做种边界的**形式冲突（待评审）**；
> `make perf` 的 1GiB 回环参考值固定 `nfr_perf_01.met=false`，不等于千兆内网达标。
> 不得据本地用例宣称「已与主流客户端实测互通」「全矩阵通过」或「千兆内网性能达标」。
> `make interop-gate` 在门禁未满足时明确失败。
> **P0 不是功能完整版**——ed2k / 真实站点规则包 / WASM 硬隔离 / sidecar / L1 RPC facade
> 仍为 P1/P3 目标，详见
> [ADR-0020](docs/adr/ADR-0020-P0实现基线与范围声明.md) 与 [更新日志](CHANGELOG.md)。

## 三条不可动摇的架构原则

1. **三类输入归一**：传输协议、链接封装格式、内容源解析器不是一类东西，全部经 `Resolver → Resource → Transport → PostProcessor` 处理。
2. **单调度器**：队列、限速、断点续传、进度、文件布局只在 Go 核心实现一次，不允许第二个组件拥有任务生命周期。
3. **纯 Go 单二进制**：传输层零 sidecar、零 cgo、零 GPL 依赖；外部能力一律以可降级插件形式接入，桌面与移动端可达性优先。

## 快速开始（P0）

```bash
# 需要 Go 1.27.4（因 GO-2026-6809 / GO-2026-6859 两次安全升级）；所有构建强制 CGO_ENABLED=0
make lint          # gofmt + go vet + 架构静态检查（6 守卫）
make test          # 全量单测 / 契约 / 集成
make build         # 六平台交叉编译 → dist/（udd + udctl 各 6 个）
make gates         # 评审门禁：覆盖率 / SBOM / 许可证 / govulncheck / 基准 / 回滚演练 → dist/
make interop       # WBS 11 互操作套件 v1（本地回环用例）→ dist/interop.json + INTEROP.md
make interop-gate  # L4 发布门禁（外部矩阵未执行时明确失败）
```

> `make gates` / `make gates-strict` 需要 `govulncheck`（`go install golang.org/x/vuln/cmd/govulncheck@latest`，
> 可用 `GOVULNCHECK=<path>` 指定）。工具缺失或漏洞库不可达时该门禁明确报 WARN，不会伪装通过。
> 若本机全局 `GOSUMDB=off`，Makefile 会在项目级设为 `sum.golang.org` 以允许 `GOTOOLCHAIN=auto` 校验工具链模块，不修改用户全局配置。

启动 daemon 与 CLI（默认 `127.0.0.1:0` 随机端口 + 本地发现文件，不占固定 6800）：

```bash
cp config.example.yaml config.yaml
go run ./cmd/udd --config config.yaml          # 或 go build -o udd ./cmd/udd
go run ./cmd/udctl health
go run ./cmd/udctl capabilities
go run ./cmd/udctl add "https://example.com/file.iso" --conn 4
go run ./cmd/udctl list
go run ./cmd/udctl show <id>
```

浏览器打开 daemon 地址可看到嵌入式最小 UI（仅用于验证控制面契约，可替换）。`GET /api/v1/healthz`、`/stats`、`/capabilities` 与 `WS /api/v1/events` 可用；接口详见 [`docs/07`](docs/07-接口规范-API.md)。

> 若默认 Go 模块代理不可达，可覆盖：`GOPROXY=https://goproxy.cn,direct make build`。

## 代码地图（当前仓库）

| 目录 | 职责 |
|---|---|
| `internal/core` | 冻结的 `Resource` 全字段模型、Task 状态机、错误码、ULID、事件总线、全部契约接口（零实现依赖） |
| `internal/config` | YAML/环境变量配置、校验、运行时 `PATCH /config` 管理器 |
| `internal/security` | SSRF Guard（含 FTP 被动数据通道规则）、PathGuard、脱敏、`CredentialStore` 的进程内参考实现 |
| `internal/store` | SQLite DDL v1（11 表）、Resource 规范化存取、分片真值表、按清单算法的整文件/逐片校验、崩溃恢复、备份 |
| `internal/resolve` | 内置 Resolver 注册链：Metalink v3/v4 + RFC 6249、thunder/flashget/qqdl 解码、HTTP(S) 与 FTP(S)/SFTP 直链、`Unsupported` 兜底（安全敏感内置优先） |
| `internal/transport/http` | Range 分段 / 无 Range 退化 / 重定向逐跳 / 重试 / 分片 sha256 |
| `internal/transport/ftp` · `sftp` | FTP/FTPS（隐式 + 显式 TLS、REST 续传、被动数据通道 SSRF pin）与 SFTP（host key pin、ReadAt 随机读） |
| `internal/transport/tputil` | 三个传输共享的凭据 / 目标校验 / 分片循环 / Budget 计费 |
| `internal/postproc` | ADR-0012 本地文件契约的 P0 `none` 直通实现 |
| `internal/scheduler` | 唯一调度器、唯一 Limiter、ChunkWriter、清单 piece 长度锁定分片边界、原子提交 |
| `internal/api` · `internal/app` | L0 REST + WebSocket、daemon 装配、嵌入式最小 UI |
| `internal/plugin` | WBS 10 规则通道：manifest 严格校验、ES5.1 静态扫描、Ed25519 签名、goja 运行时、能力裁剪 / 逐跳 SSRF、目录指纹热更新与离线回放；在通配 `direct-http` 之前接入注册链 |
| `internal/interop` | WBS 11 互操作套件：L4 门禁注册表、外部 harness、BEP 3/15 自建 tracker fixture、9 条回环自动化用例与证据输出 |
| `cmd/udd` · `cmd/udctl` · `cmd/udinterop` | daemon、CLI 与互操作套件入口 |
| `tools/` | `build_docs.py`（文档门户）、`check_arch.py`（架构守卫）、`gates.py`（评审门禁证据） |

## 文档入口

**→ [`docs/README.md`](docs/README.md)：文档总览、阅读路径、完整清单与 P0 实现基线索引**

| 我想…… | 读这份 |
|---|---|
| 了解项目边界、非目标、成功指标 | [`docs/01-项目章程.md`](docs/01-项目章程.md) |
| 查功能/非功能需求与 P0 追踪矩阵 | [`docs/03-需求规格说明书-SRS.md`](docs/03-需求规格说明书-SRS.md) |
| 理解整体架构与流水线 | [`docs/04-架构设计说明书-SAD.md`](docs/04-架构设计说明书-SAD.md) |
| 查接口签名、状态机与 P0 检查表 | [`docs/05-详细设计说明书-LLD.md`](docs/05-详细设计说明书-LLD.md) + [`docs/06-数据模型与存储设计.md`](docs/06-数据模型与存储设计.md) |
| 查 REST / WS / CLI 契约 | [`docs/07-接口规范-API.md`](docs/07-接口规范-API.md) |
| 查「为什么这么定」 | [`docs/adr/README.md`](docs/adr/README.md) |
| 查实际改了什么、修了什么 | [`CHANGELOG.md`](CHANGELOG.md) |
| 查 P0 范围边界与残余风险 | [`ADR-0020`](docs/adr/ADR-0020-P0实现基线与范围声明.md) |
| 查排期与 P0 验收结果 | [`docs/14-项目计划与风险管理.md`](docs/14-项目计划与风险管理.md) |
| 查 P0 评审结论、签署与附加条件 | [`docs/17-P0里程碑评审材料.md`](docs/17-P0里程碑评审材料.md) |
| 看哪些文档待审核 / 已决定 | 文档门户首页 `#/home` 的状态总览 |

预览型阅读：运行 `python3 tools/build_docs.py` 后打开 `docs/_site/index.html`。这是一个带侧栏的单文件门户，**默认打开 `#/home` 状态首页**：统计卡 + 状态徽章 + 分类卡片，直观显示每份文档是「待审核 / 已决定 / 持续维护 / 已废止 / 未标注」，并支持按状态、类型筛选与搜索；侧栏保留全部 41 篇文档链接，CHANGELOG、ADR 与 P0 评审材料均可直接跳转。`docs/_site/` 是生成物，禁止手工编辑。

## 明确的能力边界（写进产品文案）

- ✅ **P1 WBS 11 已实现（套件）**：`BT-01..BT-14` 门禁注册表、自建 HTTP(S)/UDP tracker fixture、9 条可复跑回环用例与 `dist/INTEROP.md` 证据链；❌ **L4 真实客户端矩阵（qBittorrent / Transmission）与 NFR-PERF-01 双机千兆基准未执行**，`make interop` 绿灯**不等于**发布门禁通过，不得宣称「已与主流客户端实测互通」。✅ **补充证据（不计入 L4）**：`BX-01..BX-04`（aria2-next 3.0.9 + 真实 `libtorrent 2.1.1`，4/4 PASS、sha256 校验）均为**单向回环**证据；libtorrent 虽点名于 `BT-03`，但该判据要求双向且需抓包 / 千兆内网，故不通过。⚠️ **「14/14 PASS」与 ADR-0004 不做种边界存在形式冲突，待评审处置**（`12` §3.1）；`make perf` 只是 1GiB 回环参考（固定 `nfr_perf_01.met=false`）。
- ✅ **P1 WBS 10 已实现**：goja ES5.1 插件规则引擎（manifest 严格校验、Ed25519 签名、能力裁剪、逐跳 SSRF Guard、目录指纹热更新、`udctl plugin` 离线工具链、`RULE_OUTDATED` 兜底）；**软隔离**，不承诺 WASM / Extism 硬隔离、Node/Deno / 浏览器扩展脚本兼容、现代 JS 语法、官方规则仓库与真实站点规则包（L4 互操作矩阵属 WBS 11）。
- ✅ **P1 WBS 7 已实现**：BT / 磁力链接下载（标准 BitTorrent 网络、公开 HTTP/UDP/HTTPS tracker、magnet 经 BEP 9 取元数据；做种默认关闭、私有种子禁 DHT/PEX、诚实 PeerID）；标准 eD2k 网络（线协议层）为 P3 目标，尚未实现。
- ✅ **P0 已实现**：HTTP(S) 直链、Range 分段、断点续传、暂停/恢复/取消、崩溃恢复、全局限速、L0 REST/WS/CLI、最小 UI、六平台纯 Go 构建。
- ❌ **不承诺**私有 tracker 准入、迅雷 P2SP/高速通道/离线下载、ed2k 下载速度、DRM 内容解析、100% aria2 RPC 兼容。
- ⚠️ 微信视频号嗅探为**单独分发、默认不安装**的可选插件，涉及逆向与平台条款风险。

## P0 已知限制

1. `MemoryCredentialStore` 是进程内参考实现，`mem://` 引用在 daemon 重启后失效；OS 钥匙串适配器待 P1。
2. 非回环监听必须配置 `remoteToken`（否则拒绝启动），且目前只有 Bearer 鉴权，无 TLS / Origin 校验。
3. `postProcess` 只支持 `none`；最小 UI 是契约验证占位件（OPEN-02 已定档 Flutter，正式 UI 不进入 P1 编码范围）。
4. CycloneDX SBOM / `govulncheck` / 许可证扫描 / 覆盖率报告 / 回滚演练已由 `make gates-strict` 执行，覆盖率全部达标；WBS 11 起互操作套件 v1 的**本地自动化部分**已由 `make interop` 执行，但 `BT-01..BT-14` L4 真实客户端矩阵与 NFR-PERF-01 千兆内网性能基准**仍未执行**，发布门禁未通过（见 `dist/INTEROP.md`、`docs/17` §3/§5）。

完整清单见 [`docs/11` §10](docs/11-安全与合规设计.md) 与 [`docs/14` §9](docs/14-项目计划与风险管理.md)。

## 许可证

**代码：MPL-2.0**（2026-11-09 定档，见 [`LICENSE`](LICENSE)）；**文档：CC-BY-4.0**。发布二进制须随附 `LICENSE` 与 `THIRD_PARTY_NOTICES.md`，修改过的 MPL 文件按 MPL-2.0 提供并记录修改清单。依赖许可证矩阵见 `docs/10-技术选型与依赖清单.md` §4，P0 实际锁定依赖见其 §7。
