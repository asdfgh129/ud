# 07 接口规范（API）

> 文档编号：UD-DOC-07 ｜ 版本：v0.2 ｜ 状态：草案（L0 已落地） ｜ 日期：2026-11-08 ｜ 相关：ADR-0003、ADR-0016、ADR-0019、ADR-0020

> **v0.2 实现校准**：§2 的 L0 REST 端点、§4 WS 事件、§6 错误码表已在 `internal/api/server.go` / `internal/core/errors.go` 全量落地；§5 的 L1 facade 按 ADR-0019 **未实现**（`/jsonrpc` 不存在）。补充 §1 远程鉴权的强制约束与 §8 的 P0 契约实现矩阵。

## 1. 通用约定

| 项 | 规定 |
|---|---|
| 基址 | `http://127.0.0.1:<port>/api/v1`（默认仅回环监听；远程需显式开启 + token） |
| 版本 | URL 路径版本；破坏性变更升 `v2`，不静默改 `v1` |
| 编码 | UTF-8 JSON；时间用 RFC 3339 / 毫秒 Unix 时间戳（字段以 `*_at` 标注者用毫秒整数） |
| 鉴权 | 本机回环默认无需；远程 `Authorization: Bearer <token>`；L1 facade 用 aria2 `token:<secret>` 前缀 |
| 幂等 | 创建类请求可带 `Idempotency-Key`；重复提交返回首次结果 |
| 分页 | `?cursor=&limit=`（默认 50，上限 200） |
| 脱敏 | 响应与日志一律不返回 `cookie` / `authorization` / `passkey` 明文，以 `***` 或 `cred_ref` 代替 |

**远程鉴权为强制约束（v0.2 明确）**：`listen` 为非回环地址（如 `0.0.0.0:6800`）且未配置 `remoteToken` 时，`config.Validate` **拒绝启动**，而不是退化为无鉴权监听；daemon 侧 `RequireAuth` 对任何非回环监听恒为 `true`（fail closed）。这与 ADR-0016「远程访问需显式开启 + token」一致，由 `internal/config/config_test.go:TestRemoteListenRequiresToken` 固定。

**内联凭据头被拒绝**：`POST /tasks`、`POST /tasks/batch`、`PATCH /tasks/{id}` 的请求体若含 `Authorization` / `Cookie` / `X-Api-Key` 等敏感头，返回 `400 E_INVALID_INPUT`，要求先 `POST /credentials` 再用 `credentialRef` 引用。理由见 `06` §6——DB 只存 `cred_ref`，绝不存明文。

**统一错误格式**
```json
{ "error": { "code": "E_RESOLVE_SSRF_BLOCKED", "message": "URL targets a private address", "retryable": false, "details": { "host": "169.254.169.254" } } }
```

