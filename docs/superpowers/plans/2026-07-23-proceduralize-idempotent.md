# proceduralize 幂等化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `proceduralize` 幂等——同一批 L1 episodic 重复 `worker --once` 不再产生重复 L3 playbook(S15 的 bug)。

**Architecture:** 对齐 `consolidate` 的 ADD/UPDATE/NOOP 范式:cluster 满足后先生成 candidate playbook,检索工作区已有 L3 playbook,按 `content_hash`/相似度判 NOOP/UPDATE/ADD。双层根因同修:① proceduralize 加"检索已有 L3 + 分类";② `put_playbook` 补设 `content_hash`(对齐 `put_fact`,否则 NOOP 短路)。

**Tech Stack:** Python 3.12、pytest、SQLiteStore + HashingEmbedding(测试)、ollama(线上)。spec: `docs/superpowers/specs/2026-07-23-proceduralize-idempotent-design.md`。

## Global Constraints

- **范围仅阶段 1**。阶段 2(`since` 短路)/3(LLM authoring)/4(独立 dedup 阈值)已在 spec 规划,本 plan **不实现**。
- `content_hash` 用 `from ..layers.semantic import content_hash`(`hashlib.blake2b(text.encode(), digest_size=16).hexdigest()`,semantic.py:18)。
- 去重阈值复用 proceduralize 现有 `similarity_threshold=0.55`(**不新增 config**)。
- UPDATE 走 `from .supersedes import retire`,`retire(store, old, *, new_id=...)` —— 不就地改,新建 + retire 旧(保留血缘)。
- **关键不变量**:`candidate_content` 必须与 `put_playbook` 产物的 content 公式逐字一致(经 `_playbook_content` helper 单一来源)。
- 不动:`engine.py:249` 直接调用路径、`operations/system2.py`、`config.py`。
- 测试用 `Config.for_testing`(hashing embedding,确定性)。pytest 命令前缀 `uv run`(或 `.venv/bin/python -m pytest`)。
- 每个任务结束 commit;commit message 前缀 `fix(proceduralize):` / `refactor(proceduralize):`。

---

## File Structure

- `src/ladym/layers/procedural.py` — 加 `_playbook_content` helper;`put_playbook` 复用 helper 且补设 `content_hash`。
- `src/ladym/operations/proceduralize.py` — 加 `_retrieve_existing_playbooks` + `_classify_playbook`;`proceduralize()` cluster 块改为检索→分类→ADD/UPDATE/NOOP;`ProceduralizeReport` 加 `actions`。
- `tests/unit/test_layers.py` — helper + content_hash 单测。
- `tests/unit/test_proceduralize_decay.py` — 幂等(NOOP)+ 演化(UPDATE)两个 TDD 测试。

---

### Task 1: `put_playbook` 补 content_hash + 抽 `_playbook_content` helper

**Files:**
- Modify: `src/ladym/layers/procedural.py`
- Test: `tests/unit/test_layers.py`

**Interfaces:**
- Produces: `ladym.layers.procedural._playbook_content(name: str, steps: list[str]) -> str`;`put_playbook` 产物的 `Memory.content_hash` 非空(= `content_hash(content)`)。

- [ ] **Step 1: 写失败测试**

在 `tests/unit/test_layers.py` 末尾追加:

```python
def test_playbook_content_helper_canonical():
    from ladym.layers.procedural import _playbook_content

    assert _playbook_content("Deploy", ["build", "ship"]) == "Deploy\n1. build\n2. ship"


def test_put_playbook_sets_content_hash(store, embedder):
    """put_playbook must set content_hash (was '' before — broke NOOP dedup)."""
    from ladym.layers.procedural import ProceduralMemory

    pm = ProceduralMemory(store, embedder, workspace="t")
    pb = pm.put_playbook("Deploy", ["build", "ship"])
    assert pb.content_hash, "content_hash must be non-empty for dedup"
```

- [ ] **Step 2: 跑测试确认失败**

Run: `uv run pytest tests/unit/test_layers.py::test_playbook_content_helper_canonical tests/unit/test_layers.py::test_put_playbook_sets_content_hash -v`
Expected: FAIL — `ImportError: cannot import name '_playbook_content'`(helper 不存在);content_hash 测试 FAIL(`pb.content_hash == ""`)。

- [ ] **Step 3: 实现 helper + put_playbook 补 content_hash**

