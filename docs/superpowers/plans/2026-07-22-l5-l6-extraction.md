# L5/L6 Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `Engine.extract_mental_models()` and `Engine.predict_forward_intents()` so the System2 cycle produces L5 mental-model and L6 forward-intent memories (the deferred half of SPEC §2.8).

**Architecture:** Two new pure operation modules (`operations/l5.py`, `operations/l6.py`) mirror `consolidate`/`proceduralize` — each takes `(store, embedder, *, cfg, workspace, llm, prompt)` and returns a report dataclass. `Engine` gains two thin methods that resolve the agent via the existing lazy `_get_agent(op)` and the prompt via `AgentRegistry`, then delegate. `operations/system2.py` is **unchanged** — its `hasattr` guards already call these methods, so they activate automatically once the methods exist. L5 = incremental extract (cluster uncovered L2/L3 by cosine + union-find → one mental model per cluster, `abstracts` edges) + periodic merge (re-cluster L5 models → supersedes chain). L6 = watermark-windowed prediction, one memory per intent, `metadata.valid_to` expiry + retire sweep.

**Tech Stack:** Python 3.11+, pydantic v2, numpy (already a core dep — used for the cosine Gram matrix), `importlib.resources` for prompt files, `FakeLLMProvider` for hermetic tests. No new dependencies.

## Global Constraints

- **NFR-1:** Read path (`recall`/`reflect`) is never touched by this work; L5/L6 are write-path only.
- **NFR-2 (hermetic):** No new runtime dependency. The default suite (`provider="none"`) must stay offline — both new methods are no-ops (`skipped=True`, no writes, no raise) when the agent is `None`. Never `import langchain` on the offline path.
- **NFR-3:** No generative LLM on the read path. L5/L6 run only inside the System2 write cycle.
- **NFR-4:** `Engine(Config())` and existing public signatures unchanged; all new `System2Config` fields have defaults.
- **NFR-5:** Secrets stay env-only (untouched — keys still flow through `make_agent`).
- **Build backend is hatchling** (`[tool.hatch.build.targets.wheel] packages = ["src/ladym"]`). Hatchling includes **all** files under package dirs by default, so `.txt` prompts ship in the wheel with **no `pyproject.toml` change**.
- **Baseline lint debt:** `uv run ruff check src/ tests/` currently reports **7 errors in untouched files** (`symbol_graph`×4 / `store` / `activation` / `decay`). Do NOT fix these — only the files this plan touches must be clean.
- **Test hermeticity:** CLI/config tests use an autouse fixture (chdir tmp + isolate HOME + clear `LADYM_*`). New tests in `tests/unit/` use the `tmp_path` + `Engine(Config.for_testing(tmp_path))` pattern (see `tests/unit/test_system2.py`). `Config.for_testing` sets `llm_provider="none"`, `prefer_sqlite_vec=False`.
- Run tests with `uv run pytest -q`; lint with `uv run ruff check`.

---

## File Structure

- **Create** `src/ladym/prompts/__init__.py` — empty; makes `ladym.prompts` an importable subpackage for `importlib.resources`.
- **Create** `src/ladym/prompts/l5.txt` — default system prompt for the mental-model agent.
- **Create** `src/ladym/prompts/l6.txt` — default system prompt for the forward-intent agent.
- **Create** `src/ladym/operations/l5.py` — `L5ExtractionReport`, `extract()`, internal `_merge()`, clustering helpers. Responsibility: L5 incremental extraction + periodic merge.
- **Create** `src/ladym/operations/l6.py` — `L6PredictionReport`, `predict()`. Responsibility: L6 prediction + expiry sweep.
- **Modify** `src/ladym/config.py` — add 6 fields to `System2Config`.
- **Modify** `src/ladym/engine.py` — add 2 methods + 2 report-type imports.
- **Create** `tests/unit/test_l5_l6_scaffolding.py` — config defaults + prompt packaging.
- **Create** `tests/unit/test_l5_extraction.py` — L5 incremental + idempotency + offline.
- **Append to** `tests/unit/test_l5_extraction.py` (Task 3) — L5 merge tests.
- **Create** `tests/unit/test_l6_prediction.py` — L6 prediction + watermark + expiry + offline.
- **Append to** `tests/unit/test_system2.py` — Engine method + cycle integration tests.

---

## Task 1: Scaffolding — System2Config knobs + prompt package

**Files:**
- Modify: `src/ladym/config.py` (the `System2Config` dataclass, ~line 118-129)
- Create: `src/ladym/prompts/__init__.py`, `src/ladym/prompts/l5.txt`, `src/ladym/prompts/l6.txt`
- Test: `tests/unit/test_l5_l6_scaffolding.py`

**Interfaces:**
- Produces: `Config.system2.l5_cluster_similarity` (float, default `0.65`), `l5_min_cluster_size` (int, `3`), `l5_merge_similarity` (float, `0.80`), `l5_merge_every_n_cycles` (int, `5`), `l6_max_episodes` (int, `50`), `l6_horizon_s` (float, `259200.0`); and a packaged `ladym.prompts` subpackage whose `l5.txt` / `l6.txt` are readable via `importlib.resources`.

- [ ] **Step 1: Write the failing test**

Create `tests/unit/test_l5_l6_scaffolding.py`:

```python
"""Scaffolding: System2Config L5/L6 knobs + packaged prompt files."""

from __future__ import annotations

from importlib.resources import files

from ladym.config import Config


def test_system2_config_l5_l6_defaults():
    s2 = Config().system2
    assert s2.l5_cluster_similarity == 0.65
    assert s2.l5_min_cluster_size == 3
    assert s2.l5_merge_similarity == 0.80
    assert s2.l5_merge_every_n_cycles == 5
    assert s2.l6_max_episodes == 50
    assert s2.l6_horizon_s == 3 * 24 * 3600.0


def test_prompts_are_packaged_and_readable():
    prompts = files("ladym.prompts")
    assert "mental model" in (prompts / "l5.txt").read_text().lower()
    assert "intent" in (prompts / "l6.txt").read_text().lower()
```

