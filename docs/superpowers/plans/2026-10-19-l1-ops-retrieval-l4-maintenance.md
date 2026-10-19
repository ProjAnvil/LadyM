# L1 操作历史检索与 L4 自动维护 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 spec `docs/superpowers/specs/2026-10-19-l1-ops-retrieval-l4-maintenance-design.md` 的全部内容:资源×动作复合键的 L1 操作历史检索(recall_ops)、L4 写时确定性建边与 worker 维护(MaintainL4)、正确性修复(NeighborCounts join、悬挂边 GC)、重索引残留清理。

**Tech Stack:** Go 1.x(zero cgo)、modernc.org/sqlite、pytest 不适用(纯 `go test`)、HashingEmbedding(测试,确定性,零网络)。

**验证基线:** 每个 GREEN 步骤后跑 `go test ./...`。开始前先跑一次确认全绿。

## Global Constraints(每个任务都受约束)

- **读路径零 LLM**:recall_ops 管线任何分支不得调用 LLMProvider。
- **旧调用零行为变化**:`RecordEvent` 不带资源时输出与现状逐字节一致;`recall` 管线不动。
- **两 store 同步**:Store 接口每个新方法在 SQLite 与 Postgres 各实现一份,SQL 风格对齐现有代码。
- **幂等**:机器投影边一律用 `schema.ProjectedEdgeID` 生成确定性 ID(任务 4),PutEdge 按 ID upsert(store_sqlite.go:511 已确认 ON CONFLICT)。
- **每任务一 commit**,前缀:`fix(storage)`/`feat(ops)`/`feat(engine)`/`feat(mcp)`/`feat(cli)`/`feat(sdk)`/`docs:`。
- **TDD 强制**:每个任务先写失败测试、跑红、再实现、跑绿。测试文件放同包 `*_test.go`,命名 `TestXxx`。
- Postgres 专属测试沿用现有 PG 测试基建(无 DSN 时 skip,参考 `operations/recall_parity_pg_test.go` 的 gating 方式)。
- 配置新段落仿照 `AttentionConfig` 的加载模式(config/config.go:138-141 + loader)。

## File Structure(全景)

- `schema/schema.go` — TypeResource 常量;ProjectedEdgeID helper
- `storage/store.go`(Store 接口,store.go:10) — SweepDanglingEdges、DeleteCodeArtifactsByFile 接口方法
- `storage/store_sqlite.go` / `storage/store_postgres.go` — 上述两方法实现;NeighborCounts join 修正
- `operations/actions.go`(新) — 动作词表 + NormalizeAction
- `operations/resource.go`(新) — ResolveResource
- `operations/maintain.go`(新) — MaintainL4(GC/回填/语义建链/别名合并)
- `operations/system2.go` — 周期尾部接 MaintainL4;System2Report.Maintain
- `operations/consolidate.go` — produced_from/operates_on 投影;删死代码行 81
- `engine/scope.go` / `engine/engine.go` — RecordOperation、RecallOperations
- `mcp/server.go` — recall_ops 工具;record_event 扩展参数
- `cli/` — recall-ops 命令(新文件)
- `ladym/ladym.go` — SDK 便捷函数
- `config/config.go` — ResourceConfig、MaintainConfig
- `code/indexer.go` — 重索引清理钩子、code_refs 投影
- `ARCHITECTURE.md` / `README.md` / `README.zh-CN.md` — 文档修正

---

# M0 正确性修复

### Task 1: NeighborCounts 端点 join(修 P0-1)

**Files:**
- Modify: `storage/store_sqlite.go`(NeighborCounts 实现,搜 `func (s *SQLiteStore) NeighborCounts`)
- Modify: `storage/store_postgres.go:603`(NeighborCounts)
- Test: `storage/neighbor_counts_join_test.go`(新)

**Steps:**

- [ ] **RED:** 新建 `storage/neighbor_counts_join_test.go`,测试:
  1. 建临时 SQLite store(沿用现有测试 helper 模式),put 两条 memory A、B,`PutEdge(NewEdge(A,B,"related_to"))`。
  2. `DeleteMemory(A)`。
  3. 断言 `NeighborCounts()` 中 B 的计数为 0、A 不在 map 中(悬挂边不计入)。
  4. 再建 C 并连 B-C 有效边,断言 B 计数为 1(有效边正常计入)。
