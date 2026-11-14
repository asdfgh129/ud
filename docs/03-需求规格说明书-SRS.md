# 03 软件需求规格说明书（SRS）

> 文档编号：UD-DOC-03 ｜ 版本：v0.9 ｜ 状态：草案（P0 追踪矩阵已补全，P1 WBS 7/8/9/10/11 已补记，待评审） ｜ 日期：2026-11-14 ｜ 上游：`01`、`adr/`
>
> **v0.9 变更 / 2026-11-14 时点口径（ADR-0021 路径 A，追加；下文 v0.8 及更早说明为提案期时点值，保留不改）**：
> 项目发起人明确指令「按 A 方案补齐」，ADR-0021 由 `Proposed` 转 `Accepted（路径 A）`
> 并同步 ADR 索引。**实现差距已消除**：`bt.*` 生效、受限做种 / 分享率 / `seed-time` /
> 上传槽 / UPnP-NAT-PMP / 诚实 PeerID / `tasks.bytes_*` 统一统计与
> `Completed → Seeding → Completed` 条件迁移已落地（见 `internal/transport/bt/seeding.go`、
> `internal/config/config.go`、`internal/app/seeding_observer.go`）。
> **但 `12` §3.1 的 L4 判据未改**：`BT-01..BT-14` 仍 **0/14**、`BT-02` / `BT-11`
> 仍 `UNSUPPORTED`（实现具备 ≠ 真实客户端矩阵已验证），`release_gate_met=false`、
> `l4_executed=false`、`nfr_perf_01.met=false`、序 6 仍开放。
> **评审组五方签署表仍全部留白**：本次为发起人指令裁决，不冒充三方会签；
> 法务口径、`14` R-10 反吸血风险重评与安全 / P2P 礼仪复签仍待补。
>
> **v0.8 变更**（2026-11-13，序 6 阻塞定档，序 6 仍不关闭）：按 `15` §4 规则②核对
> 已批准的 ADR-0004 / ADR-0017 与本文件 **FR-INT-01 / FR-INT-04 / FR-TRN-05（M）**、
> `01` 章程、`05` §5、`07` / `13` §7，确认它们都要求显式实现做种礼仪；而 WBS 7 起
> `bt.clientConfig` 固定 `NoUpload=true` / `Seed=false`、`config.BTConfig` 仍是预留字段。
> 故 v0.7 说明与 §8 补记中「**ADR-0004 不做种边界**」属**误归属**，保留为时点措辞不改，
> 以本注校准：ADR-0004 把 `Seed=false` 记为「须自行补偿的库默认」，ADR-0017 决策 4
> 更要求「必须自行实现 P2P 礼仪」。§8 新增三行登记该合规差距、ADR-0021 裁决提案与
> L4 实验室可执行链路；**M 级需求未落地不等于已豁免**，待评审按 ADR-0021 签署后
> 才允许修订本文件需求等级或范围。L4 **0/14**、`release_gate_met: false` 不变。
>
> **v0.7 变更**：P1 WBS 11 续包补记（序 6 **仍不关闭**）。§8 追踪矩阵新增
> `BX-01..BX-04` 真实第三方引擎单向回环补充证据（aria2-next 3.0.9 + `libtorrent 2.1.1`，
> 4/4 PASS、**不计入 L4**）、`make perf` 1GiB 回环参考基准（固定 `nfr_perf_01.met=false`）
> 与**L4 判据与 ADR-0004 不做种边界的形式冲突（待评审）**；§9 准则 2 的
> 「互操作套件全矩阵通过」仍未满足，`release_gate_met: false`。
> 门禁包 16 / 链接模块 90 / `make gates-strict` 6 PASS 口径不变，零新增依赖。
>
> **v0.6 变更**：P1 WBS 11（互操作套件 v1）落地，§8 新增「P1 WBS 11 追踪矩阵补记」表。
> **真实公网 / 真实客户端 L4 互操作矩阵（`12` §3.1 `BT-01..BT-14`）仍全部 `SKIPPED`**
> （`BT-02`/`BT-11` 为产品明确不做的 `UNSUPPORTED`），`release_gate_met: false`，
> §9 准则 2 的「互操作套件全矩阵通过」**仍未满足**，序 6 不关闭。
>
> **v0.5 变更**：P1 WBS 10（插件引擎）落地，§8 新增「P1 WBS 10 追踪矩阵补记」表。
>
> **v0.4 变更**：P1 WBS 7（BT / 磁力）落地，§8 新增「P1 WBS 7 追踪矩阵补记」表。
> §7 里程碑表与 P0 追踪矩阵保持历史排期原值不改；真实公网 BT 互操作矩阵（L4）仍为 WBS 11 待办。

## 1. 引言

### 1.1 目的与范围
定义 UD 的功能需求（FR）、非功能需求（NFR）、外部接口与验收标准，作为设计（`04`–`09`）、测试（`12`）与里程碑验收（`14`）的基线。所有需求必须可验证；不可验证的陈述写入 `01` 的愿景而非本文档。

### 1.2 需求优先级
`M` 必须（P0/P1 前完成）｜ `S` 应该 ｜ `C` 可以 ｜ `W` 期望但本期不做。优先级与里程碑绑定见 §7。