- [ ] **Step 2: Run test to verify it fails**

Run: `uv run pytest tests/unit/test_l5_l6_scaffolding.py -q`
Expected: FAIL — `AttributeError` on `l5_cluster_similarity` (fields not added yet), and `files("ladym.prompts")` raises (subpackage does not exist yet).

- [ ] **Step 3: Add the 6 System2Config fields**

In `src/ladym/config.py`, edit the `System2Config` dataclass. Current:

```python
@dataclass
class System2Config:
    """Background reflection cycle knobs."""

    enabled: bool = False
    interval_s: int = 300
    min_episodes_to_run: int = 3
    # After this many consecutive cycle failures, ``Engine.start_system2`` logs
    # critical and stops the worker thread so a persistently broken index /
    # LLM endpoint is visible instead of looping silently forever. The CLI
    # ``worker`` loop is unbounded (user-supervised) and does not use this knob.
    max_consecutive_errors: int = 10
```

Add the six L5/L6 fields after `max_consecutive_errors`:

```python
    max_consecutive_errors: int = 10
    # L5 mental-model extraction (SPEC §2.8): cluster uncovered L2/L3 memories.
    l5_cluster_similarity: float = 0.65   # cosine to put two memories in one cluster
    l5_min_cluster_size: int = 3          # minimum component size to abstract
    l5_merge_similarity: float = 0.80     # cosine to merge two existing L5 models
    l5_merge_every_n_cycles: int = 5      # run the merge pass every N extract cycles
    # L6 forward-intent prediction (SPEC §2.8): predict next intents from episodes.
    l6_max_episodes: int = 50             # cap on episodes handed to the agent per tick
    l6_horizon_s: float = 3 * 24 * 3600.0  # default prediction TTL (3 days)
```

(Adding fields to a dataclass already listed in `_NESTED_DATACLASS_SECTIONS` makes them TOML-configurable as `[system2]` keys automatically — no loader change.)

- [ ] **Step 4: Create the prompt package**

Create `src/ladym/prompts/__init__.py` (empty file — just a header comment):

```python
"""Packaged default prompts for the L5/L6 agents (overridable via agents.<op>.prompt_template)."""
```

Create `src/ladym/prompts/l5.txt`:

```text
You are a memory-consolidation engine for a brain-inspired agent memory system. Your job is to
abstract several concrete memories into ONE concise, general mental model.

You are given a list of memories, each prefixed with its type (e.g. "(fact) ..."). Produce a
single higher-level mental model that captures the shared concept, rule, or pattern they point to.

Reply ONLY with JSON matching exactly this schema:
  {"title": "<short label, at most 8 words>", "model": "<one to three sentences>"}

Rules:
- Generalise; do not merely concatenate the inputs.
- Stay faithful to the inputs — never invent specifics they do not support.
- Even if the memories seem loosely related, produce the best single umbrella statement you can.
```

Create `src/ladym/prompts/l6.txt`:

```text
You are a forward-prediction engine for a brain-inspired agent memory system. You are given a
list of recent episodic events (what the agent just did or observed). Predict the most likely
NEXT intents the agent or user will pursue.

Reply ONLY with JSON matching exactly this schema:
  {"intents": [{"intent": "<a concrete next action or goal>", "confidence": <0.0-1.0>, "horizon_s": <seconds this prediction stays plausible>}]}

Rules:
- Return between 1 and 5 intents, most likely first.
- "intent" must be a concrete next step, not a vague topic.
- You may omit "horizon_s" on an intent only if you cannot estimate it; the system defaults it.
```

- [ ] **Step 5: Run test to verify it passes**

Run: `uv run pytest tests/unit/test_l5_l6_scaffolding.py -q`
Expected: PASS (2 tests).

- [ ] **Step 6: Run full suite + lint, then commit**

Run: `uv run pytest -q && uv run ruff check src/ladym/config.py src/ladym/prompts tests/unit/test_l5_l6_scaffolding.py`
Expected: full suite green (was 204; now 206); lint clean on touched files.

```bash
git add src/ladym/config.py src/ladym/prompts tests/unit/test_l5_l6_scaffolding.py
git commit -m "feat(l5l6): add System2Config knobs + packaged prompt defaults

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 2: L5 incremental extraction

**Files:**
- Create: `src/ladym/operations/l5.py`
- Test: `tests/unit/test_l5_extraction.py`

**Interfaces:**
- Consumes: `System2Config.l5_cluster_similarity`, `l5_min_cluster_size` (Task 1); `store.iter_memories`, `store.neighbors`, `store.put_memory`, `store.put_edge`, `store.get_meta`/`set_meta`; `embedder.embed`/`embed_batch`; `supersedes.is_retired`/`retire`; `llm.complete_structured(messages, schema) -> dict`.
- Produces: `L5ExtractionReport` (fields: `new_models: int`, `merged_models: int`, `clusters: list[dict]`, `skipped: bool`); `extract(store, embedder, *, cfg, workspace=None, llm=None, prompt=None) -> L5ExtractionReport`. Writes `L5_mental`/`MENTAL_MODEL` memories and `relation="abstracts"` edges (`src=L5`, `dst=member`).

- [ ] **Step 1: Write the failing tests**

Create `tests/unit/test_l5_extraction.py`:

```python
"""L5 mental-model extraction (SPEC §2.8) — incremental pass."""

from __future__ import annotations

import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.operations.l5 import L5ExtractionReport, extract
from ladym.operations.supersedes import is_retired
from ladym.providers import FakeLLMProvider
from ladym.schema import Layer