把 `src/ladym/layers/procedural.py` 的 import 段(顶部)加一行:

```python
from ..layers.semantic import content_hash
```

> 注:`procedural.py` 与 `semantic.py` 同在 `layers/` 包,用相对导入 `from .semantic import content_hash`(不是 `..layers.semantic`)。最终该行为:
> `from .semantic import content_hash`

在 `class ProceduralMemory:` **之前**加模块级 helper:

```python
def _playbook_content(name: str, steps: list[str]) -> str:
    """Canonical content string for a playbook — single source of truth.

    Used by both ``put_playbook`` (when writing) and ``proceduralize`` (when computing a
    candidate hash for dedup) so the two never drift.
    """
    return name + "\n" + "\n".join(f"{i+1}. {s}" for i, s in enumerate(steps))
```

把 `put_playbook` 方法体改为(用 helper + 设 content_hash):

```python
    def put_playbook(self, name: str, steps: list[str], *,
                     preconditions: list[str] | None = None,
                     expected_outcome: str = "",
                     tags: list[str] | None = None) -> Memory:
        body = {
            "name": name,
            "preconditions": preconditions or [],
            "steps": steps,
            "expected_outcome": expected_outcome,
        }
        content = _playbook_content(name, steps)
        mem = Memory(
            layer=Layer.PROCEDURAL,
            type=MemoryType.PLAYBOOK,
            content=content,
            summary=name,
            tags=(tags or []) + ["playbook"],
            metadata=body,
            source="proceduralize",
            workspace=self.workspace,
            content_hash=content_hash(content),
        )
        self.store.put_memory(mem, vector=self.embedder.embed(content))
        return mem
```

- [ ] **Step 4: 跑测试确认通过**

Run: `uv run pytest tests/unit/test_layers.py -v`
Expected: PASS(含原有 `test_procedural_put_playbook_and_snippet` + 两个新测试)。

- [ ] **Step 5: commit**

```bash
git add src/ladym/layers/procedural.py tests/unit/test_layers.py
git commit -m "refactor(procedural): put_playbook sets content_hash + extract _playbook_content helper"
```

---

### Task 2: proceduralize NOOP 幂等(content_hash 去重)+ `ProceduralizeReport.actions`

**Files:**
- Modify: `src/ladym/operations/proceduralize.py`
- Test: `tests/unit/test_proceduralize_decay.py`

**Interfaces:**
- Consumes: `_playbook_content`(Task 1)、`content_hash`、`Action`(`from .consolidate import Action`)、`store.vector_index.search(vec, top_k) -> list[(mid, sim)]`、`store.get_memory(mid)`。
- Produces: `ProceduralizeReport.actions: dict[str,int]`(keys ADD/UPDATE/NOOP);`proceduralize()` 第二次对同批 L1 返回 `actions["NOOP"] >= 1` 且 `playbooks_created == 0`。

- [ ] **Step 1: 写失败测试**

在 `tests/unit/test_proceduralize_decay.py` 末尾追加:

```python
def test_proceduralize_idempotent_same_episodes(engine):
    """Same batch of L1 → two proceduralize calls must not duplicate L3 (content_hash NOOP)."""
    for _ in range(3):
        engine.episodic.record(
            agent="bot", action="deploy",
            observation="ran deploy.sh",  # identical → identical cluster/playbook content
            outcome="success",
        )
    r1 = engine.proceduralize(min_cluster_size=3)
    assert r1.actions["ADD"] == 1
    assert r1.playbooks_created == 1

    # second call on the same L1 → NOOP, no new playbook
    r2 = engine.proceduralize(min_cluster_size=3)
    assert r2.actions["NOOP"] == 1
    assert r2.actions["ADD"] == 0
    assert r2.playbooks_created == 0

    # exactly one L3 playbook in the store
    resp = engine.recall("deploy", types=[MemoryType.PLAYBOOK])
    playbooks = [r for r in resp.results if r.memory.layer == Layer.PROCEDURAL.value]
    assert len(playbooks) == 1
```

- [ ] **Step 2: 跑测试确认失败**

Run: `uv run pytest tests/unit/test_proceduralize_decay.py::test_proceduralize_idempotent_same_episodes -v`
Expected: FAIL — `AttributeError: 'ProceduralizeReport' object has no attribute 'actions'`(第二次调用会重复 ADD,L3 变 2)。