### 1.3 需求编号与追踪
`FR-<域>-<序号>` / `NFR-<类别>-<序号>`；追踪矩阵见 §8，测试用例编号约定 `T-<需求号>-<序号>`。

## 2. 用户画像与典型场景

| 画像 | 场景 | 关注点 |
|---|---|---|
| 普通用户 | 粘贴一条磁力/视频页/直链，点下载 | 一键、进度清晰、能暂停 |
| 重度用户 | 数百任务、限速、排队、批量导入 Metalink / 种子 | 队列、全局限速、断点 |
| 移动端用户 | 手机上下 BT / Telegram 文件 | 无子进程、省电、后台续传 |
| NAS / 运维 | headless 部署、AriaNg 接管、远程 API | RPC 兼容、日志、低资源 |
| 插件开发者 | 新增一个站点解析规则 | 稳定契约、热更新、沙箱 |

## 3. 功能需求

### 3.1 任务管理 `FR-TSK`
| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-TSK-01 | 创建/暂停/恢复/取消/删除任务，语义对**所有来源一致** | M |
| FR-TSK-02 | 批量创建、批量操作、优先级与队列顺序调整 | S |
| FR-TSK-03 | 任务状态机可持久化；进程崩溃重启后恢复到可运行状态 | M |
| FR-TSK-04 | 全局并发上限、每任务连接数上限、每任务下载/上传限速 | M |
| FR-TSK-05 | 提供进度、速度、剩余时间、连接数、peer 数、分片位图 | M |
| FR-TSK-06 | 完成/失败/暂停等事件通过 WebSocket 推送 | S |

### 3.2 输入解析与 Resolver `FR-RSV`
| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-RSV-01 | 接受 URL、magnet、`.torrent` 文件/base64、ed2k 链接、Metalink 文件、视频页 URL、Telegram 消息引用、剪贴板与浏览器扩展提交 | M |
| FR-RSV-02 | 自动识别输入类别并路由到对应 Resolver；失败给出可读原因 | M |
| FR-RSV-03 | 解析 `thunder://` / `flashget://` / `qqdl://`（Base64 包裹还原为真实 URL）；**仅解码，不实现 P2SP** | M |
| FR-RSV-04 | 解析 Metalink v3/v4（RFC 5854：file/url/hash/pieces/metaurl）与 Metalink-HTTP（RFC 6249：`Link: rel=duplicate`、`Digest:`），产出多镜像 Resource | S |
| FR-RSV-05 | 视频站 Resolver 只返回流地址、`http_headers`、`cookies`、格式列表，**不得自行写文件** | M |
| FR-RSV-06 | Resolver 输出必须是完整 `Resource`，含多文件布局与协议身份字段 | M |
| FR-RSV-07 | 所有 Resolver 输入视为不可信，强制经 SSRF 白名单与大小/数量限制 | M |

### 3.3 传输能力 `FR-TRN`
| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-TRN-01 | HTTP/HTTPS 多线程 Range 分段、断点续传、慢启动、重定向跟随、自定义 Header/Cookie | M |
| FR-TRN-02 | 分片与整文件校验（SHA-256 / SHA-1 / MD5 / Metalink piece hash），失败重下该分片 | M |
| FR-TRN-03 | 多镜像自动切换（按优先级/地理位置/失败率） | S |
| FR-TRN-04 | FTP / FTPS（`jlaffaye/ftp`，显式 TLS，offset 续传）与 SFTP（`pkg/sftp`） | S |
| FR-TRN-05 | BT / 磁力：DHT、PEX、uTP、MSE、WebSeed、IPv6、HTTP/UDP tracker、private flag、选择下载、做种策略 | M |
| FR-TRN-06 | BT 流式读取（边下边播）：`File.NewReader` 语义可对接自有存储后端 | S |
| FR-TRN-07 | ed2k：eD2k server + Kad、AICH、Secure Ident、积分、Source Exchange、混淆、Low ID 回调 | C |
| FR-TRN-08 | HLS/DASH：清单解析、AES-128 解密、按分辨率/音轨选流、分片并发与顺序提交 | M |
| FR-TRN-09 | 所有 Transport 由同一个 Scheduler 支配；禁止外部组件自建队列/限速 | M |
| FR-TRN-10 | 重试策略可配（指数退避、最大次数、错误分类）；tracker/镜像失败自动降级 | M |

### 3.4 内容源 `FR-SRC`
| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-SRC-01 | 中文视频站进程内解析（`lux` + goja 规则插件），规则包可热更新 | S |
| FR-SRC-02 | 长尾站点与 YouTube 经 `yt-dlp` sidecar 解析（`-J --skip-download`） | C |
| FR-SRC-03 | YouTube 链路需 Deno 2.3+（或 Node 22+）作为 yt-dlp 子依赖，随包/按需提供 | C |
| FR-SRC-04 | Telegram 经 `gotd/td` 纯 Go MTProto 下载，支持 2GB（Premium 4GB）、CDN 重定向、`FLOOD_WAIT`、会话持久化、流式写入自有 Transport | S |
| FR-SRC-05 | 微信能力（公众号视频 HTML 解析 / 视频号 MITM）为**单独分发的可选插件**，默认不安装；提权操作隔离在独立助手 | W |
| FR-SRC-06 | 凭据（Cookie、Telegram session、代理密码）存系统钥匙串，按任务最小化传递 | M |

