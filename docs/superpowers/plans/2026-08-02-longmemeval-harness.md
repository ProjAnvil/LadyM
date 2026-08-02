# LongMemEval Benchmark Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a structured, cached benchmark harness under `benchmarks/longmemeval/` that measures ladyM's retrieval quality (Phase A) and end-to-end QA accuracy (Phase B) on the LongMemEval benchmark, with raw-vs-consolidated variant comparison.

**Architecture:** Per-instance workspace isolation (one LadyM SQLite DB per LongMemEval instance). Ingest writes haystack turns as episodic memories via `record_event`; the `consolidated` variant additionally runs `eng.consolidate()`. Retrieval/QA read via `eng.recall()`. Evaluation reuses the official vendored LongMemEval scripts (pinned commit) so scores are directly comparable to published results.

**Tech Stack:** Python 3.10+, ladyM SDK (`Engine`/`record_event`/`recall`/`consolidate`), vendored upstream eval scripts (`evaluate_qa.py` = GPT-4o judge), `requests` for HuggingFace download, `numpy` for metrics, `pytest` for tests.

**Spec:** `docs/superpowers/specs/2026-08-02-longmemeval-harness-design.md`

## Global Constraints

- **Judge model is fixed:** `print_qa_metrics.py` asserts `autoeval_label['model'] == 'gpt-4o-2024-08-06'` — `evaluate_qa.py` MUST be invoked with metric_model `gpt-4o` (never `gpt-4o-mini`), or aggregation crashes.
- **Eval scripts are vendored, not cloned-at-runtime:** the 4 upstream files live under `benchmarks/longmemeval/upstream_eval/` with the pinned commit SHA in each file's header. Never `git clone` at runtime.
- **Data is never committed:** `benchmarks/.cache/` is gitignored; data is downloaded + hash-checked on demand.
- **`record_event` bypasses the attention gate** (guaranteed persist) — use it for ingest, NOT `remember`.
- **Per-instance isolation:** each instance gets `workspace=f"lme-{question_id}"` + its own DB file; 500 instances never share state.
- **Doc-id convention:** each ingested turn memory stores `metadata["doc_id"] = f"{session_id}_{turn_idx}"` and `metadata["session_id"]`; the retrieval metrics map recalled memories back to turns/sessions via these.
- **Top-k:** retrieval-eval run uses `top_k=50` (needed for `recall_all@50`); QA run uses `top_k=10` (context).
- **answer-LLM + consolidate-LLM** share ladyM's config-driven provider (inject via `ModelRouting`); the GPT-4o judge key (`OPENAI_API_KEY`) comes from the Secret Store / env.

## File Structure

| File | Responsibility |
|---|---|
| `benchmarks/longmemeval/__init__.py` | package marker |
| `benchmarks/longmemeval/config.py` | `BenchConfig` dataclass + dir derivation |
| `benchmarks/longmemeval/download_data.py` | fetch 3 JSONs from HuggingFace + hash-check |
| `benchmarks/longmemeval/ingest.py` | per-instance `record_event` ingest (+ optional consolidate) |
| `benchmarks/longmemeval/metrics.py` | `recall_all`/`ndcg` primitives + `build_metric_dict` (upstream-attributed) |
| `benchmarks/longmemeval/run_retrieval.py` | recall → per-query metrics → `retrieval.jsonl` (Phase A) |
| `benchmarks/longmemeval/run_qa.py` | recall + RAG + answer-LLM → `hypothesis.jsonl` (Phase B) |
| `benchmarks/longmemeval/evaluate.py` | wrap vendored scripts → `scores.md` |
| `benchmarks/longmemeval/upstream_eval/` | 4 vendored files (pinned SHA in headers) |
| `benchmarks/longmemeval/requirements-lite.txt` | `openai`, `requests`, `numpy` |
| `benchmarks/longmemeval/README.md` | how to run |
| `tests/benchmarks/__init__.py`, `tests/benchmarks/longmemeval/__init__.py` | test packages |
| `tests/benchmarks/longmemeval/fixtures.py` | synthetic 2-session instance fixture |
| `tests/benchmarks/longmemeval/test_*.py` | one test file per module |
| `.gitignore` (repo root) | add `benchmarks/.cache/` |

---

### Task 1: Scaffold package, gitignore, requirements

**Files:**
- Create: `benchmarks/longmemeval/__init__.py`
- Create: `benchmarks/longmemeval/requirements-lite.txt`
- Create: `benchmarks/.gitignore`
- Modify: repo-root `.gitignore` (append one line) — first Read it to follow existing style

**Interfaces:** Produces the empty package `benchmarks.longmemeval` importable as `from benchmarks.longmemeval import ...`.

- [ ] **Step 1: Read existing .gitignore to match style**

Run: `Read .gitignore`

- [ ] **Step 2: Append cache ignore to repo-root .gitignore**

Append this line to repo-root `.gitignore` (use Edit):
```
benchmarks/.cache/
```

- [ ] **Step 3: Create `benchmarks/.gitignore`**

Create `benchmarks/.gitignore`:
```
.cache/
```

- [ ] **Step 4: Create the package + requirements**

Create `benchmarks/longmemeval/__init__.py`:
```python
"""LongMemEval benchmark harness for ladyM."""
```

Create `benchmarks/longmemeval/requirements-lite.txt`:
```
openai>=1.0
requests>=2.28
numpy>=1.24
```

- [ ] **Step 5: Verify package imports**

Run: `python -c "import benchmarks.longmemeval; print('ok')"`
Expected: prints `ok`

- [ ] **Step 6: Commit**

```bash
git add benchmarks/ .gitignore
git commit -m "feat(bench): scaffold longmemeval harness package + gitignore"
```

---

### Task 2: Vendor upstream eval scripts (pinned SHA)

**Files:**
- Create: `benchmarks/longmemeval/upstream_eval/__init__.py`
- Create: `benchmarks/longmemeval/upstream_eval/evaluate_qa.py` (vendored)
- Create: `benchmarks/longmemeval/upstream_eval/print_qa_metrics.py` (vendored)
- Create: `benchmarks/longmemeval/upstream_eval/print_retrieval_metrics.py` (vendored)
- Create: `benchmarks/longmemeval/upstream_eval/eval_utils.py` (vendored)
- Test: `tests/benchmarks/longmemeval/test_upstream_vendor.py`

**Interfaces:** Produces 4 importable modules under `benchmarks.longmemeval.upstream_eval`. Their public surfaces (used by later tasks):
- `evaluate_qa`: CLI `python evaluate_qa.py <metric_model> <hyp_file> <ref_file>`; reads JSONL `{question_id, hypothesis}`, writes `<hyp_file>.eval-results-<short>` with `autoeval_label`.
- `print_qa_metrics`: CLI `python print_qa_metrics.py <in_file> <ref_file>`.
- `print_retrieval_metrics`: CLI `python print_retrieval_metrics.py <in_file>`; reads JSONL `{question_id, retrieval_results:{metrics:{session,turn}}}`.
- `eval_utils`: functions `dcg`, `ndcg`, `recall_any`, `recall_all` (reference formulas for Task 7).

- [ ] **Step 1: Clone upstream shallow + capture SHA**

Run:
```bash
cd /tmp && rm -rf LongMemEval && git clone --depth 1 https://github.com/xiaowu0162/LongMemEval.git && cd LongMemEval && git rev-parse HEAD
```
Record the printed SHA (e.g. `abc1234...`) — use it in Step 3.

