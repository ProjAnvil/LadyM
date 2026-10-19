# LadyM — L1 操作历史检索（资源×动作复合键）与 L4 自动维护 (SPEC)

- 日期:2026-10-19
- 状态:待批准
- 作者:yuhaochen + WorkBuddy
- 相关:`ARCHITECTURE.md` · `docs/superpowers/specs/`(既有 spec 惯例)
- 读者:未参与设计讨论、仅凭本文档实现的工程师

---

## 0. Context（先读）

LadyM 是五层（L0–L4）脑启发记忆框架,Go 单二进制,零 cgo,本地 SQLite,读路径无 LLM。
本 spec 合并两条已充分探索的设计线:

1. **L1 操作历史检索**:agent 记忆的检索键从"整句向量"升级为"资源×动作×时间"复合槽位键。
   三段式管线:资源解析(向量)→ L4 图连接(精确)→ ACT-R 激活排序。
2. **L4 自动维护**:L4 当前无自动维护者,存在三个已核实的问题(见 §2)。
   本 spec 使 L4 获得写时确定性建边 + worker 概率性修整。

两条线互为前置:`operates_on` 边既是 L4 建边的第一种,也是 pivot 检索的连接通道。

理论对齐(不再展开,详见 2026-10-19 探索记录):ACT-R partial match、海马体索引理论
(Teyler & DiScenna 1986)、CBR、Zep entity–episode 二部图、A-MEM 消融证据、GAM 语义漂移教训。

---

## 1. 动机

Agent 的记忆查询形态是"对某资源的某操作,最近怎么做的"。现状只有两种检索:
整句向量相似(recall)和纯时间序(Recent)。两者都无法回答这个问题:
- 整句余弦分不清匹配来自资源还是动作,两个维度纠缠在同一向量里。
- 槽位结构(agent/action/observation/outcome)在写入时被拍平成一句话,只存 metadata JSON,不参与检索。

核心判断:**向量从"整个键"降级为"资源解析这一步的工具"**。
动作槽走受控词表近精确匹配(可 hash),时间是精确轴。贵的操作只在小候选集上发生。

---

## 2. 现状问题(代码证据,全部已核实)

| # | 问题 | 证据 | 影响 |
|---|---|---|---|
| P0-1 | `NeighborCounts` 不校验端点存在性 | `storage/store_sqlite.go` / `store_postgres.go`:仅查 `valid_to IS NULL`,不 join memories | 悬挂边(限无 FK 旧库/外部写库,见 P0-2 修正)计入中心性,激活排序偏差;每次 recall 都调用。EXISTS join 是防御层 |
| P0-2 | 悬挂边防御缺口(修正:新库无此问题) | edges 表 DDL 自带 `FOREIGN KEY ... ON DELETE CASCADE`,SQLite 连接 `PRAGMA foreign_keys=ON`(store_sqlite.go:162),PG 同——**新库公共 API 造不出悬挂边**;但 `CREATE TABLE IF NOT EXISTS` 不会给 Python 时代旧库补 FK,外部直写库同样绕过 | 风险限于旧库与外部写库;GC + join 作为双保险(纵深防御),非修复现行 bug |
| P0-3 | consolidate 溯源只在 metadata,不进图 | `operations/consolidate.go:176-215`:UPDATE 已是"新建+Retire 建 supersedes 边"(核实修正,早先"原地改写"结论有误);但 `source_episode`/`updated_from` 只写 metadata 键,图里无 `produced_from` 边,fact 不继承 episode 的资源目标 | 事实-证据-资源三层在图上断开,只有向量相似一条通道 |
| P1-1 | 写路径零建边 | `engine/scope.go:107` 直接透传 `Episodic.Record` | ARCHITECTURE.md 的 "Auto-link on write" 未实现 |
| P1-2 | code_refs 不进 L4 | `code/indexer.go` 写 code_refs 表;`operations/recall.go tier2Expand` 只遍历 edges 表 | 最富的确定性关系数据(调用/导入)对图扩展不可见;ARCHITECTURE.md 的 "callers/callees via L4 expansion" 未实现 |
| P1-3 | 重索引残留(消失符号/投影行/向量索引) | `code/indexer.go:233,252`:code_file 按文件删旧、重发符号按限定名删旧;但文件中消失的符号(改名/删除)不清理;`code_symbols`、`code_refs` 投影行残留;`DeleteMemoriesByTypeSource`/`DeleteSymbolMemories`(store_sqlite.go:718,727)直执 SQL,不更新进程内向量索引 | 陈旧符号记忆与调用关系累积;向量索引含已删 ID(正确性由 recall 的 nil 检查兜底,但索引膨胀) |
| P2-1 | 边的 valid_to 无人写 | 全库除手动 `AssociativeMemory.Retire` 外无写入 | 双时间轴设计空转 |