@pytest.fixture
def engine(tmp_path):
    e = Engine(Config.for_testing(tmp_path))
    yield e
    e.close()


def _fake_model(title, body):
    return FakeLLMProvider(structured_fn=lambda msgs, schema: {"title": title, "model": body})


def test_extract_creates_model_and_abstracts_edges(engine):
    # Three similar L2 facts; force a single cluster with threshold 0.0.
    for c in ["auth uses JWT", "auth uses JWT tokens", "authentication via JWT"]:
        engine.semantic.put_fact(c)
    engine.config.system2.l5_cluster_similarity = -1.0
    engine.config.system2.l5_min_cluster_size = 2

    report = extract(engine.store, engine.provider, cfg=engine.config,
                     llm=_fake_model("Auth", "Authentication is JWT-based"))

    assert isinstance(report, L5ExtractionReport)
    assert report.new_models == 1
    # one L5 mental model exists
    l5 = list(engine.store.iter_memories(workspace="test", layer=Layer.L5_MENTAL.value))
    assert len(l5) == 1
    # it abstracts all three facts
    members = engine.store.neighbors(l5[0].id, relation="abstracts")
    assert len(members) == 3
    assert all(e.src_id == l5[0].id for e in members)


def test_extract_is_idempotent(engine):
    for c in ["auth uses JWT", "auth uses JWT tokens", "authentication via JWT"]:
        engine.semantic.put_fact(c)
    engine.config.system2.l5_cluster_similarity = -1.0
    engine.config.system2.l5_min_cluster_size = 2
    fake = _fake_model("Auth", "Authentication is JWT-based")

    r1 = extract(engine.store, engine.provider, cfg=engine.config, llm=fake)
    assert r1.new_models == 1
    # second run: every fact is now covered -> no new candidates -> no new models
    r2 = extract(engine.store, engine.provider, cfg=engine.config, llm=fake)
    assert r2.new_models == 0
    l5 = list(engine.store.iter_memories(workspace="test", layer=Layer.L5_MENTAL.value))
    assert len(l5) == 1  # still just the one from r1


def test_extract_below_min_cluster_size_creates_nothing(engine):
    engine.semantic.put_fact("only one lonely fact here")
    engine.semantic.put_fact("a second unrelated fact about cats")
    engine.config.system2.l5_cluster_similarity = -1.0
    engine.config.system2.l5_min_cluster_size = 3  # clusters of <3 are skipped

    report = extract(engine.store, engine.provider, cfg=engine.config,
                     llm=_fake_model("X", "Y"))
    assert report.new_models == 0
    assert list(engine.store.iter_memories(workspace="test", layer=Layer.L5_MENTAL.value)) == []


def test_extract_offline_noop_when_llm_none(engine):
    for c in ["auth uses JWT", "auth uses JWT tokens", "authentication via JWT"]:
        engine.semantic.put_fact(c)
    report = extract(engine.store, engine.provider, cfg=engine.config, llm=None)
    assert report.skipped is True
    assert report.new_models == 0
    assert list(engine.store.iter_memories(workspace="test", layer=Layer.L5_MENTAL.value)) == []
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `uv run pytest tests/unit/test_l5_extraction.py -q`
Expected: FAIL — `ImportError: cannot import name 'L5ExtractionReport' / 'extract' from 'ladym.operations.l5'` (module does not exist yet).

- [ ] **Step 3: Implement `src/ladym/operations/l5.py` (incremental extraction only — merge comes in Task 3)**

