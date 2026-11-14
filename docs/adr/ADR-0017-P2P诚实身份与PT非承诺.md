# ADR-0017 P2P 保持诚实 PeerID，不承诺私有 tracker 准入

- **状态**：Accepted（2026-11-09 P0 里程碑评审批量批准）
- **日期**：2026-11-08
- **决策者**：架构组 + 产品 + 法务
- **关联**：`11` §7、`16` §4/§5/§7、`03` FR-INT-03/04/06

## 背景

「线协议互通」不等于「网络接纳」。私有 tracker 普遍只白名单 qBittorrent / Transmission / rTorrent / Deluge 等特定版本，靠 PeerID + 扩展握手识别；未列入白名单的 `-GT0001-` 会被拒或标记。公网侧还有 PeerBanHelper 类按 PeerID / Client Name 黑名单 + 伪装检测 + 假进度检查的工具。

同时核实：eMule 积分是**双向、按客户端对**计分，新客户端 modifier 恒 1.0、排队列末尾；`Seed` 在 `anacrolix/torrent` 中**默认 false**，默认只为换回数据而上传，`NoUpload` 开着即纯吸血。

## 决策

1. **保持诚实客户端身份**：默认 `-GT0001-` + 扩展握手 `go.torrent dev 20150624`；`Bep20` / `PeerID` / `ExtendedHandshakeClientVersion` 允许配置，但**禁止伪装成 qBittorrent / Transmission 等白名单客户端**。伪装违反站点规则，且 announce 时序、格式、统计交叉校验容易被反作弊识别。
2. **产品只声明「兼容标准 BT / eD2k 网络，不承诺 PT 准入」**；文案口径见 `16` §7。确有 PT 需求时提供可选 **BYO 适配器**，把任务交给用户已安装的白名单客户端（qBittorrent WebUI），而非自己冒充。
3. `private=1`（BEP 27）任务自动关闭 DHT / PEX / LSD；passkey 只存钥匙串引用，**不进日志、不进事件、不跨任务同步**，announce URL 在内存中拼接。
4. **必须自行实现 P2P 礼仪**：分享率 / 做种时长策略、上传槽、UPnP/NAT-PMP 端口映射；`uploaded/downloaded/left` 统一取 `tasks.bytes_*`，禁止各 Transport 自算。
5. eD2k 侧显式开启混淆与 Secure Ident、持久化 RSA 身份与积分（ADR-0005）；接受 Low ID 与积分带来的速度限制，不做假进度、不做积分伪造。
6. 不提供任何规避平台反作弊、限速或社区规则的能力。

## 备选方案

| 方案 | 结论 |
|---|---|
| 伪装 PeerID 以进入 PT | ❌ 违反站点规则、易被识别、法务与声誉风险 |
| 保持库默认 `Seed=false` | ❌ 被视为吸血，PT 上 H&R、公网上被封禁 |
| 在文档中写「支持 PT」 | ❌ 虚假宣传，见 `16` §7 禁止表述 |
| 把 passkey 打进日志便于排查 | ❌ 凭据泄漏，违反 `11` §3 |

## 后果

**正面**：合规边界清晰、长期声誉可控；诚实做种可缓解公网反吸血工具的封禁；私网流量特征符合 private flag 语义。

**负面**：PT 用户群体无法直接服务；新 eD2k 身份在积分体系中排队靠后、初期速度慢；需要做种与端口映射的额外实现与运维（UPnP 失败时需在 UI 提示手动映射）。

## 合规与验证

- `12` BT-08 抓包验证 `private=1` 无 DHT/PEX 流量；BT-14 抓包验证 PeerID 诚实；BT-11 验证分享率达标后停止做种。
- 全量日志/事件扫描无 passkey 明文（`11` §9）。
- 发布前由产品/法务对照 `16` §7 逐条检查对外文案并签字。

## 实现落地与偏差登记（2026-11-13 补记，不改写上述决策）

> 本节只记录实现事实；上文「决策 / 备选方案 / 后果 / 合规与验证」保持原样。

