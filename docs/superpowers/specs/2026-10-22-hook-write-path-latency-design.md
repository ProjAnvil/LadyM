# 规格：修 hook 写入路径的两处性能根因

> 日期：2026-10-22。状态：待用户确认。
> 前置：eval/ 评测台已在 main（分支 feat/hook-write-path-eval），基线由 `make eval-hook` 产出。

## 1. 目标

把 agent 每次工具调用等 hook 的耗时降下来，并且**证明确实降了**。

两个已定位的根因，各自改一处：

1. `storage/store_sqlite.go:198-200` —— `NewStore` 无条件 `warmIndexFromBlobs()`，全表扫描 embedding 并逐行 `decodeVec` + `Upsert`。每个进程启动付一次，O(n)。
2. `storage/embeddings.go:36` `segmentCJK` —— CJK 切词开销。

非目标：

- 不改 hook 的纯观察者语义：stdout 恒空、exit 永不 2（`cli/hook.go:16-19`）。
- 不引入常驻进程，不改 hook 的 fork-per-event 架构。
- 不改 ASCII / 英文的 token 化行为。

## 2. 实测基线（本轮重测，非复述旧数）

### 2.1 引擎打开 = 预热向量索引

直接量 `NewStore`（dim=256，全部行带 embedding，页缓存已热）：

| 行数 | NewStore |
|---|---|
| 100 | 0.85ms |
| 1k | 2.52ms |
| 10k | 21.9ms |

与 eval 报的 `engine open` 0.6 / 2.1 / 15.2ms 同构。**`NewStore` 几乎全部是预热。**

### 2.2 CJK 的真正开销：一次性词典加载，不是逐字成本

原判断是「中文切词约 0.1ms/字，2000 rune 要 348ms」。**实测不成立**：

| 输入 | 首次调用 | 稳态（5 次均值） |
|---|---|---|
| 2000 个汉字 | **592ms** | 4.7ms |
| 2000 个 ASCII | — | 0.077ms |

拆开看这 592ms：

| 阶段 | 耗时 |
|---|---|
| 读 s_1.txt + t_1.txt（8.6MB） | 5.4ms |
| 按行 split（589k 行） | 28.4ms |
| **建 cedar trie + CalcToken（352k 词条）** | **~520ms** |

也就是说：**逐字成本只有 ~2.4µs/字，真正的开销是每进程一次的 ~520ms trie 构建。** eval 里 `claude/prompt-cjk` 的 spawn p50 = 432 / 441 / 481ms（三档规模），对应的是这个加载，不是切词本身。

**推论：切词缓存救不了这一处。** 缓存能省的是重复的切词计算（稳态 4.7ms 那部分），而 592ms 是进程第一次碰到 CJK 就必须付的固定成本，缓存命中与否都要付。

## 3. 设计决策

| 决策点 | 结论 | 依据 |
|---|---|---|
| 向量索引预热时机 | 改为懒加载：首次 `VectorSearch` 才预热 | hook 稳态下资源多已存在，解析走精确别名命中，不触发向量检索 |
| 冷态下的索引写入 | 索引未预热时，`Upsert`/`Delete` 直接跳过 | 冷态下后续预热必然从库里读全量，跳过不会丢数据；不跳过则索引永远处于「只有 1 条」的部分状态 |
| 预热加载方式 | 批量装载：一次查询直接填装矩阵，不走逐行 `Upsert` | 逐行路径每行 2 次分配 + 1 次归一化 + map 操作；批量路径每行 1 次解码 |
| CJK 切词 | **保留词典切词为默认**，新增 `chars` 模式可选 | 用户明确选择（2026-10-22） |
| CJK 切词缓存 | **不做** | 见 §2.2：瓶颈是一次性词典加载，缓存对它无效；且会引入一份全局可变状态 |

### 3.1 为什么 CJK 默认保留词典切词，但仍要提供 `chars`

用户选择了保留词典切词。这一轮把「hashing embedding 是否真的需要词典切词」这个问题也量了：

**消融实验**（40 条中文文档、40 条取自文档的短查询、dim=256、hashing unigram+bigram）：

| 切词方式 | MRR | top-1 | 最优间隔 |
|---|---|---|---|
| gse 词典切词 | 0.988 | 39/40 | 0.3196 |
| 逐字（每字成 token） | **1.000** | **40/40** | **0.3226** |

Python / 机器学习侧的通行做法也是两条路并存：

