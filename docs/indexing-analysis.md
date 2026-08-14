# LadyM 索引机制分析与改进方案

> 本文分析当前 LadyM 的代码索引 / 检索机制的**实际实现**（不是文档描述），对照业界同类产品，给出可落地的改进优先级。
> 结论基于对 `src/ladym/code/*`、`src/ladym/operations/recall.py`、`src/ladym/storage/store.py` 以及 Go 移植 `code/*` 的逐行阅读。

---

## 1. 当前机制到底做了什么

### 1.1 索引（`index_code` / `code/indexer.go`）

```
walk 仓库（rglob，排序）
  └─ 跳过目录：硬编码 _SKIP_DIRS（.git/.venv/node_modules/__pycache__/...）
  └─ 跳过：extra_ignore_globs 的“去尾 * 和 / 后对 basename 做子串匹配”（极弱，几乎不生效）
  └─ 逐文件：
       ├─ blake2b body hash → 与 index_state 比对 → 未变则跳过（增量）
       ├─ tree-sitter 解析（gotreesitter，纯 Go，206 语法）
       ├─ 提取符号：module / class / function / method
       │     → qualified_name、signature、docstring、行号、前 40 行 body
       ├─ 提取调用点 calls（只认 call/call_expression 节点）
       ├─ build_refs：只解析【同文件内】callee 命中已定义符号的调用边
       │     → code_refs(src_symbol, dst_symbol, ref_kind="calls")
       ├─ 每个符号 → L2 memory(type=code_symbol)，content = kind+签名+doc+body
       ├─ 每个文件 → L2 memory(type=code_file)，content = “文件名 + 各类符号计数 + 前 8 个符号名”（启发式字符串，非语义摘要）
       └─ index_state 记录 hash + 时间
```

### 1.2 检索（`recall` / `operations/recall.go`）

```
recall(query):
  tier-1: vector_search(query) + ACT-R 激活重排（跨 L1+L2+L3）
  tier-2（仅当 reflect 判定 tier-1 不足）:
       L4 图扩展（沿 edges 跳 2 层，含 supersedes 链）
       + “source backtrack”：对命中的 code_symbol，按其 metadata.file_path
         把对应 code_file 也拉进来（仅此而已，没有符号级回溯）
```

### 1.3 关键事实（与文档描述的差距）

| 文档说法 | 实际实现 |
|---|---|
| ARCHITECTURE §7 “extract cross-refs: **calls, imports, defines**” | 只提取 `calls`；imports / defines 从未提取 |
| README “callers/callees are queryable via the symbol graph” | `code_refs` 只有**同文件内**的 calls 边；跨文件符号引用**未解析** |
| README roadmap “GraphRAG-style **cross-file ref resolution**” | 尚未实现（正是缺口） |
| `Config.code_index.respect_gitignore` | **声明了但从没被读取**；walk 只用硬编码目录 + 弱 glob 匹配 |
| tier-2 “graph_expand / backtrack_to_source / fetch_symbol_context” | backtrack 只补 code_file 记忆，不做符号级 caller/callee/文件上下文 |

---

## 2. 对照外部产品

### 2.1 Aider Repo Map（最契合 LadyM 的“别再重读文件”使命）