- [ ] 跑 `go test ./storage/ -run TestNeighborCounts -v` 确认 FAIL。
- [ ] **GREEN:** 两 store 的 NeighborCounts SQL 加端点存在性过滤:
  - SQLite(Postgres 同义改写):`SELECT src_id AS id FROM edges e WHERE e.valid_to IS NULL AND EXISTS (SELECT 1 FROM memories m WHERE m.id = e.src_id) UNION ALL SELECT dst_id ... (同 EXISTS dst_id)`。
- [ ] 跑测试绿;`go test ./...` 全绿;commit `fix(storage): neighbor counts exclude dangling edges`。

### Task 2: SweepDanglingEdges store 方法(修 P0-2)

**Files:**
- Modify: `storage/` Store 接口定义文件(加方法签名)
- Modify: `storage/store_sqlite.go`、`storage/store_postgres.go`
- Test: `storage/sweep_dangling_test.go`(新)

**Steps:**

- [ ] **RED:** 测试:
  1. put A、B、C 三条 memory;连 A-B(有效)、A-C(有效);`DeleteMemory(C)`(制造悬挂边 A-C)。
  2. 断言 `SweepDanglingEdges()` 返回 (1, nil)。
  3. 断言 `Neighbors(A, "")` 只剩 B 那条;A-B 边仍 valid_to==nil。
  4. 二次调用返回 (0, nil)。
- [ ] 跑红。
- [ ] **GREEN:** 接口加 `SweepDanglingEdges() (int, error)`。实现 SQL:
  `DELETE FROM edges WHERE valid_to IS NULL AND (src_id NOT IN (SELECT id FROM memories) OR dst_id NOT IN (SELECT id FROM memories))`,用 `RowsAffected` 计数。SQLite 记得同步 `s.vectorIndex` 无需处理(边不进向量索引)。
- [ ] 绿 + 全量绿 + commit `fix(storage): sweep dangling edges`。

### Task 3: GC 接入 System 2 周期(MaintainL4 骨架)

**Files:**
- Modify: `operations/system2.go`
- Modify: `engine/engine.go`(Engine 补一个委托方法)
- Test: `operations/maintain_cycle_test.go`(新)

**Steps:**

- [ ] **RED:** 测试:构造含悬挂边的 store,用一个最小 runner(参考 `operations/pipeline_test.go:1145` 的 `cycleRunner` 模式,扩展 `SweepDanglingEdges` 委托到真 store),跑 `RunSystem2Cycle`,断言悬挂边被清、`report.Maintain` 非 nil。
- [ ] 跑红(编译失败也算红:字段不存在)。
- [ ] **GREEN:**
  1. `System2Report` 加 `Maintain any`。
  2. `System2Runner` 接口加 `SweepDanglingEdges() (int, error)`;`Engine` 实现:`func (e *Engine) SweepDanglingEdges() (int, error) { return e.Store.SweepDanglingEdges() }`。
  3. `RunSystem2Cycle` 在 Decay 之后调用,结果写 `report.Maintain`(map[string]any{"swept": n})。
  4. 接口扩展后,所有实现 `System2Runner` 的测试 fake 同步补方法
     (`grep -rn "System2Runner" --include="*_test.go" .` 找全,如 operations/parity_test.go 的 fakeSystem2Runner、pipeline_test.go 的 cycleRunner)。
- [ ] 绿 + 全量(注意:engine 的现有 fake runner 若实现 System2Runner 接口需同步补方法)+ commit `feat(ops): system2 cycle sweeps dangling edges`。

---

# M1 写路径

### Task 4: schema 基础件(TypeResource + ProjectedEdgeID)

**Files:**
- Modify: `schema/schema.go`
- Test: `schema/schema_test.go`(追加)

**Steps:**

- [ ] **RED:** 测试:`ProjectedEdgeID("a","operates_on","b")` 两次调用相等;前缀 `"proj:"`;长度 5+16;不同任一参数则不同。`TypeResource == "resource"`。
- [ ] 跑红。
- [ ] **GREEN:**
  1. MemoryType 常量块(schema.go:59-69)加 `TypeResource MemoryType = "resource"`。
  2. 加函数:
     ```go
     // ProjectedEdgeID returns the deterministic id for machine-projected
     // edges so re-running a projection upserts instead of duplicating.
     func ProjectedEdgeID(src, relation, dst string) string {
         h := sha256.Sum256([]byte(src + "|" + relation + "|" + dst))
         return "proj:" + hex.EncodeToString(h[:])[:16]
     }
     ```
     (import crypto/sha256、encoding/hex)
