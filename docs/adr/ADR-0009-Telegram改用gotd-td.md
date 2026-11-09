# ADR-0009 Telegram 改用 gotd/td 纯 Go，移除 Telethon

- **状态**：Proposed
- **日期**：2026-11-08
- **决策者**：架构组
- **关联**：`04` §6、`09` §3、`10` §3

## 背景

初版把 Telethon 与 yt-dlp 合并在一个 Python worker 里。追问「视频站 + Telegram 能否直接用 Go 改写、不引入 sidecar」后核实：`gotd/td`（MIT，纯 Go MTProto 2.0）已是生产级实现，无需 Python。

另有一条功能硬约束：**Telegram Bot API 的 `getFile` 只能下 20MB，而用户文件上限为 2GB（Premium 4GB）**，因此必须走 MTProto，不能图省事用 Bot API。

## 决策

1. 采用 **`gotd/td`（MIT，纯 Go）** 作为 Telegram 实现，进程内接入，`gotd/telegram/downloader` 的 `Stream(ctx, io.Writer)` 把字节流直接灌进自有 Transport。
2. 使用 `WithPartSize(512KB)`、`WithAllowCDN(true)`；进度、限速、断点仍归核心 Scheduler 管。
3. **从 Python worker 中删除 Telethon**；Python worker 只承载 yt-dlp，零新增进程、零 cgo。
4. 必须处理 `FLOOD_WAIT`（按服务端给定秒数退避）、DC 迁移、2FA、MTProxy、会话存储（文件 0600）；会话失效走重新授权流程。
5. 不得做规避限速的多账号滥用（`11` §7）。

## 备选方案

| 方案 | 结论 |
|---|---|
| Telethon（合并进 Python worker） | ❌ 多一份 Python 运行时依赖；纯 Go 方案已足够，无保留理由 |
| 官方 `TDLib`（C++，cgo 进程内） | ❌ 引入原生依赖与 ABI 负担，破坏纯 Go 交叉编译 |
| Bot API `getFile` | ❌ 20MB 上限，无法满足大文件 |
| Pyrogram | 同 Telethon，无优势 |

## 后果

**正面**：零新增进程、零 cgo；`Stream(io.Writer)` 与统一流水线天然契合，进度/限速/断点不被割裂；CDN 重定向、并行分片、会话管理由库承担。

**负面**：需自行实现会话持久化与授权交互；MTProto 协议与平台策略变动需跟进 `gotd/td` 版本；`FLOOD_WAIT` 需纳入统一错误模型（`05` §7 `KindTransient`）。

**后续动作**：P3 实现；`12` 增加 2GB 文件、`FLOOD_WAIT`、DC 迁移、会话失效用例；`11` §3 规定 session 文件权限与脱敏。

## 合规与验证

- 传输路径无 Python 依赖；`CGO_ENABLED=0` 构建通过。
- `12` 用例：2GB 文件完成且哈希一致；`FLOOD_WAIT` 按给定秒数退避而非暴力重试。
- 遵守平台限速与条款，能力限定为下载用户有权访问的内容。