[Aider 的仓库地图](https://deepwiki.com/Aider-AI/aider/4.1-repository-mapping-system) /
[Repository Map Pattern](https://github.com/agentpatterns-ai/website/blob/main/context-engineering/repository-map-pattern.md)：

- tree-sitter 构建**符号调用图**（节点=符号，边=调用/引用）。
- 对图跑 **PageRank**，选出“被引用最多 / 最重要的”符号与文件。
- 输出一张**紧凑的仓库地图**（top-N 符号 + 签名 + 所在文件），直接塞进 LLM 上下文。

> LadyM 已经建了一半这个图（code_symbols + code_refs），但**没有 PageRank，也没有“repo_map”这个对外操作**。这是最该补的一块。

### 2.2 代码知识图谱 + 混合检索（ctx-sys / reposkein / code-graph-rag）

[ctx-sys](https://github.com/david-franz/ctx-sys)、[reposkein](https://github.com/reposkein/reposkein)、
[code-graph-rag](https://github.com/vitali87/code-graph-rag) 这类新一代工具的共识：

- tree-sitter 符号 + **关系图**（call / import / reference / contains）。
- **混合检索**：keyword（SQLite FTS5 / BM25）+ vector + graph，用 **RRF（Reciprocal Rank Fusion）** 融合。
- local-first，经 MCP 暴露。

> LadyM 有 vector + L4 图，但**没有 keyword/BM25 通道，也没有 RRF 融合**。而它的默认 embedding 是 hashing（≈词重叠），本质上接近“稀疏词法信号”，却缺了真正的 BM25 精确匹配（代码标识符 `get_user_name` 这类精确命中，BM25 明显强于 hashing-embedding）。

### 2.3 SCIP（精确代码智能索引的工业标准）

[Sourcegraph SCIP](https://webflow.sourcegraph.com/blog/announcing-scip)：语言无关的**精确跨文件代码智能索引格式**（definition / reference / hover / implementation），替代 LSIF。

> LadyM 的 tree-sitter 提取是“启发式、同文件、只有 calls”；SCIP 是“精确、跨文件、完整引用”。对支持 SCIP 的语言，SCIP 是最省力拿到**精确跨文件引用**的路子；不支持的语言退回 tree-sitter tags。

### 2.4 混合检索是行业标配

[BM25 + vector + RRF](https://github.com/Brainwires/project-rag/blob/main/docs/adr/002-hybrid-search-with-rrf.md) 已经是 RAG 领域默认做法，尤其对代码（标识符精确匹配 + 语义同义匹配两者都要）。

---

## 3. 差距清单（按影响排序）

| # | 差距 | 影响 | 难度 |
|---|---|---|---|
| 1 | 没有 repo-map（PageRank 调用图摘要） | 无法回答“这个仓库长什么样 / 核心模块是哪些” | 低（图已有一半） |
| 2 | 跨文件引用未解析（只有同文件 calls，无 import/define） | “callers/callees” 检索形同虚设，L4 图对代码基本是孤岛 | 中 |
| 3 | 无 BM25/keyword 通道 + 无 RRF 融合 | 代码标识符精确检索弱；默认 hashing embedding 语义也弱 | 低–中 |
| 4 | `respect_gitignore` 未生效 + 弱 glob 匹配 | 会索引进 dist/build/生成物 | 低 |
| 5 | 默认 embedding 是 hashing | 零配置但语义弱；真实场景需 code-tuned 模型 | 配置/文档 |
| 6 | 检索单位是“整个符号”，无更细 chunk | 大函数/大类的召回粒度粗 | 中 |
| 7 | 向量索引是暴力扫描（Go 端内存；Py 端 sqlite-vec ANN） | >几万符号后检索变慢 | 中（可换 HNSW） |

---

## 4. 建议方案（分优先级）

### P0 —— 立刻做，杠杆最大且契合使命

1. **新增 `repo_map` 操作**（Aider 风格）
   - 建图：节点 = code_symbol，有向边 = code_refs（calls）。
   - 跑 PageRank，产出 top-N 符号 + 所属文件 + 签名；也按文件聚合“文件重要性”。
   - 暴露为：Engine 方法 + CLI `ladym repo-map` + MCP 工具 `repo_map`。
   - 收益：一次回答“核心入口/核心模块”，正是 LadyM 承诺却未交付的能力。

2. **跨文件引用解析 + import 图**
   - 索引改为两阶段：pass-1 收集全部文件的 qualified_name 与 import/export 表；pass-2 把 call site 关联到**跨文件**定义，写入 `code_refs`（扩展 ref_kind：calls / imports / defines / contains）。
   - 让 `recall` 的 tier-2 从“拉 code_file”升级为“沿调用图拉真 callers/callees + 所在文件”。
   - 收益：L4 关联层对代码从“孤岛”变“真图”，兑现 README 的路线图承诺。

3. **混合检索（BM25 + vector + RRF）**
   - 用 SQLite FTS5 建 code_symbol 的 token 索引（现代 Go SQLite 驱动已带 FTS5），与向量通道用 RRF 融合进 activation 的 `similarity` 项。
   - 收益：标识符精确检索大幅提升，成本低。

### P1 —— 质量与正确性

4. **修复 `respect_gitignore`**：接入真正的 .gitignore matcher（如 `go-git` 的 `gitignore` 包或自己实现），替换弱 glob 匹配。
5. **符号级 chunk**：对超长函数按语句块切分（每块携带所属符号的 qualified_name + 行号），作为 L2 的 code_symbol 子粒度。
6. **文档化并推荐 code-tuned embedding**：hashing 保留给 hermetic 测试；真实使用推荐 Ollama `qwen3-embedding`（`ladym.toml` 已配）或 OpenAI `text-embedding-3`。

### P2 —— 规模与精度

7. **ANN 索引（HNSW）**：符号 > 几万时把暴力余弦换成 HNSW（chromem-go 或纯 Go HNSW 实现）。
8. **可选 SCIP 后端**：对支持 SCIP 的语言用精确索引拿跨文件引用，其余退回 tree-sitter tags（gotreesitter 已为每门语法内置 `TagsQuery`，可替代现在手写的 `DefinitionKinds`/`CallKinds`）。

---

## 5. 一句话总结

LadyM 的**记忆/分层/激活这套“认知”架构是它独有的、也是它最强的部分**；但**代码索引这半套目前只做到“同文件符号 + 向量召回”，比 Aider 的 repo-map、Sourcegraph 的 SCIP、以及新一代 code-graph-rag 都浅**。差距不在“要不要树-sitter”（已用 gotreesitter 补齐且是纯 Go），而在三件事：**缺 repo-map、缺跨文件引用图、缺混合检索**。按 P0 三项补齐后，LadyM 的“代码记忆 + 通用记忆一体”的定位才能真正成立。