- [ ] 绿 + commit `feat(schema): resource type + projected edge id`。

### Task 5: ResourceConfig 配置段

**Files:**
- Modify: `config/config.go`
- Test: `config/config_test.go`(追加)

**Steps:**

- [ ] **RED:** 测试:`config.Default()` 的 `Resource.ResolveThreshold==0.85`、`ReadThreshold==0.75`;TOML 文本 `[resource]\nresolve_threshold = 0.9\nactions = ["frobnicate"]` 加载后生效(仿照现有 config TOML 测试的加载方式)。
- [ ] 跑红。
- [ ] **GREEN:** 加 `type ResourceConfig struct { ResolveThreshold float64; ReadThreshold float64; Actions []string; ActionAliases map[string]string }`,`Config` 加 `Resource ResourceConfig` 字段,Default() 填默认,TOML loader 按 AttentionConfig 模式挂 `[resource]` 段(applyToml 逐段 switch,config.go:491 起)。
- [ ] 绿 + commit `feat(config): resource section`。

### Task 6: 动作词表 + NormalizeAction

**Files:**
- Create: `operations/actions.go`
- Test: `operations/actions_test.go`(新)

**Steps:**

- [ ] **RED:** 测试:命中规范词(`"migrate"`→`"migrate"`);别名(`"RUN"`→`"execute"`,`"rm"`→`"delete"`);未命中(`"frobnicate"`→`""`);config 追加词生效(`cfg.Resource.Actions=["frobnicate"]` 后命中);config 追加别名生效。
- [ ] 跑红。
- [ ] **GREEN:** `operations/actions.go`:
  1. `defaultActions` 30 词(照 spec §5.5 分组常量);`defaultAliases` 映射(run/launch→execute, rm→delete, grep→search, fetch/get→read, modify→update, list/ls→query, add→create)。
  2. `func NormalizeAction(raw string, cfg *config.Config) string`:lower+trim;先查 cfg.Resource.ActionAliases 合并 defaultAliases,再查 cfg.Resource.Actions 合并 defaultActions;未命中返回 `""`。
- [ ] 绿 + commit `feat(ops): action vocabulary + normalize`。

### Task 7: ResolveResource(资源解析与注册表)

**Files:**
- Create: `operations/resource.go`
- Test: `operations/resource_test.go`(新)

**Steps:**

- [ ] **RED:** 测试(hashing embedding,确定性):
  1. 首次 `ResolveResource(store, emb, cfg, "t", "ladym.db")` → 新建(L2/resource),`created=true`,aliases 含原名。
  2. 再次同名 → 命中别名,`created=false`,返回同 ID。
  3. 内容差异大的名("totally-unrelated-thing")→ 新建。
  4. 向量分支:新建资源 "postgres primary db",再查 "postgres primary database"(hashing embedding 下需构造可过 0.75/0.85 阈值的样本;若 hashing 向量下不可达阈值,则直接调低 cfg.Resource.ResolveThreshold 到可命中值来测向量分支逻辑本身)。
  5. 向量命中后 aliases 增长(原查询名被追加)。
- [ ] 跑红。
- [ ] **GREEN:** `operations/resource.go`:
  ```go
  func ResolveResource(store storage.Store, embedder storage.EmbeddingProvider,
      cfg *config.Config, ws, name string) (*schema.Memory, bool, error)
  ```
  1. `IterMemories(ws, string(LayerSemantic), string(TypeResource))`,精确匹配 content 或 metadata.aliases(大小写不敏感)→ 返回 (m, false, nil)。
  2. `emb := embedder.Embed(name)`;`store.VectorSearch(emb, 50)` → 逐个 `GetMemory` 过滤 `type==resource && workspace==ws`;取 sim 最高且 `>= cfg.Resource.ResolveThreshold` → 追加别名(aliases 去重后 `vec, _ := embedder.Embed(m.Content); store.PutMemory(m, vec)` 重写)→ 返回 (m, false, nil)。
  3. 否则新建:NewMemory(L2, TypeResource),Content=name,Summary=name,metadata.aliases=[name],Embed 后 PutMemory → (m, true, nil)。
