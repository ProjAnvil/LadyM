# 调研：ai-memory（akitaonrails）与 LadyM 对比

> 日期：2026-10-20。资料来源：ai-memory GitHub README（main 分支）与 LadyM ARCHITECTURE.md（本仓库）。二手转述均经 README 原文核对。

## 0. 一句话结论

两个项目解的是不同问题。ai-memory 解「agent 之间的交接与协作」，LadyM 解「单个 agent 的记忆认知过程」。前者是工程化的共享工作台，后者是认知架构加代码 RAG。交集只在本地优先、单二进制、MCP 接入、可选向量。

## 1. 基本面对照

| 维度 | ai-memory | LadyM |
|---|---|---|
| 语言/形态 | Rust 单二进制 | Go 单二进制（零 cgo） |
| 真相源 | git 版本化的 Markdown wiki；SQLite 只是派生索引，可随时重建 | 单个 SQLite 文件；向量存 BLOB |
| 定位 | 编码 agent 的跨工具长期记忆，核心场景是任务交接 | agent 记忆认知框架 + codebase RAG |
| LLM 依赖 | 默认路径零 LLM、零 API key；LLM 全部 opt-in | 读路径无 LLM；consolidate/proceduralize/L5/L6 需要 LLM，无 LLM 时跳过 |
| 检索 | FTS5 + 实体 + 图邻居 RRF 融合，向量是可选通道 | 向量近邻 + ACT-R 激活重排 + 两档检索 + 图扩展 |
| 代码索引 | 无 | tree-sitter 符号抽取 + code_refs 投影为 L4 边 |
| 成熟度 | 11.5k stars，819 forks，4115 commits，Nix/AUR/RPM/Docker/桌面应用生态 | 早期，21 包测试全绿，覆盖率 90%+ |

## 2. 方向差异（本质分歧）

### 2.1 一等公民不同

ai-memory 把「会话上下文」当一等公民。它的核心原语是 handoff：类型化协议，每个交接有 owner，恰好被 claim 一次，内容是「上次到哪、什么失败了、什么没解决」。跨 harness 时给下一个 agent 一份私有临时 Markdown 投影（`AI_MEMORY_PREVIOUS_SESSION_HISTORY`），子进程退出即删。

LadyM 把「记忆的整理过程」当一等公民。核心原语是 consolidate、proceduralize、激活排序。它建模的是海马体回放、程序性技能沉淀、心智模型抽取（L5/L6）。

一句话：ai-memory 做「团队 wiki + 交接协议」，LadyM 做「认知架构 + 代码 RAG」。

### 2.2 真相源哲学相反

ai-memory：Markdown 是真相，可 grep、可用 Obsidian 打开、可手改、可 code review。数据库只是索引，删了能重建。不硬删除，`restore-page` 沿 git 版本链恢复。

LadyM：SQLite 的 typed rows 是真相。符号、边、向量、元数据都在一张表体系里，机器结构优先。代价是对人类不透明，误删恢复只能靠备份。

这是「人类可读优先」对「机器结构优先」的分歧，两边各自贯彻到底。

### 2.3 认知建模深度不同

ai-memory 全部 README 只出现一个认知术语：episodic（冷的 episodic 笔记压缩为持久事实）。它的「分层」是工程意义上的 tier（衰减曲线按 tier 调），没有 semantic/procedural/working memory 体系。

LadyM 有 L0-L6 完整分层，对齐 ACT-R、HyMem、A-MEM、Zep、CoALA 七篇参考。激活函数带 recency、frequency、centrality、type prior。检索分两档，反思门控决定是否下探。

差别不是谁对谁错。ai-memory 选了实用主义：工程上够用就好。LadyM 选了认知科学路线：建模更深，但每个机制都要付出实现和验证成本。

### 2.4 零 LLM 的彻底程度不同

两边都说「无 LLM 可用」，但含义不同。

ai-memory 的默认路径覆盖捕获、检索、交接、老化，全部零 API key。它的记忆老化（衰减、访问加权保留、抽取式压缩、DBSCAN 去重、矛盾标记）默认零 LLM。

LadyM 的读路径确实无 LLM，向量默认 hashing 也可离线。但 LadyM 的核心增值——L2 整理、L3 沉淀、L5/L6——恰恰都在 LLM 路径上。无 LLM 时这些层空转，LadyM 退化为一个带激活排序的检索引擎。

