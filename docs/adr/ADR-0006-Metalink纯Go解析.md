# ADR-0006 Metalink 自写纯 Go 解析，撤回 libmetalink

- **状态**：Accepted（2026-11-09 P0 里程碑评审批量批准）
- **日期**：2026-11-08
- **决策者**：架构组
- **关联**：`08` §7、`10` §3/§5、`11` §2、ADR-0003

## 背景

初版规划建议用 `libmetalink`（C / MIT）解析 Metalink XML，或用 aria2 承担全量 Metalink。核实后两点改变结论：① `libmetalink` 是 C 库，引入 cgo 会破坏纯 Go 单二进制交叉编译，尤其 iOS / WASM；② curl 自 7.78.0 起因安全问题移除 Metalink 支持，生态事实性衰退，唯一全量实现只剩 aria2（已按 ADR-0003 删除）。

## 决策

1. **撤回 `libmetalink` 推荐**，Metalink 作为「多源清单容器」由纯 Go 自写解析（约 300–500 行）。
2. 支持范围：
   - RFC 5854 XML：`<file>`（name/size）、`<url>`（priority/location/type）、`<hash>`、`<pieces>`（type/length）、`<metaurl>`、v3 旧命名空间；
   - RFC 6249 HTTP 头：`Link: <...>; rel=duplicate; pri=; geo=`、`Digest: SHA-256=`。
3. 可参考 `dpb587/metalink`（Go / MIT）的 v4 结构体，v3 命名空间与 HTTP 头自补几十行。
4. 产出为多镜像 / 分块校验的 `Resource`，交给自有 HTTP 引擎与分片校验，**不引入任何传输实现**。
5. Metalink 归入「兼容能力」，**不进核心排期**（P1 顺带完成，优先级低于 BT）；不承诺「完整 Metalink 生态」。

## 备选方案

| 方案 | 结论 |
|---|---|
| `libmetalink` | ❌ C 库 / cgo，破坏单二进制与移动端 |
| 交给 aria2 | ❌ 已删除（ADR-0003） |
| 完全不支持 | 可接受但需求明确包含；自写成本仅数百行，性价比高 |

## 后果

**正面**：零 cgo、零外部进程、与自有分片引擎和 SSRF Guard 天然对齐；「同文件多源 + 分块校验」可用自有分片 + hash 复现。

**失去**：aria2 那种「同一个 ISO 同时从 HTTP/FTP/BT 取不同分片」的跨协议混合拉流现成实现（列为 NG-08 远期项）。

**安全要求（必做）**：Metalink 是**不可信输入** —— XML 大小上限（默认 4MiB）、禁止 DTD/外部实体、镜像数量上限（默认 32）、每个 URL 逐跳过 SSRF Guard、整文件与分块哈希强制校验、失败分片只重下该片并降镜像权重。

## 合规与验证

- `12` §3.3 覆盖 v3 / v4 / RFC 6249 / 多镜像优先级 / piece hash / 恶意清单用例。
- `11` §2 SSRF 用例集中包含 `Link: rel=duplicate` 指向私网与云元数据的场景。
- 产品文案只写「支持 Metalink 多源清单解析」，不写「完整 Metalink 生态」。