- [ ] 绿 + commit `feat(ops): resource registry resolve`。

### Task 8: Scope.RecordOperation + operates_on 建边

**Files:**
- Modify: `engine/scope.go`、`engine/engine.go`
- Test: `engine/record_operation_test.go`(新)

**Steps:**

- [ ] **RED:** 测试:
  1. `Scope.RecordOperation(OperationEvent{Agent:"a", Action:"migrate schema", Resources:[]string{"ladym.db"}, Outcome:"success"})` → episode 存在,`metadata.action_class=="migrate"`、`action_raw=="migrate schema"`;
  2. `operates_on` 边存在且 ID==`schema.ProjectedEdgeID(epID,"operates_on",resID)`;
  3. 资源实体可被第二次 RecordOperation 复用(同 ID);
  4. 空 Resources → 无 operates_on 边;
  5. `RecordEvent` 旧行为:content 格式 `agent=a | action=... | observation=... | outcome=...` 与现状一致(对照既有 engine 测试断言)。
- [ ] 跑红。
- [ ] **GREEN:**
  1. `engine/scope.go` 加:
     ```go
     type OperationEvent struct {
         Agent, Action, ActionClass string
         Resources                  []string
         Observation, Outcome       string
         Tags                       []string
         Metadata                   map[string]any
     }
     func (s *Scope) RecordOperation(ev OperationEvent) (*schema.Memory, error)
     ```
     流程:action_class = 显式值或 `operations.NormalizeAction(ev.Action, cfg)`;逐个 `operations.ResolveResource`;调现有 `s.Episodic.Record(...)`(槽位写进 metadata);逐资源 `PutEdge`(NewEdge + ProjectedEdgeID 覆盖 ID,weight 1,ValidFrom=now)。
  2. `RecordEvent` 改为构造 OperationEvent(空 Resources)转调。
  3. `engine/engine.go` 加顶层 `Engine.RecordOperation` 委托(镜像 RecordEvent 的模式)。
- [ ] 绿 + 全量(重点:engine 既有 RecordEvent 测试不碎)+ commit `feat(engine): record operation with resource slots`。

### Task 9: consolidate 溯源投影(produced_from + operates_on)

**Files:**
- Modify: `operations/consolidate.go`
- Test: `operations/consolidate_provenance_test.go`(新)

**Steps:**

- [ ] **RED:** 测试(离线路径,llmClassify=nil):
  1. 录 episode(带 operates_on 资源边)→ consolidate 走 ADD → 新 fact 有 `produced_from` 边(fact→episode)与 `operates_on` 边(fact→resource,ProjectedEdgeID)。
  2. 再录相似 episode(content 相似度超阈值)→ consolidate 走 UPDATE → 新 fact 同样有两条边,且旧 fact 有 supersedes 指向新 fact(既有行为,回归保护)。
  3. 重复同 content episode → NOOP(既有回归,不重放)。
- [ ] 跑红。
- [ ] **GREEN:** consolidate.go:
  1. 抽 helper `projectProvenance(store, factID, epID string) error`:建 produced_from 边;`Neighbors(epID, "operates_on")` 取 dst,逐个建 (factID, "operates_on", dst) 投影边。
  2. ADD 分支(PutFact 后)与 UPDATE 分支(PutMemory 后)各调一次。
  3. 删除 offlineClassify 行 81-83 的 `superseded_by == candidateHash` 死分支,留一行注释:`// dead Python-parity check removed: superseded_by holds a memory id, never a content hash; NOOP replay protection is the content_hash equality above`。
- [ ] 绿 + commit `feat(ops): consolidate provenance projection`。

### Task 10: DeleteCodeArtifactsByFile(重索引残留清理)

**Files:**
- Modify: `storage/` Store 接口 + `store_sqlite.go` + `store_postgres.go`
- Test: `storage/delete_code_artifacts_test.go`(新)

**Steps:**

- [ ] **RED:** 测试:
  1. 造数据:两个符号 memory(code_symbol, source=rel)+ code_symbols 投影行 + code_refs 行(src_symbol=符号1)+ 无关文件的符号。
  2. `DeleteCodeArtifactsByFile(ws, rel)` 后:该文件符号 memory 全删、code_symbols 行全删、code_refs 相关行删、无关文件符号不动。
  3. SQLite:`VectorSearch` 不再返回已删符号 id(向量索引同步)。
