# ADR-0008 Sidecar 判定标准与零 sidecar 基线

- **状态**：Accepted（2026-11-09 P0 里程碑评审批量批准）
- **日期**：2026-11-08（多轮修订后的最终版）
- **决策者**：架构组
- **关联**：`04` §6、`09`、`13` §9、`03` FR-PLT-06

## 背景

初版清单有 6 个 sidecar（Python worker 承载 yt-dlp+Telethon、ffmpeg、Deno、aria2c、N_m3u8DL-RE、提权助手），其中 ffmpeg 被列为「必需」。后续追问逐项收敛：aria2c 删除（ADR-0003）、Telethon 删除（ADR-0009）、ffmpeg 降为可选（ADR-0011）。需要把「什么才算 sidecar」固化成判据，防止后续随意新增。

## 决策

**判定标准**：只有满足以下任一条件，才拆独立进程 —— ① 语言运行时不同；② 许可证需要隔离；③ 需要提权或系统级副作用；④ 需要独立热更新 / 崩溃隔离。

**最终清单（5 项）**：

| Sidecar | 必需性 |
|---|---|
| Python worker（仅 yt-dlp） | ⚠️ 做 YouTube / 千站长尾时必需 |
| Deno 2.3+（yt-dlp 子依赖，EJS 求解） | ⚠️ 随上一项必需 |
| ffmpeg / ffprobe CLI | 🔵 可选（桌面高级后处理） |
| N_m3u8DL-RE | 🔵 可选（复杂直播 / 多轨） |
| 提权助手（仅微信插件） | 🔵 可选，单独分发默认不安装 |

**最小集合**：仅传输协议 = **0**；+ Telegram + 中文视频站 + HLS/DASH Remux（纯 Go）= **0（零 sidecar 基线，含移动端）**；+ YouTube/千站长尾 = **2**；+ 高级后处理 = 可选 2；+ 微信 = 可选 1。

**统一契约**：JSON-RPC 2.0 over stdin/stdout（不占端口、无网络鉴权面）；aria2 式 HTTP/WS RPC 已不适用；ffmpeg 参数白名单 exec；manifest 声明 `name/version/minVersion/capabilities/sha256/license/sourceURL/buildconf`；启动时 `--version` 能力协商；心跳 + 超时强杀 + 崩溃重启上限 3 次；每个任务只传它需要的 cookie/header；工作目录限于下载目录；不继承核心环境变量；**PATH 固定自有目录优先**。

**降级铁律**：任何 sidecar 缺失、版本过低或崩溃，只把对应能力标记不可用，**绝不阻塞其余下载**。

**时间安排**：P0/P1 按零 sidecar 建骨架，只预留 `Resolver` / `PostProcessor` 接口；P2 真正接 yt-dlp 时再补统一 manifest + JSON-RPC + 监督器，避免过早设计。

## 备选方案

| 方案 | 结论 |
|---|---|
| 一开始就搭完整 sidecar 框架 | ❌ 过早设计，P0/P1 用不到 |
| cgo 内嵌 CPython / 静态链 libav* 以把进程数降为 0 | ❌ **消除进程 ≠ 消除依赖**：外来运行时仍在，静态编译与 iOS/Android 全废 |
| gpt：把 `goproxy` MITM 转发外置 | ❌ 它是 Go 库，本体在核心内；只有装 CA/改系统代理需提权助手 |
| 把 Telegram 并入 Python worker | 曾为推荐以省一份运行时，但 ADR-0009 后已无 Python-only 的必要 |

## 后果

**正面**：零 sidecar 版完全可达（传输协议 + Telegram + 中文站 + HLS/DASH remux = 纯 Go 单二进制含移动端）；进程数增长完全由内容源能力驱动，与 BT/ed2k 无关。

**负面**：sidecar 框架延后到 P2 才落地，届时需在已有接口上补监督器；yt-dlp 规则独立更新通道需单独建设。

## 合规与验证

- `12` 故障注入覆盖「每个 sidecar 逐个缺失/崩溃」场景，验证其余下载不受影响。
- `13` 移动端包中不含任何 sidecar 二进制；iOS 不得按需下载可执行码。
- `09` §3 的能力演进表作为每个里程碑的能力面板依据。
