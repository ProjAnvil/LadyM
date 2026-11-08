# LongMemEval benchmark harness for the ladyM Go engine

Measures the **Go** ladyM engine's long-term memory on
[LongMemEval](https://github.com/xiaowu0162/LongMemEval) (ICLR 2025), driving
`ladym serve` through `ladym_wrapper.LadymClient` (MCP stdio). Ported from
`benchmarks/longmemeval` on the Python `main` branch; the ingest → retrieval →
QA → evaluate flow and legacy metric computation are unchanged. Provenance-aware
`evidence_metrics` are emitted separately alongside the original `metrics`.

Two phases share ingest+recall code:

- **Phase A — retrieval quality**: does `recall()` surface the right turns/sessions?
- **Phase B — end-to-end QA**: recall → answer-LLM → GPT-4o judge accuracy.

Run both for two variants to quantify consolidation's value:
- `raw` — episodic ingest only (offline, deterministic given fixed embeddings)
- `consolidated` — ingest + `consolidate()` (configured LLM classifier, or
  conservative offline exact duplicate `NOOP` / additive `ADD`)

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
uv run python -m longmemeval qa       --difficulty oracle --variant raw \
  --model gpt-4o-mini --limit 2 --budget-seconds 30
uv run python -m longmemeval evaluate --difficulty oracle --variant raw \
  --model gpt-4o --limit 2 --budget-seconds 30

# Repeat with --variant consolidated to compare.
# Scores land in benchmarks/.cache/results/<difficulty>/<variant>/scores.md
# (base_dir is relative to the CWD — run from benchmarks/).
```

Live answer/judge calls require an explicitly named model, positive question
limit and finite positive wall-time budget. Credentials alone never authorize
network. The wall budget is seconds, **not** dollars, billing tokens or a count
of judge retries; the pinned upstream judge runs inside a bounded subprocess.
The answer client disables SDK retries and uses remaining-deadline timeouts.
Injected test answer functions remain offline. No live QA/LLM gains are inferred
from synthetic fixtures or unit-test hypotheses.

## Four offline learning readers

From the repository root, using the existing environment:

```sh
PYTHONPATH=benchmarks/src python -m longmemeval.learning_eval
PYTHONPATH=benchmarks/src python -m longmemeval.learning_eval \
  --backend postgres --edition enterprise
python -m pytest benchmarks/tests/test_learning_variants.py
```

The latter backend requires explicit `LADYM_TEST_PG_DSN` and creates only random
isolated test databases on the supplied server. No server, dataset download,
generative provider, hosted tokenizer or new dependency is started.

The real Go Engine fixture compares `stateless`, `direct_source`, `r1`,
`r2_topic`. Per variant: `source_coverage` is reachable known source roots over
two persisted cross-file fixtures; `stale_usage` is the fraction of selected
memories failing shared freshness; `read_elapsed` is reader-call seconds;
`context_units` is assembled UTF-8 bytes, `unit="bytes"`. Provenance follows the
actual Store graph. Topic construction is explicit, not compulsory for an
exact lookup. Every reader sees the same locally embedded corpus, including a
rejected distractor; a separate scenario covers exact nonembedded source lookup.
Results are contract/navigation observations, not QA accuracy,
billing-token savings or proof that R2 outperforms R1.
See [maintenance and B01–B14 evidence](../docs/memory-maintenance.md).

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

## Provenance and compatibility

`record_event` takes no metadata argument, but MCP recall now returns a
provenance whitelist: `session_id`, `source_episode`, `source_episodes`,
`knowledge_status`, `evidence_missing`, `verified_at`, `action_class`,
`tool_outcome`, `task_outcome`. Arbitrary server metadata is not exposed.
The harness still needs ingest `session_id`/`date`/`doc_id`, so `_runtime.GoEngine`
keeps an append-only sidecar
`<question_id>.meta.jsonl` next to each instance db mapping
`memory_id → metadata`, captured from the ids `record_event` returns and
re-attached to recall results by memory ID. Direct-ID sidecar fields take
precedence over server fields, and original raw metadata remains intact.
Derived source IDs resolve through this same sidecar into `evidence_sources`
(list of metadata dictionaries with `memory_id`), deduped by source memory ID.
There is no network lookup, session guessing or replacement of raw doc IDs
with a derived fact's ID. Unknown source IDs have no mapping.

### Two retrieval metric namespaces

`retrieval_results.metrics` retains the exact legacy shape/numbers and upstream
grading input, including first-encounter session deduplication. Derived evidence
is **not** inserted into this legacy namespace.

`retrieval_results.evidence_metrics` has the same keys: turn `recall_all@5/10/50`
and `ndcg_any@5/10/50`; session `recall_all@5/10` and `ndcg_any@5/10`.
**K counts recalled memory slots**, never flattened evidence sources:

- Each memory supplies a set of turn IDs and a set of session IDs. A multi-source
  fact can cover multiple gold IDs within one slot.
- `recall_all@K` is 1 iff the union of the first K slots covers every gold ID.
- `ndcg_any@K` gives a slot binary gain 1 only if it introduces a previously
  uncovered gold source. Duplicates give no extra credit; scores do not exceed 1.
- Ideal DCG has `min(K, len(gold))` binary gains. The existing upstream `_dcg`
  keeps **both first ranks undiscounted**. For gold `{a,b}`, groups `[{a,b}]`:
  K=1 gives recall=1, NDCG=1; K=2 gives recall=1, NDCG=0.5. This grouped score
  is not a count of sources contributed. Empty gold yields zero.
- Unknown provenance consumes a slot with zero gain. Turn/session ranks are
  separate sets per memory, not the legacy deduplicated session ranking.
- Older raw clients without server provenance use their direct doc/session
  metadata as singleton groups; unique single-source rankings match legacy
  slot scores. Missing direct metadata remains unknown.

Consequences / limitations:

- **Partial or absent provenance.** Consolidated facts can now match gold through
  their source mappings, but only when source IDs exist in the ingest sidecar.
  Old facts without provenance and missing sidecar entries remain unknown.
  Evidence metrics measure source retrieval, not claim correctness or task
  verification. No QA improvement, token saving or production p95 has been
  measured by adding these metrics.
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
