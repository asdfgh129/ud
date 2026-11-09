# ADR-0015 状态用 SQLite（modernc，纯 Go），凭据存系统钥匙串

- **状态**：Accepted（2026-11-09 P0 里程碑评审批量批准）
- **日期**：2026-11-08
- **决策者**：架构组 + 安全
- **关联**：`06`、`11` §3、`13` §8

## 背景

单调度器的直接要求是**状态只有一份真值源**。若让各 Transport 自管断点（如 `.aria2` 控制文件）就会出现双真值源；同时凭据（Cookie、Telegram session、代理密码、PT passkey）绝不能随 DB 一起被复制、备份或打印。

## 决策

1. 主存储采用 **SQLite + `modernc.org/sqlite`（纯 Go、免 cgo、BSD）**，WAL 模式、`foreign_keys=ON`、`busy_timeout`。
2. 表结构见 `06` §3：`tasks / resources / files / mirrors / protocol_identity / checksums / chunks / task_options / events / sidecars / app_meta`。`chunks` 是统一分片真值表（HTTP Chunk 与 BT Piece 同表）。
3. 事务不变式：**状态迁移 + 分片进度更新**在同一事务；完成提交与最终路径写入在同一事务。进度批量刷盘（500ms 或 4MB 聚合），不每片一事务。
4. **凭据一律存系统钥匙串**（Keychain / Credential Manager / libsecret），DB 与日志只存 `credential_ref`；Telegram session / eD2k RSA 私钥存 0600 文件（必须持久化，否则重启换身份、积分归零）。
5. 崩溃恢复只信任 `chunks` 表 + 实际 `.part` 文件校验结果，不信任内存态；`assigned` 分片一律回 `pending`（`06` §5）。
6. 迁移用 `app_meta.schema_version` 单调递增 + 幂等脚本；升级前自动备份，无回滚脚本时拒绝启动。
7. 日志、事件、REST 响应统一过 `security.Redact`；passkey 不进日志、不进事件、不跨任务同步。

## 备选方案

| 方案 | 结论 |
|---|---|
| `mattn/go-sqlite3`（cgo） | ❌ 破坏 `CGO_ENABLED=0` 与移动端交叉编译 |
| 各协议自带断点文件（`.aria2` 等） | ❌ 双真值源，与单调度器冲突 |
| 凭据加密后存 DB | ❌ 备份/复制扩散风险，密钥管理仍回到系统钥匙串 |
| KV 存储 / 自研文件格式 | ❌ 缺事务与查询能力，分片状态一致性难保证 |

## 后果

**正面**：单一真值源、事务化状态迁移、崩溃可恢复、纯 Go 可交叉编译、`udctl backup` 单文件即可。

**负面**：`chunks` 是高频写表，需批量刷盘与 WAL 调优；1000 任务约 20 万行需索引与保留策略；凭据与 DB 分离使备份/恢复多一步（需用户单独导出钥匙串）。

## 合规与验证

- `12` 故障注入：`kill -9` 后已落盘分片 100% 复用、无重复下载、无损坏；`.part` 被外部篡改时只重下该片。
- 全量扫描 DB、日志、事件、REST 响应，无 `cookie|authorization|passkey|session` 明文（`11` §9）。
- `12` 发布门禁校验 `app_meta.schema_version` 迁移链与回滚脚本。