- [ ] **Step 3: 实现 NOOP/ADD 幂等核心**

把 `src/ladym/operations/proceduralize.py` 的 import 段(顶部,`from __future__` 之后)整体替换为:

```python
from collections import Counter
from dataclasses import dataclass, field

from ..config import Config
from ..layers.procedural import ProceduralMemory, _playbook_content
from ..layers.semantic import content_hash
from ..schema import Layer, Memory, MemoryType
from ..storage.embeddings import EmbeddingProvider
from ..storage.store import SQLiteStore
from .consolidate import Action
```

把 `ProceduralizeReport` 替换为(加 `actions`):

```python
@dataclass
class ProceduralizeReport:
    clusters_examined: int = 0
    playbooks_created: int = 0  # = ADD count (kept for backward compat)
    actions: dict[str, int] = field(
        default_factory=lambda: {"ADD": 0, "UPDATE": 0, "NOOP": 0}
    )
    details: list[dict] = None  # type: ignore[assignment]

    def __post_init__(self):
        if self.details is None:
            self.details = []
```

在 `def proceduralize(` **之前**加两个 helper:

```python
def _retrieve_existing_playbooks(
    store: SQLiteStore, candidate_vec: list[float], ws: str, top_k: int
) -> list[tuple[Memory, float]]:
    """Top similar existing L3 playbooks in this workspace (mirrors consolidate's L2 retrieval)."""
    raw = store.vector_index.search(candidate_vec, top_k=top_k)
    similar: list[tuple[Memory, float]] = []
    for mid, sim in raw:
        if sim < 0.1:
            continue
        m = store.get_memory(mid)
        if m is None or m.workspace != ws:
            continue
        if m.layer != Layer.PROCEDURAL.value or m.type != MemoryType.PLAYBOOK.value:
            continue
        similar.append((m, sim))
    similar.sort(key=lambda t: t[1], reverse=True)
    return similar


def _classify_playbook(
    candidate_hash: str,
    similar: list[tuple[Memory, float]],
    threshold: float,
) -> Action:
    """ADD/NOOP for a candidate playbook vs existing L3 (UPDATE added in next task)."""
    for existing, _sim in similar:
        if existing.content_hash and existing.content_hash == candidate_hash:
            return Action.NOOP
    return Action.ADD
```

把 `proceduralize()` 内 `if len(cluster) >= min_cluster_size:` 整块(原 `:67-83`,从 `assigned[i] = True` 到 `report.details.append(...)`)替换为:

```python
        if len(cluster) >= min_cluster_size:
            assigned[i] = True
            report.clusters_examined += 1
            actions = [c.metadata.get("action", "do") for c in cluster]
            top_action = Counter(actions).most_common(1)[0][0]
            steps = _derive_steps(cluster)
            name = f"How to {top_action} ({len(cluster)} episodes)"
            # idempotency: check existing L3 playbooks before writing
            candidate_content = _playbook_content(name, steps)
            candidate_hash = content_hash(candidate_content)
            candidate_vec = embedder.embed(candidate_content)
            similar = _retrieve_existing_playbooks(
                store, candidate_vec, ws,
                top_k=cfg.consolidate.min_episodes_to_trigger + 5,
            )
            action = _classify_playbook(candidate_hash, similar, similarity_threshold)
            report.actions[action.value] += 1
            if action == Action.ADD:
                proc.put_playbook(
                    name=name, steps=steps,
                    preconditions=list({c.metadata.get("agent", "agent") for c in cluster}),
                    expected_outcome="success",
                    tags=[top_action],
                )
                report.playbooks_created += 1
            # NOOP: skip; UPDATE added in Task 3
            report.details.append({"action": action.value, "action_verb": top_action, "size": len(cluster)})
```

> 注:`cosine_similarity` 仍按原样在内层聚类循环(`for j in range(i + 1, len(succ)):` 内)`from ..storage.embeddings import cosine_similarity` 就地 import,不动。

- [ ] **Step 4: 跑测试确认通过**

Run: `uv run pytest tests/unit/test_proceduralize_decay.py -v`
Expected: PASS(新 `test_proceduralize_idempotent_same_episodes` + 原有测试)。

- [ ] **Step 5: commit**

```bash
git add src/ladym/operations/proceduralize.py tests/unit/test_proceduralize_decay.py
git commit -m "fix(proceduralize): content_hash NOOP dedup — same L1 no longer duplicates L3"
```