---

## 3. 目标与非目标

### 目标
1. `record_event` 接受结构化资源/动作槽位;旧调用行为不变。
2. 新检索 `recall_ops`:资源+动作+时间窗 pivot 查询,三段式,读路径零 LLM。
3. L4 写时确定性建边:`operates_on`、`produced_from`、`calls`/`imports` 投影。
4. L4 worker 维护:悬挂边回收、槽位回填、语义建链(门控)、别名合并。
5. 正确性修复:NeighborCounts join、重索引残留清理;consolidate 溯源(produced_from/
   operates_on)进图。

### 非目标
- 不新增记忆层(L0–L4 结构不动)。
- 不做跨 workspace 资源共享(资源实体按 workspace 隔离,同 memories)。
- 不在检索读路径引入任何 LLM 调用(硬约束)。
- 不做 Console 资源时间线 UI(后续独立 spec)。
- 不自动把 worker 学到的动作词写入词表(M3 只建议,人工 config 确认)。

---

## 4. 方案取舍(已决策,含备选)

| 决策点 | 选定 | 备选与放弃原因 |
|---|---|---|
| 键形态 | 槽位三段式 | 纯向量泛化查询:维度纠缠,分不清资源/动作匹配来源 |
| 资源实体存储 | L2 `type=resource` 记忆 | 独立 `resources` 表:破坏"一个系统"原则,consolidate/recall 需旁路;L2 方案零新表,天然参与现有管线 |
| episode 槽位 | `metadata` 约定 + L4 边 | memories 加 SQL 列:需 ALTER TABLE 迁移;资源可查性由边承担,动作过滤在 Go 内存做,本地规模够用 |
| 语义建链时机 | worker 异步(门控) | 写时同步( A-MEM 原始做法):每条记忆 k 次 LLM 调用,阻塞写路径;Zep 为此优化 6 个月降 40% 成本 |
| 投影边幂等 | 确定性边 ID(内容哈希) | 随机 ID:重索引产生重复边 |
| 悬挂边处置 | 硬删除 | Retire:悬挂边无信息量,保留只膨胀表 |

---

## 5. 数据模型

### 5.1 资源实体(L2)

```
layer = L2, type = resource
content   = 规范名 + 简述(供 embedding)
metadata.aliases = ["ladym.db", "SQLite store", ...]   // 别名表,lazy 增长
metadata.kind     = "database" | "file" | "service" | "repo" | ...(可选,自由文本)
workspace 隔离,同 memories。
```

资源解析算法(写时,同步,零 LLM)。注意:`VectorSearch(queryVec, topK)` 无类型过滤
(store_sqlite.go:237),资源限定靠 Go 侧过滤——取 top-K·N 后 `GetMemory` 逐条验
`type=resource && workspace` 再选 top-1(与 recall.go 的过滤模式一致):

```
resolveResource(ws, name):
  1. 精确别名匹配:遍历 ws 的 L2 type=resource,aliases/content 命中 → 返回
  2. 向量最近邻:embed(name),VectorSearch top-N(默认 50)→ Go 过滤 type=resource
     sim >= cfg.Resource.ResolveThreshold(默认 0.85)
       → 返回,并把 name 追加进该资源 aliases(lazy 别名增长)
     否则 → 新建资源实体(content=name, aliases=[name]),embed,put,返回
```

阈值取高值,宁新建勿错并。错并由 worker 别名合并(§8.4)修正。

### 5.2 episode 槽位(metadata 约定)

```
metadata.action_class = "migrate"        // 受控词表归一后的动作
metadata.action_raw   = "migrate schema" // 上游原始动作串(未命中词表时保留)
metadata.outcome      = "success" | "fail" | ""(现状已有)
```

资源不进 metadata 重复存储,以 `operates_on` 边为准(单一事实来源)。

### 5.3 新边类型

| relation | src → dst | 产生时机 | 含义 |
|---|---|---|---|
| `operates_on` | episode → resource | 写时,record_event 带 resources | 该操作作用于该资源 |
| `operates_on` | fact → resource | consolidate 产出 fact 时 | fact 继承源 episode 的资源目标 |
| `produced_from` | fact → episode | consolidate ADD/UPDATE 时 | 事实的证据溯源 |
| `calls` / `imports` | code_symbol → code_symbol | index_code 后投影 | 调用/导入(code_refs 表的 L4 投影) |
| `related_to` | 任意 | worker 语义建链(门控) | 语义关联(现状已有,补生产者) |