### 3.5 后处理 `FR-PST`
| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-PST-01 | 内置纯 Go `Remux`：HLS/DASH 分离流合并为可播放 MP4/MKV（TS 解封装、Annex B→AVCC、ADTS→MP4、PTS/DTS 重基准、写 moov + faststart） | S |
| FR-PST-02 | 可选 `Transcode`（ffmpeg 桌面插件）：转码、转 MP3、烧字幕、缩略图、坏流修复 | C |
| FR-PST-03 | `PostProcessor` 输入**只能是本地文件路径**；输出写 `.part` 后由核心原子 rename 提交 | M |
| FR-PST-04 | 字幕（VTT/SRT 转换）、封面、元数据写入 | S |
| FR-PST-05 | 移动端默认禁用 ffmpeg，由 Remux 顶上；ffmpeg 缺失/版本不符只降级高级后处理 | M |

### 3.6 平台与接口 `FR-PLT`
| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-PLT-01 | REST + WebSocket + CLI 三类控制面；核心以 daemon 运行，UI 分离 | M |
| FR-PLT-02 | 可选 L1 aria2 JSON-RPC facade（compat subset，约 25 方法 + 6 通知），可用 AriaNg 直接连接 | C |
| FR-PLT-03 | 插件引擎：goja 起步（ES5.1），可选 wazero/Extism 硬隔离；manifest + 签名 + 能力裁剪 | S |
| FR-PLT-04 | SQLite（`modernc.org/sqlite`，纯 Go）持久化任务/分片/状态，WAL 模式 | M |
| FR-PLT-05 | 外部依赖 manifest 化：version / sha256 / license / buildconf / capabilities / downloadURL；启动时能力协商 | M |
| FR-PLT-06 | 任何 sidecar 缺失、版本过低或崩溃，只把对应能力标记为不可用，不阻塞其余下载 | M |
| FR-PLT-07 | 结构化日志（脱敏）、指标、健康检查、任务事件审计 | S |
| FR-PLT-08 | `CGO_ENABLED=0` 下产出 Windows/macOS/Linux x64+arm64 与移动端构建物 | M |

### 3.7 互操作 `FR-INT`
| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-INT-01 | 与 qBittorrent / Transmission / libtorrent 系客户端双向做种与下载 | M |
| FR-INT-02 | 兼容公开 HTTP/HTTPS/UDP tracker、compact peer list、多 tracker 分层、IPv6、passkey、private flag、scrape | M |
| FR-INT-03 | `private=1` 任务自动关闭 DHT/PEX/LSD；passkey 不进日志、不跨任务同步 | M |
| FR-INT-04 | 实现做种分享率/时长策略、上传槽、UPnP/NAT-PMP 端口映射；announce 统计取自统一 Task 模型 | M |
| FR-INT-05 | eD2k 默认开启协议混淆与 Secure Ident，并持久化 RSA 身份与积分 | C |
| FR-INT-06 | 保持诚实 PeerID，禁止伪装白名单客户端；产品不承诺 PT 准入 | M |

## 4. 非功能需求

| 编号 | 类别 | 需求与可验证指标 | 优先级 |
|---|---|---|---|
| NFR-PERF-01 | 性能 | 千兆内网单任务 HTTP 吞吐 ≥ 80MB/s；1000 任务常驻内存增量 ≤ 300MB | M |
| NFR-PERF-02 | 性能 | 下载热路径不引入额外进程/额外一次全文件拷贝；ffmpeg 仅作用于后处理 | M |
| NFR-REL-01 | 可靠性 | 进程 kill -9 后重启，已落盘分片 100% 复用；无数据损坏（校验通过） | M |
| NFR-REL-02 | 可靠性 | 单个任务 panic 不影响 daemon；sidecar 崩溃自动重启且不影响其他任务 | M |
| NFR-REL-03 | 可靠性 | 取消任务能可靠终止所有子进程（Unix 进程组 / Windows 进程树） | M |
| NFR-SEC-01 | 安全 | 所有不可信 URL 经 SSRF 白名单；通过私网/回环/元数据地址与 DNS 重绑定攻击用例 | M |
| NFR-SEC-02 | 安全 | 凭据不落 SQLite、不进日志；passkey/authorization 全脱敏 | M |
| NFR-SEC-03 | 安全 | 二进制依赖校验 sha256；插件经签名校验与能力裁剪 | M |
| NFR-MNT-01 | 可维护性 | `Resource`/`Resolver`/`Transport`/`PostProcessor` 接口稳定；新增站点不需重发主程序 | M |
| NFR-MNT-02 | 可维护性 | 核心包不依赖 UI、不依赖具体站点规则；依赖方向单向 | M |
| NFR-PORT-01 | 可移植性 | 传输层 `CGO_ENABLED=0`；iOS/Android 可运行 BT/HTTP 下载 | M |
| NFR-PORT-02 | 可移植性 | 不依赖 6800 等固定端口；无 `.aria2` 等外部状态文件 | M |
| NFR-LEG-01 | 合规 | 传输热路径 GPL 依赖数 = 0；所有依赖有许可证与溯源记录 | M |
| NFR-LEG-02 | 合规 | 产品文案与 `01` §5、`16` §7 口径一致，不出现「支持迅雷加速」「支持 PT」 | M |
| NFR-USE-01 | 易用性 | 任务创建到开始传输 ≤ 1s（不含解析）；错误提示可读且可操作 | S |