- [ ] **Step 2: Copy the 4 files into upstream_eval/**

Run:
```bash
mkdir -p benchmarks/longmemeval/upstream_eval
cp /tmp/LongMemEval/src/evaluation/evaluate_qa.py benchmarks/longmemeval/upstream_eval/
cp /tmp/LongMemEval/src/evaluation/print_qa_metrics.py benchmarks/longmemeval/upstream_eval/
cp /tmp/LongMemEval/src/evaluation/print_retrieval_metrics.py benchmarks/longmemeval/upstream_eval/
cp /tmp/LongMemEval/src/retrieval/eval_utils.py benchmarks/longmemeval/upstream_eval/
```

- [ ] **Step 3: Prepend provenance header to each vendored file**

To EACH of the 4 copied files, prepend (replace `<SHA>` with the Step-1 SHA, `<DATE>` with today's date):
```python
# VENDORED from https://github.com/xiaowu0162/LongMemEval @ <SHA>
# Copied <DATE> — pinned for reproducible scoring. Do NOT edit except to update pin.
# Upstream license: MIT. Original paths:
#   evaluate_qa.py, print_qa_metrics.py, print_retrieval_metrics.py -> src/evaluation/
#   eval_utils.py -> src/retrieval/
```
Use Edit to insert at the top of each file (after reading it once).

- [ ] **Step 4: Create the upstream_eval package marker**

Create `benchmarks/longmemeval/upstream_eval/__init__.py`:
```python
"""Vendored LongMemEval evaluation scripts (pinned commit). See each file header."""
```

- [ ] **Step 5: Write the failing test**

Create `tests/benchmarks/__init__.py` (empty), `tests/benchmarks/longmemeval/__init__.py` (empty), then `tests/benchmarks/longmemeval/test_upstream_vendor.py`:
```python
import importlib
import pathlib

VENDORED = ["evaluate_qa", "print_qa_metrics", "print_retrieval_metrics", "eval_utils"]


def test_each_vendored_file_has_pin_header():
    d = pathlib.Path("benchmarks/longmemeval/upstream_eval")
    for name in VENDORED:
        text = (d / f"{name}.py").read_text()
        assert "VENDORED from" in text, f"{name}.py missing provenance header"
        assert "@" in text, f"{name}.py missing commit SHA"


def test_modules_import():
    for name in VENDORED:
        importlib.import_module(f"benchmarks.longmemeval.upstream_eval.{name}")
```

- [ ] **Step 6: Run test — expect PASS**

Run: `python -m pytest tests/benchmarks/longmemeval/test_upstream_vendor.py -v`
Expected: 2 passed. (If `eval_utils.py` imports `numpy`, ensure it's installed: `pip install numpy`.)

- [ ] **Step 7: Commit**

```bash
git add benchmarks/longmemeval/upstream_eval/ tests/benchmarks/
git commit -m "feat(bench): vendor LongMemEval eval scripts (pinned SHA)"
```

---

### Task 3: BenchConfig + dir derivation

**Files:**
- Create: `benchmarks/longmemeval/config.py`
- Test: `tests/benchmarks/longmemeval/test_config.py`

**Interfaces:** Produces:
```python
@dataclass
class BenchConfig:
    difficulty: Literal["oracle","s","m"]
    variant: Literal["raw","consolidated"]
    limit: int | None
    top_k: int
    base_dir: Path
    # derived read-only properties: data_dir, db_dir, results_dir (Path)
```
Consumed by: all later tasks (`cfg.db_dir`, `cfg.results_dir`, `cfg.data_dir`, `cfg.top_k`).

- [ ] **Step 1: Write the failing test**

Create `tests/benchmarks/longmemeval/test_config.py`:
```python
from pathlib import Path
from benchmarks.longmemeval.config import BenchConfig


def test_dirs_derive_from_difficulty_variant(tmp_path):
    cfg = BenchConfig(difficulty="s", variant="consolidated", base_dir=tmp_path)
    assert cfg.data_dir == tmp_path / "data"
    assert cfg.db_dir == tmp_path / "db" / "s" / "consolidated"
    assert cfg.results_dir == tmp_path / "results" / "s" / "consolidated"


def test_defaults():
    cfg = BenchConfig(base_dir=Path("/tmp/x"))
    assert cfg.difficulty == "s"
    assert cfg.variant == "raw"
    assert cfg.limit is None
    assert cfg.top_k == 50


def test_db_path_per_instance(tmp_path):
    cfg = BenchConfig(base_dir=tmp_path)
    p = cfg.db_path_for("q_1")
    assert p == tmp_path / "db" / "s" / "raw" / "q_1.db"
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `python -m pytest tests/benchmarks/longmemeval/test_config.py -v`
Expected: FAIL (ModuleNotFoundError / ImportError)

- [ ] **Step 3: Implement config.py**

Create `benchmarks/longmemeval/config.py`:
```python
"""Configuration for the LongMemEval harness."""
from __future__ import annotations
from dataclasses import dataclass
from pathlib import Path
from typing import Literal


@dataclass
class BenchConfig:
    difficulty: Literal["oracle", "s", "m"] = "s"
    variant: Literal["raw", "consolidated"] = "raw"
    limit: int | None = None          # None = all 500; int = dev subset
    top_k: int = 50                   # retrieval-eval needs @50; QA uses 10 via override
    base_dir: Path = Path("benchmarks/.cache")

    @property
    def data_dir(self) -> Path:
        return self.base_dir / "data"

    @property
    def db_dir(self) -> Path:
        return self.base_dir / "db" / self.difficulty / self.variant

    @property
    def results_dir(self) -> Path:
        return self.base_dir / "results" / self.difficulty / self.variant

    def db_path_for(self, question_id: str) -> Path:
        return self.db_dir / f"{question_id}.db"
```

- [ ] **Step 4: Run test — expect PASS**

Run: `python -m pytest tests/benchmarks/longmemeval/test_config.py -v`
Expected: 3 passed

- [ ] **Step 5: Commit**

```bash
git add benchmarks/longmemeval/config.py tests/benchmarks/longmemeval/test_config.py
git commit -m "feat(bench): add BenchConfig with dir derivation"
```

---

### Task 4: download_data.py (HuggingFace + hash check)

**Files:**
- Create: `benchmarks/longmemeval/download_data.py`
- Test: `tests/benchmarks/longmemeval/test_download.py`

**Interfaces:** Produces:
```python
FILES: dict[str, str]  # filename -> HuggingFace resolve URL
def expected_size(name: str) -> int  # known byte size; mismatch raises
def download(cfg: BenchConfig, *, force: bool = False) -> dict[str, Path]
# idempotent: skips files that exist + pass size check
```
Consumed by: ingest/run tasks (call `download(cfg)` first).

- [ ] **Step 1: Capture the real file sizes**

Run:
```bash
for f in longmemeval_oracle.json longmemeval_s_cleaned.json longmemeval_m_cleaned.json; do
  curl -sI "https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/main/$f" | grep -i content-length
done
```
Record each byte count; use them in Step 3's `EXPECTED_SIZES`.

- [ ] **Step 2: Write the failing test (uses monkeypatched requests, no network)**

Create `tests/benchmarks/longmemeval/test_download.py`:
```python
from pathlib import Path
import json
from benchmarks.longmemeval.config import BenchConfig
from benchmarks.longmemeval import download_data


def test_download_skips_existing_valid(tmp_path, monkeypatch):
    cfg = BenchConfig(difficulty="oracle", base_dir=tmp_path)
    cfg.data_dir.mkdir(parents=True, exist_ok=True)
    # pre-place a valid-sized file
    target = cfg.data_dir / "longmemeval_oracle.json"
    payload = json.dumps([{"question_id": "x"}])
    target.write_text(payload)
    monkeypatch.setattr(download_data, "EXPECTED_SIZES",
                        {"longmemeval_oracle.json": len(payload.encode())})
    monkeypatch.setattr(download_data, "_http_get", lambda url: (_ for _ in ()).throw(AssertionError("should not download")))
    got = download_data.download(cfg)
    assert got["longmemeval_oracle.json"] == target


def test_download_rejects_wrong_size(tmp_path, monkeypatch):
    cfg = BenchConfig(difficulty="oracle", base_dir=tmp_path)
    cfg.data_dir.mkdir(parents=True, exist_ok=True)
    (cfg.data_dir / "longmemeval_oracle.json").write_text("too small")
    monkeypatch.setattr(download_data, "EXPECTED_SIZES",
                        {"longmemeval_oracle.json": 999999})
    try:
        download_data.download(cfg)
        assert False, "should have raised"
    except (ValueError, RuntimeError):
        pass
```

- [ ] **Step 3: Run test — expect FAIL**

Run: `python -m pytest tests/benchmarks/longmemeval/test_download.py -v`
Expected: FAIL (import error)

- [ ] **Step 4: Implement download_data.py**

Create `benchmarks/longmemeval/download_data.py`:
```python
"""Download LongMemEval data from HuggingFace with size verification."""
from __future__ import annotations
import requests
from pathlib import Path
from .config import BenchConfig

HF_BASE = "https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/main"

FILES = {
    "longmemeval_oracle.json": f"{HF_BASE}/longmemeval_oracle.json",
    "longmemeval_s_cleaned.json": f"{HF_BASE}/longmemeval_s_cleaned.json",
    "longmemeval_m_cleaned.json": f"{HF_BASE}/longmemeval_m_cleaned.json",
}

# Filled in Task 4 Step 1 from `curl -sI` content-length. Mismatch => data drifted.
EXPECTED_SIZES: dict[str, int] = {
    "longmemeval_oracle.json": 0,        # TODO replace with real value from Step 1
    "longmemeval_s_cleaned.json": 0,
    "longmemeval_m_cleaned.json": 0,
}

# difficulty -> which file
DIFFICULTY_FILE = {"oracle": "longmemeval_oracle.json", "s": "longmemeval_s_cleaned.json", "m": "longmemeval_m_cleaned.json"}


def _http_get(url: str) -> bytes:
    r = requests.get(url, timeout=120)
    r.raise_for_status()
    return r.content


def download(cfg: BenchConfig, *, force: bool = False) -> dict[str, Path]:
    """Fetch this difficulty's JSON if missing/invalid. Returns {name: Path}."""
    cfg.data_dir.mkdir(parents=True, exist_ok=True)
    name = DIFFICULTY_FILE[cfg.difficulty]
    target = cfg.data_dir / name
    expected = EXPECTED_SIZES[name]
    if target.exists() and not force:
        if expected and target.stat().st_size == expected:
            return {name: target}
        # wrong size -> stale; fall through to re-download
    content = _http_get(FILES[name])
    if expected and len(content) != expected:
        raise RuntimeError(
            f"{name}: size {len(content)} != expected {expected}; "
            f"upstream data may have changed — verify and update EXPECTED_SIZES."
        )
    target.write_bytes(content)
    return {name: target}
```
**IMPORTANT:** replace the three `0` placeholders in `EXPECTED_SIZES` with the real byte counts captured in Step 1 before committing. If a size is genuinely unknown, leave `0` (the `if expected and ...` guards treat `0` as "skip check"), but prefer a real value.

- [ ] **Step 5: Run test — expect PASS**

Run: `python -m pytest tests/benchmarks/longmemeval/test_download.py -v`
Expected: 2 passed

- [ ] **Step 6: Manual smoke (real network, opt-in)**

Run: `python -c "from benchmarks.longmemeval.config import BenchConfig; from benchmarks.longmemeval import download_data; print(download_data.download(BenchConfig(difficulty='oracle')))"`
Expected: downloads + prints the oracle path. (Skip if offline.)

- [ ] **Step 7: Commit**

```bash
git add benchmarks/longmemeval/download_data.py tests/benchmarks/longmemeval/test_download.py
git commit -m "feat(bench): add HuggingFace data download with size check"
```

---

### Task 5: Synthetic test fixture

**Files:**
- Create: `tests/benchmarks/longmemeval/fixtures.py`

**Interfaces:** Produces:
```python
def make_mini_instance() -> dict   # one LongMemEval-shaped instance, 2 sessions, hand-annotated
```
Consumed by: ingest/run/metrics tests (deterministic, no network, no API key).

- [ ] **Step 1: Write the fixture**

Create `tests/benchmarks/longmemeval/fixtures.py`:
```python
"""Synthetic LongMemEval-shaped instance for offline unit tests.

Matches the real schema exactly so ingest/metrics code is identical to production.
Two sessions; session 2 has a knowledge-update (supersedes session 1's value).
Gold: answer_session_ids=['sess_1']; evidence turn marked has_answer.
"""
import copy


def make_mini_instance() -> dict:
    return {
        "question_id": "mini_1",
        "question_type": "single-session-user",
        "question": "What is Alice's favorite color?",
        "answer": "blue",
        "question_date": "2024-02-02",
        "haystack_session_ids": ["sess_1", "sess_2"],
        "haystack_dates": ["2024-01-01", "2024-02-01"],
        "haystack_sessions": [
            [  # sess_1
                {"role": "user", "content": "I love the color blue.", "has_answer": True},
                {"role": "assistant", "content": "Noted, blue it is."},
            ],
            [  # sess_2 (knowledge update: now green)
                {"role": "user", "content": "Actually I changed my mind, green is nicer."},
                {"role": "assistant", "content": "Got it, green now."},
            ],
        ],
        "answer_session_ids": ["sess_1"],
    }


def make_mini_dataset() -> list[dict]:
    """Two-instance list incl. an abstention question."""
    base = make_mini_instance()
    abstention = copy.deepcopy(base)
    abstention["question_id"] = "mini_2_abs"
    abstention["question"] = "What is Alice's shoe size?"
    abstention["answer"] = "The question is unanswerable."
    abstention["answer_session_ids"] = []
    abstention["question_type"] = "single-session-user"
    return [base, abstention]
```

- [ ] **Step 2: Verify it loads**

Run: `python -c "from tests.benchmarks.longmemeval.fixtures import make_mini_dataset; import json; print(json.dumps(make_mini_dataset()[0]['question_id']))"`
Expected: prints `mini_1`

- [ ] **Step 3: Commit**

```bash
git add tests/benchmarks/longmemeval/fixtures.py
git commit -m "test(bench): add synthetic LongMemEval fixture"
```

---

### Task 6: ingest.py — raw variant (record_event per turn)

**Files:**
- Create: `benchmarks/longmemeval/ingest.py`
- Test: `tests/benchmarks/longmemeval/test_ingest.py`

**Interfaces:** Produces:
```python
def ingest_instance(instance: dict, cfg: BenchConfig, *, engine_factory=None, force: bool = False) -> Path
# opens Engine(db_path=cfg.db_path_for(qid), workspace=f"lme-{qid}")
# record_event per turn (timestamp order), metadata={session_id,date,turn_idx,doc_id,has_answer}
# variant=="raw": no further action. variant=="consolidated": run eng.consolidate() (Task 7)
# skips if DB exists + memory count matches expected (unless force)
```
`engine_factory`: injection point for tests (defaults to `lambda db_path, workspace: Engine(Config(db_path=..., workspace=...))`). Consumes `BenchConfig` (Task 3).

- [ ] **Step 1: Write the failing test**

Create `tests/benchmarks/longmemeval/test_ingest.py`:
```python
from benchmarks.longmemeval.config import BenchConfig
from benchmarks.longmemeval import ingest
from tests.benchmarks.longmemeval.fixtures import make_mini_instance


class _FakeMemory:
    def __init__(self, metadata):
        self.metadata = metadata


class _FakeEngine:
    def __init__(self):
        self.recorded = []
        self.consolidated = False
    def record_event(self, *, agent, action, observation="", metadata=None, **kw):
        self.recorded.append({"agent": agent, "action": action, "metadata": metadata or {}})
    def consolidate(self, **kw):
        self.consolidated = True
    def stats(self):
        class S:
            total_memories = len(self.recorded)
        return S()
    def close(self):
        pass


def test_ingest_records_every_turn_in_order(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    inst = make_mini_instance()
    fake = _FakeEngine()
    ingest.ingest_instance(inst, cfg, engine_factory=lambda **kw: fake)
    assert len(fake.recorded) == 4  # 2 sessions x 2 turns
    # timestamp order: sess_1 before sess_2
    assert fake.recorded[0]["metadata"]["session_id"] == "sess_1"
    assert fake.recorded[2]["metadata"]["session_id"] == "sess_2"
    # doc_id convention
    assert fake.recorded[0]["metadata"]["doc_id"] == "sess_1_0"
    # has_answer propagated
    assert fake.recorded[0]["metadata"]["has_answer"] is True
    assert fake.recorded[1]["metadata"]["has_answer"] is False
    # raw variant must NOT consolidate
    assert fake.consolidated is False


def test_ingest_user_assistant_agent(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    fake = _FakeEngine()
    ingest.ingest_instance(make_mini_instance(), cfg, engine_factory=lambda **kw: fake)
    assert fake.recorded[0]["agent"] == "user"
    assert fake.recorded[1]["agent"] == "assistant"
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `python -m pytest tests/benchmarks/longmemeval/test_ingest.py -v`
Expected: FAIL (ModuleNotFoundError)

- [ ] **Step 3: Implement ingest.py**

Create `benchmarks/longmemeval/ingest.py`:
```python
"""Ingest a LongMemEval instance's haystack into a per-instance LadyM DB."""
from __future__ import annotations
from pathlib import Path
from .config import BenchConfig


def _default_engine_factory(db_path, workspace):
    from ladym import Engine, Config
    return Engine(Config(db_path=str(db_path), workspace=workspace))


def _expected_turn_count(instance: dict) -> int:
    return sum(len(sess) for sess in instance["haystack_sessions"])


def ingest_instance(instance: dict, cfg: BenchConfig, *,
                    engine_factory=_default_engine_factory, force: bool = False) -> Path:
    """Write every haystack turn as an episodic memory. Returns the DB path.

    Skips if the DB already holds the expected turn count (unless force).
    variant=='consolidated' runs eng.consolidate() after ingest.
    """
    qid = instance["question_id"]
    db_path = cfg.db_path_for(qid)
    db_path.parent.mkdir(parents=True, exist_ok=True)
    expected = _expected_turn_count(instance)

    if db_path.exists() and not force:
        if _count_memories(engine_factory, db_path, qid) == expected:
            return db_path
        # stale/partial -> rebuild by removing
        db_path.unlink(missing_ok=True)

    eng = engine_factory(db_path=db_path, workspace=f"lme-{qid}")
    try:
        sids = instance["haystack_session_ids"]
        dates = instance["haystack_dates"]
        sessions = instance["haystack_sessions"]
        # pair sessions with their id+date, sort by date (stable)
        ordered = sorted(zip(sids, dates, sessions), key=lambda t: str(t[1]))
        for sid, date, turns in ordered:
            for turn_idx, turn in enumerate(turns):
                eng.record_event(
                    agent=turn["role"],
                    action=turn["content"],
                    observation="",
                    metadata={
                        "session_id": sid,
                        "date": str(date),
                        "turn_idx": turn_idx,
                        "doc_id": f"{sid}_{turn_idx}",
                        "has_answer": bool(turn.get("has_answer", False)),
                    },
                )
        if cfg.variant == "consolidated":
            eng.consolidate()
    finally:
        eng.close()
    return db_path


def _count_memories(engine_factory, db_path, qid) -> int:
    eng = engine_factory(db_path=db_path, workspace=f"lme-{qid}")
    try:
        return eng.stats().total_memories
    finally:
        eng.close()
```

- [ ] **Step 4: Run test — expect PASS**

Run: `python -m pytest tests/benchmarks/longmemeval/test_ingest.py -v`
Expected: 2 passed

- [ ] **Step 5: Commit**

```bash
git add benchmarks/longmemeval/ingest.py tests/benchmarks/longmemeval/test_ingest.py
git commit -m "feat(bench): per-instance episodic ingest (raw variant)"
```

---

### Task 7: ingest.py — consolidated variant

**Files:**
- Modify: `benchmarks/longmemeval/ingest.py` (already calls consolidate; this task adds the test + cache-count adjustment)
- Test: `tests/benchmarks/longmemeval/test_ingest.py` (append)

**Interfaces:** No new public surface — `ingest_instance` with `cfg.variant=="consolidated"` now runs `eng.consolidate()`. The cache skip-check is disabled for the consolidated variant (memory count changes post-consolidation; a separate marker guards re-runs).

- [ ] **Step 1: Write the failing test**

Append to `tests/benchmarks/longmemeval/test_ingest.py`:
```python
def test_consolidated_variant_runs_consolidate(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="consolidated", base_dir=tmp_path)
    fake = _FakeEngine()
    # consolidated path uses a marker file, not memory-count skip
    ingest.ingest_instance(make_mini_instance(), cfg, engine_factory=lambda **kw: fake)
    assert fake.consolidated is True
```

- [ ] **Step 2: Run test — expect FAIL or PASS-if-already-correct**

Run: `python -m pytest tests/benchmarks/longmemeval/test_ingest.py::test_consolidated_variant_runs_consolidate -v`
Expected: PASS if Task 6's `if cfg.variant == "consolidated": eng.consolidate()` is in place. If it fails, the consolidated branch is missing — fix in Step 3.

- [ ] **Step 3 (if needed): Adjust cache-skip to use a marker for consolidated**

The raw skip-check (memory count == turn count) is WRONG for consolidated (count changes). Edit `ingest_instance`: for `variant=="consolidated"`, skip only if a marker file `db_path.with_suffix(".done")` exists; write it after a successful consolidated ingest. For `raw`, keep the count check. Concretely, replace the skip block:
```python
    done_marker = db_path.with_suffix(".done")
    if not force:
        if cfg.variant == "consolidated" and done_marker.exists():
            return db_path
        if cfg.variant == "raw" and db_path.exists() \
                and _count_memories(engine_factory, db_path, qid) == expected:
            return db_path
    if db_path.exists():
        db_path.unlink(missing_ok=True)
```
and after the `try/finally` ingest, for consolidated add:
```python
    if cfg.variant == "consolidated":
        done_marker.touch()
```

- [ ] **Step 4: Run all ingest tests — expect PASS**

Run: `python -m pytest tests/benchmarks/longmemeval/test_ingest.py -v`
Expected: 3 passed

- [ ] **Step 5: Commit**

```bash
git add benchmarks/longmemeval/ingest.py tests/benchmarks/longmemeval/test_ingest.py
git commit -m "feat(bench): consolidated variant runs eng.consolidate + marker cache"
```

---

### Task 8: metrics.py — retrieval metric primitives + dict builder

**Files:**
- Create: `benchmarks/longmemeval/metrics.py`
- Test: `tests/benchmarks/longmemeval/test_metrics.py`

**Interfaces:** Produces:
```python
def recall_all(recalled_ids: list[str], gold: set[str], k: int) -> float
def ndcg(recalled_ids: list[str], gold: set[str], k: int) -> float
def build_metric_dict(
    recalled_turn_doc_ids: list[str],   # ranked, len>=50 for @50
    gold_turn_doc_ids: set[str],
    gold_session_ids: set[str],
    recalled_session_ids_ordered: list[str],  # sessions in first-encounter order
) -> dict   # {"session": {...}, "turn": {...}} matching upstream schema
```
Formulas attributed to vendored `upstream_eval/eval_utils.py`. Consumed by `run_retrieval.py` (Task 9).

Output schema (must match `print_retrieval_metrics.py` exactly):
```
{"session": {"recall_all@5","ndcg_any@5","recall_all@10","ndcg_any@10"},
 "turn":    {"recall_all@5","ndcg_any@5","recall_all@10","ndcg_any@10","recall_all@50","ndcg_any@50"}}
```

- [ ] **Step 1: Write the failing test**

Create `tests/benchmarks/longmemeval/test_metrics.py`:
```python
from benchmarks.longmemeval.metrics import recall_all, ndcg, build_metric_dict


def test_recall_all_all_gold_present():
    assert recall_all(["a", "b", "c"], {"a", "b"}, k=3) == 1.0


def test_recall_all_missing_one():
    assert recall_all(["a", "x", "c"], {"a", "b"}, k=3) == 0.0


def test_recall_all_respects_k():
    # gold at position beyond k -> 0
    assert recall_all(["x", "y", "a"], {"a"}, k=2) == 0.0
    assert recall_all(["x", "y", "a"], {"a"}, k=3) == 1.0


def test_ndcg_perfect_ranking():
    # both gold first -> ndcg 1.0
    assert ndcg(["a", "b", "x"], {"a", "b"}, k=2) == 1.0


def test_ndcg_worst_ranking():
    # gold at the very end of top-k -> low but > 0
    val = ndcg(["x", "y", "a"], {"a"}, k=3)
    assert 0.0 < val < 1.0


def test_build_metric_dict_schema():
    turns = ["sess_1_0", "sess_2_0", "x_0"] * 20  # enough for @50
    d = build_metric_dict(
        recalled_turn_doc_ids=turns,
        gold_turn_doc_ids={"sess_1_0"},
        gold_session_ids={"sess_1"},
        recalled_session_ids_ordered=["sess_1", "sess_2", "x"],
    )
    assert set(d.keys()) == {"session", "turn"}
    assert set(d["session"]) == {"recall_all@5", "ndcg_any@5", "recall_all@10", "ndcg_any@10"}
    assert set(d["turn"]) == {"recall_all@5", "ndcg_any@5", "recall_all@10",
                              "ndcg_any@10", "recall_all@50", "ndcg_any@50"}
    # sess_1 is gold and ranked first -> recall_all@5 == 1.0
    assert d["session"]["recall_all@5"] == 1.0
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `python -m pytest tests/benchmarks/longmemeval/test_metrics.py -v`
Expected: FAIL (import error)

- [ ] **Step 3: Implement metrics.py**

Create `benchmarks/longmemeval/metrics.py`:
```python
"""Retrieval metrics matching the LongMemEval upstream schema.

Formulas attributed to vendored ``upstream_eval/eval_utils.py`` (dcg/ndcg/recall_any/recall_all).
Adapted to a doc-id-ranked interface (upstream uses integer indices into a corpus).
"""
from __future__ import annotations
import math


def recall_all(recalled_ids: list[str], gold: set[str], k: int) -> float:
    """1.0 iff every gold id appears in the top-k recalled, else 0.0."""
    if not gold:
        return 0.0
    topk = set(recalled_ids[:k])
    return 1.0 if all(g in topk for g in gold) else 0.0


def _dcg(relevances: list[float]) -> float:
    if not relevances:
        return 0.0
    total = relevances[0]
    for i, r in enumerate(relevances[1:], start=2):
        total += r / math.log2(i)
    return total


def ndcg(recalled_ids: list[str], gold: set[str], k: int) -> float:
    """Standard NDCG@k with binary relevance. Ideal = all gold ranked first."""
    if not gold:
        return 0.0
    topk = recalled_ids[:k]
    rel = [1.0 if c in gold else 0.0 for c in topk]
    ideal = [1.0] * min(len(gold), k)
    idcg = _dcg(ideal)
    if idcg == 0.0:
        return 0.0
    return _dcg(rel) / idcg


_TURN_KS = [5, 10, 50]
_SESSION_KS = [5, 10]


def build_metric_dict(
    recalled_turn_doc_ids: list[str],
    gold_turn_doc_ids: set[str],
    gold_session_ids: set[str],
    recalled_session_ids_ordered: list[str],
) -> dict:
    """Build the {"session":..., "turn":...} dict matching print_retrieval_metrics.py input."""
    turn = {}
    for k in _TURN_KS:
        turn[f"recall_all@{k}"] = recall_all(recalled_turn_doc_ids, gold_turn_doc_ids, k)
        turn[f"ndcg_any@{k}"] = ndcg(recalled_turn_doc_ids, gold_turn_doc_ids, k)
    session = {}
    for k in _SESSION_KS:
        session[f"recall_all@{k}"] = recall_all(recalled_session_ids_ordered, gold_session_ids, k)
        session[f"ndcg_any@{k}"] = ndcg(recalled_session_ids_ordered, gold_session_ids, k)
    return {"session": session, "turn": turn}
```

- [ ] **Step 4: Run test — expect PASS**

Run: `python -m pytest tests/benchmarks/longmemeval/test_metrics.py -v`
Expected: 6 passed

- [ ] **Step 5: Cross-check against vendored eval_utils.py**

Open `benchmarks/longmemeval/upstream_eval/eval_utils.py` and confirm: its `recall_all` = `all(doc in recalled_docs ...)`, its `ndcg` = dcg(actual)/dcg(sorted desc). Verify your `_dcg` matches its `dcg` (position-1 full weight, position-n divided by log2(n)). If the vendored dcg uses `np.arange(2, size+1)` note that's `[2,3,...,size]` — identical to your `enumerate(start=2)`. No code change if they agree; this is a read-only audit step.

- [ ] **Step 6: Commit**

```bash
git add benchmarks/longmemeval/metrics.py tests/benchmarks/longmemeval/test_metrics.py
git commit -m "feat(bench): retrieval metrics matching upstream schema"
```

---

### Task 9: run_retrieval.py (Phase A)

**Files:**
- Create: `benchmarks/longmemeval/run_retrieval.py`
- Test: `tests/benchmarks/longmemeval/test_run_retrieval.py`

**Interfaces:** Produces:
```python
def run_retrieval(dataset: list[dict], cfg: BenchConfig, *, engine_factory=None) -> Path
# for each non-abstention instance: open DB, recall(question, top_k=cfg.top_k),
# map recalled memories -> doc_ids + session_ids (first-encounter order),
# compute build_metric_dict, emit {question_id, retrieval_results:{metrics:...}} per line.
# writes cfg.results_dir/"retrieval.jsonl"; returns that path.
```
Consumes: `ingest` (DBs must exist), `metrics.build_metric_dict`, `BenchConfig`.

- [ ] **Step 1: Write the failing test**

Create `tests/benchmarks/longmemeval/test_run_retrieval.py`:
```python
import json
from benchmarks.longmemeval.config import BenchConfig
from benchmarks.longmemeval import run_retrieval
from tests.benchmarks.longmemeval.fixtures import make_mini_dataset


class _FakeResult:
    def __init__(self, metadata):
        self.metadata = metadata


class _FakeRecallResp:
    def __init__(self, metas):
        self.results = [_FakeResult(m) for m in metas]


class _FakeEngine:
    def __init__(self, metas_by_qid):
        self._m = metas_by_qid
    def recall(self, query, **kw):
        # hand back a fixed ranked list for whichever qid is being queried
        return _FakeRecallResp(self._m["_current"])
    def close(self):
        pass


def test_run_retrieval_emits_schema(tmp_path, monkeypatch):
    cfg = BenchConfig(difficulty="oracle", variant="raw", top_k=50, base_dir=tmp_path)
    data = make_mini_dataset()
    # recall returns: gold turn first, then a distractor
    metas = [{"doc_id": "sess_1_0", "session_id": "sess_1"},
             {"doc_id": "sess_2_0", "session_id": "sess_2"}] * 25
    fake = _FakeEngine({})
    def factory(**kw):
        fake._m["_current"] = metas
        return fake
    out = run_retrieval.run_retrieval(data, cfg, engine_factory=factory)
    lines = [json.loads(l) for l in out.read_text().splitlines()]
    # abstention (mini_2_abs) is EXCLUDED
    ids = [l["question_id"] for l in lines]
    assert ids == ["mini_1"]
    m = lines[0]["retrieval_results"]["metrics"]
    assert set(m) == {"session", "turn"}
    assert "recall_all@50" in m["turn"]
    assert m["session"]["recall_all@5"] == 1.0  # sess_1 gold, ranked first
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `python -m pytest tests/benchmarks/longmemeval/test_run_retrieval.py -v`
Expected: FAIL (import error)

- [ ] **Step 3: Implement run_retrieval.py**

Create `benchmarks/longmemeval/run_retrieval.py`:
```python
"""Phase A: recall per question, emit retrieval.jsonl in upstream metric format."""
from __future__ import annotations
import json
from pathlib import Path
from .config import BenchConfig
from .metrics import build_metric_dict


def _default_engine_factory(db_path, workspace):
    from ladym import Engine, Config
    return Engine(Config(db_path=str(db_path), workspace=workspace))


def run_retrieval(dataset: list[dict], cfg: BenchConfig, *,
                  engine_factory=_default_engine_factory) -> Path:
    cfg.results_dir.mkdir(parents=True, exist_ok=True)
    out = cfg.results_dir / "retrieval.jsonl"
    with out.open("w") as f:
        for instance in dataset:
            qid = instance["question_id"]
            if "_abs" in qid:           # abstention: skip retrieval eval (upstream convention)
                continue
            db_path = cfg.db_path_for(qid)
            eng = engine_factory(db_path=db_path, workspace=f"lme-{qid}")
            try:
                resp = eng.recall(instance["question"], top_k=cfg.top_k)
            finally:
                eng.close()
            recalled = [r.metadata for r in resp.results]
            recalled_turn_doc_ids = [m.get("doc_id", "") for m in recalled]
            # session ids in first-encounter order (dedup, preserve rank)
            seen = set()
            recalled_session_ids_ordered = []
            for m in recalled:
                sid = m.get("session_id", "")
                if sid and sid not in seen:
                    seen.add(sid)
                    recalled_session_ids_ordered.append(sid)
            # gold: evidence turns (has_answer) + evidence sessions
            gold_turn_doc_ids = _gold_turn_doc_ids(instance)
            gold_session_ids = set(instance.get("answer_session_ids", []))
            metrics = build_metric_dict(
                recalled_turn_doc_ids, gold_turn_doc_ids,
                gold_session_ids, recalled_session_ids_ordered,
            )
            f.write(json.dumps({
                "question_id": qid,
                "retrieval_results": {"metrics": metrics},
            }) + "\n")
    return out


def _gold_turn_doc_ids(instance: dict) -> set[str]:
    ids = set()
    for sid, turns in zip(instance["haystack_session_ids"], instance["haystack_sessions"]):
        for turn_idx, turn in enumerate(turns):
            if turn.get("has_answer"):
                ids.add(f"{sid}_{turn_idx}")
    return ids
```

- [ ] **Step 4: Run test — expect PASS**

Run: `python -m pytest tests/benchmarks/longmemeval/test_run_retrieval.py -v`
Expected: 1 passed

- [ ] **Step 5: Commit**

```bash
git add benchmarks/longmemeval/run_retrieval.py tests/benchmarks/longmemeval/test_run_retrieval.py
git commit -m "feat(bench): Phase A retrieval run → upstream metric jsonl"
```

---

### Task 10: run_qa.py (Phase B)

**Files:**
- Create: `benchmarks/longmemeval/run_qa.py`
- Test: `tests/benchmarks/longmemeval/test_run_qa.py`

**Interfaces:** Produces:
```python
def run_qa(dataset: list[dict], cfg: BenchConfig, *, engine_factory=None,
           answer_llm=None, top_k_context: int = 10) -> Path
# recall(question, top_k=top_k_context) -> build RAG context -> answer_llm(prompt) -> hypothesis
# abstention (_abs): if best recall score < ABSTAIN_SCORE_FLOOR -> hypothesis = "I don't know."
# writes cfg.results_dir/"hypothesis.jsonl": {question_id, hypothesis}
```
`answer_llm`: callable `(system, user) -> str`. If `None`, build from ladyM config provider via `ModelRouting` (production); tests inject a fake. `ABSTAIN_SCORE_FLOOR = 0.05`.

- [ ] **Step 1: Write the failing test**

Create `tests/benchmarks/longmemeval/test_run_qa.py`:
```python
import json
from benchmarks.longmemeval.config import BenchConfig
from benchmarks.longmemeval import run_qa
from tests.benchmarks.longmemeval.fixtures import make_mini_dataset


class _FakeResult:
    def __init__(self, content, score):
        self.content = content
        self.score = score


class _FakeResp:
    def __init__(self, results):
        self.results = results


class _FakeEngine:
    def __init__(self, results):
        self._r = results
    def recall(self, query, **kw):
        return _FakeResp(self._r)
    def close(self):
        pass


def test_run_qa_emits_hypothesis(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    data = make_mini_dataset()
    res = [_FakeResult("user said blue", 0.9), _FakeResult("assistant agreed", 0.5)]
    eng = _FakeEngine(res)
    calls = []
    def fake_llm(system, user):
        calls.append(user)
        return "blue"
    out = run_qa.run_qa(data, cfg, engine_factory=lambda **kw: eng, answer_llm=fake_llm)
    lines = [json.loads(l) for l in out.read_text().splitlines()]
    assert {l["question_id"] for l in lines} == {"mini_1", "mini_2_abs"}
    hyps = {l["question_id"]: l["hypothesis"] for l in lines}
    assert hyps["mini_1"] == "blue"


def test_run_qa_abstention_when_low_score(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    data = make_mini_dataset()
    # abstention q with near-zero recall score -> "I don't know."
    res = [_FakeResult("irrelevant", 0.01)]
    eng = _FakeEngine(res)
    called = []
    out = run_qa.run_qa(data, cfg, engine_factory=lambda **kw: eng,
                         answer_llm=lambda s, u: (called.append(1), "no")[1])
    lines = {json.loads(l)["question_id"]: json.loads(l)["hypothesis"]
             for l in out.read_text().splitlines()}
    assert lines["mini_2_abs"] == "I don't know."
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `python -m pytest tests/benchmarks/longmemeval/test_run_qa.py -v`
Expected: FAIL (import error)

- [ ] **Step 3: Implement run_qa.py**

Create `benchmarks/longmemeval/run_qa.py`:
```python
"""Phase B: RAG recall + answer-LLM -> hypothesis.jsonl."""
from __future__ import annotations
import json
from pathlib import Path
from .config import BenchConfig

ABSTAIN_SCORE_FLOOR = 0.05


def _default_engine_factory(db_path, workspace):
    from ladym import Engine, Config
    return Engine(Config(db_path=str(db_path), workspace=workspace))


def _default_answer_llm():
    """Build answer-LLM from ladyM config provider (ModelRouting-injectable)."""
    from ladym import Config
    from ladym.providers import make_agent
    cfg = Config()
    agent = make_agent(cfg, "consolidate")   # reuse the same op/provider as consolidate
    def _call(system: str, user: str) -> str:
        msgs = [{"role": "system", "content": system}, {"role": "user", "content": user}]
        return agent.complete(msgs)
    return _call


_SYSTEM = ("You answer the user's question using ONLY the provided memory context. "
           "If the context does not contain the answer, say 'I don't know.'")
_ABSTAIN = "I don't know."


def run_qa(dataset: list[dict], cfg: BenchConfig, *,
           engine_factory=_default_engine_factory, answer_llm=None,
           top_k_context: int = 10) -> Path:
    if answer_llm is None:
        answer_llm = _default_answer_llm()
    cfg.results_dir.mkdir(parents=True, exist_ok=True)
    out = cfg.results_dir / "hypothesis.jsonl"
    with out.open("w") as f:
        for instance in dataset:
            qid = instance["question_id"]
            eng = engine_factory(db_path=cfg.db_path_for(qid), workspace=f"lme-{qid}")
            try:
                resp = eng.recall(instance["question"], top_k=top_k_context)
            finally:
                eng.close()
            results = resp.results or []
            best_score = results[0].score if results else 0.0
            if "_abs" in qid and best_score < ABSTAIN_SCORE_FLOOR:
                hypothesis = _ABSTAIN
            else:
                context = "\n".join(
                    f"[{r.metadata.get('date','')}] {r.metadata.get('session_id','')}: {r.content}"
                    for r in results
                ) or "(no relevant memories)"
                user = f"Memory context:\n{context}\n\nQuestion: {instance['question']}"
                hypothesis = answer_llm(_SYSTEM, user)
            f.write(json.dumps({"question_id": qid, "hypothesis": hypothesis}) + "\n")
    return out
```

- [ ] **Step 4: Run test — expect PASS**

Run: `python -m pytest tests/benchmarks/longmemeval/test_run_qa.py -v`
Expected: 2 passed

- [ ] **Step 5: Commit**

```bash
git add benchmarks/longmemeval/run_qa.py tests/benchmarks/longmemeval/test_run_qa.py
git commit -m "feat(bench): Phase B RAG QA run → hypothesis.jsonl"
```

---

### Task 11: evaluate.py (wrap vendored scripts → scores.md)

**Files:**
- Create: `benchmarks/longmemeval/evaluate.py`
- Test: `tests/benchmarks/longmemeval/test_evaluate.py`

**Interfaces:** Produces:
```python
def evaluate(cfg: BenchConfig, dataset_path: Path, *, judge_model: str = "gpt-4o") -> Path
# 1. retrieval: subprocess vendored print_retrieval_metrics.py on retrieval.jsonl -> capture stdout
# 2. qa: subprocess evaluate_qa.py gpt-4o hypothesis.jsonl <dataset> -> .eval-results; then print_qa_metrics.py
# 3. write scores.md with both variant blocks (caller runs evaluate twice: raw, consolidated)
```
Uses `subprocess.run` on the vendored scripts (they're CLI tools). Judge `OPENAI_API_KEY` from env/Secret Store.

- [ ] **Step 1: Write the failing test (no real judge — mock subprocess)**

Create `tests/benchmarks/longmemeval/test_evaluate.py`:
```python
from benchmarks.longmemeval.config import BenchConfig
from benchmarks.longmemeval import evaluate


def test_retrieval_block_parses_upstream_stdout(tmp_path, monkeypatch):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    cfg.results_dir.mkdir(parents=True, exist_ok=True)
    (cfg.results_dir / "retrieval.jsonl").write_text('{"question_id":"x","retrieval_results":{"metrics":{}}}\n')
    fake_stdout = ("Session-level metrics:\n\trecall_all@5 = 0.8\nTurn-level metrics:\n\trecall_all@50 = 0.6\n")
    def fake_run(cmd, **kw):
        class R:
            stdout = fake_stdout
            returncode = 0
        return R()
    monkeypatch.setattr(evaluate.subprocess, "run", fake_run)
    block = evaluate.run_retrieval_metrics(cfg)
    assert "recall_all@5 = 0.8" in block
    assert "Session-level" in block


def test_scores_md_written(tmp_path, monkeypatch):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    cfg.results_dir.mkdir(parents=True, exist_ok=True)
    (cfg.results_dir / "retrieval.jsonl").write_text("\n")
    (cfg.results_dir / "hypothesis.jsonl").write_text('{"question_id":"x","hypothesis":"y"}\n')
    def fake_run(cmd, **kw):
        class R:
            stdout = "Overall Accuracy: 0.75"
            returncode = 0
        return R()
    monkeypatch.setattr(evaluate.subprocess, "run", fake_run)
    evaluate.evaluate(cfg, tmp_path / "data.json", judge_model="gpt-4o")
    md = (cfg.results_dir / "scores.md").read_text()
    assert "Overall Accuracy: 0.75" in md
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `python -m pytest tests/benchmarks/longmemeval/test_evaluate.py -v`
Expected: FAIL (import error)

- [ ] **Step 3: Implement evaluate.py**

Create `benchmarks/longmemeval/evaluate.py`:
```python
"""Wrap vendored upstream scripts; produce scores.md."""
from __future__ import annotations
import subprocess
import sys
from pathlib import Path
from .config import BenchConfig

_HERE = Path(__file__).parent / "upstream_eval"
_PY = sys.executable


def run_retrieval_metrics(cfg: BenchConfig) -> str:
    """Run print_retrieval_metrics.py on retrieval.jsonl; return captured stdout."""
    retrieval = cfg.results_dir / "retrieval.jsonl"
    proc = subprocess.run(
        [_PY, str(_HERE / "print_retrieval_metrics.py"), str(retrieval)],
        capture_output=True, text=True, check=True,
    )
    return proc.stdout


def run_qa_metrics(cfg: BenchConfig, dataset_path: Path, judge_model: str) -> str:
    """evaluate_qa.py then print_qa_metrics.py; return aggregated stdout."""
    hyp = cfg.results_dir / "hypothesis.jsonl"
    # 1. judge
    subprocess.run(
        [_PY, str(_HERE / "evaluate_qa.py"), judge_model, str(hyp), str(dataset_path)],
        capture_output=True, text=True, check=True,
    )
    # evaluate_qa writes <hyp>.eval-results-<short>
    short = "gpt4o" if judge_model == "gpt-4o" else judge_model
    eval_log = Path(str(hyp) + f".eval-results-{short}")
    # 2. aggregate
    proc = subprocess.run(
        [_PY, str(_HERE / "print_qa_metrics.py"), str(eval_log), str(dataset_path)],
        capture_output=True, text=True, check=True,
    )
    return proc.stdout


def evaluate(cfg: BenchConfig, dataset_path: Path, *, judge_model: str = "gpt-4o") -> Path:
    retrieval_block = run_retrieval_metrics(cfg) if (cfg.results_dir / "retrieval.jsonl").exists() else "(no retrieval run)"
    qa_block = "(no qa run)"
    if (cfg.results_dir / "hypothesis.jsonl").exists():
        qa_block = run_qa_metrics(cfg, dataset_path, judge_model)
    md = [
        f"# LongMemEval scores — {cfg.difficulty} / {cfg.variant}",
        f"_top_k(retrieval)={cfg.top_k}_\n",
        "## Retrieval (Phase A)\n```\n" + retrieval_block + "\n```\n",
        "## QA (Phase B, judge=" + judge_model + ")\n```\n" + qa_block + "\n```\n",
    ]
    out = cfg.results_dir / "scores.md"
    out.write_text("\n".join(md))
    return out
```

- [ ] **Step 4: Run test — expect PASS**

Run: `python -m pytest tests/benchmarks/longmemeval/test_evaluate.py -v`
Expected: 2 passed

- [ ] **Step 5: Commit**

```bash
git add benchmarks/longmemeval/evaluate.py tests/benchmarks/longmemeval/test_evaluate.py
git commit -m "feat(bench): evaluate wrapper → scores.md"
```

---

### Task 12: CLI entrypoint + README + end-to-end smoke

**Files:**
- Create: `benchmarks/longmemeval/__main__.py`
- Create: `benchmarks/longmemeval/README.md`
- Test: `tests/benchmarks/longmemeval/test_cli.py`

**Interfaces:** Produces `python -m benchmarks.longmemeval {ingest|retrieve|qa|evaluate} --difficulty S --variant raw [--limit N]`.

- [ ] **Step 1: Write the failing test (argparse wiring, no real run)**

Create `tests/benchmarks/longmemeval/test_cli.py`:
```python
import subprocess, sys
from benchmarks.longmemeval import __main__ as cli


def test_parse_ingest():
    args = cli.parse_args(["ingest", "--difficulty", "oracle", "--variant", "raw", "--limit", "2"])
    assert args.command == "ingest"
    assert args.difficulty == "oracle"
    assert args.variant == "raw"
    assert args.limit == 2
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `python -m pytest tests/benchmarks/longmemeval/test_cli.py -v`
Expected: FAIL (import error)

- [ ] **Step 3: Implement __main__.py**

Create `benchmarks/longmemeval/__main__.py`:
```python
"""CLI: python -m benchmarks.longmemeval <command> [opts]"""
from __future__ import annotations
import argparse
import json
from .config import BenchConfig


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    p = argparse.ArgumentParser(prog="benchmarks.longmemeval")
    p.add_argument("command", choices=["ingest", "retrieve", "qa", "evaluate"])
    p.add_argument("--difficulty", default="s", choices=["oracle", "s", "m"])
    p.add_argument("--variant", default="raw", choices=["raw", "consolidated"])
    p.add_argument("--limit", type=int, default=None)
    p.add_argument("--top-k", type=int, default=50)
    p.add_argument("--force-ingest", action="store_true")
    return p.parse_args(argv)


def _load_dataset(cfg: BenchConfig):
    from .download_data import download, DIFFICULTY_FILE
    name = DIFFICULTY_FILE[cfg.difficulty]
    path = download(cfg)[name]
    data = json.loads(path.read_text())
    if cfg.limit:
        data = data[: cfg.limit]
    return data, path


def main(argv: list[str] | None = None) -> None:
    args = parse_args(argv)
    cfg = BenchConfig(difficulty=args.difficulty, variant=args.variant,
                      limit=args.limit, top_k=args.top_k)
    if args.command == "ingest":
        from .ingest import ingest_instance
        data, _ = _load_dataset(cfg)
        for inst in data:
            ingest_instance(inst, cfg, force=args.force_ingest)
            print(f"ingested {inst['question_id']}")
    elif args.command == "retrieve":
        from .run_retrieval import run_retrieval
        data, _ = _load_dataset(cfg)
        out = run_retrieval(data, cfg)
        print(f"wrote {out}")
    elif args.command == "qa":
        from .run_qa import run_qa
        data, _ = _load_dataset(cfg)
        out = run_qa(data, cfg)
        print(f"wrote {out}")
    elif args.command == "evaluate":
        _, dataset_path = _load_dataset(cfg)
        from .evaluate import evaluate
        out = evaluate(cfg, dataset_path)
        print(f"wrote {out}")


if __name__ == "__main__":
    main()
```

- [ ] **Step 4: Run test — expect PASS**

Run: `python -m pytest tests/benchmarks/longmemeval/test_cli.py -v`
Expected: 1 passed

- [ ] **Step 5: Write README.md**

Create `benchmarks/longmemeval/README.md`:
```markdown
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
```

- [ ] **Step 6: Full unit test sweep**

Run: `python -m pytest tests/benchmarks/ -v`
Expected: all green

- [ ] **Step 7: Commit**

```bash
git add benchmarks/longmemeval/__main__.py benchmarks/longmemeval/README.md tests/benchmarks/longmemeval/test_cli.py
git commit -m "feat(bench): CLI entrypoint + README"
```

- [ ] **Step 8: Optional end-to-end smoke (real data, opt-in)**

If network + an embedding provider is configured:
```bash
python -m benchmarks.longmemeval ingest   --difficulty oracle --variant raw --limit 2
python -m benchmarks.longmemeval retrieve --difficulty oracle --variant raw --limit 2
cat benchmarks/.cache/results/oracle/raw/retrieval.jsonl
```
Expected: 2 well-formed JSONL lines (abstention excluded). This is the first real signal; not required for the plan to be "done".

---

## Self-Review (completed during authoring)

1. **Spec coverage**: download ✓ (T4), ingest raw ✓ (T6), ingest consolidated ✓ (T7), metrics ✓ (T8), Phase A retrieval ✓ (T9), Phase B QA ✓ (T10), evaluate→scores.md ✓ (T11), isolation (per-instance workspace+DB, T6) ✓, abstention handling (T9 skip + T10 floor) ✓, vendored pinned scripts (T2) ✓, error handling per-instance try/finally (T6/T9/T10) ✓. Gap: `extract_mental_models()` optional step mentioned in spec — deferred (consolidate alone covers the variant; L5 is optional enrichment, not gating). Acceptable.
2. **Placeholder scan**: Task 4 Step 3 `EXPECTED_SIZES` has `0` placeholders with explicit instruction to fill from Step 1's `curl` output before commit — flagged, not hidden. No other TBD/TODO.
3. **Type consistency**: `engine_factory(db_path=, workspace=)` signature identical across T6/T7/T9/T10. `build_metric_dict` params match between T8 def and T9 call. `BenchConfig.db_path_for` used consistently. `run_retrieval`/`run_qa` write `{question_id, ...}` lines consumed by T11. ✓
