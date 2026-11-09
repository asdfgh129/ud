# ADR-0003 完全移除 aria2 二进制，只保留其 wire protocol 规范

- **状态**：Accepted（2026-11-09 P0 里程碑评审批量批准）
- **日期**：2026-11-08（经三轮追问收敛）
- **决策者**：架构组
- **关联**：`04` §5、`07` §5、`10` §5、ADR-0002、ADR-0019

## 背景

初版规划把 `aria2c` 列为「可选 FTP/SFTP + Metalink 全量」sidecar。第一次追问指出：只要 aria2 承担任何实际传输，就已引入第二调度器（ADR-0002）。修订版把它降级为「解析器 / 接口兼容层」。第二次追问指出该定位仍有歧义且不自洽：降级后残留的两块工作都可以自写，没有必要保留程序。

关键澄清：**「接口兼容层」不是包一层去调用 aria2，而是用自有 Task API 去实现 aria2 的 wire protocol**，与 aria2 二进制零关系。准确表述是「保留协议规范，删除程序」。

## 决策

1. **从依赖清单、安装包、运行时中彻底删除 `aria2c`**，包括「可选兜底后端」定位。
2. 残留工作全部自写且纯 Go：
   - Metalink 解析：RFC 5854 XML + RFC 6249 HTTP 头，约 300–500 行，或参考 `dpb587/metalink`（MIT）；
   - aria2 RPC facade：HTTP POST `/jsonrpc` + WebSocket、JSON-RPC 2.0、`token:` 前缀、compat subset，约 800–1500 行。
3. **撤回最初推荐 `libmetalink`**：C 库引入 cgo，破坏纯 Go 单二进制与 iOS/WASM 交叉编译。
4. FTP / SFTP 进核心：`jlaffaye/ftp`（ISC）+ `pkg/sftp` + `golang.org/x/crypto/ssh`，全纯 Go。
5. 现成 Go「aria2 兼容实现」（`chenjia404/go-aria2`、`wchen99998/goaria`）**只可复用方法清单、16 位 hex GID 规则、`StatusInfo`/`FileInfo`/`PeerInfo` 结构体、`system.multicall` 语义**，不得整体引入——它们自带下载引擎，等于请回第二个调度器。`zyxar/argo` 等客户端库仅作类型参考。
6. `Resource.engine` 保留 `go | byo_adapter` 字段用于**任务级**路由；BYO 适配器属于可选集成，不进主包、不作为依赖。

## 备选方案

| 方案 | 结论 |
|---|---|
| 保留 aria2 作 FTP/Metalink 兜底 | ❌ 传输路径出现第二调度器；GPL-2.0；6800 端口与进程监督；移动端不可达 |
| 用 `libmetalink` 解析 | ❌ C 库需 cgo，破坏单二进制 |
| 整体引入 go-aria2 / goaria | ❌ 自带引擎，等于回到方案 C |
| 采用 `aria2-next` v3.0.9（libtorrent 2.1、补 BT v2/uTP/ED2K/HLS） | 观察项，不采纳：~700 star、仍 GPL-2.0、不导入旧 `.aria2` 状态，不宜作为架构基石 |

## 后果

**正面**：纯 Go 单二进制、**零 GPL 传输依赖**、iOS/Android 可跑、单调度器、无 6800 端口与进程监督、无 `.aria2` 状态双源；sidecar 清单从 6 减到 5，传输能力 0 sidecar。

**失去（仅两项，可接受）**：
1. 同文件多协议混合拉流 + Metalink 分块校验的现成实现（可用自有分片引擎 + hash 复现）；
2. 已部署 aria2 的 NAS 用户复用旧配置（做成 BYO 可选插件）。

**难点不在 RPC，在语义映射**：`getFiles` 要求 `Resource` 原生多文件；`tellStatus` 的 `bitfield/numPieces/pieceLength/connections/followedBy/belongsTo` 需从自有分片表与 anacrolix peer 信息映射（HTTP 任务 bitfield 返回空串）；选项翻译表；未知选项**接受但忽略**（返回错误会让前端直接挂）；`addTorrent` 用 `anacrolix/metainfo` 解 base64；`saveSession`/`getSessionInfo` 由 SQLite 顶掉可 no-op。

## 合规与验证

- 发布产物中不存在 `aria2c` 二进制，SBOM 无 GPL-2.0 传输依赖。
- 若启用 L1 facade：以 **AriaNg 作为验收测试套件**，只声明 compat subset（约 25 方法），不承诺 100% 兼容。
- Metalink 属不可信输入：SSRF 白名单、镜像数上限、XML 大小上限、整文件与分块哈希校验（`11` §2）。