## 5. 外部接口需求（摘要）

完整契约见 `07`。控制面：REST `/api/v1/*`、WebSocket `/api/v1/events`、CLI `udctl`；可选 `/jsonrpc` aria2 兼容端点。外部组件通信：JSON-RPC 2.0 over stdio（sidecar）、参数白名单 exec（ffmpeg）、内置 RPC（历史兼容，非依赖）。

## 6. 用例

| 编号 | 用例 | 主成功流程 | 关键异常 |
|---|---|---|---|
| UC-01 | 下载直链 | 提交 URL → Resolver 产出 Resource → HTTP 分段 → 校验 → 完成 | 无 Range 支持时退化为单连接 |
| UC-02 | 下载磁力 | 提交 magnet → 获取元数据 → 用户选文件 → BT 分片 → 做种 | DHT 无 peer、private flag |
| UC-03 | 下载视频页（合流格式） | Resolver 只取 JSON → 核心下 progressive 单文件 → 完成 | 站点规则失效，降级/提示更新 |
| UC-04 | 下载视频页（分离流） | 取两条流 → 各存一份 → Remux → 原子提交 | 无 ffmpeg 时画质受限（yt-dlp 默认退化）→ 由自有 formats 选取规避 |
| UC-05 | 下载 Telegram 大文件 | MTProto 解析 → `Stream(io.Writer)` → 自有 Transport 落盘 | `FLOOD_WAIT`、DC 迁移、会话失效 |
| UC-06 | 导入 Metalink | 解析多镜像 + piece hash → 多源分段 → 逐片校验 | 恶意清单：私网 URL、超大 XML、镜像爆炸 |
| UC-07 | 崩溃恢复 | 重启 → 读 SQLite → 校验已落盘分片 → 续传 | 分片文件被外部改动，重下该片 |
| UC-08 | AriaNg 接管（L1） | AriaNg 调 `addUri/tellStatus` → facade 映射到自有 Task | 未知选项必须忽略而非报错 |

## 7. 需求 → 里程碑映射

| 阶段 | 覆盖需求 | 阶段验收 |
|---|---|---|
| P0 | FR-TSK-01/03/04/05、FR-RSV-01/02/06、FR-TRN-01/02/09、FR-PST-03、FR-PLT-01/04/08、NFR-REL-01、NFR-SEC-01/02、NFR-PORT-01 | 一条 HTTP 直链走完整流水线，kill -9 可续传 |
| P1 | FR-TRN-04/05/10、FR-RSV-03/04、FR-PLT-03、FR-INT-01/02/03/04 | BT 与 qBittorrent 双向互通；SSRF 用例全过 |
| P2 | FR-SRC-01/02/03、FR-TRN-08、FR-PST-01/02/04/05 | 分离流合成为可播放 MP4，无 ffmpeg |
| P3 | FR-TRN-07、FR-SRC-04/05、FR-INT-05、FR-PLT-02 | eD2k 小范围试连；AriaNg 连接成功 |

## 8. 需求追踪矩阵（样例）

| 需求 | 设计文档 | 接口 | 测试用例 | 验收阶段 |
|---|---|---|---|---|
| FR-TRN-09 单调度器 | `04` §5 / ADR-0002 | — | T-FR-TRN-09-01 全局限速唯一性 | P0 |
| FR-RSV-06 协议身份字段 | `05` §4 / `06` §4 / ADR-0013 | `06` Resource JSON | T-FR-RSV-06-01 多文件+双 infohash+passkey | P0 |
| FR-PST-03 本地文件契约 | `05` §9 / ADR-0012 | `PostProcessor` | T-FR-PST-03-01 `.part` 原子提交 | P0 |
| FR-INT-03 private flag | `16` §3 | — | 互操作套件 BT-12 | P1 |
| FR-PLT-06 降级 | `09` §6 | manifest | T-FR-PLT-06-01 删除 ffmpeg 后下载不受影响 | P2 |

> 完整追踪矩阵由 QA 在 P0 启动前补齐并随每次需求变更更新（`15` §5）。

**P0 阶段追踪矩阵补全（2026-11-08）**：下表把 §7 中标为 P0 的 `M` 需求映射到已存在的自动化用例（用例 ID 沿用 `T-<需求号>-<序号>` 命名），完整用例清单见 `12` §9。P1+ 行仍为空格是**当时的**正确排期状态，不是遗漏；WBS 8 已实现的 P1 需求见下方补记。