## 2. REST 端点

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/tasks` | 创建任务 |
| GET | `/tasks` | 列表（支持 `state`、`kind`、分页） |
| GET | `/tasks/{id}` | 详情（含 `Resource`、分片位图、peer 摘要） |
| PATCH | `/tasks/{id}` | 修改选项（限速、优先级、连接数、选文件） |
| POST | `/tasks/{id}/pause` | 暂停 |
| POST | `/tasks/{id}/resume` | 恢复 |
| POST | `/tasks/{id}/cancel` | 取消 |
| DELETE | `/tasks/{id}` | 删除（`?withFiles=true` 同时删文件） |
| POST | `/tasks/batch` | 批量创建 / 批量操作 |
| POST | `/resolve` | 只解析不下载，返回 `Resource` 预览（供 UI 选流/选文件） |
| GET | `/capabilities` | 返回可用 Resolver / Transport / Sidecar / 后处理能力 |
| GET | `/config` · PATCH `/config` | 全局配置（限速、并发、目录、代理、SSRF 白名单） |
| GET | `/stats` | 全局吞吐、连接数、任务统计 |
| GET | `/healthz` | 健康检查（含 DB 与 sidecar 状态摘要） |
| POST | `/credentials` | 写入凭据到钥匙串，返回 `cred_ref` |
| GET | `/events` (WS) | 事件推送 |

**创建任务示例**
```json
POST /api/v1/tasks
{
  "input": "thunder://QUFodHRwczovL2V4YW1wbGUuY29tL2ZpbGUuaXNvWlo=",
  "options": {
    "dir": "/Downloads",
    "maxConnections": 16,
    "maxDownloadLimit": "2MiB/s",
    "priority": 0,
    "postProcess": "remux"
  },
  "credentialRef": "keychain://ud/cred/2f1c"
}
→ 201
{
  "id": "01J8Z...", "state": "Pending",
  "resource": { "kind": "single", "files": [{ "index": 0, "path": "file.iso", "size": 0 }], "mirrors": [{"url":"https://example.com/file.iso"}] },
  "createdAt": 1762570000000
}
```

**任务详情关键字段**：`state, bytesDone/Total, speed, eta, connections, bitfield(hex), numPieces, pieceLength, files[], peers[], trackers[], selectedFiles, followedBy, belongsTo`。后六个字段为使 L1 facade 可直接映射而预留（见 §5）。

> **P0 响应差异**：创建任务返回 `{id, state, createdAt}`（`201`），不含上例中的 `resource` 预览字段；`Resource` 需通过 `GET /tasks/{id}` 或 `POST /resolve` 获取。P0 的 `peers[]` / `trackers[]` 为空（BT/eD2k 引擎属 P1+）。`bitfield` 按 `06` §10 的 `ceil(numPieces/8)` 字节规则生成。上述差异登记在 [ADR-0020](adr/ADR-0020-P0实现基线与范围声明.md)，P1 补齐前不视为接口破坏。

## 3. CLI（`udctl`）

```
udctl add <input> [--dir] [--limit] [--conn] [--out] [--select-file] [--no-postprocess]
udctl list [--state] [--json]
udctl show <id> | pause <id> | resume <id> | cancel <id> | rm <id> [--with-files]
udctl resolve <input>            # 打印 Resource JSON，不下载
udctl capabilities | stats | health
udctl config get/set <key> <value>
udctl backup <path> | restore <path> | migrate --from N
udctl plugin install/verify/list <path-or-url>
udctl daemon [--config <file>] [--listen 127.0.0.1:0]
```

> **P0 已实现子集**：`add` / `list` / `show` / `pause` / `resume` / `cancel` / `rm` / `resolve` / `capabilities` / `stats` / `health` / `config get|set`。`backup|restore|migrate`、插件管理、`daemon` 子命令与 `[--select-file]` 选项属 P1+。CLI 通过本地发现文件（`$XDG_RUNTIME_DIR/ud/discovery.json` 或用户缓存目录）自动找到随机端口的 daemon，也支持 `--addr` 直连。
>
> 已修复的用法陷阱：Go `flag` 包遇到首个位置参数即停止解析，导致 `udctl add <url> --conn 4` 的选项被忽略。`cmd/udctl/main.go:reorderFlags` 在 `add` / `list` / `rm` 前把标志重排到位置参数之前，使本文示例写法可用。

## 4. WebSocket 事件（`/api/v1/events`）

```json
{ "type": "task.progress", "taskId": "01J8Z...", "ts": 1762570001000,
  "payload": { "bytesDone": 10485760, "speed": 2097152, "connections": 8 } }