---

### Task 3: UPDATE 分支(cluster 演化 → supersedes 更新)

**Files:**
- Modify: `src/ladym/operations/proceduralize.py`
- Test: `tests/unit/test_proceduralize_decay.py`

**Interfaces:**
- Consumes: `from .supersedes import retire`(`retire(store, old, *, new_id=None)`)、Task 2 的 `_classify_playbook` / `_retrieve_existing_playbooks`。
- Produces: cluster 演化(name/steps 变,content_hash 变但高相似)时 `actions["UPDATE"] >= 1`,旧 playbook 被 retire(`is_retired`),活跃 L3 不净增。

- [ ] **Step 1: 写失败测试**

在 `tests/unit/test_proceduralize_decay.py` 末尾追加:

```python
def test_proceduralize_update_on_cluster_evolution(engine):
    """Cluster grows (3→4 episodes, name changes) → UPDATE existing playbook via supersedes."""
    from ladym.operations.supersedes import is_retired

    for _ in range(3):
        engine.episodic.record(
            agent="bot", action="deploy",
            observation="ran deploy.sh", outcome="success",
        )
    r1 = engine.proceduralize(min_cluster_size=3)
    assert r1.actions["ADD"] == 1
    first = [r for r in engine.recall("deploy", types=[MemoryType.PLAYBOOK]).results
             if r.memory.layer == Layer.PROCEDURAL.value][0].memory

    # grow cluster: one more identical episode → name "(3 episodes)"→"(4 episodes)",
    # content_hash differs but content is near-identical → UPDATE (not ADD, not NOOP)
    engine.episodic.record(
        agent="bot", action="deploy",
        observation="ran deploy.sh", outcome="success",
    )
    r2 = engine.proceduralize(min_cluster_size=3)
    assert r2.actions["UPDATE"] == 1
    assert r2.actions["ADD"] == 0

    # old playbook retired (supersedes chain), lineage preserved
    assert is_retired(engine.store.get_memory(first.id))

    # exactly one ACTIVE L3 playbook, and it's the updated "(4 episodes)" one
    active = [r for r in engine.recall("deploy", types=[MemoryType.PLAYBOOK]).results
              if r.memory.layer == Layer.PROCEDURAL.value and not is_retired(r.memory)]
    assert len(active) == 1
    assert "(4 episodes)" in active[0].memory.summary
```

- [ ] **Step 2: 跑测试确认失败**

Run: `uv run pytest tests/unit/test_proceduralize_decay.py::test_proceduralize_update_on_cluster_evolution -v`
Expected: FAIL — `assert r2.actions["UPDATE"] == 1`(Task 2 的 `_classify_playbook` 对 hash 不同 + 高相似仍返回 ADD,UPDATE 计数 0;且 ADD 使活跃 playbook 变 2)。

- [ ] **Step 3: 实现 UPDATE 分支**

在 `src/ladym/operations/proceduralize.py` 顶部 import 段加一行(在 `from .consolidate import Action` 之后):

```python
from .supersedes import retire as _retire
```

把 `_classify_playbook` 替换为(加 UPDATE 判定):

```python
def _classify_playbook(
    candidate_hash: str,
    similar: list[tuple[Memory, float]],
    threshold: float,
) -> Action:
    """ADD/UPDATE/NOOP for a candidate playbook vs existing L3."""
    for existing, _sim in similar:
        if existing.content_hash and existing.content_hash == candidate_hash:
            return Action.NOOP
    if similar and similar[0][1] >= threshold:
        return Action.UPDATE
    return Action.ADD
```

在 `proceduralize()` 的 cluster 块里,把 Task 2 的 `# NOOP: skip; UPDATE added in Task 3` 注释行替换为 UPDATE 分支(紧接 `if action == Action.ADD:` 块之后、`report.details.append(...)` 之前):

```python
            elif action == Action.UPDATE and similar:
                new_mem = proc.put_playbook(
                    name=name, steps=steps,
                    preconditions=list({c.metadata.get("agent", "agent") for c in cluster}),
                    expected_outcome="success",
                    tags=[top_action],
                )
                _retire(store, similar[0][0], new_id=new_mem.id)
            # NOOP: skip
```

- [ ] **Step 4: 跑测试确认通过**

Run: `uv run pytest tests/unit/test_proceduralize_decay.py -v`
Expected: PASS(三个 proceduralize 幂等/演化测试 + 原有)。