- **词级**：jieba 切词后喂给 `TfidfVectorizer(tokenizer=...)`，是大多数教程的默认写法。
- **字符级**：`TfidfVectorizer(analyzer='char', ngram_range=(1,3))` 或 `analyzer='char_wb', ngram_range=(2,4)`。字符 n-gram 天然处理未登录词，对短文本和专名更稳。
- 学界结论（Foo & Li 等，中文文本分类对比）：**高维特征空间下字符 bigram 普遍优于词**；词级优势只在低维或人工高质量分词时出现，且词级效果对分词器质量敏感。
- 现代神经网络侧：中文 BERT 直接按字切，不做分词。

结论：**hashing embedding 不需要词典切词**，字符 n-gram 是通行且实测不更差的选择。但尊重用户决定，默认仍是 `dict`。

### 3.2 CJK 模式切换的迁移

切词方式一变，已落库的中文向量就与新向量不同源，互相检索会退化。处理方式复用现成的维度不一致机制：

- `meta` 增加 `embedding_tokenizer` 键（值如 `dict-v1` / `chars-v1`）。
- `engine.enforceEmbeddingDim()` 同一处增加校验：已存值与当前值不同时，走和 `EmbeddingDimensionMismatch` 完全相同的分支——`allow_dim_change=true` 则 `RebuildVectorIndex` + `reembedAll`，否则报错提示。
- 不新增迁移命令，不新增机制。

## 4. 改动清单

### 4.1 向量索引懒加载（storage/store_sqlite.go）

```
SQLiteStore 增加：
  indexMu   sync.Mutex
  indexWarm bool

NewStore：删掉 s.warmIndexFromBlobs()

VectorSearch(q, k)：
  s.ensureIndex()          // 冷则预热，一次
  return s.vectorIndex.Search(q, k)

PutMemory / 批量 upsert / DeleteMemory 等写路径：
  if !s.indexWarm { 跳过 vectorIndex 操作 }

RebuildVectorIndex(newDim)：indexWarm = false
```

不变式（写进注释）：

> 索引只有两种状态：冷（空，未从库读）和热（与库中全部 embedding 一致）。
> 冷态下的写不进索引，因为热化时必然从库里读全量，会自然包含这些写。
> 任何读（`VectorSearch`）前必热化。

### 4.2 预热批量装载（storage/vector_index.go + store_sqlite.go）

`InMemoryVectorIndex` 增加：

```go
// BulkLoad replaces the index contents in one shot. Vectors must already be
// L2-normalised and of length dim, as Upsert guarantees on write.
func (ix *InMemoryVectorIndex) BulkLoad(ids []string, mat [][]float32)
```

`warmIndexFromBlobs` 改成：一次 `SELECT id, embedding`，把每行 blob 解到共享 backing array 的切片里，攒齐后 `BulkLoad`。当 `vectorIndex` 不是 `*InMemoryVectorIndex` 时回退到逐行 `Upsert`（当前代码里该字段只会被赋 `NewInMemoryVectorIndex`，回退只是保险）。

### 4.3 CJK 切词模式（storage/embeddings.go + config/config.go）

- `config` 增加 `EmbeddingCJKTokenizer string`，取值 `dict`（默认）/ `chars`。
- `segmentCJK`：新增参数或包级开关，`chars` 时直接走现有的逐字回退分支，不碰 `cjkSegmenterFor`，因此**不触发词典加载**。
- `engine.enforceEmbeddingDim` 增加 tokenizer 版本校验（§3.2）。

## 5. 验收

| 项 | 要求 |
|---|---|
| `make eval-hook` 复测 | 三档规模的 spawn p50 与 engine open 都要低于基线 |
| 基线 | 100 行 10.6 / 0.6ms；1k 行 13.2 / 2.1ms；10k 行 30.9 / 15.2ms |
| `claude/prompt-cjk` | 默认 `dict` 不变；附 `chars` 模式的对照数 |
| 主套件 | 22 个包全绿 |
| `make lint-comments` | 通过（Go 注释只能英文） |
| hook 语义 | stdout 恒空、exit 永不 2（已有测试钉住，不得改） |
| eval/README.md | Headline findings 写入改前改后对照 |

## 6. 测量纪律（沿用，不得重蹈）

- 不做冷热拆分：语料复制后立刻探测，文件在页缓存里，量不到「冷」。
- 采样前必须预热，否则首调虚高约 20 倍。
- 同一 payload 重复采样会让 p50 == p95 == p99，token 样本只取每次 trial 的首次 spawn。
- 机器绝对值不可跨机比较，只与同机基线比。