- [ ] 跑红。
- [ ] **GREEN:** 接口加 `DeleteCodeArtifactsByFile(workspace, filePath string) error`,实现按 spec §6.4 五步(先 code_refs 子查询删除,再取 memory_id 列表,删 code_symbols,删 memories(type=code_symbol AND source=filePath AND workspace),SQLite 对列表逐一 `s.vectorIndex.Delete(id)`)。全部同事务(`s.db.Exec` 顺序执行)。
- [ ] 绿 + commit `fix(storage): delete code artifacts by file on reindex`。

### Task 11: indexer 清理钩子 + code_refs→L4 投影

**Files:**
- Modify: `code/indexer.go`
- Test: `code/indexer_projection_test.go`(新)

**Steps:**

- [ ] **RED:** 测试(临时目录建两文件:A 定义函数,A2 调用它;索引 → 断言 calls 边存在、ID 为 ProjectedEdgeID):
  1. 修改 A 删除该函数,重索引 → 旧符号 memory/code_symbols/code_refs 全清,旧 calls 边因端点消失成为悬挂边(**本任务断言**:重投影后新边不重复;悬挂旧边留给 GC,不断言)。
  2. 不改文件重索引 → 边数不变(幂等)。
  3. IndexReport(定义于 code/indexer.go:23)加 `ProjectedEdges int` 字段。
- [ ] 跑红。
- [ ] **GREEN:**
  1. indexer 主循环:body_hash 变化分支里,在写新符号前调 `store.DeleteCodeArtifactsByFile(ws, rel)`。
  2. 索引收尾:遍历本批插入的 code_refs(照 store 现有 refs 写入方法的可见范围;若 store 无列举接口,加 `ListRefsByFile(ws, filePath)` 或在写入时同步投影,选最小改动),双向查 code_symbols 换 memory_id,两端存在才 `PutEdge(NewEdge(src,kind,dst) + ProjectedEdgeID)`,kind 映射 calls/imports。
  3. 投影失败记 report 不中断。
- [ ] 绿 + 全量 + commit `feat(code): reindex cleanup + code_refs l4 projection`。

---

# M2 读路径

### Task 12: Scope.RecallOperations 三段式

**Files:**
- Modify: `engine/scope.go`(或新 `engine/ops_recall.go`,与 scope.go 现有组织方式一致)
- Test: `engine/recall_operations_test.go`(新)

**Steps:**

- [ ] **RED:** 测试:
  1. 录 3 条操作(2 条 migrate ladym.db、1 条 deploy nginx),`RecallOperations{Resource:"ladym.db", Action:"migrate"}` → 恰好 2 条,含槽位与 outcome,按激活排序。
  2. Action 空 → 3 条中 ladym.db 的全部(2 条)。
  3. 别名解析:用别名查回同一资源。
  4. 时间窗:Since 排除旧记录。
  5. Outcome="fail" 过滤。
  6. 资源未命中("nonexistent-xyz")→ Fallback=true,items 来自普通 Recall,不 panic。
  7. 冷启动(无任何资源实体)→ Fallback=true。
  8. 读路径零 LLM:全程 llm==nil 的引擎上行为正确(天然满足,断言即可)。
- [ ] 跑红。
- [ ] **GREEN:**
  ```go
  type OperationsQuery struct {
      Resource  string
      Action    string
      Since, Until float64
      Outcome   string
      Limit     int
  }
  type OperationsResponse struct {
      Resource *schema.Memory
      Items    []*schema.RecallResult
      Fallback bool
  }
  func (s *Scope) RecallOperations(q OperationsQuery) (*OperationsResponse, error)
  ```
  Stage1:别名精确 → `VectorSearch(top50)` Go 侧过滤 resource,阈值 `cfg.Resource.ReadThreshold`;未命中→ 回退走 `s.eng.Recall(q.Resource+" "+q.Action, ...)`(Engine.Recall,engine.go:343;Scope 无 Recall 方法),结果转 RecallResult,Fallback=true。
  Stage2:`store.Neighbors(resID, "operates_on")`,取 src 端(注意 Neighbors 双向返回,`e.DstID==resID` 时取 SrcID);`GetMemory` 过滤 workspace/非 retired/action_class==NormalizeAction(q.Action)(q.Action 空则跳过)/时间窗/outcome。
  Stage3:`operations.ActivationScore`(六参,activation.go:54),queryTypes 传 nil;
  sim 无向量值,取平面代理:直接邻居 0.5、二跳 0.5/2(注意:tier2Expand 实际公式是
  `anchor.Score*0.5/depth`,recall.go:268,锚定 tier-1 分数;pivot 场景无锚分数,
  故取平面代理值,这是有意偏离);截断 Limit(默认 10)。