| 需求 | 设计文档 | 自动化用例 | 结果 |
|---|---|---|---|
| FR-TSK-01/03/04/05 任务生命周期与进度 | `05` §4 | `TestPauseResumeCancel`、`TestFullPipelineSingleFile`、`TestApplyTransitionClearsErrorAndSetsCompletedAt` | ✅ |
| FR-RSV-01/02 输入归一与分类 | `05` §2 | `TestParseInputClassification`（含 `ed2k://`） | ✅ |
| FR-RSV-06 协议身份字段 | `05` §3 / `06` §4 / ADR-0013 | `TestResourceJSONRoundTrip`、`TestTorrentMetaNotSerialized`、`TestResourcePersistenceRoundTrip` | ✅ |
| FR-TRN-01/02 HTTP Range 与断点 | `05` §5 | `TestRangeDownload`、`TestNoRangeFallback`、`TestTransientRetryThenSuccess` | ✅ |
| FR-TRN-09 单调度器 / 唯一 Limiter | `04` §5 / ADR-0002 | `TestLimiterSingleAuthority`、`TestDynamicSlots`；`check_arch.py` 守卫 4 | ✅ |
| FR-PST-03 本地文件契约 | `05` §9 / ADR-0012 | `TestPostProcessorSignature`、`TestPassthroughRejectsNonLocalInput`；守卫 3 | ✅ |
| FR-PLT-01/04/08 L0 控制面与最小 UI | `07` §2/§4 | `TestRESTContract`、`TestWebSocketEventDelivery`、`TestMinimalUIIsServed` | ✅ |
| NFR-REL-01 崩溃恢复 | `06` §5 | `TestCrashRecoveryReusesChunks`、`TestCrashRecoveryResetsAndVerifies` | ✅ |
| NFR-SEC-01/02 SSRF 与凭据 | `11` §2/§3 | `TestSSRFGuardBlocksUntrustedTargets`、`TestRedaction`、`TestSensitiveHeadersNeverPersisted`、`TestInlineCredentialHeadersRejected`、`TestRemoteListenRequiresToken` | ✅ |
| NFR-PORT-01 纯 Go 六平台 | `13` §1/§2 | `make build`（12 个二进制）；守卫 2/5 | ✅ |

**P1 WBS 8 追踪矩阵补记（2026-11-10）**：本节只记录已落地部分，不代表 P1 全部完成。

| 需求 | 设计文档 | 自动化用例 | 结果 |
|---|---|---|---|
| FR-RSV-03 thunder/flashget/qqdl 仅解码 | `08` §6 / ADR-0007 | `wrapper_test.go`：`TestWrapperDecodesAllThreeFormats`、`TestWrapperRejectsMalformedInput`、`TestWrapperPropagatesSSRFBlock` | ✅ |
| FR-RSV-04 Metalink v3/v4 + RFC 6249 | `08` §7 / ADR-0006 | `metalink_test.go`：`TestMetalinkV4ProducesCompleteResource`、`TestMetalinkV3Dialect`、`TestMetalinkRFC6249Headers`、`TestMetalinkXMLSecurityLimits` | ✅（多 `<file>` 与远端清单抓取为显式不支持） |
| FR-TRN-02 分片与整文件校验 | `06` §4 / ADR-0006 | `store/checksum_test.go`：`TestWholeFileChecksumAlgo`、`TestVerifyPieceChecksums` | ✅（md5 / sha-1 / sha-256 / sha-512） |

**P1 WBS 9 追踪矩阵补记（2026-11-10）**：本节只记录已落地部分，不代表 P1 全部完成。

| 需求 | 设计文档 | 自动化用例 | 结果 |
|---|---|---|---|
| FR-TRN-04 FTP / FTPS / SFTP | `10` §8 / `11` §2–§3 | `transport/ftp/integration_test.go::TestRealFTPClientRoundTrip`（EPSV + PASV 投毒）、`transport/sftp/integration_test.go::TestRealSSHAndSFTPRoundTrip` / `TestRealSFTPRejectsWrongHostKey` | ✅（主动模式、连接池、公网互操作矩阵未覆盖） |
| FR-RSV-02 输入归一（文件 scheme） | `05` §2 | `resolve/direct_file_test.go::TestParseInputClassifiesFileSchemesAsURL`、`TestDirectFileResolvesAllThreeSchemes` | ✅ |
| NFR-SEC-01 SSRF（数据通道） | `11` §2 | `security/ftp_guard_test.go::TestCheckDataAddr`、`TestDialDataContextFailsClosedBeforeDialing` | ✅ |
| NFR-SEC-02 凭据不落库 / host key pin | `11` §3 | `transport/tputil/tputil_test.go::TestLoadCredentialAnonymousAndStrict`、`transport/sftp/sftp_test.go::TestHostKeyCallbackFailsClosed` | ✅ |
| NFR-PORT-01 纯 Go 六平台 | `13` §1/§2 | `make build`（12 个二进制，新增 4 个 ISC/BSD 链接模块）；`check_arch.py` 守卫 2/5 | ✅ |

**P1 WBS 7 追踪矩阵补记（2026-11-10）**：本节只记录已落地部分，不代表 P1 全部完成；真实公网
BT 客户端互操作矩阵（`12` §3.1 BT-01…BT-14）在 WBS 7 时点为 WBS 11 待办、证据为回环 fixture。
**WBS 11 后（2026-11-11）更新**：套件 v1 已交付本地回环子集与门禁注册表，但该 L4 矩阵
**仍未执行**（全部 `SKIPPED` / `UNSUPPORTED`），见下文 WBS 11 补记。

