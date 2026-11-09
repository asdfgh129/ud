# ADR-0014 插件引擎 goja 起步，wazero/Extism 提供可选硬隔离

- **状态**：Proposed
- **日期**：2026-11-08
- **决策者**：架构组 + 安全
- **关联**：`08`、`11` §5、`10` §3

## 背景

站点规则几乎每周失效，必须支持**独立于主程序发版**的热更新；同时 Resolver 会处理不可信 URL 与第三方脚本，是最大攻击面。参考 Gopeed 采用 goja + JS 扩展的路线，但需要明确沙箱边界。

## 决策

1. **起步用 `goja`**（纯 Go JS 引擎，MIT，Gopeed 同款）承载声明式站点规则插件；保持零 cgo、单二进制。
2. goja 仅支持 **ES5.1**：插件不得使用 `async/await`、可选链、`fetch`、ES Module 语法（宿主做转译包装）；宿主注入 `ctx.http` / `ctx.log` / `ctx.base64` / `ctx.sha256`。
3. **能力默认全关**：插件 `capabilities` 中 `fs` / `env` / `subprocess` 默认 `false`，网络只能走显式 `allowedHosts` 白名单的 `ctx.http`；强制 `timeoutMs` / `memoryMB` / `maxResponseBytes` 上限；官方规则包需 Ed25519 签名校验。
4. 所有 Resolver 产出 URL（含 Metalink 镜像、m3u8 分片、yt-dlp formats、重定向链）在出口统一过 `security.SSRFGuard`（`11` §2）；插件 `Match()` 优先级恒低于内置实现，安全敏感的内置 Resolver（thunder 解码、Metalink、SSRF Guard）**不允许插件覆盖**。
5. **硬隔离升级路径**：规则来源不可信 / 需第三方贡献 / 需现代 JS → `wazero`（纯 Go WASM 运行时，可精确裁剪 WASI 的 net/fs/env）或 `Extism`；`wazero + QuickJS-WASM` 可在进程内跑现代 JS，但工程量大，**不用于自行维护 YouTube 对抗逻辑**（ADR-0010）。
6. 规则包走独立更新通道，遥测默认关闭；失败抛 `RULE_OUTDATED` 并允许兜底 Resolver，不得静默丢弃任务。

## 备选方案

| 方案 | 结论 |
|---|---|
| 直接用 Node/Deno 跑插件 | ❌ 引入额外运行时与进程，破坏零 sidecar 基线 |
| 只做内置 Go Resolver，不开放插件 | ❌ 站点规则无法热更新，维护成本转嫁到发版 |
| 起步即 WASM 硬隔离 | 更安全但插件生态与开发体验门槛高；作为可选升级路径 |
| 用 goja 跑 YouTube 现代挑战脚本 | ❌ ES5.1 常不足，且对抗逻辑需长期团队维护 |

## 后果

**正面**：站点规则可热更新、开发门槛低、零 cgo；沙箱与 SSRF 约束前置；未来可平滑升级到 WASM 而不改 `Resolver` 接口。

**负面**：goja 的 ES5.1 限制需要宿主转译与文档约束；纯 JS 沙箱不如 WASM 硬隔离，因此第三方规则必须先过签名与静态扫描。

## 合规与验证

- `08` §9 评审清单作为 PR 必查项；`11` §5 隔离规则纳入安全评审。
- `12` 安全用例集：插件死循环、越权访问 `fs`/`env`、白名单外请求、私网 URL、超大响应，全部被拦截或超时终止。
- 官方规则包签名校验失败时拒绝加载，本地开发需显式 `--insecure-plugins`。
