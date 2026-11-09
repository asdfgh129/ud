# 09 Sidecar 与外部依赖契约

> 文档编号：UD-DOC-09 ｜ 版本：v0.1 ｜ 状态：草案 ｜ 日期：2026-11-08 ｜ 相关：ADR-0008、ADR-0010、ADR-0011

## 1. 判定标准

只有满足以下**任一**条件，才值得拆成独立进程：

1. 语言运行时不同（Python 生态）；
2. 许可证需要隔离（GPL / LGPL arm's-length）；
3. 需要提权或有系统级副作用（装 CA、改系统代理）；
4. 需要独立热更新 / 崩溃隔离（站点规则每周失效）。

**不满足就不要拆**。`goproxy` MITM 本体是 Go 库，在核心内；只有「装 CA / 改系统代理」这一小部分需要提权助手。`libtorrent` 若走 cgo 是进程内，只有额外包一层 daemon 才会新增 sidecar。

## 2. 当前清单（5 项，必需 0 或 2）

| # | Sidecar | 必需性 | 职责 | 拆分理由 |
|---|---|---|---|---|
| 1 | **Python worker（仅 yt-dlp）** | ⚠️ 做 YouTube/千站长尾时必需 | `yt-dlp -J --skip-download` 输出 formats / url / http_headers / cookies | Python-only；站点规则需独立热更新；崩溃隔离 |
| 2 | **Deno 2.3+（或 Node 22+）** | ⚠️ 随 #1 必需 | yt-dlp 的 EJS 挑战求解（YouTube `n` 参数 / sig） | **yt-dlp 的子依赖**，不由主程序监管；须随包/按需放入 PATH；Deno 默认无文件无网络权限更安全 |
| 3 | **ffmpeg / ffprobe CLI** | 🔵 可选 | 转码、转 MP3、烧字幕、缩略图、坏流修复、直播录制容错 | C 库 + LGPL/GPL 构建差异；短生命周期 exec 最干净 |
| 4 | **N_m3u8DL-RE** | 🔵 可选 | 复杂直播录制、多轨/多语言选流、AES-128 分片 | .NET 单文件二进制；简单 m3u8 用 Go 自研即可 |
| 5 | **提权助手（仅微信插件）** | 🔵 可选 | 安装/卸载根 CA、修改系统代理、注入 JS、热更新逆向规则包 | 需管理员权限 + 系统级副作用，权限与风险必须与主进程物理隔离，单独分发默认不安装 |

**已从清单删除**：`aria2c`（ADR-0003）、`Telethon`（ADR-0009，改 `gotd/td` 纯 Go）、`ffmpeg` 已由「必需」降为「可选」（ADR-0011）。

## 3. 最小集合随功能演进

| 交付范围 | 必需 sidecar 数 |
|---|---|
| 传输协议（HTTP/BT/磁力/ed2k/FTP/SFTP/Metalink/迅雷解码） | **0** |
| + Telegram（`gotd/td`）+ 中文视频站（`lux`）+ HLS/DASH 纯 Go Remux | **0**（零 sidecar 基线，含移动端） |
| + YouTube / 千站长尾 | **2**（Python worker + Deno） |
| + 高级后处理 / 复杂直播 | 可选 2（ffmpeg、N_m3u8DL-RE） |
| + 微信视频号 | 可选 1（提权助手，隔离发布） |

> 传输能力**永远是 0 sidecar**。sidecar 数量增长完全由「内容源能力」驱动，与 BT/ed2k 无关。P0/P1 不引入任何 sidecar 框架实现，等 P2 接 yt-dlp 时再补统一 manifest + JSON-RPC + 监督器（避免过早设计）。

## 4. manifest 契约

```json
{
  "name": "ytdlp-worker",
  "version": "2026.11.08",
  "minVersion": "2025.11.12",
  "capabilities": ["resolve.site", "youtube.ejs"],
  "license": "Unlicense",
  "sha256": "9f86d081...",
  "sourceURL": "https://releases.ud.dev/sidecars/ytdlp/2026.11.08/...",
  "buildconf": "python=3.12; yt-dlp[default]; platform=any",
  "transport": "jsonrpc-stdio",
  "platforms": ["windows-x64","windows-arm64","darwin-x64","darwin-arm64","linux-x64","linux-arm64"],
  "mobile": false
}
```

字段 `name / version / minVersion / capabilities / sha256 / license / sourceURL / buildconf` 为必填。启动时执行 `--version` 做**能力协商**；版本过低或校验失败 → 标记 `degraded`，只禁用对应能力。

## 5. 通信与监督

| 组件 | 通信方式 |
|---|---|
| Python worker | **JSON-RPC 2.0 over stdin/stdout**（不占端口、无网络鉴权面） |
| ffmpeg | 参数白名单直接 `exec`；`-progress pipe:1 -nostats` 读 key=value |
| N_m3u8DL-RE | 参数 exec + 输出目录约定 |
| 提权助手 | 本地 IPC + 显式用户授权，独立进程、独立权限 |

**统一监督规则**

- 心跳 + 超时强杀；崩溃自动重启（指数退避，上限 3 次后标记 `failed`）。
- 工作目录限制在下载目录；**不继承核心环境变量**；每个任务只传它需要的 cookie/header。
- 缺失、版本过低、校验失败 → 只把对应能力标记不可用，**绝不阻塞其余下载**（FR-PLT-06）。
- 能力变化推送 `capability.changed` 事件（见 `07` §4）。
- `PATH` 优先级固定为自有目录优先，避免用户机器上 Homebrew 老版本导致 bug 不可复现。

## 6. ffmpeg 专项约束（ADR-0011 / ADR-0012）