- [ ] **Step 5: commit**

```bash
git add src/ladym/operations/proceduralize.py tests/unit/test_proceduralize_decay.py
git commit -m "fix(proceduralize): UPDATE branch — cluster evolution supersedes old playbook"
```

---

### Task 4: 回归 + S15 端到端验收

**Files:** 无(验证任务)。

- [ ] **Step 1: 跑全量相关单测/集成测试**

Run:
```bash
uv run pytest tests/unit/test_proceduralize_decay.py tests/unit/test_layers.py tests/unit/test_consolidate.py tests/unit/test_supersedes.py tests/unit/test_system2.py tests/integration/test_end_to_end.py -q
```
Expected: 全 PASS。若 `test_end_to_end.py` 有 proceduralize 断言(如 `:87`)语义变化(playbooks_created 现在首次=1、重跑=0),按新幂等语义调整断言并说明。

- [ ] **Step 2: S15 端到端验收(两次 worker 后 L3 不增)**

Run(在仓库根,使用项目 `.venv`;CLI 必须带 `LADYM_LLM_PROVIDER=none` 避免无 key 崩溃):
```bash
export LADYM_LLM_PROVIDER=none
DB=e2e.ladym.db
L=.venv/bin/ladym
# reset scn-s15
sqlite3 $DB "DELETE FROM edges WHERE src_id IN (SELECT id FROM memories WHERE workspace='scn-s15') OR dst_id IN (SELECT id FROM memories WHERE workspace='scn-s15'); DELETE FROM memories WHERE workspace='scn-s15';"
# seed: 1 L2 fact + 3 identical L1
$L remember "scn-s15 持久语义事实" -w scn-s15 --db $DB
$L record --agent claude --action "scn-s15 deploy" --observation "ran deploy.sh" --outcome success -w scn-s15 --db $DB
$L record --agent claude --action "scn-s15 deploy" --observation "ran deploy.sh" --outcome success -w scn-s15 --db $DB
$L record --agent claude --action "scn-s15 deploy" --observation "ran deploy.sh" --outcome success -w scn-s15 --db $DB
# first worker → produces 1 L3 playbook
$L worker --once -w scn-s15 --db $DB
echo "after 1st worker:"; $L stats -w scn-s15 --db $DB
# second worker → L3 must NOT grow (idempotent NOOP)
$L worker --once -w scn-s15 --db $DB
echo "after 2nd worker:"; $L stats -w scn-s15 --db $DB
```
Expected: 第一次 worker 后 `L3_procedural = 1`;**第二次 worker 后 `L3_procedural` 仍 = 1**(修复前为 2)。`L2_semantic` 两次都不变。

- [ ] **Step 3: 回滚 S15 测试数据(reset)**

Run:
```bash
sqlite3 e2e.ladym.db "DELETE FROM edges WHERE src_id IN (SELECT id FROM memories WHERE workspace='scn-s15') OR dst_id IN (SELECT id FROM memories WHERE workspace='scn-s15'); DELETE FROM memories WHERE workspace='scn-s15';"
```

- [ ] **Step 4: 最终 commit(spec/plan 已随各 task 提交则跳过;否则补提交文档)**

```bash
git add docs/superpowers/specs/2026-07-23-proceduralize-idempotent-design.md docs/superpowers/plans/2026-07-23-proceduralize-idempotent.md
git commit -m "docs(proceduralize): idempotency spec + implementation plan"
```

---

## Self-Review(spec 覆盖核对)

- **根因(双层)**:Task 1 补 put_playbook content_hash(第 2 层);Task 2/3 补 proceduralize 检索+分类(第 1 层)。✓
- **NOOP(content_hash)**:Task 2。✓
- **UPDATE(相似度 + supersedes)**:Task 3。✓
- **ADD(原行为,首次产出)**:Task 2 保留,S05 回归。✓
- **`_playbook_content` 不变量**:Task 1 抽 helper,Task 2 proceduralize 复用。✓
- **`ProceduralizeReport.actions`**:Task 2。✓
- **阈值复用 0.55**:Task 2/3 用 `similarity_threshold`,无新 config。✓
- **不动 engine.py:249/system2.py/config.py**:各 task 均未触及。✓
- **S15 验收**:Task 4 Step 2。✓
- **阶段 2/3/4 不实现**:本 plan 无 since/LLM/独立阈值任务。✓