```

| 事件 | 触发 |
|---|---|
| `task.created` / `task.state` | 创建 / 状态迁移 |
| `task.progress` | 节流推送（默认 500ms） |
| `task.chunk.failed` | 分片失败（含 mirror 与 attempt） |
| `task.completed` / `task.failed` / `task.canceled` | 终态 |
| `capability.changed` | sidecar 就绪/降级/失败 |
| `config.changed` | 全局配置变更 |

## 5. 可选 L1：aria2 JSON-RPC 兼容 facade

> **仅为协议兼容，不捆绑、不调用 `aria2c` 二进制**（ADR-0003、ADR-0019）。P0/P1 停在 L0（只做自有 API）；有用户明确要接 AriaNg 时再启用 L1。

| 层级 | 内容 | 状态 |
|---|---|---|
| L0 | 仅自有 REST/WS/CLI | **P0/P1 采用** |
| L1 | 自写 facade，实现 compat subset | 按需启用 |
| L2 | 捆绑 aria2c 二进制 | **已否决** |

### 5.1 端点
`POST /jsonrpc`（JSON-RPC 2.0）、`WS /jsonrpc`；`token:<secret>` 作为首参或 `jsonrpc` 字段前缀；`system.multicall` 必须实现；GID 为 16 位 hex 字符串，与内部 ULID 建立双向映射表。

### 5.2 compat subset（约 25 个方法）

| 分组 | 方法 |
|---|---|
| 添加 | `addUri`、`addTorrent`、`addMetalink` |
| 控制 | `pause`、`pauseAll`、`forcePause`、`unpause`、`unpauseAll`、`remove`、`forceRemove`、`removeDownloadResult` |
| 查询 | `tellStatus`、`tellActive`、`tellWaiting`、`tellStopped`、`getUris`、`getFiles`、`getPeers`、`getServers`、`getGlobalStat`、`getOption` |
| 配置 | `changeOption`、`changeGlobalOption`、`changeUri`、`changePosition` |
| 会话 | `getVersion`、`getSessionInfo`、`saveSession`、`shutdown`、`forceShutdown` |
| 批量 | `system.multicall`、`system.listMethods`、`system.listNotifications` |

**通知（6 类）**：`aria2.onDownloadStart / onDownloadPause / onDownloadStop / onDownloadComplete / onDownloadError / onBtDownloadComplete`。

### 5.3 语义映射（难点在映射，不在 RPC）

| aria2 概念 | 映射 |
|---|---|
| `getFiles` | `resources.files[]`（原生多文件，P0 已预留） |
| `bitfield` | 由 `chunks` 表生成 hex；HTTP 任务返回空串以兼容 AriaNg |
| `numPieces` / `pieceLength` / `connections` | BT 取自 torrent 元信息；HTTP 取分片数与当前连接数 |
| `followedBy` / `belongsTo` | 后处理/多阶段任务映射 |
| `uploaded/downloaded/left` | 统一取 `tasks.bytes_uploaded / bytes_done / bytes_total`，禁止各 Transport 自算 |
| `saveSession` / `getSessionInfo` | SQLite 已是单一真值源 → no-op 返回 OK |
| `addTorrent` | base64 `.torrent` → `anacrolix/metainfo` 解析 |

### 5.4 选项翻译表（节选）

| aria2 option | 自有配置 | 备注 |
|---|---|---|
| `split` / `max-connection-per-server` | `maxConnections` | 取较小值 |
| `max-download-limit` / `max-overall-download-limit` | 任务限速 / 全局限速 | 全局由 Limiter 唯一权威 |
| `dir` / `out` / `check-integrity` | 输出目录 / 文件名 / 强制校验 | — |
| `select-file` | `files[].selected` | 多文件选择 |
| `bt-tracker` / `bt-max-peers` / `seed-time` / `seed-ratio` | `identity.trackers` / 上传槽 / 做种策略 | — |
| `all-proxy` / `header` / `cookie` | 代理 / headers / `cred_ref` | Cookie 只写钥匙串 |
| 未知选项 | **接受但忽略** | 返回错误会让前端直接挂掉 |

### 5.5 兼容性验收

以 **AriaNg 作为测试套件**：新建/暂停/恢复/删除、多文件选择、限速修改、全局统计、WS 实时刷新全部通过即可；不承诺长尾方法 100% 语义一致。

## 6. 错误码表

| 码 | HTTP | 含义 | 可重试 |
|---|---|---|---|
| `E_INVALID_INPUT` | 400 | 输入无法识别 | 否 |
| `E_RESOLVE_FAILED` | 422 | 解析失败（站点规则/凭据/格式） | 视子类 |
| `E_RESOLVE_SSRF_BLOCKED` | 403 | SSRF Guard 拒绝 | 否 |
| `E_CAPABILITY_UNAVAILABLE` | 503 | 所需 sidecar/插件不可用 | 否（降级） |
| `E_TASK_NOT_FOUND` | 404 | 任务不存在 | 否 |
| `E_ILLEGAL_TRANSITION` | 409 | 非法状态迁移 | 否 |
| `E_STORAGE_FULL` | 507 | 空间不足 | 否 |
| `E_CHECKSUM_MISMATCH` | 422 | 校验失败 | 是（重下分片） |
| `E_TRANSIENT_NETWORK` | 502 | 网络/超时/tracker 不可达 | 是 |
| `E_FLOOD_WAIT` | 429 | Telegram 限流 | 是（按给定秒数） |
| `E_POSTPROCESS_FAILED` | 500 | 后处理失败 | 是（可降级为原样保存） |
| `E_INTERNAL` | 500 | 未分类 | 视情况 |

## 7. 兼容性与演进

- `v1` 只做加法变更；新增字段不破坏客户端；删除字段需 `v2` + 6 个月并行期。
- 任何接口变更必须同步 `06` 的 JSON 示例、`12` 的契约测试用例。
- 控制面兼容（AriaNg）与 P2P 线协议兼容是两回事，不得混为一谈（见 `16`）。

## 8. P0 L0 契约实现矩阵

| 本文条款 | P0 实现 | 契约测试 |
|---|---|---|
| §1 统一错误格式 + 脱敏 | `internal/core/errors.go`；`api.writeError` 对 message/details 二次 `security.Redact` | `internal/core/state_test.go`；`internal/api/server_test.go` |
| §1 幂等键 | `Server.idempotent` 映射 | `TestIdempotencyKey` |
| §2 REST 端点（L0 全量） | `internal/api/server.go:routes` | `TestRESTContract` |
| §2 内联凭据头拒绝 | `api.validateTaskOptions` | `TestInlineCredentialHeadersRejected` |
| §3 CLI 子集 | `cmd/udctl/main.go` | 端到端冒烟（见 `CHANGELOG.md`） |
| §4 WebSocket 事件 | `internal/api/ws.go` + `core.EventBus` | `TestWebSocketEventDelivery` |
| §5 L1 facade | **未实现**（ADR-0019，符合预期） | `/jsonrpc` 返回 404 |
| §6 错误码表 | `internal/core/errors.go:errorSpecs` 全量 | `internal/core/state_test.go:TestErrorCodeTable`（HTTP 状态 / retryable / kind） |
| §2 `capabilities.rpcLevel` | 返回 `"L0"` | `TestRESTContract` |
| 最小 UI | `internal/api/ui/index.html`（`go:embed` 单文件） | `TestMinimalUIIsServed` |