- [ ] 绿 + commit `feat(engine): recall_operations pivot query`。

### Task 13: MCP 工具(recall_ops + record_event 扩展)

**Files:**
- Modify: `mcp/server.go`
- Test: `mcp/server_test.go`(追加,沿现有 handler 测试模式)

**Steps:**

- [ ] **RED:** 测试(JSON-RPC 调用现有测试基建):
  1. `tools/list` 含 `recall_ops`,schema 有 resource/action/since/until/outcome/limit。
  2. `record_event` params 增加 `resources`(array of string)与 `action_class`(string),可选;带资源调用后 `recall_ops` 查得。
  3. 不带新字段的旧式 record_event 调用仍成功(向后兼容)。
  4. `recall_ops` 资源未命中返回 fallback 标记,非错误。
- [ ] 跑红。
- [ ] **GREEN:** server.go 按现有 9 工具的注册模式加第 10 个工具 `recall_ops`;record_event handler 解析可选字段透传 Engine.RecordOperation。
- [ ] 绿 + commit `feat(mcp): recall_ops tool + structured record_event`。

### Task 14: CLI recall-ops

**Files:**
- Create: `cli/recall_ops.go`
- Modify: CLI 根命令注册文件(参照 `cli/worker.go` 等的注册位置)
- Test: `cli/recall_ops_test.go`(新,沿现有 CLI 测试模式;若 CLI 无测试基建则用 engine 层 e2e 覆盖并在任务中说明)

**Steps:**

- [ ] **RED:** 表驱动测试(cli 有现成测试基建:cli_test.go、commands_test.go、cmdtest_test.go,沿其模式):
  1. `recall-ops --resource "ladym.db" --action migrate --outcome fail --limit 5` 输出 JSON 且字段正确。
  2. 必填参数缺失 → 报错退出码非 0。
- [ ] **GREEN:** cobra 命令 `recall-ops`,flags:--resource(必填)、--action、--since、--until、--outcome、--limit;调 `Engine.RecallOperations`;输出 JSON(沿现有命令的输出风格)。
- [ ] 绿 + commit `feat(cli): recall-ops command`。

### Task 15: Go SDK 便捷函数

**Files:**
- Modify: `ladym/ladym.go`
- Test: `ladym/ladym_test.go`(追加)

**Steps:**

- [ ] **RED:** 测试:`ladym.RecordOperation(...)` 一次性写入;`ladym.RecallOperations(...)` 查回(参照现有 `ladym.Recall`/`ladym.Remember` 测试模式)。
- [ ] **GREEN:** 按 `ladym.Recall` 的 one-shot helper 模式加两函数。
- [ ] 绿 + commit `feat(sdk): record/recall operations helpers`。

---

# M3 worker 维护

### Task 16: MaintainConfig 配置段

**Files:**
- Modify: `config/config.go`
- Test: `config/config_test.go`(追加)

**Steps:**

- [ ] **RED:** 测试:Default() 的 Maintain 字段 = spec §10 默认值(enabled true, backfill_batch 50, linking_enabled true, linking_budget 20, linking_similarity 0.60, alias_merge_similarity 0.92);TOML `[maintain]` 段覆盖生效。
- [ ] **GREEN:** `MaintainConfig` struct + Config 字段 + 默认 + loader(仿 AttentionConfig)。
- [ ] 绿 + commit `feat(config): maintain section`。

### Task 17: MaintainL4 — GC + 槽位回填

**Files:**
- Create: `operations/maintain.go`
- Modify: `operations/system2.go`(Task 3 骨架替换为完整实现)
- Test: `operations/maintain_test.go`(新)

**Steps:**

