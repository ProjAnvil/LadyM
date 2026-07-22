# proceduralize 幂等化:检索已有 L3 + ADD/UPDATE/NOOP

> 日期:2026-07-23
> 状态:设计已确认,待写实现计划
> 相关:`src/ladym/operations/proceduralize.py`、`src/ladym/operations/consolidate.py`、`src/ladym/operations/supersedes.py`、`scenarios/S15-decay.md`、SPEC §2.4/§2.6

## 背景与动机

`scenarios/S15` 实跑暴露:`ladym worker --once` 连续运行会对**同一批 L1 episodic**重复生成**完全相同**的 L3 playbook——S15 第二次 worker 后 `L3_procedural` 从 1 增到 2,两条 summary 全同(`How to scn-s15 deploy (3 episodes)`),id 不同。唯一导致 scenario 硬失败的代码问题。

### 根因(`systematic-debugging` 定位)

`proceduralize()`(`proceduralize.py:30-84`)每次调用:

1. 读**全部** L1 episodic(`WHERE layer=EPISODIC AND workspace=?`,`:43-46`,**无 `since` 增量过滤**);
2. 聚类后 `len(cluster) >= min_cluster_size` 时**无条件** `proc.put_playbook(...)`(`:75`);
3. **不检索、不检查工作区已有的 L3 playbook**(主因);且 `put_playbook`(**不像 `semantic.put_fact:42` 显式设 `content_hash`**)`Memory.content_hash` 默认空字符串(`schema.py:71`),`store.put_memory` 也不回填——故即便补上检索,`existing.content_hash` 为空会使 NOOP 分支(`existing.content_hash and ...`)短路、退化为 UPDATE,而 UPDATE 只 retire 旧 + 新建(stats L3 计数仍 +1,S15 仍不解决)。**两层都须修**:proceduralize 加检索分类 + put_playbook 补 content_hash。

`run_system2_cycle`(`system2.py:33`)调 `engine.proceduralize(workspace=ws)` 也不传 `since`。故第二次 worker 对同一批 L1 重新聚类 → 再次 `put_playbook` → L3 净增。

### 对比:consolidate 是幂等的

`consolidate()` 对每个 L1 candidate 先检索相似**已有 L2 facts**(`store.vector_index.search` + 过滤同 ws/同 layer),`_offline_classify`(`consolidate.py:46-64`)按 `content_hash`/相似度判 `NOOP/UPDATE/DELETE/ADD`:

- `content_hash` 相同 → **NOOP**;
- 相似度 ≥ 阈值 → **UPDATE**(走 supersedes 链,新建 merged + retire 旧);
- 否则 → **ADD**。

**proceduralize 完全没有这一层。**

### 业界参考(已调研)

