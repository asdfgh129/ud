# ADR-0013 Resource 在 P0 一次性纳入多文件输出与协议身份字段

- **状态**：Accepted（2026-11-09 P0 里程碑评审批量批准）
- **日期**：2026-11-08
- **决策者**：架构组
- **关联**：`05` §3、`06` §3/§4、`07` §5.3、`14` R-01、`16` §4

## 背景

「与现有 BT 客户端 / tracker / eMule 客户端兼容吗」这一追问暴露出：本项目**最大的返工风险不在协议实现，而在数据模型**。

若 `Resource` 只按「URL + Header + 大小」建模，后续加以下能力时会被迫改表、改接口、改全部测试：

- BT：v1 infohash、v2 `btmh`、`private` flag（BEP 27）、tracker 分层、passkey ref、peer_id、多文件种子（选择下载）、做种统计；
- eD2k：file hash、AICH root、UserHash、持久 RSA 身份引用、`clients.met` 积分引用；
- 控制面：`getFiles`（aria2 RPC facade）要求原生多文件输出；`tellStatus` 需要 `bitfield/numPieces/pieceLength/connections/followedBy/belongsTo` 能映射出来；
- 流媒体：分离流（多 `files[]`）、variant、adaptive 标记。

## 决策

1. **P0 一次性定死 `Resource`**：`ID / Kind / Engine / Files[] / Mirrors[] / Checksum / Headers / CredentialRef / ProtocolIdentity / PostProcess[] / Limits`。具体结构见 `05` §3，DDL 见 `06` §3，JSON 示例见 `06` §4。
2. `Files[]` **原生支持多文件输出**（`index / path / size / offset / selected`），不是单文件路径加可选附加字段。
3. `ProtocolIdentity` 一次性包含：
   ```
   BT:    infohash_v1, btmh_v2, private, trackers[[tier]], passkey_ref, peer_id, torrent_meta(不序列化)
   eD2k:  ed2k_file_hash, aich_root, user_hash, ident_key_ref, credits_ref
   Stream: variant_id, is_adaptive
   ```
4. **所有敏感身份只存引用**：passkey / RSA 私钥 / 积分文件 / 凭据一律存钥匙串或 0600 文件，DB 与日志只存 `*_ref`（`11` §3）。
5. `Engine` 保留 `go | byo_adapter` 用于**任务级**路由（不是协议级混用），默认值 `go`。
6. **P0 未通过 `Resource` 评审不得启动 P1**（`14` §3 关键路径；该门禁已于 2026-11-09 满足，见 `14` §10/§11）。

## 备选方案

| 方案 | 结论 |
|---|---|
| 先做最小模型，用到时再加字段 | ❌ 改表 + 改接口 + 改 JSON + 改迁移 + 改全部测试，且 P3 才暴露问题 |
| 用 `map[string]any` 扩展字段规避改表 | ❌ 失去类型安全与契约校验，SSRF/脱敏无法静态保证 |
| 每种协议一个独立资源类型 | ❌ 统一流水线被拆散，回到「一协议一实现」 |

## 后果

**正面**：P1 的 private flag / 双 infohash、P3 的 eD2k 身份、L1 的 `getFiles` 映射全部零改动接入；多文件种子与分离流天然支持；敏感信息静态可稽核。

**负面**：P0 的模型复杂度与评审成本显著上升；`core` 包需要提前理解尚未实现的协议概念。这是**有意的复杂度前移**，用来换掉后期全局返工。

**后续动作**：`06` 的 `protocol_identity` 表用可空列扩展；`resources.options/headers` 为 JSON，新增字段向后兼容；任何字段增删必须同步 `05`、`06`、`07` 示例与 `12` 用例（`07` §7）。

## 合规与验证

- `12` 契约测试：四类 `Resource` JSON（单文件多镜像 / BT 多文件含 private 与双 infohash / eD2k / 自适应流）往返无损。
- `06` §6 脱敏规则静态检查：DB 中不存在 passkey / cookie / session 明文字段。
- PR 若修改 `Resource` 结构，必须同时更新 `05`/`06`/`07` 与追踪矩阵，否则 CI 拒绝合入（`15` §2）。