```python
"""L5 mental-model extraction (SPEC §2.8).

Incremental pass: cluster *uncovered* L2/L3 memories by embedding similarity, summarise each
cluster into one abstract mental model via the ``l5_mental_model`` agent, and link the members
with ``relation="abstracts"`` edges. A memory is "covered" once some L5 points at it, so a
re-run with no new material does no LLM work (cost-control invariant).

The periodic merge pass is added in Task 3.
"""
from __future__ import annotations

import time
from dataclasses import dataclass, field

from pydantic import BaseModel

from ..config import Config
from ..schema import Edge, Layer, Memory, MemoryType
from ..storage.embeddings import EmbeddingProvider
from ..storage.store import SQLiteStore
from .supersedes import is_retired

_L5_LAYER = Layer.L5_MENTAL.value
_ABSTRACTS = "abstracts"
# SPEC §2.8: abstract "L2 facts + L3 playbooks". Code symbols are too granular.
_ABSTRACTABLE = {
    (Layer.SEMANTIC.value, MemoryType.FACT.value),
    (Layer.SEMANTIC.value, MemoryType.NOTE.value),
    (Layer.PROCEDURAL.value, MemoryType.PLAYBOOK.value),
    (Layer.PROCEDURAL.value, MemoryType.SNIPPET.value),
}


class _MentalModel(BaseModel):
    """Structured output schema for the l5_mental_model agent."""

    title: str
    model: str


@dataclass
class L5ExtractionReport:
    new_models: int = 0
    merged_models: int = 0
    clusters: list[dict] = field(default_factory=list)
    skipped: bool = False


def _default_prompt() -> str:
    from importlib.resources import files

    return (files("ladym.prompts") / "l5.txt").read_text()


def _covered_member_ids(store: SQLiteStore, workspace: str) -> set[str]:
    """Ids of memories already abstracted by some non-retired L5 (via open ``abstracts`` edges).

    ``store.neighbors`` already filters ``valid_to IS NULL``, so edges closed by a merge
    retirement do not leak through here.
    """
    covered: set[str] = set()
    for l5 in store.iter_memories(workspace=workspace, layer=_L5_LAYER):
        if is_retired(l5):
            continue
        for e in store.neighbors(l5.id, relation=_ABSTRACTS):
            if e.src_id == l5.id:
                covered.add(e.dst_id)
    return covered


def _connected_components(
    ids: list[str], vecs: list[list[float]], threshold: float
) -> list[list[str]]:
    """Group ``ids`` by cosine similarity >= ``threshold`` (numpy Gram + pure-Python union-find)."""
    if not ids:
        return []
    import numpy as np

    mat = np.asarray(vecs, dtype=np.float64)
    norms = np.linalg.norm(mat, axis=1, keepdims=True)
    safe = np.where(norms == 0, 1.0, norms)  # avoid divide-by-zero on null vectors
    unit = mat / safe
    sim = unit @ unit.T  # cosine Gram matrix
    parent = list(range(len(ids)))

    def find(i: int) -> int:
        while parent[i] != i:
            parent[i] = parent[parent[i]]
            i = parent[i]
        return i

    def union(a: int, b: int) -> None:
        ra, rb = find(a), find(b)
        if ra != rb:
            parent[ra] = rb

    n = len(ids)
    for i in range(n):
        for j in range(i + 1, n):
            if sim[i, j] >= threshold:
                union(i, j)
    groups: dict[int, list[str]] = {}
    for i in range(n):
        groups.setdefault(find(i), []).append(ids[i])
    return list(groups.values())


def _summarise(llm, prompt: str, members: list[Memory]) -> dict | None:
    corpus = "\n".join(f"- ({m.type}) {m.content}" for m in members)
    msgs = [
        {"role": "system", "content": prompt},
        {"role": "user", "content": f"Abstract these memories into one mental model:\n{corpus}"},
    ]
    out = llm.complete_structured(msgs, _MentalModel)
    return out if isinstance(out, dict) else out.model_dump()


def _store_model(store, embedder, *, title, body, workspace, source, extra_meta) -> Memory:
    content = f"{title}: {body}" if body else title
    mem = Memory(
        layer=Layer.L5_MENTAL,
        type=MemoryType.MENTAL_MODEL,
        content=content,
        summary=title,
        tags=["mental_model"],
        metadata=extra_meta,
        source=source,
        workspace=workspace,
    )
    store.put_memory(mem, vector=embedder.embed(content))
    return mem


def extract(
    store: SQLiteStore,
    embedder: EmbeddingProvider,
    *,
    cfg: Config,
    workspace: str | None = None,
    llm=None,
    prompt: str | None = None,
) -> L5ExtractionReport:
    ws = workspace or cfg.workspace
    report = L5ExtractionReport()
    if llm is None:
        report.skipped = True
        return report
    prompt = prompt or _default_prompt()

    # candidates: uncovered abstractable L2/L3 memories
    covered = _covered_member_ids(store, ws)
    candidates = [
        m
        for m in store.iter_memories(workspace=ws)
        if m.id not in covered and (m.layer, m.type) in _ABSTRACTABLE
    ]

    by_id = {m.id: m for m in candidates}
    vecs = embedder.embed_batch([m.content for m in candidates]) if candidates else []
    components = _connected_components(
        [m.id for m in candidates], vecs, cfg.system2.l5_cluster_similarity
    )

    now = time.time()
    for comp in components:
        if len(comp) < cfg.system2.l5_min_cluster_size:
            continue
        members = [by_id[mid] for mid in comp]
        result = _summarise(llm, prompt, members)
        if not result:
            continue
        model_mem = _store_model(
            store, embedder,
            title=result.get("title", "mental model"),
            body=result.get("model", ""),
            workspace=ws, source="l5_extract",
            extra_meta={"n_members": len(members)},
        )
        for m in members:
            store.put_edge(
                Edge(src_id=model_mem.id, relation=_ABSTRACTS, dst_id=m.id, valid_from=now)
            )
        report.new_models += 1
        report.clusters.append(
            {"model_id": model_mem.id, "n_members": len(members), "action": "new"}
        )

    return report
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `uv run pytest tests/unit/test_l5_extraction.py -q`
Expected: PASS (4 tests).

- [ ] **Step 5: Run full suite + lint, then commit**

Run: `uv run pytest -q && uv run ruff check src/ladym/operations/l5.py tests/unit/test_l5_extraction.py`
Expected: full suite green; lint clean on touched files.

```bash
git add src/ladym/operations/l5.py tests/unit/test_l5_extraction.py
git commit -m "feat(l5): incremental mental-model extraction with abstracts edges

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 3: L5 periodic merge pass

**Files:**
- Modify: `src/ladym/operations/l5.py` (add `_merge()` + cycle counter, wire into `extract`)
- Append tests to: `tests/unit/test_l5_extraction.py`

**Interfaces:**
- Consumes: `System2Config.l5_merge_similarity`, `l5_merge_every_n_cycles` (Task 1); `supersedes.retire(store, old, new_id=...)`.
- Produces: extends `extract()` to increment meta key `l5_merge_cycle_count` each call and, every `l5_merge_every_n_cycles` calls, run `_merge()` which retires similar old L5 models into one merged model (supersedes chain) and re-links members. `L5ExtractionReport.merged_models` is populated.

- [ ] **Step 1: Write the failing test**

Append to `tests/unit/test_l5_extraction.py`:

