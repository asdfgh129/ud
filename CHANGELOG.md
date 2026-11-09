# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

版本语义：`0.x` 阶段为 P0 骨架期，次版本号对应里程碑（0.1.x = P0），修订号对应构建批次。
`MAJOR.MINOR.PATCH` 正式启用于 P3 结束后的首个稳定版。每次代码变更必须在合入前写入本文件，
并同步受影响的 `docs/` 文档（见 [docs/README.md](docs/README.md) 与 [docs/15-开发规范与协作流程.md](docs/15-开发规范与协作流程.md)）。

## [Unreleased]

> 本段内容已随 `v0.1.0` 附注标签（2026-11-09 评审闭环提交）发布；自该标签起暂无新变更，
> P1 变更将记入新版本段。标签 message 附本文件摘要。


### Security
- **Go 工具链升至 1.27.3**：`govulncheck` 首次扫描发现 `GO-2026-6809`——Go 1.27.2 标准库
  `crypto/x509`「Distrust compromised intermediate certificates」，本项目调用链
  `store.WholeFileChecksum → io.Copy → x509.Certificate.Verify` 可达。已同步 `go.mod`、
  `.github/workflows/ci.yml`、`docs/10` §7、`docs/13` §1/§12 与根 `README.md`；升级后复扫
  `No vulnerabilities found`。
- 新增供应链门禁：CycloneDX 1.6 SBOM（基于 `go version -m` **实际链接模块图**，12 个 component）、
  `go mod verify`、许可证扫描（0 copyleft / 0 未识别）、`THIRD_PARTY_NOTICES.md`、
  `checksums.txt` 与 `govulncheck`，接入 `make gates` 与 CI。

### Added
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
- `core.EventBus.Dropped()` 是文档化的诊断计数，但 `Publish` 从未累加；改为 `atomic.Int64`
  累加（`Publish` 只持读锁，用 mutex 计数会数据竞争），并补回归用例。
- `TestULID` 依赖「同毫秒」时序，导致全量并行跑时覆盖率在 89.4%–92.4% 抖动；
  补确定性 `TestIncrementEntropyCarry`（进位与全溢出回滚时间戳）。
- 许可证识别把标准 BSD 两条款文本判为 `NOASSERTION`；以「Neither the name」区分 BSD-3/BSD-2，
  `gorilla/websocket` 现正确识别为 BSD-2-Clause。
- 状态归类缺陷：`ADR-0001` 的 `Proposed（待立项评审批量转 Accepted）` 因备注中出现 `Accepted`
  被误标为「已决定」；现在只取括号前的主状态词。

P0 全部构建批次（含收尾修复与文档同步）仍汇总于下方 `0.1.0`；P1 待办见 `ADR-0020` 与 `docs/14` §9/§10。
**P0 里程碑评审决议（2026-11-09，随 `v0.1.0` 发布）**：结论「**有条件通过**」，由项目发起人一次性全权批准并代行五个签署角色；
ADR 0001–0020 全部转 `Accepted`；OPEN-01 定档 **MPL-2.0**（`LICENSE` 已入库）、OPEN-02 定档 **Flutter**；
`v0.1.0` 附注标签已打，`14` §3 的 P1 门禁解除（`14` §11 给出 WBS 7–11 启动顺序）。
**附加条件**：覆盖率仍有 8 个包低于阈值，P1 首个 PR 前须补齐并把 `gates.py --strict` 接入 CI；当前 WARN 不记作通过。详见 `docs/17` §1/§5/§7/§8/§9。

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
