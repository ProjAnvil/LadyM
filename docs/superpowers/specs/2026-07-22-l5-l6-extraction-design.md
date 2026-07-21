# L5 Mental Models + L6 Forward Intent Extraction — Design

- **Date:** 2026-07-22
- **Status:** Approved (brainstormed via `superpowers:brainstorming`)
- **Related:** `2026-07-21-providers-config-control-plane-design.md` §2.8 (System1/System2 + L5/L6)
- **Builds on:** control-plane MVP (merged `d0bb48b`/`9790e60`) + Phase 5 web UI (`2b8f3cf`), 204 tests green.

## 1. Problem / Goal

The control-plane MVP added the two highest brain layers as **schema-only** enums
(`Layer.L5_MENTAL` / `MemoryType.MENTAL_MODEL`, `Layer.L6_PREDICTIVE` /
`MemoryType.FORWARD_INTENT`) and reserved the `l5_mental_model` / `l6_forward_intent` agent
slots in `AgentRegistry`. But the **extraction logic was never written**: `run_system2_cycle`
guards the calls behind `hasattr(engine, "extract_mental_models")` /
`predict_forward_intents` (`operations/system2.py:38-41`), so they are silently skipped every
cycle and no L5/L6 memory is ever produced.

This spec fills that gap: implement the two Engine methods (write-path, LLM-driven, run inside
the System2 cycle) so the existing plumbing starts producing L5 mental models and L6 forward
intents. Once the methods exist, the `hasattr` guards in `system2.py` activate automatically —
**no change to `system2.py` is required.**

## 2. Context (already in place, do not re-derive)

- **Schema** (`schema.py:27-50`): `Layer.L5_MENTAL`/`L6_PREDICTIVE`, `MemoryType.MENTAL_MODEL`/
  `FORWARD_INTENT` already exist. The `Layer` docstring already reads "seven memory layers".
- **Agent slots** (`providers/agents.py:17-23`): `l5_mental_model` and `l6_forward_intent` are in
  `NAMED_OPS`; `AgentRegistry.get(op)` + `make_agent(cfg, op)` resolve them like any other op
  (inherit `[llm]` globals, per-op override via `[agents.<op>]`). `AgentConfig.prompt_template`
  is captured but currently **unused** (the consolidate path hardcodes its prompt).
- **Lazy agent wiring** (`engine.py:159-177`): `Engine._get_agent(op)` builds+caches the provider,
  returns `None` for heuristic mode (`provider == "none"`) **or** when the `[llm]` extra is missing
  (logged warning → heuristic fallback). Never raises.
- **LLM call shape** (`providers/llm.py`): `provider.complete_structured(messages, SchemaModel) -> dict`.
  `FakeLLMProvider(structured_fn=...)` is the hermetic test double.
- **System2 cycle** (`operations/system2.py`): already gates L5/L6 behind
  `_count_recent_episodes >= cfg.system2.min_episodes_to_run` and stores results in
  `System2Report.l5` / `.l6` (typed `Any`). Decay runs last.
- **Similarity / store primitives**: `store.vector_index.search(vec, top_k)`, `iter_memories(workspace,
  layer)`, `get_memory`, `put_memory(mem, vector=...)`, `put_edge(Edge(...))`, `neighbors(id,
  relation=...)`, `get_meta`/`set_meta`. `embedder.embed` / `embed_batch`. `consolidate.py` is the
  reference pattern (re-embeds candidates via `embed_batch`, uses `vector_index.search`, writes via
  `put_memory`).
- **supersedes** (`operations/supersedes.py`): `retire(store, old, new_id=...)` — UPDATE chain
  (writes `superseded_by` + a `supersedes` edge old→new, **closes all still-open outgoing edges of
  `old`**) or DELETE retirement (`new_id=None`, no successor). `is_retired(mem)` filter.

## 3. Design

Two new pure functions live in `operations/l5.py` and `operations/l6.py` (mirroring
`consolidate`/`proceduralize`: take `store`, `embedder`, `cfg`, `workspace`, return a report
dataclass). `Engine` gains two thin methods that resolve the agent, call the function, and return
the report. Both are **write-path only** and run inside the System2 cycle.

### 3.1 L5 extraction — incremental + periodic merge (option C)

A memory is **"covered"** once some L5 points at it via an `abstracts` edge. Covered memories are
never re-processed by the incremental pass — this is the cost-control invariant (each System2 tick
only pays the LLM for genuinely new material). The merge pass, run every N ticks, collapses
fragmented models that arose because similar memories arrived in different cycles.

**Incremental pass — `l5.py::extract(...)` (every cycle):**

1. **Covered set.** Iterate all non-retired `L5_mental` memories; for each, collect
   `store.neighbors(l5_id, relation="abstracts")` dst-ids into a set `covered`. (Retired L5s are
   skipped via `supersedes.is_retired`, so a merged-away model's members become re-coverable by the
   merged successor — which already re-links them — and no member is double-counted.)