```python
def test_merge_collapses_similar_l5_models(engine):
    """Every Nth extract clusters existing L5 models and merges similar ones via supersedes."""
    from ladym.schema import Edge, MemoryType

    # Two covered L2 facts, each already abstracted by its own L5 model.
    f1 = engine.semantic.put_fact("auth uses JWT")
    f2 = engine.semantic.put_fact("cache uses redis")
    now = __import__("time").time()

    def _make_l5(abstracts):
        mem = Memory(
            layer=Layer.L5_MENTAL, type=MemoryType.MENTAL_MODEL,
            content="stand-in model", summary="m", tags=["mental_model"],
            source="seed", workspace="test",
        )
        engine.store.put_memory(mem, vector=engine.provider.embed(mem.content))
        for member in abstracts:
            engine.store.put_edge(
                Edge(src_id=mem.id, relation="abstracts", dst_id=member.id, valid_from=now)
            )
        return mem

    l5a = _make_l5([f1])
    l5b = _make_l5([f2])

    # No new candidates (both facts covered) -> incremental pass is a no-op, but with
    # l5_merge_every_n_cycles=1 the merge pass runs and collapses the two models.
    engine.config.system2.l5_merge_similarity = -1.0  # force the two L5s into one cluster
    engine.config.system2.l5_merge_every_n_cycles = 1
    fake = _fake_model("Combined", "auth and cache infra")

    report = extract(engine.store, engine.provider, cfg=engine.config, llm=fake)

    assert report.new_models == 0
    assert report.merged_models == 1
    # old models retired, pointing at the merged successor
    assert is_retired(engine.store.get_memory(l5a.id))
    assert is_retired(engine.store.get_memory(l5b.id))
    assert engine.store.get_memory(l5a.id).metadata["superseded_by"]
    # exactly one non-retired L5 remains, abstracting both facts
    active = [m for m in engine.store.iter_memories(workspace="test", layer=Layer.L5_MENTAL.value)
              if not is_retired(m)]
    assert len(active) == 1
    members = engine.store.neighbors(active[0].id, relation="abstracts")
    assert {e.dst_id for e in members} == {f1.id, f2.id}
```

(Add `from ladym.schema import Memory` to the imports at the top of the test file if not already present — `Memory` is used inside the test. `Layer` is already imported.)

- [ ] **Step 2: Run test to verify it fails**

Run: `uv run pytest tests/unit/test_l5_extraction.py::test_merge_collapses_similar_l5_models -q`
Expected: FAIL — `report.merged_models == 1` fails (merge not implemented; report.merged_models stays 0).

- [ ] **Step 3: Add the merge pass to `src/ladym/operations/l5.py`**

3a. Add `retire` to the supersedes import. Change the import line:

```python
from .supersedes import is_retired
```
to:
```python
from .supersedes import is_retired, retire
```

3b. Add the `_merge` function (place it after `_store_model`, before `extract`):

```python
def _merge(
    store: SQLiteStore,
    embedder: EmbeddingProvider,
    *,
    cfg: Config,
    workspace: str,
    llm,
    prompt: str,
    report: L5ExtractionReport,
) -> L5ExtractionReport:
    """Re-cluster current non-retired L5 models; merge each similar pair via supersedes."""
    models = [
        m for m in store.iter_memories(workspace=workspace, layer=_L5_LAYER)
        if not is_retired(m)
    ]
    if len(models) < 2:
        return report
    by_id = {m.id: m for m in models}
    vecs = embedder.embed_batch([m.content for m in models])
    components = _connected_components(
        [m.id for m in models], vecs, cfg.system2.l5_merge_similarity
    )
    now = time.time()
    for comp in components:
        if len(comp) < 2:
            continue
        old_models = [by_id[mid] for mid in comp]
        # gather every member across the old models (dedup by id)
        members: list[Memory] = []
        seen: set[str] = set()
        for om in old_models:
            for e in store.neighbors(om.id, relation=_ABSTRACTS):
                if e.src_id == om.id and e.dst_id not in seen:
                    seen.add(e.dst_id)
                    mb = store.get_memory(e.dst_id)
                    if mb is not None:
                        members.append(mb)
        corpus_src = members or old_models
        result = _summarise(llm, prompt, corpus_src)
        if not result:
            continue
        merged = _store_model(
            store, embedder,
            title=result.get("title", "mental model"),
            body=result.get("model", ""),
            workspace=workspace, source="l5_merge",
            extra_meta={"n_members": len(members), "merged_from": [om.id for om in old_models]},
        )
        # retire old models (closes their abstracts out-edges), then re-link members to merged
        for om in old_models:
            retire(store, om, new_id=merged.id)
        for mb in members:
            store.put_edge(
                Edge(src_id=merged.id, relation=_ABSTRACTS, dst_id=mb.id, valid_from=now)
            )
        report.merged_models += 1
        report.clusters.append(
            {"model_id": merged.id, "n_members": len(members), "action": "merged"}
        )
    return report
```

3c. Wire the counter + merge into the tail of `extract`. Replace the final block of `extract`:

```python
        report.clusters.append(
            {"model_id": model_mem.id, "n_members": len(members), "action": "new"}
        )

    return report
```
with:

```python
        report.clusters.append(
            {"model_id": model_mem.id, "n_members": len(members), "action": "new"}
        )

    # periodic merge: every Nth extract, collapse similar existing L5 models.
    n = cfg.system2.l5_merge_every_n_cycles
    if n > 0:
        counter = int(store.get_meta("l5_merge_cycle_count") or 0) + 1
        if counter >= n:
            store.set_meta("l5_merge_cycle_count", "0")
            report = _merge(
                store, embedder, cfg=cfg, workspace=ws, llm=llm, prompt=prompt, report=report
            )
        else:
            store.set_meta("l5_merge_cycle_count", str(counter))

    return report
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `uv run pytest tests/unit/test_l5_extraction.py -q`
Expected: PASS (5 tests).

- [ ] **Step 5: Run full suite + lint, then commit**

Run: `uv run pytest -q && uv run ruff check src/ladym/operations/l5.py tests/unit/test_l5_extraction.py`
Expected: full suite green; lint clean.

```bash
git add src/ladym/operations/l5.py tests/unit/test_l5_extraction.py
git commit -m "feat(l5): periodic merge pass collapses similar mental models

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 4: L6 forward-intent prediction

