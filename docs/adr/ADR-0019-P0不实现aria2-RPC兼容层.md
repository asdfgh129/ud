# ADR-0019 RPC 兼容分 L0/L1/L2 三档，P0/P1 停在 L0

- **状态**：Proposed
- **日期**：2026-11-08
- **决策者**：架构组 + 产品
- **关联**：`07` §5、`14` WBS 17 / OPEN-04、ADR-0003

## 背景

ADR-0003 决定「保留 aria2 的协议规范、删除 aria2 的程序」，因此自写 RPC facade 是**可选能力而非依赖**。但 facade 约 800–1500 行，且难点在语义映射（`getFiles` 多文件、`bitfield`、选项翻译表、未知选项处理），过早实现会拖慢 P0 关键路径；而 `Resource` 若在 P0 未预留多文件字段，L1 又会做得很浅（ADR-0013 已解决这一前置）。

## 决策

| 档位 | 内容 | 结论 |
|---|---|---|
| **L0** | 只做自有 REST / WS / CLI，零兼容代码 | ✅ **P0/P1 采用** |
| **L1** | 自写 facade：`POST /jsonrpc` + WS、JSON-RPC 2.0、`token:` 前缀、compat subset（约 25 方法 + 6 通知）、GID 映射、选项翻译表 | 按需启用；有用户明确要接 AriaNg 时再上 |
| **L2** | 捆绑 aria2c 二进制 | ❌ **已否决**（ADR-0003） |

具体规定：

1. P0/P1 **不写任何 facade 代码**，但 `07` §2 的任务详情字段必须为 L1 预留（`bitfield / numPieces / pieceLength / connections / followedBy / belongsTo`），`06` 的 `Resource` 原生多文件（ADR-0013）。
2. L1 只声明 **compat subset**（AriaNg + 浏览器扩展实际用到的约 25 个方法），**不承诺 100% 兼容**；长尾方法是无底洞。
3. 未知选项**接受但忽略**（返回错误会让 AriaNg 等前端直接挂掉）；`saveSession` / `getSessionInfo` 由 SQLite 顶掉，做成 no-op 返回 OK。
4. `bitfield` 由 `chunks` 表生成 hex；HTTP 任务返回空串以兼容 AriaNg；`uploaded/downloaded/left` 统一取 `tasks.bytes_*`。
5. L1 的验收标准是 **直接拿 AriaNg 当兼容性测试套件**（新建/暂停/恢复/删除、多文件选择、限速修改、全局统计、WS 实时刷新）。
6. 是否纳入范围由产品在 P2 结束前决定（OPEN-04）；不纳入则永久停在 L0。

## 备选方案

| 方案 | 结论 |
|---|---|
| P0 就实现完整 aria2 RPC | ❌ 拖慢关键路径，且当时还没有真实用户需求 |
| 整体引入 `go-aria2` / `goaria` 省掉 1000 行 | ❌ 自带下载引擎，请回第二个调度器（ADR-0003） |
| 承诺 100% aria2 兼容 | ❌ 长尾语义无法穷尽，且会约束自有模型演进 |
| 完全不预留字段，L1 时再改 | ❌ `getFiles` / `bitfield` 映射会触发改表返工 |

## 后果

**正面**：P0 关键路径最短；架构不为可能永远不做的兼容能力让步；L1 若启用，前置条件已全部具备，只需写映射层。

**负面**：早期无法直接接入 AriaNg 等生态前端；若将来启用 L1，需要补一次契约评审与 AriaNg 验收用例。

## 合规与验证

- `07` §2 任务详情字段清单在 P0 评审中确认，作为 L1 预留的验收项。
- 若启用 L1：`12` §6 以 AriaNg 为套件跑通全部交互；`07` §5.4 选项翻译表逐项有测试。
- 发布产物中始终不含 `aria2c` 二进制（SBOM 校验）。
