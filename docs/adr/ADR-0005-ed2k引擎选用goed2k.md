# ADR-0005 ed2k 采用 goed2k（MIT，纯 Go）

- **状态**：Proposed
- **日期**：2026-11-08
- **决策者**：架构组
- **关联**：`10` §3、`16` §3/§5、`12` §3.2、`14` R-06

## 背景

ed2k / Kad 生态长期只有 C++（aMule/eMule）、Java（MLDonkey 系）实现，且 `amuled` 为 GPL-2.0、依赖 wxWidgets，无法嵌入纯 Go 核心。核实发现 `goed2k`（MIT，纯 Go，Gopeed v1.9.3 采用）覆盖面意外地完整。

## 决策

1. 采用 **`goed2k` v0.1.0**（模块 `github.com/monkeyWie/goed2k` / `github.com/goed2k/core`）作为 ed2k Transport，进程内、免 cgo。
2. 已知实现范围：多 eD2k 服务器 + `server.met`、Kad4/KadV6 bootstrap 与搜源发布、Source Exchange、Low ID 回调、AICH、Secure Ident（RSA 2048）、积分系统（`PeerCreditManager`/`ScoreRatio`/`client_credits.go`）、zlib 上传压缩、OfferFiles/Kad 发布、CryptLayer 协议混淆、Collection 链接。
3. **必须覆盖库的默认开关**：CryptLayer 混淆默认关、Secure Ident 默认关、`CreditsOnlyVerified=false` → 真实网络应开启混淆与 SecIdent，持久化 RSA PEM（0600），否则重启换身份、积分归零。
4. 需维护 `server.met` 与 Kad `nodes.dat` 引导列表；确保 TCP 4662 / UDP 4672 可入站（UPnP），否则为 Low ID、只能看到约 40–60% 源。
5. ed2k 排在 **P3**，作为兼容长尾能力，不进核心排期；产品不承诺下载速度。

## 备选方案

| 方案 | 结论 |
|---|---|
| `amuled` / aMule（GPL-2.0） | ❌ 许可证 + wxWidgets 重 + 需子进程，移动端不可达 |
| 自研 ed2k 协议栈 | ❌ 工作量与互操作风险过高 |
| 放弃 ed2k | 可选，但需求明确包含；且 `goed2k` 已有生产用例，成本可控 |

## 后果

**正面**：纯 Go、零 cgo、零新增进程、MIT 许可；线协议覆盖 aMule/eMule 主要互通路径。

**负面 / 主要风险**：`goed2k` 是 **AI 辅助开发的 v0.1.0 新库**，官方文档明确写「尚未与真实 eMule 全版本做大规模互操作回归；生产环境建议先小范围试连」。Gopeed 是一个生产用例但不等于全版本验证。eMule 积分是**双向、按客户端对**计分，新客户端 modifier 恒 1.0、排队列末尾，须先上传攒信用；网络规模小、冷门文件常无在线源；0.50a 与 0.72a 并存，部分 mod 有不良客户端检测。

**后续动作**：`12` §3.2 建 eD2k 互操作套件（`aMule` + eD2k 服务器 `eNode-go`（Go，含 Lugdunum 扩展/混淆/LowID 回调）+ Kad 引导，小范围试连）。**ED-01/02/05/06 未通过则 P3 不得发布**（风险 R-06）；必要时 fork 自维护（OPEN-03）。

## 合规与验证

- 互操作套件通过；重启后 UserHash 不变（ED-06）；对端「仅混淆」模式可建连（ED-05）。
- 产品文案只写「兼容标准 eD2k / Kad 网络」，禁止承诺速度与冷门资源（`16` §7）。
- 与 `16` §4 一致：eD2k 身份字段在 P0 的 `Resource` 中预留，避免 P3 改表。