**Files:**
- Create: `src/ladym/operations/l6.py`
- Test: `tests/unit/test_l6_prediction.py`

**Interfaces:**
- Consumes: `System2Config.l6_max_episodes`, `l6_horizon_s` (Task 1); `store.iter_memories`, `store.conn`, `store._row_to_memory`, `store.put_memory`, `store.get_meta`/`set_meta`; `embedder.embed`; `supersedes.is_retired`/`retire`; `llm.complete_structured(messages, schema) -> dict`. Meta keys: `l6_last_episode_ts` (watermark), value stored as `str(float)`.
- Produces: `L6PredictionReport` (fields: `predictions: int`, `expired_retired: int`, `watermark_updated_to: float | None`, `details: list[dict]`, `skipped: bool`); `predict(store, embedder, *, cfg, workspace=None, llm=None, prompt=None) -> L6PredictionReport`. Writes `L6_predictive`/`FORWARD_INTENT` memories (one per intent) with `metadata={"confidence","horizon_s","valid_to"}`, `tags=["predicted"]`.

- [ ] **Step 1: Write the failing tests**

Create `tests/unit/test_l6_prediction.py`:

```python
"""L6 forward-intent prediction (SPEC §2.8)."""

from __future__ import annotations

import time

import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.operations.l6 import L6PredictionReport, predict
from ladym.operations.supersedes import is_retired
from ladym.providers import FakeLLMProvider
from ladym.schema import Layer, Memory, MemoryType


@pytest.fixture
def engine(tmp_path):
    e = Engine(Config.for_testing(tmp_path))
    yield e
    e.close()


def _fake_intents(items):
    return FakeLLMProvider(
        structured_fn=lambda msgs, schema: {"intents": items}
    )


def test_predict_stores_one_memory_per_intent(engine):
    engine.episodic.record(agent="bot", action="deploy", observation="shipped v1")
    fake = _fake_intents([
        {"intent": "run smoke tests", "confidence": 0.9, "horizon_s": 600},
        {"intent": "open a hotfix ticket", "confidence": 0.3},
    ])
    report = predict(engine.store, engine.provider, cfg=engine.config, llm=fake)

    assert isinstance(report, L6PredictionReport)
    assert report.predictions == 2
    l6 = list(engine.store.iter_memories(workspace="test", layer=Layer.L6_PREDICTIVE.value))
    assert len(l6) == 2
    # the explicit-horizon intent keeps it; the other falls back to the config default
    by_text = {m.content: m for m in l6}
    assert by_text["run smoke tests"].metadata["horizon_s"] == 600
    assert by_text["open a hotfix ticket"].metadata["horizon_s"] == engine.config.system2.l6_horizon_s
    assert all(m.metadata["valid_to"] > time.time() for m in l6)
    assert all(m.tags == ["predicted"] for m in l6)


def test_predict_advances_watermark_and_skips_old_episodes(engine):
    engine.episodic.record(agent="bot", action="a", observation="first event")
    fake = _fake_intents([{"intent": "next", "confidence": 0.5}])
    r1 = predict(engine.store, engine.provider, cfg=engine.config, llm=fake)
    assert r1.predictions == 1
    assert r1.watermark_updated_to is not None

    # no new episodes since the watermark -> second run predicts nothing
    r2 = predict(engine.store, engine.provider, cfg=engine.config, llm=fake)
    assert r2.predictions == 0


def test_predict_retires_expired_intents(engine):
    # seed an already-expired L6 prediction directly
    expired = Memory(
        layer=Layer.L6_PREDICTIVE, type=MemoryType.FORWARD_INTENT,
        content="stale intent", summary="stale", tags=["predicted"],
        metadata={"confidence": 0.5, "horizon_s": 1.0, "valid_to": time.time() - 10},
        source="seed", workspace="test",
    )
    engine.store.put_memory(expired, vector=engine.provider.embed("stale intent"))

    engine.episodic.record(agent="bot", action="a", observation="a fresh event")
    fake = _fake_intents([])
    report = predict(engine.store, engine.provider, cfg=engine.config, llm=fake)

    assert report.expired_retired == 1
    assert is_retired(engine.store.get_memory(expired.id))


def test_predict_offline_noop_when_llm_none(engine):
    engine.episodic.record(agent="bot", action="a", observation="an event")
    report = predict(engine.store, engine.provider, cfg=engine.config, llm=None)
    assert report.skipped is True
    assert report.predictions == 0
    assert list(engine.store.iter_memories(workspace="test", layer=Layer.L6_PREDICTIVE.value)) == []
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `uv run pytest tests/unit/test_l6_prediction.py -q`
Expected: FAIL — `ImportError: cannot import name 'L6PredictionReport' / 'predict' from 'ladym.operations.l6'`.

- [ ] **Step 3: Implement `src/ladym/operations/l6.py`**

```python
"""L6 forward-intent prediction (SPEC §2.8).

From recent L1 episodes, predict likely next intents via the ``l6_forward_intent`` agent and
store one ``L6_predictive`` memory per intent. Each prediction carries ``metadata.valid_to``;
at the start of every run, expired predictions are retired (DELETE-style, no successor) so
stale intents drop out of recall but stay auditable.
"""
from __future__ import annotations

import time
from dataclasses import dataclass, field

from pydantic import BaseModel

from ..config import Config
from ..schema import Layer, Memory, MemoryType
from ..storage.embeddings import EmbeddingProvider
from ..storage.store import SQLiteStore
from .supersedes import is_retired, retire

_L6_LAYER = Layer.L6_PREDICTIVE.value
_L1_LAYER = Layer.EPISODIC.value
_WATERMARK = "l6_last_episode_ts"


class _Intent(BaseModel):
    intent: str
    confidence: float = 0.5
    horizon_s: float | None = None