方向约定遵循现状:`supersedes` 为旧→新(`operations/supersedes.go:45`)。
`Retire` 的边关闭语义(supersedes.go:32-42):`Neighbors(old, "")` 返回**双向**边
(store_sqlite.go:556 的 `src_id=? OR dst_id=?`),故 Retire 关闭的是 old 的
**全部未失效边(含入边)**,不是仅出边。本 spec 所有"关闭边"均按此语义表述。

### 5.4 确定性边 ID

机器投影的边(code_refs 投影、operates_on 投影)使用确定性 ID:

```
id = "proj:" + hex(sha256(src_id + "|" + relation + "|" + dst_id))[:16]
```

PutEdge 按 ID upsert。重跑投影幂等,不产生重复边。

### 5.5 动作词表

- 内置默认(代码常量,31 规范词,按域分组;分组枚举清单即契约,计数为 9+7+10+5):
  - 知识/数据:read, write, create, update, delete, query, search, analyze, summarize
  - 代码:index, fix, refactor, test, review, build, lint
  - 运维:deploy, migrate, configure, monitor, rollback, restart, execute, schedule, backup, restore
  - 生成/沟通/访问:generate, translate, notify, validate, auth
- 内置别名映射(代码常量,归一时展开):run/launch→execute,rm→delete,grep→search,
  fetch/get→read,modify→update,deploy→deploy(同形),list/ls→query,add→create。
- config `actions = [...]` 追加/覆盖规范词;`action_aliases = {k:v}` 追加别名。
- 学到的别名进 `meta` 表(key=`action_alias`,value=JSON),worker 可建议,人工确认后入 config。

归一算法:`normalizeAction(raw)`:lower+trim → 规范词/别名表精确匹配 → 命中返回规范词;
未命中 → `action_class` 留空,`action_raw=raw` 原样存,待回填。

词表规模判断:30 词 + 别名 + config 扩展 + worker 建议已覆盖真实 agent 操作动词的
主体(文件/代码/运维/沟通四域)。词表的价值在"近精确可 hash",不在穷举;
宁可让未命中走 action_raw 回填,不可为覆盖率把语义近似的词强行归并。

---

## 6. 写路径

### 6.1 RecordOperation(Engine/Scope 新方法)

```go
type OperationEvent struct {
    Agent, Action, ActionClass string   // ActionClass 可空,自动归一
    Resources   []string               // 资源名/别名,可空
    Observation, Outcome string
    Tags        []string
    Metadata    map[string]any
}
func (s *Scope) RecordOperation(ev OperationEvent) (*schema.Memory, error)
```

流程:
1. `action_class = ActionClass` 或 `normalizeAction(Action)`。
2. 每个 resource:`resolveResource` → resource_id 列表。
3. 复用现有 `Episodic.Record` 写 episode(槽位写入 metadata)。
4. 每个 resource_id 建 `operates_on` 边(episode→resource,确定性 ID)。

`RecordEvent` 签名不变,内部转调 `RecordOperation`(空 resources),行为与现状完全一致。

### 6.2 code_refs 投影

`index_code` 完成后(indexer 收尾步骤;现有写路径无统一事务,本投影也不依赖事务,
失败不阻塞):
1. 遍历本批 code_refs(src_qname, dst_qname, ref_kind)。
2. 两次查 `code_symbols` 表换 memory_id;任一端缺失则跳过。
3. 建 `calls`/`imports` 边(§5.4 确定性 ID),valid_from=索引时间。
4. 投影失败不阻塞索引主流程(记 report,可重跑)。

### 6.3 consolidate 溯源进图(核实后缩小范围)

核实修正:UPDATE 分支已是"新建 fact + `Retire(target, merged.ID)` 建 supersedes 边"
(consolidate.go:196-215),版本链现已在图上。本 spec 只补图投影,不动该主流程:

1. ADD 与 UPDATE 均建 `produced_from` 边(fact→源 episode,确定性 ID),
   与现有 `metadata.source_episode` 并存(元数据供显示,边供图遍历)。
