# ADR-0001 核心语言选择 Go，坚持纯 Go 单二进制

- **状态**：Proposed（待立项评审批量转 Accepted）
- **日期**：2026-11-08
- **决策者**：架构组
- **关联**：`01` §8 C-01、`04` §1、`10` §2、`13` §2

## 背景

需求覆盖 BT、ed2k、HTTP、Metalink、迅雷链接、视频站、Telegram、微信，且要求跨平台（含移动端）。不存在单一语言原生覆盖全部能力：yt-dlp/Telethon 是 Python，libtorrent 是 C++，ed2k 库曾只有 C++/Java。因此语言选择实质上是选**核心**用什么，并通过进程边界容纳其他生态。

关键约束核实：iOS 不允许第三方 App 派生子进程；Android 10/API 29+ 因 W^X 禁止 exec 应用私有目录文件。任何把核心能力放在子进程的方案，都会让移动端直接失去该能力。

## 决策

1. **核心守护进程使用 Go**，且必须保持 `CGO_ENABLED=0`、单二进制静态编译。
2. 传输层全部在进程内用 Go 库实现；只有语言运行时不同、许可证需隔离、需提权、需独立热更新的能力才外置为 sidecar。
3. 禁止为引入任何外部能力而破坏纯 Go 单二进制与移动端可达性（包括 cgo 内嵌 CPython、静态链接 `libav*`）。
4. 追求极致 BT 性能时，可选路径是 **cgo 接 libtorrent 到进程内**并单独立项评估，而不是引入子进程。

## 备选方案

| 方案 | 否决/保留理由 |
|---|---|
| Rust | `librqbit` + `reqwest` 性能好，但 ed2k / 站点解析库缺失，sidecar 更多，起步慢。团队 Rust 明显更强时可重新评估 |
| C++ / Qt | libtorrent 2.0 协议最强，但 Boost 依赖、跨平台打包重、迭代慢；仅作为可选 BT 后端 |
| Python | yt-dlp 原生、开发最快，但打包分发难、GIL、不适合常驻 daemon；**只做 sidecar** |
| Electron / TS | 体积大，不适合下载内核；仅 UI 备选 |
| 混合：Go 核心 + cgo 内嵌 CPython / libav* | 进程数变 0 但外来运行时仍在，静态编译与 iOS/Android 全废。**消除进程 ≠ 消除依赖** |

## 后果

**正面**：单二进制可分发性最好；协议库最齐（`anacrolix/torrent` + `goed2k` + `net/http` + `goja` + `goproxy` 全在进程内）；goroutine 天然适合分块并发；Gopeed 已验证这条路线；传输层零 sidecar、零 GPL。

**负面**：GC 与 BT 生态深度不如 libtorrent；yt-dlp / ffmpeg 生态无法原生复用，需要 sidecar 契约；Go 的 JS 引擎（goja）仅 ES5.1。

**后续动作**：`13` 建立六平台 `CGO_ENABLED=0` 构建矩阵；`15` §2 加入 CI 静态检查防止 cgo/exec 回潮；`10` 维护依赖清单。

## 合规与验证

- `CGO_ENABLED=0 go build ./...` 六平台通过（`12` 发布门禁）。
- `go version -m` 与 SBOM 一致，无未登记的原生依赖。
- 每次 PR 检查传输包中无 `exec.Command`、无 `import "C"`。
