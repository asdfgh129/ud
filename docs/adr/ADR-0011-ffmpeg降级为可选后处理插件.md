# ADR-0011 ffmpeg 降级为桌面可选 Transcode 插件

- **状态**：Proposed（**修订初版「ffmpeg 必需」定位**）
- **日期**：2026-11-08
- **决策者**：架构组 + 法务
- **关联**：`09` §6、`11` §6、`13` §5/§6、ADR-0012

## 背景

两点前提校正：

1. **FFmpeg 不是 C++，是 C**（C99 + 大量手写汇编），形态是「一组 C 库（`libavcodec/libavformat/libavutil/libavfilter/libswscale/libswresample/libavdevice`）+ CLI」。我们 exec 的是 `ffmpeg` / `ffprobe` CLI，不是链接 C++ 库。
2. **许可证比语言更关键**：默认 **LGPL-2.1+**；`--enable-gpl` → GPL-2+；`--enable-version3` → v3；`--enable-nonfree` → 二进制不可再分发。市面预编译包多数是 GPL 构建。

进一步核实「只下载是否需要 ffmpeg」：**不需要**。三层产物：
- A 原样保存（直链、`.ts` 分片、分离音视频各存一份、字幕/封面）→ Range 写盘 + `crypto/aes` 即可；
- B 合成单个可播放 MP4/MKV → 仍零 ffmpeg，但要自写纯 Go transmux（TS 解封装、Annex B→AVCC、ADTS→MP4、**PTS/DTS 重基准**、写 `moov` + faststart）；
- C 改变内容（转码、转 MP3、硬字幕、缩略图、坏流启发式修复、直播录制容错）→ 才真需要 ffmpeg。

> 两处表述修正：正常 **PTS/DTS 重基准本来就是 remux 的一部分**，纯 Go 必须自己实现，不是 ffmpeg 专属；单独下载音频流或字幕文件也零 ffmpeg，只有「转码成 MP3」「把字幕烧进画面」才需要。`ffprobe` 同样非必需（`mkvgo probe` / `mp4ff-info` 读容器头即可）。

## 决策

1. `PostProcessor` 分两层：**`Remux`（纯 Go，默认内置、零依赖）** 与 **`Transcode`（ffmpeg，桌面端可选插件）**。
2. 纯 Go remux 可用 `gravity-zero/mkvgo`（无 cgo 无 ffmpeg，MKV/WebM↔MP4 无损 remux、probe、HLS/DASH 打包）、`Eyevinn/mp4ff`（MIT，MP4/fMP4 读写、CENC）、`ugparu/gomedia`（MIT）。**下载器里 90% 的「合并」是 remux 而非转码。**
3. ffmpeg 采用 **exec 独立 CLI** 形态（A），不链接 `libav*`；桌面默认 PATH 探测 → 首次使用时按需下载 **LGPL 构建** + sha256 校验；**移动端默认禁用**，由 Remux 顶上。
4. 接入形态 B（cgo/JNI 静态链）会破坏纯 Go 单二进制与交叉编译、并触发 LGPL §6「可重链接」义务，**不采用**；形态 C（ffmpeg.wasm）因 2GB 单文件上限、整体载入内存、无硬加速、性能约原生 7%–28%，**本项目不可用**。
5. ffmpeg 为 `PostProcessor` 实现，**不在传输热路径**；`-i URL` 直接拉流**严格禁止**（ADR-0012）。
6. 启动时 `-version` / `-buildconf` / `-encoders` / `-hwaccels` 能力协商，按 major 版本分支命令模板（FFmpeg 9.0「Lei」2026-08 删除 `-vsync`/`-top`/`-filter_complex_script`、改 TLS 默认校验证书等）。

## 备选方案

| 方案 | 结论 |
|---|---|
| 把 ffmpeg 列为必需 sidecar | ❌ 初版定位错误，remux 可纯 Go 完成 |
| cgo 静态链 `libav*` | ❌ 破坏交叉编译 + LGPL 合规更难；exec 才是 arm's-length 最干净形态 |
| ffmpeg.wasm | ❌ 下载器场景不可用 |
| 完全移除 ffmpeg | 可选，但转码/烧字幕等高级能力会缺失，保留为可选插件最经济 |

## 后果

**正面**：零 sidecar 版完全可达；移动端保持可达性；exec 天然崩溃隔离且不触发链接义务；体积、二进制矩阵、签名公证、许可证义务全部被关在桌面插件内。

**负面 / 代价（难度排序：跨平台 ≫ 部署 > 性能）**：
- **跨平台**：BtbN 官方自动构建仅 Windows/Linux x64+arm64；**macOS 无官方静态构建**（evermeet 仅 Intel/GPL、OSXExperts 需去 quarantine + ad-hoc 签名、Homebrew 非静态）；`ffmpeg-static` darwin-arm64 带 `--enable-nonfree` 不可再分发 —— **不存在单一、许可证干净、静态、覆盖全平台的源**。iOS 禁止派生子进程 + App Store 2.5.2 禁止下载可执行码（按需下载在 iOS 违规）；Android API 29+ W^X、Android 15+ 16KB page；FFmpegKit 2025 已退役。硬件加速（NVENC/QSV/VA-API/D3D11VA/VideoToolbox/MediaCodec）碎片化。
- **部署**：LGPL 静态包约 160MB(Win x64) / 130MB(Linux x64)；许可证义务（不带 gpl/nonfree、随附精确对应源码与 configure 行、About/EULA 声明、EULA 不得禁止逆向、保留 buildconf）；macOS helper 单独签名公证、Windows SmartScreen、Flatpak/Snap 可能禁 exec。
- **性能（最轻）**：单次 exec 200–500ms 可忽略（千个 15s 短片批处理累计约 5 分钟）；真实成本是输入输出**双写 2× 磁盘 I/O**、CPU 争抢（须 `-threads N` + 全局并发信号量）、可观测性（`-progress pipe:1 -nostats`、同时读 stdout/stderr 防死锁）、取消时杀整个进程组/进程树。

## 合规与验证

- `12` 发布门禁：桌面缺 ffmpeg 时下载与 Remux 不受影响；移动端产物不含 sidecar。
- 发布前锁定 LGPL 构建、校验 sha256 与 buildconf、随附源码获取方式、法务签字（`11` §6）。
- `09` §6 的 7 条强制实现约束纳入代码评审清单。
