# LongMemEval benchmark harness for ladyM

Measures ladyM's long-term memory on [LongMemEval](https://github.com/xiaowu0162/LongMemEval) (ICLR 2025).
Two phases share ingest+recall code:

- **Phase A — retrieval quality**: does `recall()` surface the right turns/sessions?
- **Phase B — end-to-end QA**: recall → answer-LLM → GPT-4o judge accuracy.

Run both for two variants to quantify consolidation's value:
- `raw` — episodic ingest only (offline, deterministic given fixed embeddings)
- `consolidated` — ingest + `eng.consolidate()` (LLM write path)

## Quick start

```bash
pip install -r benchmarks/longmemeval/requirements-lite.txt
pip install -e .                        # ladyM itself
export OPENAI_API_KEY=sk-...            # GPT-4o judge (Phase B) — or use Secret Store

# Phase A (raw, offline-capable)
python -m benchmarks.longmemeval ingest   --difficulty oracle --variant raw
python -m benchmarks.longmemeval retrieve --difficulty oracle --variant raw

# Phase B (needs answer-LLM config + judge key)
python -m benchmarks.longmemeval qa       --difficulty oracle --variant raw
python -m benchmarks.longmemeval evaluate --difficulty oracle --variant raw

# Repeat with --variant consolidated to compare.
# Scores land in benchmarks/.cache/results/<difficulty>/<variant>/scores.md
```

`--limit 5` during development to cap cost. `--difficulty s` for the main 500-question run.

## Vendored eval scripts

`upstream_eval/` holds 4 files pinned to a specific LongMemEval commit (SHA in each header).
The judge is fixed to `gpt-4o` — `print_qa_metrics.py` asserts on the model id.