- [ ] **RED:** 测试:
  1. GC 子步骤即 Task 3 行为(保留断言)。
  2. 回填-启发式:录 episode(metadata.action="run tests",无 action_class)→ `MaintainL4(store, emb, cfg, ws, nil)` → action_class=="test"(run→execute?注意:"run tests" 非 run 精确匹配,启发式只做词表**精确**匹配,断言用 metadata.action="run" 场景 → "execute")。
  3. 回填-LLM:fake LLMProvider(仿 l5/l6 测试的 fake 模式)返回 `{resources:["ladym.db"],action:"migrate"}` → episode 获 operates_on 边与 action_class。
  4. llm==nil → LLM 回填跳过,无 panic;已回填的不重复处理。
  5. 批量上限 backfill_batch 生效(造 60 条,一轮只处理 50)。
- [ ] 跑红。
- [ ] **GREEN:** `operations/maintain.go`:
  ```go
  type MaintainReport struct { Swept int; Backfilled int; Linked int; Merged int; Skipped string }
  func MaintainL4(store, embedder, cfg, ws, llm) (*MaintainReport, error)
  ```
  前置:LLM 子步骤 op 注册——`l4_backfill`、`l4_link`、`l4_alias_merge` 加入
  `providers/agents.go` 的 `NAMED_OPS` 与 AgentRegistry(仿 `l5_mental_model` 现有
  注册),支持 [agents] 覆盖;无 LLM 时 MaintainL4 接 nil 直接跳过三步。
  顺序:
  ① `store.SweepDanglingEdges()`。
  ② 回填。候选筛选(无批量接口,实现路径):`IterMemories(ws, L1)` → Go 过滤
     `metadata.action_class` 缺失 → 截断 batch 上限(50) → 对截断后的集合逐条
     `Neighbors(ep.ID, "operates_on")` 复核确无资源边(避免全量 Neighbors 扫描)。
     启发式:`NormalizeAction(metadata.action)` 命中即 stamp——重写必须**重新嵌入**:
     `vec, _ := embedder.Embed(ep.Content)` 后 `PutMemory(ep, vec)`
     (⚠️ 不能复用 `ep.Embedding` 字段:scanMemory 不从 DB 读回该字段,
     store_sqlite.go:296-326,传 nil 会把 embedding 列写 NULL,静默损坏向量索引);
     llm!=nil 时对启发式未命中的走 `l4_backfill`
     op 结构化抽取 `{resources:[], action:""}`,资源走 ResolveResource+建边。
     测试 fake 用 `providers.FakeLLMProvider`(providers/llm.go:41,
     CompleteFn/StructuredFn 可脚本化)。
  ③④⑤ 由 Task 18/19 填充(本任务先留空实现与 report 字段)。
- [ ] 绿 + commit `feat(ops): maintain gc + backfill`。

### Task 18: MaintainL4 — 语义建链(门控/桶/预算)

**Files:**
- Modify: `operations/maintain.go`
- Test: `operations/maintain_test.go`(追加)

**Steps:**

- [ ] **RED:** 测试:
  1. cfg.Maintain.LinkingEnabled=false → Skipped 标记,无 LLM 调用。
  2. llm==nil → 跳过。
  3. **episode 数门控**:近期 episodes < cfg.System2.MinEpisodesToRun → 跳过
     (spec §8.3 三条件缺一不可,与 L5/L6 同形态)。
  4. 桶:同 (layer-pair, action_class) 的两条 episode、fake LLM 确认 → related_to
     边;不同 action_class 或跨层的相似 pair 不产生候选。
  5. 预算:候选 30 对、budget 20 → LLM 恰被调 20 次。
  6. sim 预过滤:低于 linking_similarity 的 pair 不进 LLM。
- [ ] 跑红。
- [ ] **GREEN:** 子步骤③:候选生成 = 按 **(layer-pair, action_class)** 分桶(spec §8.3,防跨层漂移),桶内两两 cos(用 EmbedBatch 缓存向量,桶大时先 top-N 截断防 O(n²) 爆炸:单桶 >100 条只取最近 100);门控三条件:`LinkingEnabled && llm!=nil && CountRecentEpisodes>=cfg.System2.MinEpisodesToRun`;LLM 判定走 `l4_link` op(Task 17 已注册)结构化输出 `{link bool, relation string, weight float64}`;确认才 PutEdge(related_to,NewEdge+ProjectedEdgeID)。
- [ ] 绿 + commit `feat(ops): gated semantic linking`。

### Task 19: MaintainL4 — 别名合并

**Files:**
- Modify: `operations/maintain.go`
- Test: `operations/maintain_test.go`(追加)

**Steps:**