2. 源 episode 的 `operates_on` 目标投影到新 fact(§5.3)。
3. 顺带清理:`offlineClassify` 行 81 的 `superseded_by == candidateHash` 判定
   在 Go 移植中比较 ID 与内容哈希,恒为假(Python 遗留死代码)。M3 附带删除并注释,
   NOOP 重放保护由行 78 的 `content_hash` 相等判定承担(语义不变)。

检索侧零改动:recall 已过滤 `IsRetired`(recall.go:103),tier2 已顺 supersedes
追新版本(recall.go:229)。

### 6.4 重索引残留清理(并入本 spec,原独立问题)

现状(已核实):code_file 按文件删旧重写(`indexer.go:233`);重发符号按限定名删旧
重写(`indexer.go:252`)。残留三处:文件中消失的符号(改名/删除)不清理;
`code_symbols`、`code_refs` 投影行残留;批量删除不走 `DeleteMemory`,
SQLite 进程内向量索引不同步(store_sqlite.go:718,727 直执 SQL)。

设计:body_hash 变化的文件,在写新符号前按文件全量清理:

```
DeleteCodeArtifactsByFile(ws, filePath):   // Store 接口新方法,两实现
  1. DELETE FROM code_refs
       WHERE src_symbol IN (SELECT qualified_name FROM code_symbols WHERE file_path=?)
          OR dst_symbol IN (同上)            // 必须先于 code_symbols 删除
  2. SELECT memory_id FROM code_symbols WHERE file_path=?  → id 列表
  3. DELETE FROM code_symbols WHERE file_path=?
  4. DELETE FROM memories WHERE type=code_symbol AND source=? AND workspace=?
     (即 DeleteMemoriesByTypeSource 语义,覆盖消失符号)
  5. SQLite:对 id 列表逐一 vectorIndex.Delete(id)  // 同步进程内索引
```

随后照常写新符号(putFileMemory 已自带 code_file 删旧,保持不动)。
被删符号的悬挂边由 MaintainL4 GC 同周期回收(§8.1)。

---

## 7. 读路径:recall_ops

### 7.1 查询与响应

```go
type OperationsQuery struct {
    Resource  string    // 必填:资源名/别名
    Action    string    // 可空:动作词(归一后精确匹配)
    Since, Until float64 // 可空:时间窗
    Outcome   string    // 可空:"success"|"fail"
    Limit     int
}
type OperationsResponse struct {
    Resource   *schema.Memory  // 解析命中的资源实体
    Items      []*schema.RecallResult // episode/fact,含槽位与 outcome
    Fallback   bool            // true=资源未解析,降级走普通 recall
}
```

### 7.2 三段式管线(读路径零 LLM)

```
Stage 1 资源解析(模糊,向量只在这里):
  精确别名 → 向量最近邻(VectorSearch top-N + Go 侧 type=resource 过滤,
  阈值 cfg.Resource.ReadThreshold,默认 0.75,低于写侧)
  未命中 → Fallback=true,降级 Recall(Resource + " " + Action),返回。
Stage 2 图连接(精确,便宜):
  Neighbors(resID, "operates_on") 取 src 侧(episode/fact)
  过滤:workspace、非 retired、action_class 匹配(Action 空则不过滤)、
        时间窗、outcome。
Stage 3 激活排序(ACT-R,便宜):
  ActivationScore 六参(operations/activation.go:54)。pivot 候选无向量 sim,
  取平面代理:直接邻居 0.5、二跳 0.5/2(有意偏离 tier2Expand 的
  `anchor.Score*0.5/depth` 公式,recall.go:268——pivot 无锚分数);
  queryTypes 传 nil。排序后截断 Limit。
```

冷启动(无槽位数据)自然降级为 Fallback,无行为回退。

### 7.3 前端暴露

- Engine:`RecallOperations(q)`(Scope 方法 + 顶层便捷函数)。
- MCP:新工具 `recall_ops`(参数即 OperationsQuery);`record_event` 增加
  可选 `resources []string`、`action_class string`(旧客户端不受影响)。
- CLI:`ladym recall-ops --resource "ladym.db" [--action migrate] [--since ...] [--outcome fail]`。
- Go SDK:`ladym.RecordOperation` / `ladym.RecallOperations` 一次性便捷函数。

---

## 8. worker 维护(MaintainL4,System 2 cycle 新步骤)

周期顺序调整为:Consolidate → Proceduralize → L5/L6(门控)→ Decay → **MaintainL4**。
(放在 Decay 后:当轮新产生的悬挂边同轮回收。)
`System2Report` 增加 `Maintain any` 字段。