2. **Candidates.** `L2_semantic` prose memories (types FACT, NOTE — *not* CODE_FILE /
   CODE_SYMBOL, which are too granular to usefully abstract into a model) + `L3_procedural`
   (PLAYBOOK, SNIPPET) memories in the workspace **minus** `covered`. (Matches SPEC §2.8
   "cluster L2 facts + L3 playbooks".)
3. **Cluster.** `embedder.embed_batch` the candidate contents (re-embed, exactly as `consolidate`
   does — do not depend on whether `get_memory` returns the stored BLOB). Normalise; build a cosine
   Gram matrix (numpy is already a core dep, no new dependency); threshold
   `cfg.system2.l5_cluster_similarity`; pure-Python **union-find** connected components.
4. **Summarise.** For each component with `size >= cfg.system2.l5_min_cluster_size`: hand the
   members' contents to the `l5_mental_model` agent (`complete_structured` → `{title, model}`); store
   one `L5_mental`/`MENTAL_MODEL` memory; write `Edge(src=L5, relation="abstracts", dst=member)` for
   every member. **Singletons are skipped** (one fact is not a model). `tags = ["mental_model"]`.

**Merge pass — `l5.py::merge(...)` (every N cycles):**

5. `meta` counter `l5_merge_cycle_count` increments each `extract`; when it reaches
   `cfg.system2.l5_merge_every_n_cycles` the merge runs and the counter resets.
6. Re-cluster the **current non-retired L5 models** by their content-embedding similarity at the
   higher `l5_merge_similarity` threshold (same union-find). For each resulting cluster of `>= 2`
   models: concatenate member contents, summarise into one merged model, store as a new `L5_mental`;
   `supersedes.retire(old_l5, new_id=merged.id)` for each old model (this closes the old models'
   `abstracts` out-edges automatically); then write fresh `abstracts` edges from the merged model to
   **every** member memory across all old models.

**Idempotency:** step 1 guarantees the incremental pass only ever sees uncovered memories, so a
re-run with no new material produces zero new models and zero LLM calls. The merge pass is the only
code that retouches existing L5s, and it is gated by the cycle counter.

### 3.2 L6 prediction — `l6.py::predict(...)`

1. **Expire sweep (entry).** Iterate `L6_predictive` memories; for each with
   `now > float(metadata["valid_to"])`, call `supersedes.retire(store, mem)` (DELETE retirement, no
   successor) so stale predictions drop out of recall but remain auditable.
2. **Window.** Read watermark `l6_last_episode_ts` from `meta` (default 0.0). Select
   `L1_episodic` rows with `created_at > watermark`, `ORDER BY created_at ASC LIMIT
   cfg.system2.l6_max_episodes`.
3. **Predict.** Hand the episode texts to the `l6_forward_intent` agent
   (`complete_structured` → `list[{intent, confidence, horizon_s}]`).
4. **Store — one memory per intent.** Each predicted intent becomes its own
   `L6_predictive`/`FORWARD_INTENT` memory so it can be embedded, recalled, and expired
   independently. `horizon_s` is the per-intent value the model returned, falling back to
   `cfg.system2.l6_horizon_s` when the model omits it; `valid_to = now + horizon_s`.
   `metadata = {"confidence": ..., "horizon_s": ..., "valid_to": valid_to}`,
   `tags = ["predicted"]`.
5. **Advance watermark** to the max `created_at` of the batch (so the next tick processes only newer
   episodes).

### 3.3 Configuration

Extend `System2Config` (`config.py`) with six new fields. Because `_apply_toml` overlays a nested
dataclass section field-by-field via `fields(section_cls)`, adding fields to `System2Config`
**automatically** exposes them as `[system2]` TOML keys — no loader change, no signature change
(NFR-4). All have defaults so `Engine(Config())` is unaffected.