| 需求 | 设计文档 | 自动化用例 | 结果 |
|---|---|---|---|
| FR-RSV-01 接受 magnet / `.torrent` raw·base64·本地文件 | ADR-0004 / `05` §2 | `magnet_test.go::TestMagnetResolverResolve` / `TestMagnetResolverMatch`；`torrent_test.go::TestTorrentResolverRawBencode` / `TestTorrentResolverBase64` / `TestTorrentResolverLocalFile` / `TestTorrentResolverRejectsRemoteURL` | ✅（远端 `*.torrent` 显式能力错误） |
| FR-RSV-02 输入归一（注册链） | `05` §2 / `14` §11 序 4 | `app/daemon_test.go::TestBuiltinResolverChainOrderAndDispatch`、`scheduler/extra_test.go::TestResolveOnlyUsesRegistry`、`resolve/resolve_test.go::TestUnsupportedIsPhaseAware` | ✅ |
| FR-TRN-05 BT / 磁力、private flag、tracker、选择下载、做种策略 | ADR-0004 / ADR-0017 / `16` §2/§4 | `torrent_test.go::TestTorrentResolverPrivateFlag`；`bt_test.go::TestBuildSpec` / `TestPickInfoFileMulti`；`bt_cover_test.go::TestProbePureMagnetMetadata` / `TestProbePureMagnetNoInfohash` | ✅（DHT/PEX 对私有种子关闭；默认 `Seed=false`；不承诺 PT） |
| FR-TRN-06 BT 流式读取 `File.NewReader` | ADR-0004 / `16` §2 | `bt_test.go::TestTransportLoopbackDownload`（bootstrap peer 回环真实协议下载）、`bt_cover_test.go::TestRunOutOfBoundsChunk` | ✅（anacrolix ↔ anacrolix 回环） |
| FR-TRN-09 单调度器 / 统一 Budget | `04` §5 / ADR-0002 | `bt_cover_test.go::TestRunBuildSpecError`、`bt_test.go::TestBuildSpec`；`app/daemon_test.go::TestBuiltinTransportChainOrder` | ✅（字节经 `readWithBudget` 计费） |
| FR-INT-03 private flag | `16` §3/§4 / `11` §7.1 | `bt_test.go::TestHonestPeerID`；`torrent_test.go::TestTorrentResolverPrivateFlag` | ✅（诚实 PeerID、禁 DHT/PEX/LSD） |
| NFR-SEC-01 SSRF（tracker / bootstrap peer） | `11` §2/§7.1 | `magnet_test.go::TestMagnetResolverMissingInfohash`、`bt_cover_test.go::TestProbeInvalidEmbeddedMetainfo` / `TestProbeInvalidInfoDict` / `TestBuildSpecErrors` | ✅（畸形输入 fail closed） |
| NFR-PORT-01 纯 Go 六平台 | `13` §1/§2 / `10` §8.1 | `make build`（12 个二进制）；`make gates-strict` 许可证 86 模块 0 copyleft / 0 未识别；`check_arch.py` 守卫 2/5 | ✅（MPL-2.0 修改清单为空） |

**P1 WBS 10 追踪矩阵补记（2026-11-10）**：本节只记录已落地部分，不代表 P1 全部完成；
WASM 硬隔离、第三方规则仓库 / 发布流水线、规则失效遥测、`lux` 站点解析与 `yt-dlp` 兜底仍为 P2+ 待办。