**LLM agent 注册**:MaintainL4 的三个 LLM 子步骤(回填/语义建链/别名合并)各自
注册为独立 op:`l4_backfill`、`l4_link`、`l4_alias_merge`,加入
`providers/agents.go` 的 `NAMED_OPS` 与 AgentRegistry,支持 `[agents]` 按段覆盖
(与 `consolidate`/`l5_mental_model` 等现有 op 同机制)。无 LLM 配置时全部跳过。

四个子步骤按序:

### 8.1 悬挂边回收(零 LLM,每周期)

```
SweepDanglingEdges:
  DELETE FROM edges WHERE valid_to IS NULL
    AND (src_id NOT IN (SELECT id FROM memories)
     OR  dst_id NOT IN (SELECT id FROM memories))
```
Store 接口新增方法,SQLite/Postgres 各实现。report 记回收数。
NeighborCounts 的端点 join(§9)作为双保险,即使 GC 未跑统计也正确。

### 8.2 槽位回填(启发式免费,LLM 可选)

选无 `action_class` 且无 `operates_on` 边的 episode,批量(默认 50/周期):
1. 启发式(总是跑):现有 `metadata.action` 对词表精确/包含匹配 → 写 `action_class`。
2. LLM(有配置才跑):结构化抽取 `{resources:[], action:""}` → 走 §6.1 的解析与建边。
   无 LLM 则跳过,下轮启发式能覆盖的先覆盖。

### 8.3 语义建链(门控,LLM,有预算)

门控:`cfg.Maintain.LinkingEnabled && llm != nil && CountRecentEpisodes >= MinEpisodesToRun`
(复用 L5/L6 的门控形态;不满足置 `SkippedLLMSteps` 语义)。
桶内连边抑制 GAM 式语义漂移:候选对只在同 (layer-pair, action_class) 桶内产生。
1. 桶内向量最近邻产生候选对,预过滤 sim >= 0.60。
2. 每周期最多 N 对(默认 20,预算)送 LLM 判定 `{link, relation, weight}`。
3. 确认才建 `related_to` 边;拒绝则记入 meta 表避重(当轮)。

### 8.4 别名合并(门控,LLM)

资源实体对 cos sim >= 0.92 → LLM 确认同实体 → 合并:
1. winner 保留,aliases 并入 loser 的 aliases。
2. loser 的入边(`operates_on` 指向 loser 的边)逐条重建为指向 winner(确定性 ID)。
3. `Retire(loser, winner.ID)` 关闭 loser 全部未失效边(双向,含入边,§5.3)
   并留 supersedes 链。步骤 2 重建的新边指向 winner,不受影响。

---

## 9. 正确性修复(M0,独立可发布)

1. `NeighborCounts`(两 store):端点 join memories,悬挂边不计入(防御层)。
2. GC 方法(§8.1)。测试造数需绕过 FK 模拟无 FK 旧库(SQLite 事务外
   `PRAGMA foreign_keys=OFF` 后删;PG `DISABLE TRIGGER` 后恢复),并附
   "FK 正常库 DeleteMemory 后 Neighbors 为空"的回归锁。
3. consolidate 溯源进图(produced_from/operates_on 投影,§6.3),归 M1;
   UPDATE 的 supersedes 链已存在,无需修。

---

## 10. 配置

```toml
[resource]
resolve_threshold    = 0.85   # 写侧:匹配 vs 新建
read_threshold       = 0.75   # 读侧:pivot 解析
actions              = []     # 追加动作词(内置见 §5.5)

[maintain]                     # worker 维护,System 2 周期最后一步
enabled              = true
backfill_batch       = 50
linking_enabled      = true
linking_budget       = 20
linking_similarity   = 0.60
alias_merge_similarity = 0.92
```

全部有默认值。语义澄清:维护只在 System 2 周期内运行;`[system2].enabled`
(默认 false,config.go:287)控制 serve 时内嵌 worker 是否自启,`[maintain].enabled`
只控制周期内是否执行维护步骤。零配置默认下 System 2 不自启(现状不变),
显式运行(`ladym worker` / 启用 system2)后维护默认开。

---

## 11. 兼容性与迁移

- 无 ALTER TABLE,无数据迁移;旧 DB 打开即用。
- 旧客户端 `record_event` 不带新字段 → 行为与现状逐字节一致(无资源解析、无建边)。
- `recall` 管线零改动(仅 NeighborCounts 语义修正,属 bug fix)。
- Postgres store 同步实现全部新方法(现有 parity 测试框架覆盖)。
- 跨进程约束:投影/建边均在既有写路径或 worker 锁内,无新锁需求。