## 3. LadyM 强在哪

1. **代码 RAG 融合**。tree-sitter 抽符号、code_refs 投影为 L4 边、文件级增量重索引、残留清理。代码符号和普通记忆同表、同激活函数、同检索管线。ai-memory 完全没有代码索引能力，README 只字未提。这是 LadyM 独有的整块能力。
2. **结构化操作历史**。`recall_ops` 用资源 × 动作复合键做 pivot 检索，动作用受控词表（31 词 + 别名），写时确定性建 `operates_on` 边，`ProjectedEdgeID` 保证幂等。ai-memory 的 entity-match + graph-neighbor 有相似味道，但没做到这个结构化程度。
3. **认知架构完整度**。ACT-R 激活、两档检索、L3 procedural playbook、temporal edge（true from t1 to t2）、supersedes 链。ai-memory 的矛盾处理只是 flag（默认关闭），没有时间有效性边。
4. **测试纪律**。21 包测试全绿，覆盖率 90%+，hermetic CI，跨语言 golden vectors 逐字节对齐。这在同类项目里少见。
5. **整合决策原语**。mem0 式 ADD/UPDATE/DELETE/NOOP 决策、`consolidated_at` 戳扫描 backlog。ai-memory 的压缩是抽取式的（保留文件路径、错误码、决策），不做语义合并决策。

## 4. 差距在哪

按影响从大到小排：

1. **静默捕获**。ai-memory 用 lifecycle hooks 自动记录 prompts、工具调用、会话边界，agent 零配合。捕获前在类型化隐私边界脱敏，客户端强制 `ignore_paths` 和 allowlist。LadyM 完全依赖 agent 主动调 `remember`/`record_event`。这是可用性质差：真实场景里 agent 不自觉记录，记忆库就是空的。LadyM 的整个认知管线建立在「有人喂料」的前提上。
2. **Handoff 协议**。claim-once 类型化交接、跨 harness 私有历史投影、agent 间定向收件箱。LadyM 无对应物。跨会话延续目前只能靠 recall 检索，没有结构化的「任务状态移交」。
3. **人类可读的真相源**。git 版本化 Markdown 可 grep 可手改可 review，删除可逆。LadyM 的 SQLite 对人类不透明。用户信任一个能亲眼检查的记忆系统，比信任一个黑盒容易。
4. **团队运维**。多用户认证、OIDC SSO、审计日志、per-person 归因、备份恢复、完整 uninstall。LadyM 有 console 和 Basic auth，团队级运维薄弱。
5. **生态与成熟度**。11.5k stars、4115 commits、发行包、桌面应用、社区教程。LadyM 是早期项目。这不是技术差距，但决定采用意愿。
6. **隐私工程**。类型化脱敏边界、DATA_HANDLING 文档、两条 opt-in 外发路径的白纸黑字。LadyM 有 AES 加密 secret 存储，但捕获侧无脱敏概念（因为不自动捕获，暂无此风险面）。

## 5. 可借鉴清单（差距转行动）

按性价比排：

1. **hooks 自动捕获通道**。给 L1 补一条不依赖 agent 自觉的写入路径。切入点：Claude Code hooks 或 MCP wrapper 层。捕获内容走 `record_event` 的既有槽位（resources/action_class）。这是最大缺口，补上后 LadyM 的认知管线才有持续料源。
2. **Handoff 原语**。claim-once 交接可以落成 L1 的一个 `type=handoff` 子类加状态列，或独立表。跨会话延续从「靠检索碰运气」升级为「结构化移交」。
3. **Markdown 投影层**。不必放弃 SQLite 主存储。从 memories/edges 派生一份 git 版本化的 Markdown 视图（每条记忆一页，边渲染为 wiki-link），定期导出。人类可读、可 review、可灾备，成本只是一个导出器。
4. **捕获脱敏边界**。做 hooks 通道时同步引入，别事后补。

## 6. 结论

方向不同，正面冲突小。ai-memory 的护城河在工程完成度和「agent 零配合」的捕获体验；LadyM 的护城河在认知架构深度和代码 RAG 融合，这两块 ai-memory 短期不会做。

LadyM 最该警惕的不是 ai-memory 的检索或老化（LadyM 建模更深），而是它的静默捕获——没有自动喂料，再好的整理和检索都是空转。建议把 hooks 捕获通道列为下一步优先级最高的能力缺口。
