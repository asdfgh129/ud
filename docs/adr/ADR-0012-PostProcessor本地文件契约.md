# ADR-0012 PostProcessor 输入必须是本地文件路径，输出 .part + 原子提交

- **状态**：Accepted（2026-11-09 P0 里程碑评审批量批准）
- **日期**：2026-11-08
- **决策者**：架构组
- **关联**：`05` §9、`09` §6、`13` §10、`03` FR-PST-03

## 背景

ffmpeg 一旦以 `-i URL` 直接拉取网络流，进度、限速、断点续传、Cookie/Header 管理就全部落到外部进程手里 —— 前面否掉的「双调度器」问题会原样回归。同理，若 PostProcessor 接收 `io.Reader` 或自行决定最终路径，核心就失去了对任务完成时刻与文件一致性的控制。

这条契约必须在 **P0 定死**：它是「加 ffmpeg 只是插件、不是架构承诺」的前提。若等 P2 再改接口，会牵动 `Resource`、`Store`、API 与全部测试。

## 决策

1. `PostProcessor.Process(ctx, in LocalFiles, out OutputSpec)` 的**输入只能是本地文件路径**；禁止 URL、`io.Reader`、网络流、远程句柄。
2. 输出统一写 `<target>.part`，成功后由**核心**（不是插件）执行原子 `rename` 提交；失败保留原输入以便重试。
3. `Remux`（纯 Go，内置）与 `Transcode`（ffmpeg，可选）实现同一接口，互不影响；移动端只启用 `Remux`。
4. 执行前预检可用空间（输入 + 输出约 2×）；空间不足返回 `E_STORAGE_FULL` 并暂停任务，不 OOM。
5. 可观测性：ffmpeg 用 `-progress pipe:1 -nostats -nostdin -y` 的 key=value，**同时读 stdout 与 stderr** 防管道死锁；禁止解析随版本变化的 stderr 进度行。
6. 取消：Unix `Setpgid: true` 杀进程组；Windows `taskkill /T /F` 杀进程树，确保无残留进程继续写文件。
7. 资源约束：`-threads N` + 全局并发信号量 + 内存上限，避免与 Go 调度器、BT 分片哈希抢 CPU。

## 备选方案

| 方案 | 结论 |
|---|---|
| PostProcessor 接收 URL / 流，ffmpeg 自取网络 | ❌ 双调度器回归，进度/限速/断点/Cookie 全部失控 |
| 插件自行写最终文件并通知核心 | ❌ 核心失去原子提交与一致性控制，崩溃后状态不可判 |
| 直接覆盖输入文件（in-place） | ❌ 失败不可重试，数据可能半损坏 |
| 用内存缓冲传递 | ❌ 大文件不可行 |

## 后果

**正面**：核心保持对完成时刻、文件布局、状态迁移的唯一控制；失败可重试；`.part` + 原子 rename 让崩溃恢复简单（存在 `.part` 且缺最终文件 → 重进 `PostProcessing`）；ffmpeg 完全被关在插件边界内。

**负面**：输入已落盘、输出再写一份 = **2× 磁盘 I/O 与临时空间**，这是不可避免的成本；需要在 UI 上让用户看到「后处理中」阶段而非误以为已完成。

## 合规与验证

- CI 静态检查：`PostProcessor.Process` 签名不含 URL / `io.Reader`；传输包内无 `exec.Command`（`15` §2）。
- `12` 故障注入：后处理中 `kill -9`、用户取消、磁盘写满、ffmpeg 崩溃、输出被外部篡改，全部可恢复且无损坏。
- 日志/事件中出现 `task.postprocessing` → `task.completed` 的严格顺序。