class _Intents(BaseModel):
    intents: list[_Intent]


@dataclass
class L6PredictionReport:
    predictions: int = 0
    expired_retired: int = 0
    watermark_updated_to: float | None = None
    details: list[dict] = field(default_factory=list)
    skipped: bool = False


def _default_prompt() -> str:
    from importlib.resources import files

    return (files("ladym.prompts") / "l6.txt").read_text()


def predict(
    store: SQLiteStore,
    embedder: EmbeddingProvider,
    *,
    cfg: Config,
    workspace: str | None = None,
    llm=None,
    prompt: str | None = None,
) -> L6PredictionReport:
    ws = workspace or cfg.workspace
    report = L6PredictionReport()
    if llm is None:
        report.skipped = True
        return report
    prompt = prompt or _default_prompt()
    now = time.time()

    # 1. expire sweep: retire predictions whose valid_to has passed.
    for m in store.iter_memories(workspace=ws, layer=_L6_LAYER):
        if is_retired(m):
            continue
        try:
            valid_to = float(m.metadata.get("valid_to", 0))
        except (TypeError, ValueError):
            continue
        if now > valid_to:
            retire(store, m)
            report.expired_retired += 1

    # 2. window: episodes newer than the watermark, capped.
    watermark = float(store.get_meta(_WATERMARK) or 0.0)
    rows = store.conn.execute(
        "SELECT * FROM memories WHERE layer = ? AND workspace = ? AND created_at > ? "
        "ORDER BY created_at ASC LIMIT ?",
        (_L1_LAYER, ws, watermark, cfg.system2.l6_max_episodes),
    ).fetchall()
    episodes = [store._row_to_memory(r) for r in rows]
    if not episodes:
        return report

    # 3. predict.
    corpus = "\n".join(f"- {m.content}" for m in episodes)
    msgs = [
        {"role": "system", "content": prompt},
        {"role": "user", "content": f"Predict likely next intents from these recent episodes:\n{corpus}"},
    ]
    out = llm.complete_structured(msgs, _Intents)
    out = out if isinstance(out, dict) else out.model_dump()
    default_horizon = cfg.system2.l6_horizon_s

    # 4. store one memory per intent.
    for it in out.get("intents", []):
        intent_text = (it.get("intent") or "").strip() if isinstance(it, dict) else ""
        if not intent_text:
            continue
        confidence = float(it.get("confidence", 0.5))
        horizon = it.get("horizon_s")
        horizon = default_horizon if horizon is None else float(horizon)
        valid_to = now + horizon
        mem = Memory(
            layer=Layer.L6_PREDICTIVE,
            type=MemoryType.FORWARD_INTENT,
            content=intent_text,
            summary=intent_text[:80],
            tags=["predicted"],
            metadata={"confidence": confidence, "horizon_s": horizon, "valid_to": valid_to},
            source="l6_predict",
            workspace=ws,
        )
        store.put_memory(mem, vector=embedder.embed(intent_text))
        report.predictions += 1
        report.details.append(
            {"intent": intent_text, "confidence": confidence, "valid_to": valid_to}
        )

    # 5. advance the watermark past the batch we just consumed.
    newest = max(m.created_at for m in episodes)
    store.set_meta(_WATERMARK, str(newest))
    report.watermark_updated_to = newest
    return report
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `uv run pytest tests/unit/test_l6_prediction.py -q`
Expected: PASS (4 tests).

- [ ] **Step 5: Run full suite + lint, then commit**

Run: `uv run pytest -q && uv run ruff check src/ladym/operations/l6.py tests/unit/test_l6_prediction.py`
Expected: full suite green; lint clean.

```bash
git add src/ladym/operations/l6.py tests/unit/test_l6_prediction.py
git commit -m "feat(l6): forward-intent prediction with watermark window + TTL expiry

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 5: Engine methods + System2 integration

**Files:**
- Modify: `src/ladym/engine.py` (add 2 imports + 2 methods)
- Append tests to: `tests/unit/test_system2.py`

**Interfaces:**
- Consumes: `extract` / `predict` from Tasks 2-4; `Engine._get_agent(op)` (returns the provider or `None`); `AgentRegistry(cfg).get(op).prompt_template`.
- Produces: `Engine.extract_mental_models(*, workspace=None) -> L5ExtractionReport` and `Engine.predict_forward_intents(*, workspace=None) -> L6PredictionReport`. `operations/system2.py` is **unchanged** — its existing `hasattr` guards now resolve to these methods, so `run_system2_cycle` populates `report.l5` / `report.l6` whenever the episode threshold is met and an agent is configured.

- [ ] **Step 1: Write the failing tests**

Append to `tests/unit/test_system2.py`. Replace the file's existing import block (`import pytest` + the five `from ladym...` lines) with this ruff-isort-ordered block:

```python
import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.operations import system2 as system2_module
from ladym.operations.l5 import L5ExtractionReport
from ladym.operations.l6 import L6PredictionReport
from ladym.operations.system2 import System2Report, run_system2_cycle
from ladym.providers import FakeLLMProvider
```

(`Layer` is imported locally inside the new integration test below, so it is not needed at the top.)

Then append these tests:

```python
def test_engine_extract_offline_returns_skipped(engine):
    """Default (provider=none): the method is a no-op, never raises, writes nothing."""
    report = engine.extract_mental_models()
    assert isinstance(report, L5ExtractionReport)
    assert report.skipped is True
    assert report.new_models == 0