| 需求 | 设计文档 | 自动化用例 | 结果 |
|---|---|---|---|
| FR-PLT-03 插件引擎 goja（ES5.1）+ manifest + 签名 + 能力裁剪 | ADR-0014 / `08` §2/§3/§10 | `plugin/manifest_test.go::TestParseManifestValidAndDefaults` / `TestParseManifestRejectsInvalid`；`plugin/scan_test.go::TestScanRejectsForbiddenSyntax` / `TestTransformRewritesESMSugar`；`plugin/signature_test.go::TestSignVerifyRoundTrip` / `TestSignPackRoundTrip` | ✅（manifest 严格校验、静态禁用 API 扫描、Ed25519 规则包签名 fail-closed） |
| FR-RSV-01/02 插件 Resolver 注册链优先级 | `05` §10 / `08` §5 | `app/daemon_test.go::TestPluginResolverPlacementInChain` / `TestRunWithPluginRuleChannel`；`plugin/manifest_test.go::TestParseManifestRejectsInvalid`（match 与内置冲突） | ✅（安全敏感内置恒在前，通用 direct-http 兜底在后） |
| NFR-SEC-01 SSRF（插件 egress 与产出 URL） | `11` §2/§5 / ADR-0014 §4 | `plugin/runtime_test.go::TestRuntimeWhitelistBlocksRequests` / `TestRuntimePrivateURLBlockedByGuard` / `TestRuntimeRedirectHopIsWhitelisted` / `TestRuntimeOutputValidation`；`http_test.go::TestGuardBlocksProducedMirrorEvenWhenWhitelisted` | ✅（逐跳白名单 + SSRF Guard + 输出路径 PathGuard + 敏感头剔除） |
| NFR-SEC-02 凭据不落库 / 最小授权 | `11` §3 / `08` §9 | `plugin/runtime_test.go::TestRuntimeHasNoFsEnvOrSubprocess` / `TestRuntimeRequestHeaderValidation`；`plugin/scan_test.go::TestScanRejectsForbiddenSyntax` | ✅（fs/env/subprocess 加载期拒绝；`Authorization`/`Cookie`/`Host` 禁止设置） |
| NFR-SEC-03 插件经签名校验与能力裁剪 | `11` §5 / ADR-0014 §7 | `plugin/manager_test.go::TestManagerRejectsUnsignedAndBadPacks` / `TestManagerKeepsLastGoodVersionOnBadUpdate`；`cmd/udctl/plugin_test.go::TestPluginCommandSignVerifyTestList` | ✅（篡改脚本签名失效；失败保留上一版） |
| FR-PLT-06 规则失效不静默丢弃任务 | ADR-0014 §6 / `08` §10 | `plugin/runtime_test.go::TestRuntimeRuleOutdatedIsClassified`；`plugin/manager_test.go::TestManagerFallbackOnRuleOutdated`；`plugin/fixture_test.go::TestRunFixtureOfflineReplay` | ✅（`RULE_OUTDATED` → `E_CAPABILITY_UNAVAILABLE` + 兜底 Resolver；yt-dlp 兜底未实现） |
| NFR-SEC-01 DoS（死循环 / 超大响应 / 内存预算） | `11` §1 / ADR-0014 §7 | `plugin/runtime_test.go::TestRuntimeInfiniteLoopKilledByTimeout` / `TestRuntimeOversizedResponseRejected` / `TestRuntimeOutputBudgetAndNonObject` | ✅（`vm.Interrupt` 超时强杀、响应体与输出 JSON 上限） |
| 规则热更新（独立于主程序发版） | ADR-0014 §6 / `08` §8/§10 | `plugin/manager_test.go::TestManagerHotReloadSwapsSnapshot` / `TestManagerWatchReloads` / `TestFingerprintAndPluginDirsLimits` | ✅（目录指纹 + 原子快照 + 轮询；无规则仓库/发布流水线） |
| NFR-PORT-01 纯 Go 六平台 | `13` §1/§2 / `10` §8 | `make build`（12 个二进制）；`make gates-strict` 许可证 90 模块 0 copyleft / 0 未识别；`check_arch.py` 6 守卫 | ✅（goja / regexp2 / go-sourcemap 均为 MIT/BSD，零 cgo） |

**P1 WBS 11 追踪矩阵补记（2026-11-11）**：只记录**已落地且可复跑的本地自动化部分**；
`12` §3.1 的 `BT-01..BT-14` 为 **L4 发布门禁判据**，本次全部未执行，**不代表 P1 验收完成**。

| 需求 | 设计文档 | 自动化用例 / 证据 | 结果 |
|---|---|---|---|
| FR-INT-03 / FR-BT 互操作回归套件（框架） | `12` §3.1、`16` §6 | `internal/interop/suite_test.go::TestRegistryCompleteness` 等；`cmd/udinterop --list`、`make interop` → `dist/interop.json` / `dist/INTEROP.md` | ✅ 14 条 L4 用例按原序登记；状态模型 `PASS`/`FAIL`/`SKIPPED`/`UNSUPPORTED`，`SKIPPED`/`UNSUPPORTED` 不计 PASS |
| FR-TRN-05 tracker（BEP 3 / BEP 15 / BEP 23） | `16` §2、`12` §3.1 BT-05/06/07 | `L3-01` HTTP(S) announce/scrape/compact v4+v6/503 与挂起故障注入、`L3-02` UDP connect/announce/scrape/compact IPv4、`L3-03` UDP IPv6 18B compact、`L3-04` HTTPS + passkey + 主备降级 | ✅ 均为**自建纯 Go fixture**（非 `chihaya`/`opentracker` 二进制） |
| FR-TRN-05 / FR-TRN-06 BT 线协议与流式 | ADR-0004、`16` §2 | `L3-05` 独立 seeder + 自建 tracker + `bttransport` 真实线协议下载、`L3-07` 纯磁力经 bootstrap peer 取 BEP 9 元数据、`L3-09` 流式首片 4KiB 时延 | ✅ 回环，非真实客户端互操作 |
| FR-INT-03 诚实 PeerID（ADR-0017） | `11` §7.1、`16` §4 | `L3-06` BT 握手抓包：PeerID 前缀 `-UD0000-`、非伪装 | ✅ 回环抓包；BT-14 的真实客户端抓包仍未执行 |
| FR-BT 选择下载 | `12` §3.1 BT-09 | `L3-08` 多文件种子部分选中，未选文件字节不进入 `ChunkWriter` | ✅ 回环 |
| NFR-PORT-01 纯 Go 六平台 / 零 cgo | `13` §1/§2、`10` §8 | `make build`（12 个二进制）；`make gates-strict` 许可证 **90 个链接模块 0 copyleft / 0 未识别**，`internal/interop` 覆盖率 87.1%（16 个门禁包） | ✅ WBS 11 **零新增依赖** |
| `12` §8 发布门禁「互操作 L4」 | `12` §3.1、`14` §11 序 6 | `make interop-gate` / `udinterop --release-gate-only`；`dist/interop.json:release_gate_met` | ❌ **未通过**：0/14 PASS，12 `SKIPPED` + 2 `UNSUPPORTED`（`BT-02`/`BT-11` 在套件 v1 注册表仍按 `ScopeProductGap` 固定 `UNSUPPORTED`；路径 A 实现已落地但 L4 真实矩阵未执行，状态未翻转），`release_gate_met: false`；ADR-0021 路径 A 实现已落地但**不得**据此宣称 PASS |
| NFR-PERF-01 千兆内网吞吐 | `12` §5 | 需隔离网络 + 1GiB 文件 + 抓包 | ❌ **未执行**（`make bench` 只是回环 32MiB 回归基线，非该门禁） |

