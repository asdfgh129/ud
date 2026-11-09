# Universal Downloader（通用下载器）

跨平台的统一下载引擎：把 **BT / 磁力 / ed2k / HTTP(S) / FTP(S) / SFTP / Metalink / 迅雷链接 / 视频网站 / Telegram / 微信** 等来源，收敛到一条流水线、一个调度器、一份状态。

> **当前阶段：P0 骨架已落地（v0.1.0，2026-11-08），评审门禁证据已备齐（2026-11-09），待里程碑评审签署。**
> 仓库已包含可运行的 Go 代码：一条 HTTP(S) 直链能走完 `Resolver → Resource → Transport → PostProcessor → Store` 全流水线。
> **P0 不是功能完整版**——BT/ed2k/FTP/站点解析/插件/sidecar/L1 RPC facade 均未实现，详见 [ADR-0020](docs/adr/ADR-0020-P0实现基线与范围声明.md) 与 [更新日志](CHANGELOG.md)。

## 三条不可动摇的架构原则

1. **三类输入归一**：传输协议、链接封装格式、内容源解析器不是一类东西，全部经 `Resolver → Resource → Transport → PostProcessor` 处理。
2. **单调度器**：队列、限速、断点续传、进度、文件布局只在 Go 核心实现一次，不允许第二个组件拥有任务生命周期。
3. **纯 Go 单二进制**：传输层零 sidecar、零 cgo、零 GPL 依赖；外部能力一律以可降级插件形式接入，桌面与移动端可达性优先。

## 快速开始（P0）

```bash
# 需要 Go 1.27.3（因 GO-2026-6809 升级）；所有构建强制 CGO_ENABLED=0
make lint          # gofmt + go vet + 架构静态检查（6 守卫）
make test          # 全量单测 / 契约 / 集成
make build         # 六平台交叉编译 → dist/（udd + udctl 各 6 个）
make gates         # 评审门禁：覆盖率 / SBOM / 许可证 / govulncheck / 基准 / 回滚演练 → dist/
```

> `make gates` 需要 `govulncheck`（`go install golang.org/x/vuln/cmd/govulncheck@latest`，
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

## 代码地图（P0）

| 目录 | 职责 |
|---|---|
| `internal/core` | 冻结的 `Resource` 全字段模型、Task 状态机、错误码、ULID、事件总线、全部契约接口（零实现依赖） |
| `internal/config` | YAML/环境变量配置、校验、运行时 `PATCH /config` 管理器 |
| `internal/security` | SSRF Guard、PathGuard、脱敏、`CredentialStore` 的进程内参考实现 |
| `internal/store` | SQLite DDL v1（11 表）、Resource 规范化存取、分片真值表、崩溃恢复、备份 |
| `internal/resolve` · `internal/transport/http` | HTTP(S) 直链 Resolver；Range 分段 / 无 Range 退化 / 重试 / 分片 sha256 |
| `internal/postproc` | ADR-0012 本地文件契约的 P0 `none` 直通实现 |
| `internal/scheduler` | 唯一调度器、唯一 Limiter、ChunkWriter、原子提交 |
| `internal/api` · `internal/app` | L0 REST + WebSocket、daemon 装配、嵌入式最小 UI |
| `cmd/udd` · `cmd/udctl` | daemon 与 CLI 入口 |
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
| 查 P0 评审要签什么、还差什么 | [`docs/17-P0里程碑评审材料.md`](docs/17-P0里程碑评审材料.md) |
| 看哪些文档待审核 / 已决定 | 文档门户首页 `#/home` 的状态总览 |

预览型阅读：运行 `python3 tools/build_docs.py` 后打开 `docs/_site/index.html`。这是一个带侧栏的单文件门户，**默认打开 `#/home` 状态首页**：统计卡 + 状态徽章 + 分类卡片，直观显示每份文档是「待审核 / 已决定 / 持续维护 / 已废止 / 未标注」，并支持按状态、类型筛选与搜索；侧栏保留全部 41 篇文档链接，CHANGELOG、ADR 与 P0 评审材料均可直接跳转。`docs/_site/` 是生成物，禁止手工编辑。

## 明确的能力边界（写进产品文案）

- ✅ 兼容**标准** BT 网络（公网 peer、公开 HTTP/UDP/HTTPS tracker）与标准 eD2k 网络（线协议层）——**均为 P1/P3 目标，尚未实现**。
- ✅ **P0 已实现**：HTTP(S) 直链、Range 分段、断点续传、暂停/恢复/取消、崩溃恢复、全局限速、L0 REST/WS/CLI、最小 UI、六平台纯 Go 构建。
- ❌ **不承诺**私有 tracker 准入、迅雷 P2SP/高速通道/离线下载、ed2k 下载速度、DRM 内容解析、100% aria2 RPC 兼容。
- ⚠️ 微信视频号嗅探为**单独分发、默认不安装**的可选插件，涉及逆向与平台条款风险。

## P0 已知限制

1. `MemoryCredentialStore` 是进程内参考实现，`mem://` 引用在 daemon 重启后失效；OS 钥匙串适配器待 P1。
2. 非回环监听必须配置 `remoteToken`（否则拒绝启动），且目前只有 Bearer 鉴权，无 TLS / Origin 校验。
3. `postProcess` 只支持 `none`；最小 UI 是契约验证占位件（OPEN-02 Flutter vs Tauri 未决）。
4. CycloneDX SBOM / `govulncheck` / 许可证扫描 / 覆盖率报告 / 回滚演练已由 `make gates` 执行；但**覆盖率仍有 8 个包低于阈值**，互操作套件与千兆内网性能基准未执行，不等价于发布门禁全绿（见 `docs/17` §3/§5）。

完整清单见 [`docs/11` §10](docs/11-安全与合规设计.md) 与 [`docs/14` §9](docs/14-项目计划与风险管理.md)。

## 许可证

代码许可证待立项确定（建议 MPL-2.0 或 Apache-2.0 以匹配主要依赖）；文档采用 CC-BY-4.0。依赖许可证矩阵见 `docs/10-技术选型与依赖清单.md`，P0 实际锁定依赖见其 §7。
