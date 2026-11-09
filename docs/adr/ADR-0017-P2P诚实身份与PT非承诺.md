# ADR-0017 P2P 保持诚实 PeerID，不承诺私有 tracker 准入

- **状态**：Proposed（需产品 + 法务会签）
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