| 框架 | skill 去重做法 |
|------|----------------|
| **Voyager**([MineDojo/Voyager](https://github.com/minedojo/voyager)、[paper](https://arxiv.org/abs/2305.16291)) | **不做 embedding 语义去重**;`add_new_skill` 仅在精确 `program_name` 碰撞时版本化覆盖(V2/V3)。vector DB 仅用于检索复用,不用于 add 前查重。 |
| **CoALA**([paper](https://arxiv.org/html/2309.02427v3)) | skill 更新走 **propose → verify → update**;明确支持更新已有 procedure。 |
| **业界共识** | "加入前用 embedding 相似度检查已有" + idempotency key / dedup table。 |

**关键启示**:ladyM 自家的 `consolidate` 已比祖师爷 Voyager 更严格(`content_hash` NOOP + 相似度 UPDATE)。**proceduralize 应向 `consolidate` 看齐,而非向 Voyager 看齐。** 选用方案 B(content_hash NOOP + 相似度 UPDATE),让两个操作的幂等范式统一。

## 设计

**核心:proceduralize 在 cluster 满足阈值后,先生成 candidate playbook,再检索工作区已有 L3 playbook,按 `content_hash`/相似度判 NOOP/UPDATE/ADD —— 与 `consolidate` 同构。**

### 流程改造

当前(`proceduralize.py:67-83`):

```
cluster >= min_cluster_size  →  无条件 proc.put_playbook(...)
```

改为:

```
cluster >= min_cluster_size:
    生成 candidate(name/steps/content,先不写)
    embed(candidate content)
    检索工作区已有 L3 playbook(vector_index.search,过滤 PROCEDURAL/PLAYBOOK/同 ws)
    分类:
        content_hash 相同      → NOOP(skip)
        最相似 cosine >= 阈值   → UPDATE(put 新 playbook + retire 旧的,supersedes 链)
        否则                   → ADD(put_playbook)
    report.actions[action] += 1
```

### 分类规则(内联,不复用 `_offline_classify`)

proceduralize 内联一个简化的分类(`_offline_classify` 的 DELETE/superseded_by 分支对 playbook 无意义——playbook 无 contradiction 语义,不删):

```python
def _classify_playbook(
    candidate_hash: str,
    similar: list[tuple[Memory, float]],
    threshold: float,
) -> Action:
    for existing, _sim in similar:
        if existing.content_hash and existing.content_hash == candidate_hash:
            return Action.NOOP            # 同一批 L1 重跑 → 不重复
    if similar and similar[0][1] >= threshold:
        return Action.UPDATE              # cluster 演化(steps 变) → 更新已有
    return Action.ADD
```

`Action` 复用 `consolidate.Action`(StrEnum:ADD/UPDATE/DELETE/NOOP);proceduralize 只产出 ADD/UPDATE/NOOP。

### 检索(与 consolidate 检索 L2 同构)

```python
raw = store.vector_index.search(candidate_vec, top_k=cfg.consolidate.min_episodes_to_trigger + 5)
similar = []
for mid, sim in raw:
    if sim < 0.1:
        continue
    m = store.get_memory(mid)
    if m is None or m.workspace != ws or m.layer != Layer.PROCEDURAL.value or m.type != MemoryType.PLAYBOOK.value:
        continue
    similar.append((m, sim))
similar.sort(key=lambda t: t[1], reverse=True)
```

### UPDATE 写回(复用 `supersedes.retire`,与 consolidate UPDATE 一致)

不就地改 playbook(SPEC §2.6 保留血缘):

```python
new_mem = proc.put_playbook(name=candidate_name, steps=candidate_steps, ...)  # 新 id,已 put_memory
from .supersedes import retire as _retire
_retire(store, similar[0][0], new_id=new_mem.id)   # 旧的被 superseded_by new
```

ADD → `proc.put_playbook(...)`(原行为)。NOOP → 不写。

### 阈值

复用 proceduralize 现有 `similarity_threshold: float = 0.55`(`:37`),兼作 playbook 去重阈值。该参数当前是 episode 聚类阈值,语义自然延伸到 playbook 粒度。**不新增 config**(YAGNI);若后续实测 playbook 粒度需更严,再加独立 `cfg.proceduralize.dedup_similarity_threshold`。

### ProceduralizeReport 扩展

类比 `ConsolidationReport.actions`:

```python
@dataclass
class ProceduralizeReport:
    clusters_examined: int = 0
    playbooks_created: int = 0      # 保留(= ADD 计数),向后兼容现有测试/断言
    actions: dict[str, int] = field(default_factory=lambda: {"ADD": 0, "UPDATE": 0, "NOOP": 0})
    details: list[dict] = None
```

`playbooks_created` 保留为 ADD 计数(向后兼容 `test_proceduralize_decay.py` 等现有断言);新增 `actions` 暴露 NOOP/UPDATE。

## 连带改动

### 1. 代码

- `src/ladym/operations/proceduralize.py`:
  - cluster 满足后,先构建 candidate(name/steps/content,沿用现有 `Counter`/`_derive_steps` 逻辑),算 `candidate_hash = content_hash(candidate_content)`;
  - **关键不变量:candidate_content 必须与 `put_playbook` 产物的 content 公式逐字一致**(目前 `name + "\n" + "\n".join(f"{i+1}. {s}")`,见 `procedural.py:32`)——否则两条同内容 playbook 的 hash 永不匹配、NOOP 失效、bug 等于没修。建议把该公式抽成 `_playbook_content(name, steps)` helper,`put_playbook` 与 proceduralize 共用,杜绝漂移;
  - 插入"检索已有 L3 + `_classify_playbook` + ADD/UPDATE/NOOP"分支;
  - UPDATE 分支:`put_playbook` 后 `_retire(store, target, new_id=new_mem.id)`;
  - `ProceduralizeReport` 加 `actions` 字段;`playbooks_created` 改为只统计 ADD。
  - 导入:`from .consolidate import Action`、`from .supersedes import retire`、`from ..layers.semantic import content_hash`、`from ..schema import MemoryType`。
- `ProceduralMemory.put_playbook`(`layers/procedural.py`):**补设 `content_hash=content_hash(content)`**(对齐 `semantic.put_fact:42`;dedup 字段本就该设,不算改写入语义——仅补全一直为空的字段),并抽 `_playbook_content(name, steps)` helper 供 put_playbook 与 proceduralize 共用(锁死不变量:candidate content 公式单一来源)。
- **不动**:`engine.py:249` 的直接调用路径、`operations/system2.py`、`config.py`。

### 2. 单测

- **新增失败→通过测试** `tests/unit/test_proceduralize_decay.py`(或新文件):
  - `test_proceduralize_idempotent_same_episodes`:同批 L1,`engine.proceduralize()` 调两次 → 工作区 L3 playbook 计数不变(第二次 `actions["NOOP"] == clusters_examined`,`ADD == 0`)。
  - `test_proceduralize_update_on_cluster_evolution`:首次产出 playbook 后,加 1 条改变 steps 的新 L1,再 proceduralize → `actions["UPDATE"] >= 1`,L3 不净增,旧 playbook 被 retire(supersedes 链,`superseded_by` 非空)。
- **回归**:`test_proceduralize_decay.py` 现有 3 次 proceduralize 用例不破(若原断言 `playbooks_created` 累加,需据新语义调整:首次 ADD、后续 NOOP)。`test_layers.py::test_procedural_put_playbook_and_snippet` 不受影响(直接调 put_playbook,不经操作层去重)。`tests/integration/test_end_to_end.py` 的 proceduralize 断言同步核对。
- 验证命令:`uv run pytest tests/unit/test_proceduralize_decay.py tests/unit/test_layers.py tests/integration/test_end_to_end.py -q`

### 3. scenario

- `scenarios/S15-decay.md`:步骤 7 断言"L2/L3 与步骤 4 一致"现在成立(L3 不再因 proceduralize 重复而净增)。可在 Then 补注"proceduralize 幂等(NOOP/UPDATE)"。
- `scenarios/S05-proceduralize.md`:首次产出 playbook 仍为 ADD,断言不变。
- `scenarios/_conventions.md`:无需改(幂等是隐含期望,现落实为契约)。

### 4. SPEC

- 若 SPEC §2.4(proceduralize)有"每次产出新 playbook"措辞,补"生成前检索已有 L3,按 content_hash/相似度 NOOP/UPDATE/ADD(与 consolidate 同构)"。

## 验收

- 重跑 `scenarios/S15`:第二次 `worker --once` 后 `L3_procedural` 保持 1(NOOP),不再 2;`L2_semantic` 不变;decay 只剪旧 L1。
- 新单测全绿;现有 proceduralize/consolidate/system2/layers/e2e 测试不破。
- `worker --once -w scn-s15` 退出码 0。

## 后续阶段规划(follow-up)

**本次实现(阶段 1)只做核心:proceduralize 幂等(检索已有 L3 + ADD/UPDATE/NOOP)。** 以下为后续阶段的设计规划,各自由独立 plan 驱动,本次不实现,但设计轮廓在此锁定以避免方向漂移。

### 阶段 2:`since` 增量短路(性能,优先级低)

- **动机**:proceduralize 每次读全部 L1 并 `embed_batch`,O(n)。worker 高频运行时是主成本。
- **设计**:加 `since: float | None` 参数(类比 consolidate)。**关键:聚类本质需要全量 L1**(cluster 跨全部 episode),故 since 不能像 consolidate 那样"只处理新 candidate"——只能作**短路**:若 `since` 之后无新 L1 episodic,直接返回(不重聚类/重 embed);有新 L1 时仍全量聚类,靠阶段 1 的幂等去重保证不重复产出。
- **触发条件**:worker 高频 + L1 量大,实测 `embed_batch` 成为主成本时。
- **与核心关系**:阶段 1 保证正确性(幂等),阶段 2 仅优化无新 L1 时的短路,不改变可观察行为。

### 阶段 3:playbook 的 LLM authoring / verify(功能增强,优先级中)

- **动机**:当前 proceduralize 是纯聚类启发式(`_derive_steps` 拼接 action/observation,playbook 质量受限)。CoALA 的 propose→verify→update、Voyager 的 GPT-4 skill authoring 都用 LLM 生成更智能的 playbook。
- **设计轮廓**:当 `engine._get_agent("proceduralize")` 可用(配了 LLM)且 cluster 满足时:① LLM 生成 playbook(name + 有意义 steps + preconditions + expected_outcome,替代 `_derive_steps`);② 可选 verify(评估新 playbook 是否优于已有)。复用 L5/L6 的 lazy-agent 模式(`run_system2_cycle` 的 `enough` 守卫 + `hasattr` 探测)。`provider=none` 时回退当前的聚类启发式。
- **与幂等的关系**:LLM 生成的 playbook **同样经阶段 1 的检索去重**(content_hash/相似度 NOOP/UPDATE/ADD)——LLM 路径不破坏幂等,反而因 content 质量更高使 NOOP/UPDATE 更准。
- **触发条件**:需要高质量 playbook(非拼接式 steps)的场景。

### 阶段 4:独立 `dedup_similarity_threshold` config(调参,优先级低)

- **动机**:playbook 粒度的去重阈值最优值可能与 episode 聚类阈值(0.55)不同。
- **设计**:加 `cfg.proceduralize.dedup_similarity_threshold: float = 0.55`(`config.py` 的 ProceduralizeConfig),与 `similarity_threshold`(聚类)分离。`_classify_playbook` 用 dedup 阈值,聚类仍用 `similarity_threshold`。
- **触发条件**:实测发现 0.55 对 playbook 去重偏松(相似但不同的 procedure 被误 UPDATE)或偏紧(同一 procedure 的演化被误 ADD)时。

### 评估后决定不做:`put_playbook` 层全局去重

- **理由**:`put_playbook`(`procedural.py`)被两处调用——操作层 proceduralize 与 `engine.py:249` 的 agent 直接添加。在 `put_playbook` 内加全局去重会让"用户/agent 显式加同名 playbook"被静默吞掉(语义冲突:直接添加应尊重调用者意图)。
- **结论**:去重的正确边界在**操作层**(proceduralize 检索已有 L3),`put_playbook` 保持单纯的"写入"语义。此为**设计决策**,不列入 follow-up,记录在案以防反复讨论。