**已落实**：WBS 7 的 `internal/transport/bt` 对 `private=1` 强制 `NoDHT` /
`DisablePEX` / `NoUpload` / `Seed=false`，PeerID 使用诚实的 `-UD0000-` 前缀
（决策 1 允许配置且禁止伪装，前缀属「自定义但非伪装」）；passkey 只存钥匙串引用。
`docs/03` §9 已记录 `L3-06` 回环握手抓包证据。`BT-14` 的真实客户端抓包仍**未执行**。

**未落实（偏差）**：决策 4 要求的分享率 / 做种时长策略、上传槽、UPnP/NAT-PMP 端口映射
与「`uploaded/downloaded/left` 统一取 `tasks.bytes_*`」的做种统计链路，
以及「合规与验证」中的 `BT-11`，在 WBS 7 之后**均未实现**；
`bt.clientConfig` 固定 `NoUpload=true` / `Seed=false`，`config.BTConfig` 的
`seedRatio` / `seedTimeMinutes` / `enableUPnP` 仍是「parsed, ignored」的预留字段。
备选方案表已把「保持库默认 `Seed=false`」判为 ❌，因此这不是本 ADR 或
ADR-0004 授权的产品边界，而是按 `15` §4 规则②应视为缺陷的**未签署范围缩减**。

**处置**：已按 `docs/adr/README.md` 与 `15` §4 提交
[ADR-0021](ADR-0021-L4门禁与P2P礼仪范围冲突裁决提案.md)（`Proposed`，**未生效**），
列 A（补齐实现，使代码回到本 ADR 基线）/ B（修订范围，须把本 ADR 与 ADR-0004
标 `Superseded` 并同步 `03` 等九处）/ C（保持开放）三条须评审签署的路径。
签署前本 ADR 仍为 `Accepted`、决策 4 仍然有效且未满足；
`BT-02` / `BT-11` 继续 `UNSUPPORTED`、`release_gate_met: false`，
不得以「豁免」名义计入通过数，也不得把 `NoUpload=true` 表述为「ADR-0004 不做种边界」。

## 实现落地补记（2026-11-14，ADR-0021 路径 A；上文「实现落地与偏差登记」原文不改）

> **本段是当前态口径**。上文「未落实（偏差）」「`config.BTConfig` 仍是
> parsed, ignored」「ADR-0021 `Proposed` 未生效」等均为 2026-11-13
> 提案期时点值，按项目文档控制规则保留不改。

- 项目发起人于 2026-11-14 明确指令选择 [ADR-0021](ADR-0021-L4门禁与P2P礼仪范围冲突裁决提案.md)
  的**路径 A**；该记录转为 `Accepted（路径 A）` 并同步索引。**本次不是评审组
  五方签署**，`docs/18` §六 签署表仍留白，法务 / 安全复签与 `14` R-10 重评待补。
- **决策 4 的编码部分已落地**：分享率 / 做种时长任一达标即停、上传槽、
  UPnP / NAT-PMP 端口映射；`Client.ConnStats()` 的上传计数统一写入
  `tasks.bytes_uploaded`，与 announce 统计同源；`scheduler.Limiter` 仍是唯一
  速率权威，不新增第二个上传 limiter。诚实身份继续强制 `-UD` 前缀，非诚实
  配置自动回落；**私有种子恒不上传且恒禁 DHT / PEX**，仍**不承诺 PT**。
- **合规与验证未闭环**：`BT-14` 真实客户端抓包、`BT-11` 分享率达标后停止
  做种的 L4 实测、`BT-02` 双向矩阵与双机 NFR-PERF-01 均**未执行**；
  套件 v1 注册表仍把 `BT-02` / `BT-11` 固定为 `ScopeProductGap` +
  `UNSUPPORTED`，路径 A 实现**不翻转**该状态。L4 **0/14**、
  `release_gate_met=false`、`l4_executed=false`、序 6 **仍开放**。
- 因此本 ADR 的决策、备选方案否决结论与「发布前由产品 / 法务对照 `16` §7
  逐条检查文案并签字」要求仍然有效；不得据编码落地宣称 L4 通过、
  礼仪复签完成或 PT 准入。
