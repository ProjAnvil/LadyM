# LongMemEval benchmark harness for the ladyM Go engine

Measures the **Go** ladyM engine's long-term memory on
[LongMemEval](https://github.com/xiaowu0162/LongMemEval) (ICLR 2025), driving
`ladym serve` through `ladym_wrapper.LadymClient` (MCP stdio). Ported from
`benchmarks/longmemeval` on the Python `main` branch; the ingest → retrieval →
QA → evaluate flow and all metric computation are unchanged — only the engine
backend differs.

Two phases share ingest+recall code:

- **Phase A — retrieval quality**: does `recall()` surface the right turns/sessions?
- **Phase B — end-to-end QA**: recall → answer-LLM → GPT-4o judge accuracy.

Run both for two variants to quantify consolidation's value:
- `raw` — episodic ingest only (offline, deterministic given fixed embeddings)
- `consolidated` — ingest + `consolidate()` (LLM write path)

## Quick start

```bash
# from the repo root: build the Go binary (resolved via bin/ladym automatically)
go build -o bin/ladym ./cmd/ladym

cd benchmarks
uv sync                                # installs harness deps (dev group)
export OPENAI_API_KEY=sk-...           # GPT-4o judge (Phase B) + answer LLM

# Phase A (raw, offline-capable: Go defaults to hashing embeddings)
uv run python -m longmemeval ingest   --difficulty oracle --variant raw
uv run python -m longmemeval retrieve --difficulty oracle --variant raw

# Phase B (needs an answer LLM + judge key)
uv run python -m longmemeval qa       --difficulty oracle --variant raw
uv run python -m longmemeval evaluate --difficulty oracle --variant raw

# Repeat with --variant consolidated to compare.
# Scores land in benchmarks/.cache/results/<difficulty>/<variant>/scores.md
# (base_dir is relative to the CWD — run from benchmarks/).
```

`--limit 5` during development to cap cost. `--difficulty s` for the main
500-question run. The first run downloads the dataset from HuggingFace into
`benchmarks/.cache/data/`.

## Configuration

- **Engine**: one `ladym serve` subprocess per instance, with
  `--db <db_dir>/<question_id>.db --workspace lme-<question_id>`. The binary is
  resolved by `ladym_wrapper.find_ladym_binary` (`binary=` → `LADYM_BIN` →
  `PATH` → repo `bin/ladym`). Embedding/LLM providers of the Go engine itself
  are configured the usual Go way (`LADYM_EMBEDDING`, `LADYM_LLM_*` env vars /
  `ladym.toml`); the Go defaults (hashing embeddings, no LLM) are sufficient
  for the `raw` variant offline.
- **Answer LLM (Phase B only)**: env-configured OpenAI-compatible client —
  `OPENAI_API_KEY` (required), `OPENAI_BASE_URL` (optional),
  `LME_ANSWER_MODEL` (default `gpt-4o-mini`). The judge stays pinned to
  `gpt-4o` via the vendored scripts.

## Differences from the Python harness (engine capability gaps)

The Go MCP server (`mcp/server.go`) does not round-trip memory metadata:
`record_event` takes no metadata argument and `recall` results omit
`memory.metadata`. The harness needs `session_id`/`date`/`doc_id` per ingested
turn, so the runtime seam (`_runtime.GoEngine`) keeps an append-only sidecar
`<question_id>.meta.jsonl` next to each instance db mapping
`memory_id → metadata`, captured from the ids `record_event` returns and
re-attached to `recall` results by memory id.

Consequences / limitations:

- **Consolidated-variant retrieval caveat (carried over).** L2 facts produced
  by `consolidate()` are created server-side, have no sidecar entry, and
  surface with empty metadata — same as the Python harness, where consolidated
  facts carry no `doc_id`/`session_id`. They can occupy top-k slots but never
  match gold turns/sessions, which can artificially lower consolidated
  `recall_all@k`. Read the consolidated payoff in QA accuracy (Phase B), not
  Phase A recall.
- **Stale-db detection is coarser.** The Python harness caught a specific
  `EmbeddingDimensionMismatch` to auto-rebuild stale DBs; over MCP any probe
  failure on an existing DB (`LadymError` while counting memories) is treated
  as stale and triggers a rebuild.
- **No direct store/SQL access.** Anything the Go MCP tools don't expose
  (raw SQL, store internals, embedding config introspection) is unavailable to
  the harness by design; nothing in the ingest/retrieval/QA flow needs it.

## Notes for operators

- **`--top-k` only affects the retrieval phase** (`run_retrieval`). `run_qa`
  uses its own `top_k_context=10` and does not read `cfg.top_k`.

## Vendored eval scripts

`upstream_eval/` holds 4 files pinned to a specific LongMemEval commit (SHA in
each header). The judge is fixed to `gpt-4o` — `print_qa_metrics.py` asserts on
the model id.