**P1 WBS 11 续包追踪矩阵补记（2026-11-13）**：序 6 仍不关闭，以下均为**补充证据 / 阻塞登记**，
不改变上表 L4 与 NFR-PERF-01 的 ❌ 结论。

| 需求 / 准则 | 证据 | 状态 |
|---|---|---|
| 与真实第三方引擎互通（补充） | `BX-01..BX-04`：aria2-next 3.0.9 + `libtorrent 2.1.1` 真实做种 → 我方 `bttransport` 下载，sha256 一致；`make interop-external` 可复跑 | ✅ 4/4 PASS，**单向回环、不计入 L4** |
| `12` §8 发布门禁「互操作 L4」 | `12` §3.1「L4 门禁可满足性冲突」 | ✅ **2026-11-14 路径 A 裁决后**：实现范围缩减（合规差距）根因已消除；❌ **但判据未改、真实矩阵仍未执行**，`BT-02`/`BT-11` 仍 `UNSUPPORTED`、`BT-03` 我方做种半向未实测，14/14 PASS 仍不可满足 |
| NFR-PERF-01 千兆内网吞吐 | `make perf` → `dist/perf.json`（1GiB / 4 连接 / 256KiB，真实 HTTP Range Transport） | ⚠️ 回环参考 885.83 MB/s，固定 `nfr_perf_01.met=false`；**双机千兆实验室仍未执行** |
| 16 门禁包 / 90 链接模块 / 6 PASS | `make gates-strict`、`TestClientConfigHandshakePolicy`、`TestLoadExternalEvidenceStatuses` | ✅ 口径不变，零新增依赖 |
| **FR-INT-01 / FR-INT-04 / FR-TRN-05（M，做种礼仪）** | ADR-0004 / ADR-0017 决策 4 + `bt/seeding.go` / `config.go` / `seeding_observer.go` | ✅ **编码已落地**（2026-11-14 路径 A）：受限做种、分享率 / `seed-time`、上传槽、UPnP/NAT-PMP、`tasks.bytes_uploaded` 与 announce 同源；⚠️ **L4 实测与礼仪复签未执行**，需求不得标记为「已验收」 |
| 阻塞裁决 | [ADR-0021](adr/ADR-0021-L4门禁与P2P礼仪范围冲突裁决提案.md)（`Accepted（路径 A）`） | ✅ 路径 A 已由项目发起人指令裁决；❌ 评审组复签 / R-10 重评 / 真实 L4 矩阵 / NFR-PERF-01 仍待补，序 6 开放 |
| L4 实验室前置条件 | `make l4-preflight` → `dist/l4_preflight.json`（9 项清点，`l4_executed=false`）；示例 stub 只输出 `SKIPPED` | ⚠️ **不是 L4 执行证据**，`BT-01..BT-14` 仍 0/14 PASS |

> **§9 验收准则的 P0 边界**：准则 1 的「无空格」在 P0 仅对上表 P0 行成立；准则 2/4 的 `12` 发布门禁、互操作全矩阵、SBOM 与 `go version -m` 一致性尚未执行（P1+；其中 SBOM / `go version -m` 一致性已于 `make gates-strict` 落地，但 **L4 互操作全矩阵仍未通过**）。准则 3 法务签字未进行。详见 `14` §9 与 `ADR-0020`。

## 9. 验收准则

1. 每条 `M` 需求至少有一个自动化用例，§8 矩阵无空格。
2. `12` 发布门禁全部通过；`16` 互操作套件全矩阵通过。
3. 产品文案经法务对照 `01` §5 与 `16` §7 签字。
4. 传输层 `CGO_ENABLED=0` 构建产物中不含 GPL 组件，`go version -m` 与 SBOM 一致。

## 10. 待决事项

| ID | 事项 | 责任 | 截止 |
|---|---|---|---|
| OPEN-01 | ✅ 已决：代码许可证 **MPL-2.0**（2026-11-09，`14` §7 / `17` §8；`LICENSE` 已入库） | 法务 + 架构 | 已关闭 |
| OPEN-02 | ✅ 已决：**Flutter**（2026-11-09，`14` §7 / `17` §8；正式 UI 不进入 P1） | 客户端 | 已关闭 |
| OPEN-03 | `goed2k` 是否需 fork 自维护 | 架构 | P3 启动前 |
