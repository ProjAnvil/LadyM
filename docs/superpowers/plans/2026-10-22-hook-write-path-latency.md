# 计划：修 hook 写入路径的两处性能根因

> 日期：2026-10-22。分支：`feat/hook-write-path-latency`。
> 规格：`docs/superpowers/specs/2026-10-22-hook-write-path-latency-design.md`。

## 目标

降低 agent 每次工具调用等 hook 的耗时，并用 `make eval-hook` 证明。

根因与基线见规格 §2。要点复述：

- `NewStore` 的耗时几乎全是 `warmIndexFromBlobs`（100 行 0.85ms / 1k 2.5ms / 10k 21.9ms）。
- CJK 的瓶颈是**每进程一次的 ~520ms cedar trie 构建**，不是逐字切词（稳态仅 2.4µs/字）。切词缓存对它无效。

## 已核实的实现细节

| 事实 | 位置 |
|---|---|
| 预热入口 | `storage/store_sqlite.go:199`，`NewStore` 末行无条件调用 |
| 写路径碰索引 | `store_sqlite.go:291`（PutMemory）、`348`（DeleteMemory）、`378`（UpdateMemoryContent）、`800`（DeleteCodeArtifactsByFile） |
| 唯一读入口 | `store_sqlite.go:237` `VectorSearch` → `s.vectorIndex.Search` |
| 索引实现 | `*InMemoryVectorIndex`（`RebuildVectorIndex` 也只赋这一种），暴力余弦 |
| dim=256 | `config/config.go:275` |
| CJK 切词入口 | `storage/embeddings.go:36` `segmentCJK` → `cjkSegmenterFor`（首次触发 `loadCJKDict`） |
| 现有 dim 迁移机制 | `engine/engine.go:136` `enforceEmbeddingDim` → `RebuildVectorIndex` + `reembedAll` |

## 关键设计不变式

索引**始终允许写入**（不跳过），只把「从库里热化」推迟到首次 `VectorSearch`。理由：

- 冷态下写入索引不会造成部分状态——后续热化用 `Upsert` 全量覆盖，天然幂等。
- 冷态下跳过写入会让 `PutMemory` 的 dim 校验静默失效（`store_crud_test.go:55` 钉住了这个错误），这是可观测行为变化，不可接受。

不变式写进 `SQLiteStore` 的字段注释：

> 索引要么冷（未从库读），要么热（覆盖库中全部 embedding）。
> 写随时可发生；热化只在首次 `VectorSearch` 时发生一次，且用 Upsert 全量覆盖，因此幂等。

## 任务序列（TDD：每个任务先写红测试）

### M1 · 向量索引懒加载

**文件**：`storage/store_sqlite.go`，新增 `storage/store_lazyindex_test.go`。

- `SQLiteStore` 增加 `indexMu sync.RWMutex`、`indexWarm bool`。
- `NewStore` 删掉 `s.warmIndexFromBlobs()`。
- 新增 `func (s *SQLiteStore) ensureIndex()`：`indexMu.Lock()` → 若 `indexWarm` 直接返回 → 否则 `warmIndexFromBlobs()` 并置 `indexWarm = true`。
- `VectorSearch` 第一行调 `s.ensureIndex()`。
- `RebuildVectorIndex` 里 `indexWarm = false`。
- **红测先行**（`store_lazyindex_test.go`）：
  1. `TestNewStoreDoesNotWarmIndex`：建库写 3 行带向量 → 关闭 → 重开 → 断言内部索引 `Len() == 0`（现在会是 3，故红）。
  2. `TestVectorSearchWarmsIndexOnce`：重开后首次 `VectorSearch` 命中全部 3 行；再删一行（模拟索引落后），第二次 `VectorSearch` 仍返回 3 行——证明热化只做一次。
  3. `TestLazyIndexKeepsDimValidation`：冷态 `PutMemory` 传错 dim 必须照旧报错。
- **验证**：`go test ./storage/...`；顺带修 `TestWarmIndexFromBlobs`（改为先 `VectorSearch` 再断言）、`wal_concurrency_test.go:95`（改用 `reader.VectorSearch`）。

### M2 · 预热批量装载

**文件**：`storage/vector_index.go`、`storage/store_sqlite.go`、`storage/vector_index_test.go`。

- `InMemoryVectorIndex` 增加 `BulkLoad(ids []string, mat [][]float32)`：直接接管 `ids`/`mat`/`idToIdx`，不再逐行 `Upsert`（省掉每行一次 `slices.Clone`、一次 L2 归一化遍历、一次 map 写入）。
- `warmIndexFromBlobs` 改为：一次查询攒 `ids` 与共享 backing array 的 `mat`，末尾一次 `BulkLoad`；`vectorIndex` 非 `*InMemoryVectorIndex` 时回退逐行。
- **红测先行**：`TestBulkLoadMatchesUpsertPath` —— 同一批向量分别走逐行 `Upsert` 与 `BulkLoad`，断言两次 `Search` 的 id/相似度序列完全一致（含 tiebreak 的 id 升序）。
- **验证**：`go test ./storage/...`。

### M3 · CJK 切词模式开关

**文件**：`config/config.go`、`storage/embeddings.go`、`storage/embeddings_test.go`、`engine/engine.go`。

- `config` 增加 `EmbeddingCJKTokenizer string`，默认 `"dict"`；`ladym.toml` 与 TOML 解析照现有键的模式补（`asString`）。
- `segmentCJK`：`"chars"` 模式直接走现有逐字回退分支，不调 `cjkSegmenterFor`，因此**不触发词典加载**。
- `engine.enforceEmbeddingDim` 同处增加 tokenizer 版本校验：`meta["embedding_tokenizer"]` 与当前值不一致时，复用 `EmbeddingDimensionMismatch` 的既有分支（`allow_dim_change` → `RebuildVectorIndex` + `reembedAll`，否则报错）。
- **红测先行**：`TestCJKCharsModeSkipsDictionary` —— `chars` 模式下对含 Han 的文本 `Tokenize`，断言 `CJKDictStatusNow().Source` 仍为 `"none"`（词典没被加载）；再断言 `dict` 模式下它被加载。
- **验证**：`go test ./storage/... ./engine/... ./config/...`。

### M4 · 复测与结论

**文件**：`eval/README.md`。

- `make eval-hook`，取三档规模的 spawn p50 与 engine open，与基线逐格对照。
- 追加一次 `chars` 模式的对照跑（`LADYM_EVAL_CJK=chars` 或临时改 `ladym.toml`），记录 `claude/prompt-cjk` 的变化。
- 把对照表写进 `Headline findings`，并更正原文里「0.1ms/字」的错误表述。

### M5 · 收尾

- `go test ./...`（22 个包全绿）。
- `make lint-comments`。
- `go vet ./...`。
- 汇报，等用户决定合并 / 开 PR / 保留。

## 纪律

- 每个任务先写会失败的测试，看它红，再写最少代码。
- Go 注释只能英文（`scripts/check-comments.sh`）。
- 不改 hook 的 stdout / exit 语义。