- [ ] **RED:** 测试:
  1. 两个高相似资源实体(cos≥0.92,hashing 下构造或调低阈值),`providers.FakeLLMProvider` 确认同体 → 合并:winner 保留、aliases 并入 loser 的、loser 的 operates_on 入边重指向 winner(新边)、`Retire(loser, winner.ID)` 建 supersedes。
  2. fake LLM 拒绝 → 不动。
  3. 相似度不足 → 不进 LLM。
  4. 合并后 winner 的向量检索仍可命中(回归保护:winner 重写走重嵌,不传 nil 向量——同 Task 17 的 ⚠️)。
- [ ] 跑红。
- [ ] **GREEN:** 子步骤④:IterMemories 过滤 resource;两两 cos≥alias_merge_similarity 的对送 `l4_alias_merge` op(Task 17 已注册,`{same_entity bool}`);确认后按 spec §8.4 合并(重指向边、Retire;注意 Retire 关闭双向边,spec §5.3)。
- [ ] 绿 + commit `feat(ops): resource alias merge`。

### Task 20: System 2 周期完整接线

**Files:**
- Modify: `operations/system2.go`
- Test: `operations/maintain_cycle_test.go`(扩展)

**Steps:**

- [ ] **RED:** 测试:完整 `RunSystem2Cycle`(fake runner 或真 engine,离线 cfg)→ `report.Maintain` 含 MaintainReport 四计数;周期顺序断言(可通过 fake runner 记录调用序列:Consolidate→Proceduralize→L5/L6→Decay→Maintain)。
- [ ] **GREEN:** RunSystem2Cycle 尾部 `runner.MaintainL4()`(System2Runner 接口再扩一个方法,Engine 委托 operations.MaintainL4,LLM 走已注册的 l4_* op,沿 Engine 给 L5/L6 传 agent 的方式);`report.Maintain = report`;测试 fake 同步补方法(Task 3 已列 grep 方式)。
- [ ] 绿 + 全量 + commit `feat(ops): maintain l4 in system2 cycle`。

---

# M4 文档

### Task 21: 文档修正与增补

**Files:**
- Modify: `ARCHITECTURE.md`、`README.md`、`README.zh-CN.md`

**Steps:**

- [ ] ARCHITECTURE.md 按 spec 附录 A:Auto-link on write 描述改准确(指向 §6 机制);callers/callees 句在 code_refs 投影后为真(核对后改);L1 表格补 resource/action_class 槽位;edges 表补 operates_on/produced_from/calls/imports;§8 front-ends 列表补 recall_ops(10 工具)。
- [ ] README 两语言版:record_event 新参数示例、`ladym recall-ops` 用法、[resource]/[maintain] 配置说明。
- [ ] 验证:文档中的命令逐条可执行(手动跑一遍 recall-ops 示例)。
- [ ] commit `docs: l1 ops retrieval + l4 maintenance`。

### Task 22: 全量回归 + 覆盖率

**Steps:**

- [ ] `go test ./...` 全绿。
- [ ] `go test ./... -coverprofile=coverage.out && go tool cover -func=coverage.out`:
  新增文件(operations/actions.go, resource.go, maintain.go;engine 的 ops 相关;sweep/delete 新 store 方法)语句覆盖率 **≥80%**;不足则补测试(边界:空输入、阈值边界、nil llm、批量边界)。
- [ ] `go vet ./...` 干净。
- [ ] 汇总覆盖率数字到最终报告。
- [ ] commit `test: coverage for ops retrieval + l4 maintenance`。

---

## 任务依赖

- Task 1-3(M0)独立,可先行。
- Task 4 是 M1 全部任务的前置;Task 5(ResourceConfig)前置于 6/7/8;Task 7 前置 8;Task 8 前置 9(episode 有资源边才可投影)、12。
- Task 10 前置 11;Task 12 前置 13/14/15。
- Task 16 前置 17;17 前置 18/19;18/19 前置 20。
- Task 21/22 收尾。

## 交接给实施子代理的说明

每个子代理领一个 Task(或同文件的紧邻 Task),必须:
1. 先读 spec 对应节(spec 是行为契约,本计划是步骤契约)。
2. 严格 RED→GREEN,不许跳红。
3. 每任务后 `go test ./...` 全绿才 commit。
4. 发现计划与代码现实冲突时:**停下来报告**,不擅自改设计。