### 6.1 定位
`PostProcessor` 的桌面端可选实现（`Transcode`），**不在传输热路径**；`Remux` 为内置纯 Go 实现，永远优先。移动端默认禁用 ffmpeg。

### 6.2 接入形态与平台限制
| 形态 | 评价 |
|---|---|
| A. exec 独立 CLI | ✅ 桌面推荐；不链接、许可证最干净、天然崩溃隔离 |
| B. cgo / JNI 静态链接 `libav*` | ⚠️ 移动端唯一路径，但破坏纯 Go 单二进制与交叉编译；静态链接触发 LGPL §6「可重链接」义务 |
| C. ffmpeg.wasm | ❌ 本项目不可用（2GB 单文件上限、整体载入内存、无硬加速、性能约原生 7%–28%） |

**跨平台难点**：BtbN 官方自动构建仅覆盖 Windows/Linux x64+arm64；**macOS 无官方静态构建**（evermeet 仅 Intel/GPL、OSXExperts 需手动去 quarantine + ad-hoc 签名、Homebrew 非静态）；`ffmpeg-static` 的 darwin-arm64 带 `--enable-nonfree` 不可再分发。结论：**不存在单一、许可证干净、静态、覆盖全平台的源**，必须按槽位维护矩阵并保留 `buildconf` 溯源。

**iOS**：禁止派生子进程 + App Store 2.5.2 禁止下载/执行改变功能的代码 → **「按需下载 ffmpeg」在 iOS 直接违规**；FFmpegKit 已于 2025 年退役、预编译二进制移除。
**Android**：API 29+ W^X 禁止 exec 应用私有目录文件；Play 禁止外部下载可执行码；Android 15+ 要求 16KB page size 重编。

### 6.3 CLI 非稳定 API
FFmpeg 9.0「Lei」（2026-08）做了七个库 ABI break：删除 `-vsync`（改 `-fps_mode`）、`-top`、`-filter_complex_script`、NPP 滤镜、旧 NVENC preset，并**默认开启 TLS 证书校验**。启动时必须跑 `-version` / `-buildconf` / `-encoders` / `-hwaccels` 做能力协商，按 major 版本分支命令模板；硬件加速（NVENC/QSV/VA-API/D3D11VA/VideoToolbox/MediaCodec）按平台探测并回退软编。

### 6.4 强制实现约束
1. **禁止 `ffmpeg -i URL` 直接拉流** —— 一旦它自己取网络，进度/限速/断点/Cookie 全落入外部进程，双调度器问题原样回归。它**只读本地文件**。
2. 输入为本地路径，输出 `<target>.part`，核心原子 rename 提交；预检可用空间（约 2× 文件）。
3. `-threads N` + 全局并发信号量 + 内存上限，避免与 Go 调度器/BT 分片哈希抢 CPU。
4. `-progress pipe:1 -nostats -nostdin -y`；同时读 stdout 与 stderr 防管道死锁；不解析易变的 stderr 进度行。
5. 取消：Unix `Setpgid: true` 杀进程组，Windows `taskkill /T /F` 杀进程树。
6. 分发：桌面 PATH 探测优先 → 首次使用按需下载 **LGPL 构建** + sha256 校验；离线/内网/企业防火墙场景允许降级为 PATH 或禁用；macOS helper 需单独签名 + 公证。
7. 许可证：锁定不带 `--enable-gpl` / `--enable-nonfree` 的 LGPL 构建；随附精确对应源码与 configure 行、About/EULA 声明；EULA 不得禁止逆向（详见 `11` §6）。

## 7. yt-dlp 专项约束

1. **只取 JSON，绝不自己写文件**：`-J --skip-download`，产出 `formats / url / http_headers / cookies`，流地址交回核心 HTTP/HLS 引擎。
2. **禁止 `--downloader aria2c`**（已无 aria2 依赖）与任何自行落盘行为，否则进度/限速/断点/队列被割裂成两套。
3. 自带 formats 选取逻辑，规避「检测不到 ffmpeg 自动退化到 progressive 格式、YouTube 只有 720p」的画质陷阱（见 `12` 用例）。
4. Deno 仅作为求解器，不参与数据传输；`yt-dlp[default]` 的 EJS 脚本需校验完整性。
5. Python worker 走独立更新通道（规则失效是常态），与主程序发版解耦。
6. 站点规则失败 → `E_RESOLVE_FAILED` + 明确提示，不允许静默丢弃任务。

## 8. 降级矩阵

| 缺失组件 | 降级行为 | 用户可见 |
|---|---|---|
| Python worker / Deno | 中文站走 `lux` 进程内 Resolver；YouTube/长尾标记不可用 | 能力面板标注；其余下载不受影响 |
| ffmpeg | `Remux` 顶上；高级转码/烧字幕不可用 | 提示「高级后处理不可用」 |
| N_m3u8DL-RE | 简单 m3u8 走自有解析；复杂直播标记不可用 | — |
| 提权助手 | 微信插件整体不可用 | 插件默认未安装 |

## 9. 移动端差异

| 能力 | 桌面 | 移动 |
|---|---|---|
| BT / HTTP / FTP / SFTP / ed2k | ✅ | ✅（纯 Go 进程内） |
| Telegram / 中文站 / HLS-DASH Remux | ✅ | ✅ |
| yt-dlp / Deno / ffmpeg / 提权助手 | 可选 | ❌ 禁用 |

## 10. 分发与供应链

manifest 记录 `version / sha256 / license / sourceURL / buildconf`；首次使用按需下载并校验，GPL 组件不进主安装包；生成 SBOM 并纳入 `13` 的 CI 门禁。任何下载失败或校验不符只允许降级，不得重试到阻塞主流程。
