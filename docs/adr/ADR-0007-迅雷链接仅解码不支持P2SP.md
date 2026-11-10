# ADR-0007 迅雷链接仅做解码，不支持 P2SP

- **状态**：Accepted（2026-11-09 P0 里程碑评审批量批准）
- **日期**：2026-11-08
- **决策者**：架构组 + 产品 + 法务
- **关联**：`01` §5 NG-01、`08` §6、`16` §7

## 背景

需求中「迅雷协议」实际包含两件不同的事：① `thunder://` 专有链接格式；② 迅雷的 P2SP 加速 / 高速通道 / 离线下载服务。后者是**私有协议，无开源实现**，且涉及服务端资源与商业授权；前者只是 `Base64("AA" + 原始URL + "ZZ")`，解码后就是普通 HTTP/FTP/ed2k 直链。

## 决策

1. 内置 `WrapperResolver` 实现 `thunder://` 解码（约 20 行），并顺带支持 `flashget://`（`[FLASHGET]` 包裹）、`qqdl://`（Base64 变体）。
2. 解码结果必须重新校验 scheme 白名单与 URL 合法性，产出单文件 `Resource`，交回自有 HTTP/FTP Transport。
3. **明确不实现、不承诺**迅雷 P2SP、高速通道、离线下载、会员加速。
4. 该 Resolver 为**安全敏感内置实现，不允许插件覆盖**（`08` §5）。
5. 产品文案、功能列表、Release Note 一律使用「解析 thunder:// 链接」，禁止出现「迅雷加速」「支持迅雷协议」「高速通道」「离线下载」。

## 备选方案

| 方案 | 结论 |
|---|---|
| 逆向 P2SP 协议 | ❌ 私有协议、无开源实现、法律与稳定性风险，收益不确定 |
| 调用迅雷开放 API / 会员服务 | ❌ 无适用于通用下载器的公开授权 |
| 完全不支持 thunder:// | 可接受但成本几乎为零，且用户迁移成本低，故保留解码 |

## 后果

**正面**：以极小成本获得大量历史链接兼容；边界清晰，避免宣传与法务风险。

**负面**：用户可能误以为「支持迅雷」= 有加速能力，需靠文案与 UI 提示（解码后展示真实 URL 来源）管理预期。

## 实现落地（2026-11-10，P1 WBS 8）

`internal/resolve/wrapper.go` 实现 `WrapperResolver`：三种格式的 Base64 解码（容忍缺 padding、
URL-safe 字母表、尾斜杠与 flashget 的 `&<port>` 后缀），marker 校验，解码后
scheme ∈ {http, https, ftp, ed2k} 白名单、内嵌凭据与控制字符拒绝，并**再过一次 SSRF Guard**。
文件名走 `internal/resolve/filename.go` 的路径穿越清理。它只产出 `Resource`，不解包字节、
不连迅雷服务；`internal/app/daemon.go` 将其注册在 `DirectHTTP` 之前。

**边界不变**：P2SP / 高速通道 / 离线下载 / 会员加速一律不实现；`ftp` / `ed2k` 解码结果在
当前 Transport 只支持 HTTP(S) 的前提下会在 `transport.Select` 处报能力错误。产品文案仍按
`16` §7 执行。测试见 `internal/resolve/wrapper_test.go`（含畸形输入与 SSRF 透传）。

## 合规与验证

- `12` §3.3 覆盖 thunder/flashget/qqdl 正常与畸形输入。
- 发布前由产品/法务对照 `01` §5、`16` §7 检查全部对外文案（`11` §7）。
- UI 在解析后显示「已还原为普通链接，不提供 P2SP 加速」的提示文案。