| field | default | meaning |
|---|---|---|
| `l5_cluster_similarity` | `0.65` | cosine threshold to put two memories in one cluster (below consolidate's `0.85` because a model groups related-but-distinct items) |
| `l5_min_cluster_size` | `3` | minimum component size to abstract (matches `proceduralize`) |
| `l5_merge_similarity` | `0.80` | cosine threshold to merge two existing L5 models |
| `l5_merge_every_n_cycles` | `5` | run the merge pass every N extract cycles |
| `l6_max_episodes` | `50` | cap on episodes handed to the L6 agent per tick |
| `l6_horizon_s` | `259200.0` (3 days) | default prediction TTL |

`Config.for_testing` is unchanged.

### 3.4 Prompts

Ship defaults at `src/ladym/prompts/l5.txt` and `src/ladym/prompts/l6.txt` (the paths the agent
table in the prior SPEC, lines 414-415, already promises). Load via
`importlib.resources.files("ladym.prompts") / "<name>.txt"`. **Override:** if
`AgentConfig.prompt_template` for the op is non-empty, use it verbatim instead of the file default —
this finally makes the currently-vestigial `prompt_template` field operator-meaningful. Add
`src/ladym/prompts/` to package-data in `pyproject.toml` so the files ship in the wheel.

- `l5.txt`: system role = "abstract several memories into one concise mental model"; instructs the
  JSON shape `{title, model}` and to stay factual / not invent beyond the inputs.
- `l6.txt`: system role = "predict likely next intents from recent episodes"; instructs the JSON
  shape `list[{intent, confidence in 0..1, horizon_s}]`.

### 3.5 Report types & failure semantics

New dataclasses (alongside `ConsolidationReport` / `ProceduralizeReport`):

- `L5ExtractionReport`: `new_models: int`, `merged_models: int`, `clusters: list[dict]` (per
  cluster: `model_id`, `n_members`, `action ∈ {new, merged}`), `skipped: bool`.
- `L6PredictionReport`: `predictions: int`, `expired_retired: int`,
  `watermark_updated_to: float | None`, `details: list[dict]`, `skipped: bool`.

The Engine methods set `skipped=True` and return an empty report (no writes) whenever
`_get_agent(op) is None` — i.e. the hermetic default (`provider="none"`) or a missing `[llm]`
extra. **They never raise in that case**, so the default suite stays hermetic and the System2 cycle
does not crash without an LLM. A **runtime** LLM failure (e.g. HTTP 500 from a configured provider)
propagates to the caller, exactly as `consolidate`/`proceduralize` do; the System2 worker's existing
`try/except` logs it and counts it toward `max_consecutive_errors`.

`System2Report.l5` / `.l6` remain typed `Any` (no change to `system2.py`).

### 3.6 Files

- **New:** `src/ladym/operations/l5.py`, `src/ladym/operations/l6.py`, `src/ladym/prompts/l5.txt`,
  `src/ladym/prompts/l6.txt` (and `src/ladym/prompts/__init__.py` empty, so `importlib.resources`
  resolves the subpackage).
- **Modified:** `engine.py` (+`extract_mental_models` / `+predict_forward_intents` methods + report
  imports), `config.py` (`System2Config` +6 fields), `pyproject.toml` (prompts package-data).
- **Unchanged:** `operations/system2.py` (already wired via `hasattr`), `schema.py`, `providers/agents.py`.

## 4. NFR compliance

- **NFR-1 (read-path engine overhead < 10ms):** L5/L6 are write-path only; the read path
  (`recall`/`reflect`) is untouched. No impact on the engine-overhead budget.
- **NFR-2 (hermetic default suite):** no new runtime dependency (numpy is already core; clustering is
  pure-Python union-find). With `provider="none"` the methods are no-ops that import nothing heavy —
  the default suite never imports langchain.
- **NFR-3 (no generative LLM on the read path):** L5/L6 run solely inside the System2 write cycle.
  `reflect()` stays heuristic-only.
- **NFR-4 (`Engine(Config())` + stable signatures):** no Engine signature change; new methods have no
  required args beyond `workspace`; all new config fields have defaults.
- **NFR-5 (secrets env-only):** untouched — keys still flow through `make_agent` →
  `api_key`/`api_key_env`.

## 5. Testing strategy

- **Unit (hermetic, in the default suite):** script both agents with `FakeLLMProvider.structured_fn`.
  - L5: seed L2/L3 memories with known contents; assert the resulting clusters, the new L5 memories,
    the `abstracts` edges L5→members, that a re-run with no new material adds nothing (idempotency),
    and that the merge pass retires old models via a `supersedes` chain and re-links members.
  - L6: seed L1 episodes; assert one memory per predicted intent, `metadata.valid_to` set, watermark
    advanced, and that a second run with expired predictions retires them and processes only newer
    episodes.
  - Offline: with `provider="none"`, both methods return `skipped=True` and write nothing, no raise.
- **e2e (not in the default suite):** local `ladym.toml` (ollama embedding + DeepSeek LLM) driving
  `ladym worker --once`; guarded so the hermetic suite is unaffected.

## 6. Out of scope / deferred

- **L5 model refresh on cluster growth** (re-summarising a cluster when a later, similar memory would
  extend it rather than form a sibling) — the periodic merge pass is the chosen MVP answer to
  fragmentation; per-cluster growth-tracking refresh is deferred.
- A dedicated `[l5]` / `[l6]` config section (kept inside `[system2]` for now to avoid loader churn).
- Re-summarising L5 when its member facts are themselves superseded by consolidation.
- Surfacing L5/L6 in the `ladym config` web editor (the per-op `[agents.*]` rows already expose them;
  no new UI work needed for MVP).