def test_cycle_populates_l5_l6_when_agent_configured(engine):
    """With agents injected and enough episodes, the cycle fills report.l5 / report.l6."""
    from ladym.schema import Layer

    # 3 distinct episodes clear the min_episodes_to_run (default 3) gate.
    for obs in ("auth uses JWT", "cache uses redis", "logs ship to loki"):
        engine.episodic.record(agent="bot", action="found", observation=obs)
    # 3 distinct L2 facts for L5 to cluster — decoupled from how consolidate may merge
    # the episodes above, so the resulting cluster size is deterministic.
    for c in ("the api authenticates with jwt", "the cache is backed by redis", "logs go to loki"):
        engine.semantic.put_fact(c)

    # inject fake agents straight into the lazy cache so _get_agent returns them
    engine._agents["l5_mental_model"] = FakeLLMProvider(
        structured_fn=lambda msgs, schema: {"title": "Infra", "model": "service infrastructure"}
    )
    engine._agents["l6_forward_intent"] = FakeLLMProvider(
        structured_fn=lambda msgs, schema: {"intents": [{"intent": "rotate keys", "confidence": 0.8}]}
    )
    # force every candidate into one cluster regardless of cosine sign
    engine.config.system2.l5_cluster_similarity = -1.0
    engine.config.system2.l5_min_cluster_size = 2

    report = run_system2_cycle(engine)

    assert isinstance(report.l5, L5ExtractionReport)
    assert isinstance(report.l6, L6PredictionReport)
    assert report.l5.new_models >= 1
    assert report.l6.predictions >= 1
    assert any(m.layer == Layer.L5_MENTAL.value
               for m in engine.store.iter_memories(workspace="test"))
    assert any(m.layer == Layer.L6_PREDICTIVE.value
               for m in engine.store.iter_memories(workspace="test"))
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `uv run pytest tests/unit/test_system2.py::test_engine_extract_offline_returns_skipped tests/unit/test_system2.py::test_cycle_populates_l5_l6_when_agent_configured -q`
Expected: FAIL — `AttributeError: 'Engine' object has no attribute 'extract_mental_models'`.

- [ ] **Step 3: Add the Engine methods**

3a. In `src/ladym/engine.py`, add the report-type imports. Place them **between** the `decay` and `proceduralize` import lines so the block stays ruff-isort-ordered (alphabetical: consolidate, decay, l5, l6, proceduralize, recall):

```python
from .operations.l5 import L5ExtractionReport
from .operations.l6 import L6PredictionReport
```

3b. Add the two methods inside the `Engine` class, right after `proceduralize` (after line ~324, before `decay`):

```python
    def extract_mental_models(self, *, workspace: str | None = None) -> L5ExtractionReport:
        """L5 extraction (SPEC §2.8): cluster uncovered L2/L3 into mental models + periodic merge.

        Write-path only; a no-op (``skipped=True``) when no LLM is configured.
        """
        from .operations.l5 import extract
        from .providers.agents import AgentRegistry

        prompt = AgentRegistry(self.config).get("l5_mental_model").prompt_template or None
        return extract(
            self.store, self.provider, cfg=self.config, workspace=workspace,
            llm=self._get_agent("l5_mental_model"), prompt=prompt,
        )

    def predict_forward_intents(self, *, workspace: str | None = None) -> L6PredictionReport:
        """L6 prediction (SPEC §2.8): predict next intents from recent episodes, with TTL expiry.

        Write-path only; a no-op (``skipped=True``) when no LLM is configured.
        """
        from .operations.l6 import predict
        from .providers.agents import AgentRegistry

        prompt = AgentRegistry(self.config).get("l6_forward_intent").prompt_template or None
        return predict(
            self.store, self.provider, cfg=self.config, workspace=workspace,
            llm=self._get_agent("l6_forward_intent"), prompt=prompt,
        )
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `uv run pytest tests/unit/test_system2.py -q`
Expected: PASS (all system2 tests, including the 2 new ones).

- [ ] **Step 5: Run full suite + lint, then commit**

Run: `uv run pytest -q && uv run ruff check src/ladym/engine.py tests/unit/test_system2.py`
Expected: full suite green (204 baseline + 13 new = 217: +2 scaffolding, +5 L5, +4 L6, +2 system2). Lint clean on touched files; the 7 baseline errors in untouched files remain.

```bash
git add src/ladym/engine.py tests/unit/test_system2.py
git commit -m "feat(engine): wire extract_mental_models / predict_forward_intents into System2

Co-Authored-By: Claude <noreply@anthropic.com>"
```

- [ ] **Step 6: Final whole-repo verification**

Run: `uv run pytest -q && uv run ruff check src/ tests/`
Expected: all tests green; ruff reports only the 7 pre-existing baseline errors in untouched files.

Run (optional, real LLM — not in the hermetic suite): `uv run ladym worker --once`
Expected: with a configured `[llm]` provider, a System2 cycle runs and produces L5/L6 memories; with the default offline config it logs nothing new and exits cleanly.

---

## Self-Review (run after writing, fixed inline)

- **Spec coverage:** SPEC §3.1 (incremental extract) → Task 2; §3.1 merge → Task 3; §3.2 L6 → Task 4; §3.3 config → Task 1; §3.4 prompts → Task 1; §3.5 reports + failure semantics → Tasks 2/4 (offline noop) + Task 5 (Engine); §3.6 files → all tasks. NFR-1/2/3/4/5 addressed in Global Constraints + offline-noop tests. ✓
- **Placeholder scan:** none — every step has real code or an exact command. ✓
- **Type consistency:** `extract`/`predict` signatures match across the operation modules, the Engine methods, and the test callsites. `L5ExtractionReport`/`L6PredictionReport` field names (`new_models`, `merged_models`, `predictions`, `expired_retired`, `watermark_updated_to`, `skipped`) are used consistently. Meta keys `l5_merge_cycle_count` / `l6_last_episode_ts` match between writer and (where relevant) reader. ✓
- **Pyproject:** confirmed no change needed (hatchling includes data files under `packages`); spec's mention of "package-data in pyproject" is superseded — noted in Global Constraints. ✓
```