---

## 12. 测试策略(hermetic,hashing embedding,零网络)

1. **单元**:normalizeAction 词表/别名/未命中;resolveResource 三分支
   (精确/向量/新建);确定性边 ID 幂等(重投影计数不变)。
2. **正确性**:建边→删端点记忆→GC 回收→NeighborCounts 不含;
   NeighborCounts join 在 GC 未跑时也正确。
3. **管线**:consolidate UPDATE 产生 supersedes + produced_from + operates_on 投影;
   recall 过滤 retired 旧 fact;tier2 顺链到新 fact。
4. **recall_ops e2e**:录操作→pivot 返回;别名解析;动作/时间/outcome 过滤;
   资源未命中走 Fallback;冷启动降级。
5. **worker**:回填启发式/LLM(fake provider,沿现有 pattern);语义建链门控与预算;
   别名合并重指向。
6. **parity**:SQLite vs Postgres 行为一致。
7. **索引清理**:改文件并删函数→消失符号的记忆/code_symbols/code_refs 全清;
   SQLite 向量索引同步;code_file 不重复;重跑索引幂等。
8. **回归**:现有全量测试不动斩(除 consolidate UPDATE 行为的针对性更新)。

---

## 13. 风险与对策

| 风险 | 对策 |
|---|---|
| 资源错并(向量误合) | 写侧阈值 0.85 宁新建;worker 别名合并需 LLM 确认;合并可经 supersedes 链追溯 |
| 语义建链漂移/幻觉 | 桶内连边;sim 预过滤;预算封顶;LLM 确认制 |
| LLM 成本失控 | 建链/回填/合并全部门控+预算;无 LLM 时系统完整可用(检索/建边/回收零 LLM) |
| code_refs 投影规模 | 确定性 ID 去重;投影失败不阻塞;可重跑 |
| metadata 膨胀 | 槽位仅三个键;资源引用走边不复制进 metadata |
| consolidate UPDATE 语义迁移 | M0 单独交付+针对性回归;NOOP 判定先核对再动 |

---

## 14. 里程碑(供后续实施计划切任务)

| 里程碑 | 内容 | 交付判据 |
|---|---|---|
| M0 | §9.1-9.2 正确性修复:NeighborCounts join、悬挂边 GC | 全量测试绿;可独立发布 |
| M1 | 写路径:资源实体、RecordOperation、operates_on、code_refs 投影、确定性 ID、consolidate 溯源投影(§6.3:produced_from+fact 资源投影)、重索引残留清理(§6.4) | e2e:录操作→边存在→重投影幂等;改文件删函数→旧符号/投影行/refs 全清;consolidate 后 fact 有 produced_from+operates_on |
| M2 | 读路径:recall_ops 三段式 + Fallback + MCP/CLI/SDK | e2e:三形态查询返回正确;旧 recall 零回归 |
| M3 | worker:回填、语义建链、别名合并(op 注册 l4_backfill/l4_link/l4_alias_merge)、死代码行 81 删除 | 门控/预算行为符合;fake LLM 测试绿 |
| M4 | 文档:ARCHITECTURE.md/README 修正与新章节 | 文档-代码偏差清单清零 |

---

## 15. 开放问题(实现前需确认)

1. `operates_on` 是否需要反向查询便捷方法(`OperationsOf(resource)` 已由
   Neighbors 覆盖,是否再封装)——倾向不封装,YAGNI。
2. 资源 `kind` 字段是否引入受控枚举——倾向自由文本,先观察。

已决策(原开放问题):动作词表 30 词+别名(§5.5);重索引残留清理并入本 spec(§6.4);
consolidate NOOP 语义已核实(行 78 content_hash 判 NOOP 承担重放保护,行 81
`superseded_by==candidateHash` 为 Python 遗留死代码,M3 附带删除,§6.3)。

---

## 附录 A:文档-代码偏差修正清单(M4 执行)

| ARCHITECTURE.md 原文 | 现状 | 本 spec 动作 |
|---|---|---|
| "Auto-link on write + explicit link()" | 未实现 | M1 实现确定性建边,原文改为准确描述 |
| "recall … callers/callees via the L4 graph expansion" | tier2 仅 symbol→file 回溯 | M1 code_refs 投影后为真 |
| L1 episode 字段描述 | 无槽位说明 | M2 补 resource/action_class 说明 |
| edges 表说明 | 缺新边类型 | M1 补 operates_on/produced_from/calls/imports |
