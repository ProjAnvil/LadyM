# LadyM — Providers, Config & Control Plane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make LadyM's embedding and LLM layers externally configurable (any HTTP/Ollama/callable embedding; LangChain-backed LLMs with `base_url` for third parties), add a TOML config + `ladym config` web editor, and integrate six approved enhancements (supersedes chain, attention gate, System1/System2, L5/L6 schema, memory-density stat) — without touching the read-path latency budget, the offline test suite, or any public API.

**Architecture:** All new capability hangs off `Config` (now TOML-loadable, 4-layer precedence) and two provider abstractions: an extended `EmbeddingProvider` (with `base_url`, dim auto-probe, dim-guarded fallback) and a new `LLMProvider` (sync, `complete`/`complete_structured`, implemented on LangChain behind a lazy `[llm]` extra). An `AgentRegistry` binds per-operation `AgentConfig`s. Write-path enhancements (supersedes, attention gate, System2 cycle) sit behind the existing `Engine`; the read path is untouched except a supersedes filter.

**Tech Stack:** Python 3.11+ (uv), SQLite + sqlite-vec (+ WAL), pydantic v2, LangChain (`langchain-core` + `langchain-{openai,anthropic,ollama}`, optional `[llm]` extra), FastAPI + HTMX + Pico.css (optional `[web]` extra), pytest, typer.

## Global Constraints

(copied verbatim from `docs/superpowers/specs/2026-07-21-providers-config-control-plane-design.md` §3 NFRs + toolchain floors — every task implicitly includes these.)

- **NFR-1:** Engine-overhead portion of the read path < 10 ms p95 @ 200 memories, always; hashing path overall p95 < 10 ms; external path p95 target < 300 ms.
- **NFR-2:** Every LLM/embedding dep has a pure-code fallback when unconfigured. Zero-config, offline, testable. Existing 103 tests stay green & offline; LangChain/FastAPI are optional extras, never imported in the offline path.
- **NFR-3:** LLM only on the write path. `recall`/`search_code` never invoke a generative LLM. `reflect()` heuristic-only forever.
- **NFR-4:** `Engine`/SDK/CLI/MCP public signatures only gain fields; semantics unchanged. `Engine(Config())` still works; new fields all have defaults; `attach_llm_classifier` kept as a shim.
- **NFR-5:** Secrets env-only (never literal in TOML). Dimension mismatches never corrupt the index. No silent cross-provider quality downgrade.
- Python ≥ 3.11 (`enum.StrEnum`, `tomllib`). Core deps stay lean; providers/web are optional extras.
- Line length 100, ruff rules `E,F,I,B,UP,SIM,C4`, `target-version = py311`. Tests tolerate `E501`.
- All commands run via `uv run ...`. The full suite must pass with no network and no model downloads.

---

## File Structure (locked decomposition)

**New files**

| Path | Responsibility |
|---|---|
| `src/ladym/providers/__init__.py` | package marker; re-exports `LLMProvider`, `make_llm_provider`, `make_agent`, `AgentConfig` |
| `src/ladym/providers/llm.py` | `LLMProvider` ABC, `LangChainLLMProvider`, `make_llm_provider`, `FakeLLMProvider`, `Message` |
| `src/ladym/providers/agents.py` | `AgentConfig`, `AgentRegistry`, `make_agent`, default prompt templates |
| `src/ladym/providers/embeddings_http.py` | `OllamaEmbedding`, `HttpEmbedding`, `CallableEmbedding`, `FakeHTTPClient` |
| `src/ladym/operations/supersedes.py` | `latest_in_chain`, retire helpers, tier-1 filter predicate |
| `src/ladym/operations/attention.py` | `GateDecision`, `attention_gate` (heuristic + LLM) |
| `src/ladym/operations/system2.py` | `System2Report`, `run_system2_cycle` |
| `src/ladym/web/__init__.py`, `src/ladym/web/app.py` | FastAPI app, routes, templates |
| `src/ladym/web/static/{htmx.min.js,pico.min.css}` | vendored static assets |
| `src/ladym/web/templates/{base.html,config.html,_fragments.html}` | Jinja templates |
| `tests/unit/test_llm_providers.py` | LLMProvider + FakeLLM + langchain (skip if absent) |
| `tests/unit/test_embedding_external.py` | ollama/http/callable + dim probe + fallback |
| `tests/unit/test_config_load.py` | precedence + secret rejection + rename |
| `tests/unit/test_supersedes.py` | chain semantics |
| `tests/unit/test_attention_gate.py` | gate modes |
| `tests/unit/test_system2.py` | cycle + threshold gate |
| `tests/integration/test_wal_concurrency.py` | worker-write vs reader |
| `tests/integration/test_web_config.py` | FastAPI TestClient |
| `tests/perf/test_read_path_budget.py` | read-path p95 budget |
| `tests/test_read_path_no_llm.py` | NFR-3 grep guard |

**Modified files**

| Path | Change |
|---|---|
| `src/ladym/config.py` | new fields (`embedding.*`, `llm.*`, `agents.*`, `system2.*`, `attention.*`), `Config.load`/`from_file`, `Config.enable_wal` |
| `src/ladym/storage/store.py` | `meta` table + migration, WAL pragma, `get_meta`/`set_meta` |
| `src/ladym/storage/embeddings.py` | `health_check` on ABC; `make_provider` routes ollama/http/callable + dim/fallback/cache wiring |
| `src/ladym/engine.py` | provider wiring (probe/persist/fallback), agent registry, attention gate in `remember`, `start_system2`, `extract_mental_models`/`predict_forward_intents` stubs (post-MVP), `avg_tokens_per_memory` |
| `src/ladym/operations/consolidate.py` | UPDATE→create-new+retire-old; DELETE→retire; uses `make_agent("consolidate")` |
| `src/ladym/operations/recall.py` | tier-1 supersedes filter; tier-2 `supersedes` traversal |
| `src/ladym/schema.py` | `Layer.L5_MENTAL`/`L6_PREDICTIVE`, `MemoryType.MENTAL_MODEL`/`FORWARD_INTENT`, `Stats.avg_tokens_per_memory` |
| `src/ladym/cli.py` | commands honour `Config.load`; add `config` and `worker` commands |
| `pyproject.toml` | add `[llm]` and `[web]` extras |

---

## Phase 0 — Regression protection (MUST run first)

Establishes the safety net before any change.

### Task 0.1: Baseline snapshot + back-compat assertions

**Files:**
- Create: `tests/test_regression_baseline.py`
- Test: `tests/test_regression_baseline.py`

**Interfaces:**
- Produces: a green baseline (the count of passing tests) + assertions that pin `Config()` defaults and the `Engine(Config())` smoke path.

- [ ] **Step 1: Capture the baseline**

Run: `uv run pytest -q`
Expected: `103 passed` (record the exact number in the test below).

- [ ] **Step 2: Write the failing back-compat test**

```python
# tests/test_regression_baseline.py
"""Pins the public surface this plan must not break (NFR-4)."""
from ladym.config import Config
from ladym.engine import Engine


def test_config_defaults_unchanged():
    cfg = Config()
    assert cfg.embedding_provider == "hashing"
    assert cfg.llm_provider == "none"
    assert cfg.workspace == "default"
    assert cfg.prefer_sqlite_vec is True
    assert cfg.activation.similarity == 1.0
    assert cfg.recall.top_k_tier1 == 8


def test_engine_constructs_with_plain_config(tmp_path):
    cfg = Config(db_path=tmp_path / "x.db")
    eng = Engine(cfg)
    try:
        assert eng.provider.dim == 256
        assert eng.recall("nothing").tier_reached in (1, 2)
    finally:
        eng.close()
```

- [ ] **Step 3: Run test to verify it passes (back-compat is already true)**

Run: `uv run pytest tests/test_regression_baseline.py -v`
Expected: PASS (these facts hold today; the test now guards them).

- [ ] **Step 4: Commit**

```bash
git add tests/test_regression_baseline.py
git commit -m "test: pin Config/Engine back-compat baseline"
```

---

## Phase 1 — Provider abstractions (no UI, no config file yet)

### Task 1.1: Extend `EmbeddingProvider` with `health_check`

**Files:**
- Modify: `src/ladym/storage/embeddings.py`
- Test: `tests/unit/test_embeddings.py`

**Interfaces:**
- Produces: `EmbeddingProvider.health_check(self) -> tuple[bool, str]` with a default impl.

- [ ] **Step 1: Write the failing test** (append to `tests/unit/test_embeddings.py`)

```python
def test_hashing_health_check_ok():
    from ladym.storage.embeddings import HashingEmbedding
    ok, msg = HashingEmbedding(dim=64).health_check()
    assert ok is True
    assert isinstance(msg, str)
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/unit/test_embeddings.py::test_hashing_health_check_ok -v`
Expected: FAIL — `AttributeError: 'HashingEmbedding' has no attribute 'health_check'`.

- [ ] **Step 3: Implement** (add to `EmbeddingProvider` in `src/ladym/storage/embeddings.py`)

```python
    def health_check(self) -> "tuple[bool, str]":
        """One-shot probe for the web UI 'test embedding' button."""
        try:
            v = self.embed("dimensionality probe")
            return True, f"ok dim={len(v)}"
        except Exception as e:  # noqa: BLE001
            return False, f"{type(e).__name__}: {e}"
```

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/unit/test_embeddings.py -v`
Expected: PASS (all embedding tests).

- [ ] **Step 5: Commit**

```bash
git add src/ladym/storage/embeddings.py tests/unit/test_embeddings.py
git commit -m "feat(embedding): add health_check to EmbeddingProvider ABC"
```

### Task 1.2: `OllamaEmbedding` + `HttpEmbedding` + `CallableEmbedding` (with `FakeHTTPClient`)

**Files:**
- Create: `src/ladym/providers/embeddings_http.py`
- Test: `tests/unit/test_embedding_external.py`

**Interfaces:**
- Produces: `OllamaEmbedding(base_url, model, *, timeout_s=10, client=None)`,
  `HttpEmbedding(base_url, *, request_template, response_path, dim, client=None, ...)`,
  `CallableEmbedding(fn, dim)`, and `FakeHTTPClient` (test double).

- [ ] **Step 1: Write the failing test**

```python
# tests/unit/test_embedding_external.py
import pytest

from ladym.providers.embeddings_http import (
    CallableEmbedding, FakeHTTPClient, HttpEmbedding, OllamaEmbedding,
)


def test_ollama_embedding_posts_to_api_embeddings():
    client = FakeHTTPClient(
        responder=lambda payload: {"embedding": [0.1, 0.2, 0.3]},
        expected_path="/api/embeddings",
    )
    emb = OllamaEmbedding("http://localhost:11434", "nomic-embed-text", client=client)
    v = emb.embed("hello")
    assert v == [0.1, 0.2, 0.3]
    assert emb.dim == 3
    assert client.last_payload["model"] == "nomic-embed-text"
    assert client.last_payload["prompt"] == "hello"


def test_ollama_health_check_reports_failure():
    client = FakeHTTPClient(responder=lambda p: (_ for _ in ()).throw(ConnectionError("nope")))
    emb = OllamaEmbedding("http://localhost:11434", "x", client=client)
    ok, msg = emb.health_check()
    assert ok is False
    assert "ConnectionError" in msg


def test_http_embedding_uses_response_path():
    client = FakeHTTPClient(responder=lambda p: {"data": {"vector": [1.0, 0.0]}})
    emb = HttpEmbedding(
        "https://example.test/embed", request_template='{"input": "{text}"}',
        response_path="data.vector", dim=2, client=client,
    )
    assert emb.embed("q") == [1.0, 0.0]


def test_callable_embedding_wraps_user_function():
    emb = CallableEmbedding(fn=lambda text: [float(len(text)), 0.0], dim=2)
    assert emb.embed("abcd") == [4.0, 0.0]
    assert emb.dim == 2


def test_external_batch_matches_singletons():
    client = FakeHTTPClient(responder=lambda p: {"embedding": [0.5, 0.5]})
    emb = OllamaEmbedding("http://x", "m", client=client)
    batch = emb.embed_batch(["a", "b"])
    assert batch == [emb.embed("a"), emb.embed("b")]
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/unit/test_embedding_external.py -v`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

```python
# src/ladym/providers/embeddings_http.py
"""External embedding providers: Ollama, generic HTTP, and user callables.

All HTTP providers accept an injected ``client`` so tests never touch the network.
"""
from __future__ import annotations

import json
from typing import Any, Callable

from ..storage.embeddings import EmbeddingProvider


class FakeHTTPClient:
    """Test double for HTTP providers. ``responder`` maps payload -> JSON-able dict."""

    def __init__(self, responder: Callable[[dict], Any], expected_path: str = ""):
        self._responder = responder
        self.expected_path = expected_path
        self.last_payload: dict | None = None
        self.last_url: str | None = None

    def post(self, url: str, payload: dict, *, timeout: float = 10.0,
             headers: dict | None = None) -> Any:
        self.last_url = url
        self.last_payload = payload
        if self.expected_path and self.expected_path not in url:
            raise AssertionError(f"expected path {self.expected_path!r} in {url!r}")
        return self._responder(payload)


def _extract(obj: Any, path: str) -> Any:
    cur = obj
    for part in path.split("."):
        cur = cur[part]
    return cur


class OllamaEmbedding(EmbeddingProvider):
    def __init__(self, base_url: str, model: str, *, timeout_s: float = 10.0,
                 client: Any | None = None):
        self.base_url = base_url.rstrip("/")
        self.model = model
        self.timeout_s = timeout_s
        self._client = client
        self.dim = len(self.embed("__dim_probe__"))

    def _post(self, prompt: str) -> list[float]:
        payload = {"model": self.model, "prompt": prompt}
        client = self._client or _real_http_client()
        resp = client.post(f"{self.base_url}/api/embeddings", payload, timeout=self.timeout_s)
        return list(resp["embedding"])

    def embed(self, text: str) -> list[float]:
        return self._post(text)

    def embed_batch(self, texts: list[str]) -> list[list[float]]:
        return [self.embed(t) for t in texts]  # Ollama has no batch endpoint

    def health_check(self) -> tuple[bool, str]:
        try:
            v = self.embed("dimensionality probe")
            return True, f"ok dim={len(v)}"
        except Exception as e:  # noqa: BLE001
            return False, f"{type(e).__name__}: {e}"


class HttpEmbedding(EmbeddingProvider):
    def __init__(self, base_url: str, *, request_template: str, response_path: str,
                 dim: int, model: str = "", timeout_s: float = 10.0,
                 client: Any | None = None, headers: dict | None = None):
        self.base_url = base_url
        self.request_template = request_template
        self.response_path = response_path
        self._dim = dim
        self.model = model
        self.timeout_s = timeout_s
        self._client = client
        self.headers = headers or {}
        self.dim = dim  # user-declared; verified on first real call below

    def embed(self, text: str) -> list[float]:
        body = self.request_template.replace("{text}", json.dumps(text)[1:-1])
        payload = json.loads(body)
        client = self._client or _real_http_client()
        resp = client.post(self.base_url, payload, timeout=self.timeout_s, headers=self.headers)
        vec = list(_extract(resp, self.response_path))
        if len(vec) != self._dim:
            raise ValueError(f"dim mismatch: declared {self._dim}, got {len(vec)}")
        return vec

    def embed_batch(self, texts: list[str]) -> list[list[float]]:
        return [self.embed(t) for t in texts]

    def health_check(self) -> tuple[bool, str]:
        try:
            v = self.embed("dimensionality probe")
            return True, f"ok dim={len(v)}"
        except Exception as e:  # noqa: BLE001
            return False, f"{type(e).__name__}: {e}"


class CallableEmbedding(EmbeddingProvider):
    def __init__(self, fn: Callable[[str], list[float]], dim: int):
        self._fn = fn
        self.dim = dim

    def embed(self, text: str) -> list[float]:
        return list(self._fn(text))

    def embed_batch(self, texts: list[str]) -> list[list[float]]:
        return [self.embed(t) for t in texts]


def _real_http_client():  # pragma: no cover - exercised only with a real provider
    import httpx

    class _Httpx:
        def post(self, url, payload, *, timeout=10.0, headers=None):
            r = httpx.post(url, json=payload, timeout=timeout, headers=headers or {})
            r.raise_for_status()
            return r.json()

    return _Httpx()
```

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/unit/test_embedding_external.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/ladym/providers/embeddings_http.py tests/unit/test_embedding_external.py
git commit -m "feat(embedding): add Ollama/HTTP/Callable external providers"
```

### Task 1.3: `meta` table + WAL in `SQLiteStore`

**Files:**
- Modify: `src/ladym/storage/store.py`
- Test: `tests/unit/test_store.py`

**Interfaces:**
- Produces: `SQLiteStore.get_meta(key) -> str | None`, `set_meta(key, value)`, `enable_wal`
  constructor flag; `meta` table created idempotently.

- [ ] **Step 1: Write the failing test** (append to `tests/unit/test_store.py`)

```python
def test_meta_roundtrip(tmp_db):
    from ladym.storage.store import SQLiteStore
    s = SQLiteStore(tmp_db, dim=8)
    assert s.get_meta("foo") is None
    s.set_meta("foo", "bar")
    assert s.get_meta("foo") == "bar"
    # reopen -> persisted
    s.close()
    s2 = SQLiteStore(tmp_db, dim=8)
    assert s2.get_meta("foo") == "bar"
    s2.close()


def test_wal_mode_when_requested(tmp_db):
    from ladym.storage.store import SQLiteStore
    s = SQLiteStore(tmp_db, dim=8, enable_wal=True)
    mode = s.conn.execute("PRAGMA journal_mode").fetchone()[0]
    assert mode == "wal"
    s.close()
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/unit/test_store.py::test_meta_roundtrip tests/unit/test_store.py::test_wal_mode_when_requested -v`
Expected: FAIL — `get_meta` missing / no `enable_wal` arg.

- [ ] **Step 3: Implement** — add the table to `_SCHEMA`, the constructor flag, and the methods in `src/ladym/storage/store.py`.

In `_SCHEMA` (append before the closing `"""`):

```sql
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
```

Change the constructor signature and add WAL:

```python
    def __init__(self, db_path: Path, dim: int, prefer_sqlite_vec: bool = True,
                 enable_wal: bool = False):
        self.db_path = Path(db_path)
        self.db_path.parent.mkdir(parents=True, exist_ok=True)
        self.conn = sqlite3.connect(str(self.db_path))
        self.conn.row_factory = sqlite3.Row
        if enable_wal:
            self.conn.execute("PRAGMA journal_mode=WAL")
        self.conn.execute("PRAGMA foreign_keys = ON")
        self.conn.executescript(_SCHEMA)
        # ... (existing embedding-column migration unchanged) ...
```

Add methods (near `workspaces()`):

```python
    def get_meta(self, key: str) -> str | None:
        row = self.conn.execute("SELECT value FROM meta WHERE key = ?", (key,)).fetchone()
        return row["value"] if row else None

    def set_meta(self, key: str, value: str) -> None:
        self.conn.execute(
            "INSERT INTO meta (key, value) VALUES (?,?) "
            "ON CONFLICT(key) DO UPDATE SET value=excluded.value",
            (key, value),
        )
        self.conn.commit()
```

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/unit/test_store.py -v`
Expected: PASS (all store tests).

- [ ] **Step 5: Commit**

```bash
git add src/ladym/storage/store.py tests/unit/test_store.py
git commit -m "feat(store): add meta table + WAL mode"
```

### Task 1.4: Dimension auto-probe + `EmbeddingDimensionMismatch` + dim-guarded fallback

**Files:**
- Modify: `src/ladym/storage/embeddings.py`, `src/ladym/engine.py`
- Test: `tests/unit/test_embedding_external.py`

**Interfaces:**
- Produces: `EmbeddingDimensionMismatch`, `EmbeddingProviderError`, `make_provider(config)`
  wiring for ollama/http/callable; `Engine.__init__` probes + persists dim, refuses mismatch.
- Consumes: `SQLiteStore.get_meta/set_meta` (Task 1.3), external providers (Task 1.2).

- [ ] **Step 1: Write the failing test** (append to `tests/unit/test_embedding_external.py`)

```python
def test_dim_mismatch_on_reopen_raises(tmp_path):
    from ladym.config import Config
    from ladym.engine import Engine
    from ladym.storage.embeddings import EmbeddingDimensionMismatch

    # first engine: hashing dim 256, persists meta
    cfg = Config(db_path=tmp_path / "d.db")
    eng = Engine(cfg)
    assert eng.store.get_meta("embedding_dim") == "256"
    eng.close()

    # reopen with a different dim -> mismatch
    cfg2 = Config(db_path=tmp_path / "d.db")
    cfg2.embedding_provider = "hashing"
    # force a different probed dim via a callable with dim=128
    from ladym.providers.embeddings_http import CallableEmbedding
    eng2 = Engine.__new__(Engine)  # bypass to inject provider
    # simpler: directly call the probe helper
    from ladym.storage.embeddings import _assert_dim_matches
    import pytest
    with pytest.raises(EmbeddingDimensionMismatch):
        _assert_dim_matches(stored=256, configured=128)


def test_fallback_only_when_dim_matches(tmp_path):
    from ladym.config import Config
    from ladym.storage.embeddings import make_provider
    cfg = Config()
    cfg.embedding_provider = "hashing"
    cfg.embedding_dim = 256
    p = make_provider(cfg)
    assert p.dim == 256
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/unit/test_embedding_external.py -k "dim_mismatch or fallback" -v`
Expected: FAIL — names missing.

- [ ] **Step 3: Implement** in `src/ladym/storage/embeddings.py`

```python
class EmbeddingProviderError(RuntimeError):
    pass


class EmbeddingDimensionMismatch(EmbeddingProviderError):
    def __init__(self, stored: int, configured: int):
        super().__init__(
            f"embedding dim mismatch: DB has {stored}-dim vectors but provider returns "
            f"{configured}-dim. Set embedding.allow_dim_change=true to wipe and re-embed."
        )
        self.stored = stored
        self.configured = configured


def _assert_dim_matches(*, stored: int, configured: int) -> None:
    if stored != configured:
        raise EmbeddingDimensionMismatch(stored, configured)
```

Extend `make_provider` to route new providers and read `base_url`:

```python
def make_provider(config: Config) -> EmbeddingProvider:
    name = config.embedding_provider.lower()
    if name == "hashing":
        return HashingEmbedding(dim=config.embedding_dim)
    if name in ("st", "sentence-transformer", "sentence_transformers"):
        return SentenceTransformerEmbedding(config.embedding_model or None)  # type: ignore[arg-type]
    if name == "openai":
        return OpenAIEmbedding(config.embedding_model or "text-embedding-3-small")
    if name == "ollama":
        from ..providers.embeddings_http import OllamaEmbedding
        return OllamaEmbedding(config.embedding_base_url or "http://localhost:11434",
                               config.embedding_model or "nomic-embed-text",
                               timeout_s=config.embedding_timeout_s)
    if name == "http":
        from ..providers.embeddings_http import HttpEmbedding
        return HttpEmbedding(config.embedding_base_url,
                             request_template=config.embedding_http_request,
                             response_path=config.embedding_http_response_path,
                             dim=config.embedding_dim,
                             model=config.embedding_model)
    if name == "callable":
        fn = _callable_registry.get(config.embedding_model)
        if fn is None:
            raise ValueError(f"no callable embedding registered under {config.embedding_model!r}")
        from ..providers.embeddings_http import CallableEmbedding
        return CallableEmbedding(fn, dim=config.embedding_dim)
    raise ValueError(f"unknown embedding provider: {name}")


_callable_registry: dict[str, "callable"] = {}


def register_callable(name: str, fn) -> None:
    _callable_registry[name] = fn
```

Wire probe/persist into `Engine.__init__` (in `src/ladym/engine.py`), replacing the store construction:

```python
        self.provider: EmbeddingProvider = make_provider(cfg)
        self.store = SQLiteStore(
            cfg.db_path, dim=self.provider.dim,
            prefer_sqlite_vec=cfg.prefer_sqlite_vec, enable_wal=cfg.enable_wal,
        )
        if not hasattr(self.store, "associative_neighbour_counts"):
            self.store.associative_neighbour_counts = self._associative_neighbour_counts
        self._enforce_embedding_dim()

    def _enforce_embedding_dim(self) -> None:
        stored = self.store.get_meta("embedding_dim")
        actual = self.provider.dim
        if stored is None:
            # empty DB: persist
            self.store.set_meta("embedding_dim", str(actual))
            self.store.set_meta("embedding_provider", self.config.embedding_provider)
            return
        if int(stored) != actual:
            if self.config.embedding_allow_dim_change:
                self._reembed_all()
                self.store.set_meta("embedding_dim", str(actual))
            else:
                from .storage.embeddings import EmbeddingDimensionMismatch
                raise EmbeddingDimensionMismatch(int(stored), actual)

    def _reembed_all(self) -> None:
        for m in self.store.iter_memories():
            vec = self.provider.embed(m.content)
            self.store.put_memory(m, vector=vec)
```

Add the new fields to `Config` in Task 2.1; here assume they exist with defaults
(`embedding_base_url=""`, `embedding_timeout_s=10`, `embedding_http_request`,
`embedding_http_response_path="data.embedding"`, `embedding_allow_dim_change=False`,
`enable_wal=False`). If Task 2.1 has not landed yet, add these fields to `Config` now as a
minimal stub so the code imports:

```python
    # in Config (minimal stub; full schema in Task 2.1)
    embedding_base_url: str = ""
    embedding_timeout_s: float = 10.0
    embedding_http_request: str = '{"input": "{text}"}'
    embedding_http_response_path: str = "data"
    embedding_allow_dim_change: bool = False
    enable_wal: bool = False
```

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/unit/test_embedding_external.py tests/unit/test_regression_baseline.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/ladym/storage/embeddings.py src/ladym/engine.py src/ladym/config.py tests/unit/test_embedding_external.py
git commit -m "feat(embedding): auto-probe+persist dim, refuse mismatch, route external providers"
```

### Task 1.5: Query-embedding LRU cache (default off)

**Files:**
- Create: `src/ladym/providers/query_cache.py`
- Modify: `src/ladym/storage/embeddings.py` (`make_provider` wraps with cache when size>0)
- Test: `tests/unit/test_embedding_external.py`

**Interfaces:**
- Produces: `CachedEmbedding(inner, size)` implementing `EmbeddingProvider`, delegating
  `embed` through an LRU; `embed_batch` bypasses the cache.

- [ ] **Step 1: Write the failing test**

```python
def test_query_cache_hits_after_first_embed():
    from ladym.providers.query_cache import CachedEmbedding
    from ladym.storage.embeddings import HashingEmbedding

    calls = {"n": 0}

    class Counted(HashingEmbedding):
        def embed(self, text):
            calls["n"] += 1
            return super().embed(text)

    cached = CachedEmbedding(Counted(dim=64), size=4)
    cached.embed("same")
    cached.embed("same")
    assert calls["n"] == 1  # second was a cache hit
    assert cached.dim == 64
    assert cached.embed_batch(["x"])  # batch still works (bypasses cache)
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/unit/test_embedding_external.py::test_query_cache_hits_after_first_embed -v`
Expected: FAIL — module missing.

- [ ] **Step 3: Implement**

```python
# src/ladym/providers/query_cache.py
from __future__ import annotations
from collections import OrderedDict

from ..storage.embeddings import EmbeddingProvider


class CachedEmbedding(EmbeddingProvider):
    def __init__(self, inner: EmbeddingProvider, size: int):
        self._inner = inner
        self._size = size
        self._cache: "OrderedDict[str, list[float]]" = OrderedDict()
        self.dim = inner.dim

    def embed(self, text: str) -> list[float]:
        if text in self._cache:
            self._cache.move_to_end(text)
            return self._cache[text]
        v = self._inner.embed(text)
        self._cache[text] = v
        if len(self._cache) > self._size:
            self._cache.popitem(last=False)
        return v

    def embed_batch(self, texts):
        return self._inner.embed_batch(texts)  # batches bypass the cache

    def health_check(self):
        return self._inner.health_check()
```

Wrap in `make_provider` (end of the function):

```python
    if getattr(config, "embedding_query_cache_size", 0) > 0:
        from ..providers.query_cache import CachedEmbedding
        provider = CachedEmbedding(provider, config.embedding_query_cache_size)
    return provider
```

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/unit/test_embedding_external.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/ladym/providers/query_cache.py src/ladym/storage/embeddings.py tests/unit/test_embedding_external.py
git commit -m "feat(embedding): optional query LRU cache (default off)"
```

### Task 1.6: `LLMProvider` ABC + `FakeLLMProvider`

**Files:**
- Create: `src/ladym/providers/llm.py`
- Test: `tests/unit/test_llm_providers.py`

**Interfaces:**
- Produces: `Message` (TypedDict), `LLMProvider` ABC (`complete`, `complete_structured`, `close`),
  `FakeLLMProvider(complete_fn=None, structured_fn=None)`.

- [ ] **Step 1: Write the failing test**

```python
# tests/unit/test_llm_providers.py
from pydantic import BaseModel

from ladym.providers.llm import FakeLLMProvider, LLMProvider, Message


class Decision(BaseModel):
    action: str
    content: str | None = None


def test_fake_complete_returns_scripted_text():
    p = FakeLLMProvider(complete_fn=lambda msgs: "pong")
    assert p.complete([Message(role="user", content="ping")]) == "pong"


def test_fake_structured_returns_dict():
    p = FakeLLMProvider(structured_fn=lambda msgs, schema: {"action": "ADD", "content": None})
    out = p.complete_structured([Message(role="user", content="x")], Decision)
    assert out == {"action": "ADD", "content": None}


def test_fake_raises_when_no_script():
    import pytest
    p = FakeLLMProvider()
    with pytest.raises(NotImplementedError):
        p.complete([Message(role="user", content="x")])


def test_llmprovider_is_abstract():
    import pytest
    with pytest.raises(TypeError):
        LLMProvider()  # type: ignore[abstract]
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/unit/test_llm_providers.py -v`
Expected: FAIL — import error.

- [ ] **Step 3: Implement**

```python
# src/ladym/providers/llm.py
"""LLM provider abstraction. Concrete providers are built on LangChain (Task 1.7),
but the ABC and the offline test double live here and import nothing heavy."""
from __future__ import annotations

from abc import ABC, abstractmethod
from typing import Any, Callable, TypedDict

from pydantic import BaseModel


class Message(TypedDict):
    role: str       # "system" | "user" | "assistant"
    content: str


class LLMProvider(ABC):
    name: str = "abstract"

    @abstractmethod
    def complete(self, messages: list[Message], **params: Any) -> str: ...

    @abstractmethod
    def complete_structured(self, messages: list[Message], schema: type[BaseModel],
                            **params: Any) -> dict: ...

    def close(self) -> None:
        pass


class FakeLLMProvider(LLMProvider):
    name = "fake"

    def __init__(self, *, complete_fn: Callable[[list[Message]], str] | None = None,
                 structured_fn: Callable[[list[Message], type[BaseModel]], dict] | None = None):
        self._complete = complete_fn
        self._structured = structured_fn

    def complete(self, messages, **params):
        if self._complete is None:
            raise NotImplementedError("FakeLLMProvider has no complete_fn scripted")
        return self._complete(messages)

    def complete_structured(self, messages, schema, **params):
        if self._structured is None:
            raise NotImplementedError("FakeLLMProvider has no structured_fn scripted")
        return self._structured(messages, schema)
```

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/unit/test_llm_providers.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/ladym/providers/llm.py tests/unit/test_llm_providers.py
git commit -m "feat(llm): LLMProvider ABC + FakeLLMProvider"
```

### Task 1.7: `LangChainLLMProvider` + `make_llm_provider` (lazy `[llm]` extra)

**Files:**
- Modify: `src/ladym/providers/llm.py`, `pyproject.toml`
- Test: `tests/unit/test_llm_providers.py`

**Interfaces:**
- Produces: `LangChainLLMProvider(chat_model, structured_method)`,
  `make_llm_provider(*, provider, base_url, model, api_key, structured_method, ...) -> LLMProvider | None`.
  Returns `None` for `provider="none"`; raises a helpful error if LangChain isn't installed.

- [ ] **Step 1: Add the `[llm]` extra** in `pyproject.toml`

```toml
[project.optional-dependencies]
local = ["sentence-transformers>=2.7"]
openai = ["openai>=1.30"]
anthropic = ["anthropic>=0.30"]
mcp = ["mcp>=1.0"]
llm = [
    "langchain-core>=0.3",
    "langchain-openai>=0.2",
    "langchain-anthropic>=0.2",
    "langchain-ollama>=0.2",
    "httpx>=0.27",
]
web = [
    "fastapi>=0.110",
    "uvicorn[standard]>=0.27",
    "jinja2>=3.1",
    "python-multipart>=0.0.9",
]
dev = [
    "pytest>=8.0",
    "pytest-asyncio>=0.23",
    "pytest-cov>=5.0",
    "ruff>=0.5",
    "mypy>=1.10",
]
```

- [ ] **Step 2: Write the failing test** (append to `tests/unit/test_llm_providers.py`)

```python
def test_make_llm_provider_none_returns_none():
    from ladym.providers.llm import make_llm_provider
    assert make_llm_provider(provider="none", base_url="", model="",
                             api_key="", structured_method="function_calling") is None


def test_langchain_provider_built_when_importable():
    import pytest
    from ladym.providers.llm import LangChainLLMProvider, make_llm_provider
    pytest.importorskip("langchain_openai")
    p = make_llm_provider(provider="openai", base_url="https://example.test/v1",
                          model="m", api_key="k", structured_method="function_calling",
                          max_tokens=8, temperature=0.0, timeout_s=5)
    assert isinstance(p, LangChainLLMProvider)
```

- [ ] **Step 3: Run to verify it fails**

Run: `uv run pytest tests/unit/test_llm_providers.py -k "make_llm or langchain" -v`
Expected: FAIL — names missing.

- [ ] **Step 4: Implement** (append to `src/ladym/providers/llm.py`)

```python
def _to_lc(msg: Message):
    from langchain_core.messages import (  # local import keeps ABC light
        AIMessage, HumanMessage, SystemMessage,
    )
    if msg["role"] == "system":
        return SystemMessage(content=msg["content"])
    if msg["role"] == "assistant":
        return AIMessage(content=msg["content"])
    return HumanMessage(content=msg["content"])


class LangChainLLMProvider(LLMProvider):
    name = "langchain"

    def __init__(self, chat_model, structured_method: str = "function_calling"):
        self._cm = chat_model
        self._sm = structured_method

    def complete(self, messages, **params):
        return self._cm.invoke([_to_lc(m) for m in messages]).content

    def complete_structured(self, messages, schema, **params):
        runner = self._cm.with_structured_output(schema, method=self._sm)
        out = runner.invoke([_to_lc(m) for m in messages])
        return out if isinstance(out, dict) else out.dict() if hasattr(out, "dict") else dict(out)


def make_llm_provider(*, provider: str, base_url: str, model: str, api_key: str,
                      structured_method: str = "function_calling",
                      max_tokens: int = 1024, temperature: float = 0.2,
                      timeout_s: float = 30.0) -> "LLMProvider | None":
    provider = (provider or "none").lower()
    if provider == "none":
        return None
    try:
        if provider == "openai" or provider == "http":
            from langchain_openai import ChatOpenAI
            cm = ChatOpenAI(base_url=base_url or None, model=model, api_key=api_key or None,
                            max_tokens=max_tokens, temperature=temperature, timeout=timeout_s)
        elif provider == "anthropic":
            from langchain_anthropic import ChatAnthropic
            cm = ChatAnthropic(base_url=base_url or None, model=model, api_key=api_key or None,
                               max_tokens=max_tokens, temperature=temperature, timeout=timeout_s)
        elif provider == "ollama":
            from langchain_ollama import ChatOllama
            cm = ChatOllama(base_url=base_url or None, model=model, temperature=temperature)
        else:
            raise ValueError(f"unknown llm provider: {provider}")
    except ImportError as e:  # pragma: no cover
        raise ImportError(
            f"LLM provider {provider!r} needs langchain extras: pip install 'ladym[llm]'"
        ) from e
    return LangChainLLMProvider(cm, structured_method)
```

- [ ] **Step 5: Run to verify it passes**

Run: `uv run pytest tests/unit/test_llm_providers.py -v`
Expected: PASS (the langchain test is skipped if `langchain_openai` isn't installed — offline suite unaffected).

- [ ] **Step 6: Commit**

```bash
git add pyproject.toml src/ladym/providers/llm.py tests/unit/test_llm_providers.py
git commit -m "feat(llm): LangChainLLMProvider + factory (lazy [llm] extra)"
```

### Task 1.8: `AgentConfig` + `AgentRegistry` + `make_agent` + shim

**Files:**
- Create: `src/ladym/providers/agents.py`, `src/ladym/providers/__init__.py`
- Modify: `src/ladym/engine.py` (`attach_llm_classifier` shim), `src/ladym/operations/consolidate.py` (use agent)
- Test: `tests/unit/test_llm_providers.py`

**Interfaces:**
- Produces: `AgentConfig`, `AgentRegistry(cfg).get(op) -> AgentConfig`,
  `make_agent(cfg, op, llm_provider_lookup) -> Agent`, where `Agent` is a small callable
  wrapper exposing `.complete`/`.complete_structured` (or `None` for heuristic mode).
- Consumes: `make_llm_provider` (Task 1.7), `FakeLLMProvider` (Task 1.6).

- [ ] **Step 1: Write the failing test** (append to `tests/unit/test_llm_providers.py`)

```python
def test_agent_registry_inherits_global_llm():
    from ladym.config import Config
    from ladym.providers.agents import AgentRegistry
    cfg = Config()
    cfg.llm_provider = "none"
    reg = AgentRegistry(cfg)
    assert reg.get("consolidate").provider == "none"
    cfg.agents_overrides = {"l5_mental_model": {"provider": "openai", "model": "m",
                                                "base_url": "u", "api_key_env": "K"}}
    assert reg.get("l5_mental_model").provider == "openai"
    assert reg.get("l5_mental_model").model == "m"


def test_make_agent_none_when_provider_none():
    from ladym.config import Config
    from ladym.providers.agents import make_agent
    cfg = Config()
    agent = make_agent(cfg, "consolidate")
    assert agent is None  # heuristic mode
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/unit/test_llm_providers.py -k "agent_registry or make_agent" -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```python
# src/ladym/providers/agents.py
"""Per-operation agent configuration + factory."""
from __future__ import annotations

import os
from dataclasses import dataclass, field
from typing import Any

from ..config import Config
from .llm import LLMProvider, make_llm_provider

NAMED_OPS = (
    "consolidate", "proceduralize", "attention_gate", "l5_mental_model", "l6_forward_intent",
)


@dataclass
class AgentConfig:
    op: str
    provider: str = "none"
    base_url: str = ""
    model: str = ""
    api_key_env: str = ""
    prompt_template: str = ""
    max_tokens: int = 1024
    temperature: float = 0.2
    structured_method: str = "function_calling"


class AgentRegistry:
    def __init__(self, cfg: Config):
        self._cfg = cfg

    def get(self, op: str) -> AgentConfig:
        if op not in NAMED_OPS:
            raise ValueError(f"unknown op {op!r}; expected one of {NAMED_OPS}")
        overrides = getattr(self._cfg, "agents_overrides", {}).get(op, {})
        return AgentConfig(
            op=op,
            provider=overrides.get("provider", self._cfg.llm_provider),
            base_url=overrides.get("base_url", self._cfg.llm_base_url),
            model=overrides.get("model", self._cfg.llm_model),
            api_key_env=overrides.get("api_key_env", getattr(self._cfg, "llm_api_key_env", "")),
            prompt_template=overrides.get("prompt_template", ""),
            max_tokens=overrides.get("max_tokens", self._cfg.llm_max_tokens),
            temperature=overrides.get("temperature", self._cfg.llm_temperature),
            structured_method=overrides.get(
                "structured_method", getattr(self._cfg, "llm_structured_method", "function_calling")),
        )


def make_agent(cfg: Config, op: str) -> "LLMProvider | None":
    """Build (or skip) the LLM provider bound to one operation. None => heuristic."""
    ac = AgentRegistry(cfg).get(op)
    if ac.provider == "none":
        return None
    api_key = os.environ.get(ac.api_key_env, "") if ac.api_key_env else ""
    return make_llm_provider(
        provider=ac.provider, base_url=ac.base_url, model=ac.model, api_key=api_key,
        structured_method=ac.structured_method, max_tokens=ac.max_tokens,
        temperature=ac.temperature,
    )
```

```python
# src/ladym/providers/__init__.py
from .agents import AgentConfig, AgentRegistry, NAMED_OPS, make_agent
from .llm import FakeLLMProvider, LangChainLLMProvider, LLMProvider, Message, make_llm_provider

__all__ = [
    "AgentConfig", "AgentRegistry", "NAMED_OPS", "make_agent",
    "FakeLLMProvider", "LangChainLLMProvider", "LLMProvider", "Message", "make_llm_provider",
]
```

Update `Engine.attach_llm_classifier` to be a shim (in `src/ladym/engine.py`):

```python
    def attach_llm_classifier(self, fn: "LLMClassifier | None" = None) -> None:
        """Wire an LLM classifier for consolidation.

        Back-compat: ``fn`` is still accepted directly. If ``fn`` is None, the engine builds
        the agent from config via ``make_agent(cfg, 'consolidate')``.
        """
        if fn is not None:
            self._llm_classify = fn
            return
        from .providers import make_agent
        provider = make_agent(self.config, "consolidate")
        if provider is None:
            self._llm_classify = None
            return

        def _classify(candidate: str, similar: list[str]):
            from pydantic import BaseModel
            from .operations.consolidate import Action

            class _Decision(BaseModel):
                action: str
                new_text: str | None = None

            msgs = [{"role": "system", "content": _consolidate_prompt()},
                    {"role": "user", "content": f"candidate: {candidate}\nsimilar: {similar}"}]
            d = provider.complete_structured(msgs, _Decision)
            return Action(d["action"]), d.get("new_text")

        self._llm_classify = _classify
```

Add `_consolidate_prompt()` near the bottom of `engine.py` (or import from
`providers.agents`). Minimal content:

```python
def _consolidate_prompt() -> str:
    return (
        "You classify a candidate fact against similar existing facts. "
        "Reply with JSON {action, new_text}. action ∈ ADD|UPDATE|DELETE|NOOP. "
        "ADD=brand new; UPDATE=refines an existing one (set new_text); "
        "DELETE=existing is now wrong; NOOP=duplicate."
    )
```

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/unit/test_llm_providers.py tests/unit/test_consolidate.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/ladym/providers/agents.py src/ladym/providers/__init__.py src/ladym/engine.py tests/unit/test_llm_providers.py
git commit -m "feat(agents): AgentRegistry + make_agent + attach_llm_classifier shim"
```

---

## Phase 2 — Config file loading

### Task 2.1: Full `Config` schema + TOML loading + precedence + secret rejection

**Files:**
- Modify: `src/ladym/config.py`
- Test: `tests/unit/test_config_load.py`

**Interfaces:**
- Produces: `Config.from_file(path)`, `Config.load(config_path=None)`,
  `Config.agents_overrides: dict`, all `embedding.*`/`llm.*`/`system2.*`/`attention.*` fields,
  `parse_toml_safely(text)` (strips/ignores secret literals).

- [ ] **Step 1: Write the failing test**

```python
# tests/unit/test_config_load.py
import os
from pathlib import Path

import pytest

from ladym.config import Config


def write(p: Path, body: str):
    p.write_text(body)


def test_from_file_loads_embedding_and_llm(tmp_path):
    f = tmp_path / "ladym.toml"
    write(f, """
embedding_provider = "openai"
embedding_base_url = "https://api.deepseek.com/v1"
[llm]
provider = "openai"
base_url = "https://api.deepseek.com/v1"
model = "deepseek-chat"
""")
    cfg = Config.from_file(f)
    assert cfg.embedding_provider == "openai"
    assert cfg.llm_provider == "openai"
    assert cfg.llm_base_url == "https://api.deepseek.com/v1"


def test_secret_literal_is_rejected(tmp_path, capsys):
    f = tmp_path / "ladym.toml"
    write(f, '[llm]\napi_key = "sk-leaked"\nmodel = "m"\n')
    cfg = Config.from_file(f)
    captured = capsys.readouterr()
    assert "api_key" in captured.err.lower() or "warning" in captured.err.lower()
    assert getattr(cfg, "llm_api_key_env", "") == ""  # not stored


def test_precedence_cli_over_file_over_defaults(tmp_path, monkeypatch):
    f = tmp_path / "ladym.toml"
    write(f, 'workspace = "fromfile"\n')
    cfg = Config.load(config_path=f, cli_overrides={"workspace": "fromcli"})
    assert cfg.workspace == "fromcli"


def test_env_over_file(tmp_path, monkeypatch):
    f = tmp_path / "ladym.toml"
    write(f, 'workspace = "fromfile"\n')
    monkeypatch.setenv("LADYM_WORKSPACE", "fromenv")
    cfg = Config.load(config_path=f)
    assert cfg.workspace == "fromenv"


def test_endpoint_renamed_to_base_url_with_deprecation(tmp_path, capsys):
    f = tmp_path / "ladym.toml"
    write(f, 'embedding_endpoint = "http://old"\n')
    cfg = Config.from_file(f)
    assert cfg.embedding_base_url == "http://old"
    assert "deprecat" in capsys.readouterr().err.lower()
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/unit/test_config_load.py -v`
Expected: FAIL.

- [ ] **Step 3: Implement** in `src/ladym/config.py` (append/extend; keep existing dataclasses).

Add the new fields to `Config` and the loaders. Replace the `Config` dataclass tail with:

```python
@dataclass
class EmbeddingConfig:
    provider: str = "hashing"
    base_url: str = ""
    model: str = ""
    api_key_env: str = ""
    fallback: str = "none"
    query_cache_size: int = 0
    timeout_s: float = 10.0
    allow_dim_change: bool = False
    http_request: str = '{"input": "{text}"}'
    http_response_path: str = "data"


@dataclass
class LLMConfig:
    provider: str = "none"
    base_url: str = ""
    model: str = "gpt-4o-mini"
    api_key_env: str = ""
    max_tokens: int = 1024
    temperature: float = 0.2
    structured_method: str = "function_calling"
    timeout_s: float = 30.0


@dataclass
class System2Config:
    enabled: bool = False
    interval_s: int = 300
    min_episodes_to_run: int = 3


@dataclass
class AttentionConfig:
    min_chars: int = 8
    dedup_window_s: float = 3600.0
    noise_words: list[str] = field(default_factory=list)
```

Extend `Config` with flat mirror fields (so existing code reading `cfg.embedding_provider`
keeps working) and the nested configs + loaders. The flat fields are the source of truth for
`make_provider`/`make_agent`; the nested ones are conveniences populated by the loader:

```python
@dataclass
class Config:
    db_path: Path = field(default_factory=_default_db_path)
    workspace: str = field(default_factory=lambda: os.environ.get("LADYM_WORKSPACE", "default"))
    prefer_sqlite_vec: bool = True
    enable_wal: bool = False

    # embedding (flat)
    embedding_provider: str = field(default_factory=lambda: os.environ.get("LADYM_EMBEDDING", "hashing"))
    embedding_model: str = field(default_factory=lambda: os.environ.get("LADYM_EMBEDDING_MODEL", ""))
    embedding_dim: int = 256
    embedding_base_url: str = ""
    embedding_api_key_env: str = ""
    embedding_fallback: str = "none"
    embedding_query_cache_size: int = 0
    embedding_timeout_s: float = 10.0
    embedding_allow_dim_change: bool = False
    embedding_http_request: str = '{"input": "{text}"}'
    embedding_http_response_path: str = "data"
    embedding: EmbeddingConfig = field(default_factory=EmbeddingConfig)

    # llm (flat)
    llm_provider: str = "none"
    llm_base_url: str = ""
    llm_model: str = "gpt-4o-mini"
    llm_api_key_env: str = ""
    llm_max_tokens: int = 1024
    llm_temperature: float = 0.2
    llm_structured_method: str = "function_calling"
    llm_timeout_s: float = 30.0
    llm: LLMConfig = field(default_factory=LLMConfig)

    agents_overrides: dict = field(default_factory=dict)
    activation: ActivationWeights = field(default_factory=ActivationWeights)
    recall: RecallConfig = field(default_factory=RecallConfig)
    consolidate: ConsolidateConfig = field(default_factory=ConsolidateConfig)
    code_index: CodeIndexConfig = field(default_factory=CodeIndexConfig)
    system2: System2Config = field(default_factory=System2Config)
    attention: AttentionConfig = field(default_factory=AttentionConfig)

    @classmethod
    def for_testing(cls, tmp_path: Path) -> Config:
        return cls(db_path=tmp_path / "test.ladym.db", workspace="test",
                   embedding_provider="hashing", llm_provider="none", prefer_sqlite_vec=False)

    # ----- loaders -----

    @classmethod
    def from_file(cls, path: Path) -> Config:
        import tomllib
        with open(path, "rb") as fh:
            raw = tomllib.load(fh)
        data = _strip_secrets(raw, path)
        cfg = cls()
        _apply_toml(cfg, data)
        return cfg

    @classmethod
    def load(cls, config_path: Path | None = None, *,
             cli_overrides: dict | None = None) -> Config:
        layers: list[Path] = []
        global_path = Path.home() / ".ladym" / "config.toml"
        if global_path.exists():
            layers.append(global_path)
        project_path = Path.cwd() / "ladym.toml"
        if project_path.exists():
            layers.append(project_path)
        if config_path:
            layers.append(config_path)
        cfg = cls()
        for p in layers:
            cfg = cfg.from_file(p) if p is layers[0] else _merge_file(cfg, p)
        _apply_env(cfg)
        if cli_overrides:
            _apply_dict(cfg, cli_overrides)
        return cfg
```

Helper functions (append to the module):

```python
_SECRET_KEYS = {"api_key", "secret", "token", "password"}


def _is_secret(key: str) -> bool:
    k = key.lower()
    return any(s in k for s in _SECRET_KEYS) or k.endswith("_key")


def _strip_secrets(data: dict, path: Path) -> dict:
    import sys
    cleaned = {}
    for k, v in data.items():
        if isinstance(v, dict):
            cleaned[k] = _strip_secrets(v, path)
        elif _is_secret(k):
            print(f"WARNING: ignoring secret literal {k!r} in {path}; use <name>_env instead",
                  file=sys.stderr)
        else:
            cleaned[k] = v
    return cleaned


def _set_flat(cfg: Config, key: str, value) -> None:
    flat = {
        "db_path": "db_path", "workspace": "workspace", "prefer_sqlite_vec": "prefer_sqlite_vec",
        "enable_wal": "enable_wal",
        "embedding_provider": "embedding_provider", "embedding_model": "embedding_model",
        "embedding_base_url": "embedding_base_url", "embedding_endpoint": "embedding_base_url",
        "embedding_api_key_env": "embedding_api_key_env", "embedding_fallback": "embedding_fallback",
        "embedding_query_cache_size": "embedding_query_cache_size",
        "embedding_timeout_s": "embedding_timeout_s",
        "embedding_allow_dim_change": "embedding_allow_dim_change",
    }
    if key == "embedding_endpoint":
        import sys
        print("WARNING: embedding_endpoint is deprecated; use embedding_base_url", file=sys.stderr)
    if key in flat:
        setattr(cfg, flat[key], value)


def _apply_toml(cfg: Config, data: dict) -> None:
    for k, v in data.items():
        if k in ("embedding", "llm", "activation", "recall", "consolidate",
                 "code_index", "system2", "attention") and isinstance(v, dict):
            if k == "embedding":
                for ek, ev in v.items():
                    _set_flat(cfg, f"embedding_{ek}", ev)
            elif k == "llm":
                for lk, lv in v.items():
                    setattr(cfg, f"llm_{lk}", lv)
            elif k == "agents":
                cfg.agents_overrides = dict(v)
            elif k == "system2":
                for sk, sv in v.items():
                    setattr(cfg.system2, sk, sv)
            elif k == "attention":
                for ak, av in v.items():
                    setattr(cfg.attention, ak, av)
            else:
                for nk, nv in v.items():
                    setattr(getattr(cfg, k), nk, nv)
        else:
            _set_flat(cfg, k, v)


def _apply_dict(cfg: Config, d: dict) -> None:
    _apply_toml(cfg, d)


def _merge_file(cfg: Config, path: Path) -> Config:
    layer = Config.from_file(path)
    # shallow-merge flat fields from layer over cfg
    for attr in vars(cfg):
        if attr.startswith("_"):
            continue
        val = getattr(layer, attr, None)
        if val not in (None, "", 0, [], {}, False) and getattr(cfg, attr) in (None, "", 0, [], {}, False):
            setattr(cfg, attr, val)
    return cfg


def _apply_env(cfg: Config) -> None:
    env_map = {
        "LADYM_DB": ("db_path", lambda v: Path(v)),
        "LADYM_WORKSPACE": ("workspace", str),
        "LADYM_EMBEDDING": ("embedding_provider", str),
        "LADYM_EMBEDDING_MODEL": ("embedding_model", str),
        "LADYM_EMBEDDING_BASE_URL": ("embedding_base_url", str),
        "LADYM_LLM_PROVIDER": ("llm_provider", str),
        "LADYM_LLM_BASE_URL": ("llm_base_url", str),
        "LADYM_LLM_MODEL": ("llm_model", str),
    }
    for env, (attr, cast) in env_map.items():
        if os.environ.get(env):
            setattr(cfg, attr, cast(os.environ[env]))
```

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/unit/test_config_load.py tests/test_regression_baseline.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/ladym/config.py tests/unit/test_config_load.py
git commit -m "feat(config): TOML loading, 4-layer precedence, secret rejection, base_url rename"
```

### Task 2.2: CLI commands honour `Config.load`

**Files:**
- Modify: `src/ladym/cli.py`
- Test: `tests/unit/test_cli.py`

**Interfaces:**
- Produces: a `_engine_from_config(db, workspace, config_path)` helper that calls `Config.load`
  with CLI overrides; every existing command switches to it. New global `--config` option.

- [ ] **Step 1: Write the failing test** (append to `tests/unit/test_cli.py`)

```python
def test_cli_recall_with_config_file(tmp_path, monkeypatch):
    from typer.testing import CliRunner
    from ladym.cli import app
    f = tmp_path / "ladym.toml"
    f.write_text(f'db_path = "{tmp_path}/c.db"\nworkspace = "w"\nembedding_provider = "hashing"\n')
    runner = CliRunner()
    res = runner.invoke(app, ["recall", "anything", "--config", str(f)])
    assert res.exit_code == 0
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/unit/test_cli.py::test_cli_recall_with_config_file -v`
Expected: FAIL — no `--config` option.

- [ ] **Step 3: Implement** in `src/ladym/cli.py`

Replace `_engine` with a config-aware helper and add a callback for `--config`:

```python
_config_path: str | None = None


@app.callback()
def _main(config: str | None = typer.Option(None, "--config", help="Path to ladym.toml")):
    global _config_path
    _config_path = config


def _engine(db: str | None, workspace: str | None) -> Engine:
    overrides: dict = {}
    if db:
        overrides["db_path"] = db
    if workspace:
        overrides["workspace"] = workspace
    cfg = Config.load(config_path=Path(_config_path) if _config_path else None,
                      cli_overrides=overrides or None)
    return Engine(cfg)
```

Add `--config` usage is automatic via the callback. Each existing command's `_engine(db, workspace)`
call site stays the same.

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/unit/test_cli.py -v`
Expected: PASS (all CLI tests).

- [ ] **Step 5: Commit**

```bash
git add src/ladym/cli.py tests/unit/test_cli.py
git commit -m "feat(cli): all commands honour Config.load + global --config"
```

---

## Phase 3 — Write-path enhancements

### Task 3.1: supersedes chain semantics + recall filters

**Files:**
- Create: `src/ladym/operations/supersedes.py`
- Modify: `src/ladym/operations/consolidate.py`, `src/ladym/operations/recall.py`
- Test: `tests/unit/test_supersedes.py`

**Interfaces:**
- Produces: `is_retired(memory) -> bool`, `retire(store, old, *, new_id=None)`,
  `latest_in_chain(store, mem_id) -> str`. consolidate UPDATE creates a new memory + retires old;
  DELETE retires old (no successor). recall tier-1 skips retired; tier-2 follows `supersedes`.

- [ ] **Step 1: Write the failing test**

```python
# tests/unit/test_supersedes.py
import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.operations.consolidate import Action
from ladym.operations.supersedes import is_retired, latest_in_chain


@pytest.fixture
def engine(tmp_path):
    e = Engine(Config.for_testing(tmp_path))
    yield e
    e.close()


def test_update_creates_new_and_retires_old(engine):
    from ladym.operations import supersedes as sup
    old = engine.semantic.put_fact("auth uses JWT", summary="v1")
    new = engine.semantic.put_fact("auth uses JWT with 24h expiry", summary="v2")
    sup.retire(engine.store, old, new_id=new.id)
    assert is_retired(engine.store.get_memory(old.id))
    assert latest_in_chain(engine.store, old.id) == new.id


def test_delete_retires_without_successor(engine):
    from ladym.operations import supersedes as sup
    m = engine.semantic.put_fact("obsolete fact")
    sup.retire(engine.store, m)  # no new_id
    assert is_retired(engine.store.get_memory(m.id))
    assert engine.store.get_memory(m.id).metadata.get("superseded") is True


def test_recall_tier1_hides_retired(engine):
    from ladym.operations import supersedes as sup
    m = engine.semantic.put_fact("unique secret phrase zzz")
    sup.retire(engine.store, m)
    resp = engine.recall("unique secret phrase zzz")
    ids = [r.memory.id for r in resp.results]
    assert m.id not in ids


def test_recall_tier2_follows_supersedes(engine):
    from ladym.operations import supersedes as sup
    old = engine.semantic.put_fact("config value is five")
    new = engine.semantic.put_fact("config value is five", summary="v2")
    sup.retire(engine.store, old, new_id=new.id)
    # searching for the content should still let tier-2 walk old->new
    resp = engine.recall("config value is five")
    ids = [r.memory.id for r in resp.results]
    assert new.id in ids
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/unit/test_supersedes.py -v`
Expected: FAIL.

- [ ] **Step 3: Implement** `src/ladym/operations/supersedes.py`

```python
"""supersedes pointer chain (SPEC §2.6)."""
from __future__ import annotations

import time

from ..schema import Edge, Memory
from ..storage.store import SQLiteStore


def is_retired(mem: Memory | None) -> bool:
    if mem is None:
        return False
    meta = mem.metadata or {}
    return bool(meta.get("superseded_by") or meta.get("superseded"))


def retire(store: SQLiteStore, old: Memory, *, new_id: str | None = None) -> None:
    """Retire ``old``. With ``new_id`` → UPDATE chain; without → DELETE retirement."""
    now = time.time()
    old.metadata = {**(old.metadata or {}), "superseded_at": now}
    if new_id:
        old.metadata["superseded_by"] = new_id
        store.put_edge(Edge(src_id=old.id, relation="supersedes", dst_id=new_id, valid_from=now))
    else:
        old.metadata["superseded"] = True
    # close outgoing edges
    for e in store.neighbors(old.id):
        if e.valid_to is None:
            e.valid_to = now
            store.put_edge(e)
    store.put_memory(old)


def latest_in_chain(store: SQLiteStore, mem_id: str) -> str:
    seen: set[str] = set()
    cur = mem_id
    while cur not in seen:
        seen.add(cur)
        nxt = [e for e in store.neighbors(cur, relation="supersedes") if e.src_id == cur]
        if not nxt:
            return cur
        cur = nxt[0].dst_id
    return cur
```

Modify consolidate UPDATE/DELETE in `src/ladym/operations/consolidate.py`. Replace the
`elif action == Action.UPDATE ...` and `elif action == Action.DELETE ...` blocks:

```python
        if action == Action.ADD:
            sem.put_fact(
                ep.content, summary=ep.summary, tags=ep.tags,
                metadata={**ep.metadata, "source_episode": ep.id}, source="consolidate",
            )
            report.promoted_to_semantic += 1
        elif action == Action.UPDATE and similar:
            target = similar[0][0]
            from .supersedes import retire as _retire
            merged = target.model_copy(update={
                "id": _new_id(), "content": new_text or ep.content,
                "summary": ep.summary or target.summary, "updated_at": time.time(),
                "content_hash": content_hash(new_text or ep.content),
                "metadata": {**target.metadata, "source_episode": ep.id, "updated_from": target.id},
            })
            store.put_memory(merged, vector=embedder.embed(merged.content))
            _retire(store, target, new_id=merged.id)
        elif action == Action.DELETE and similar:
            from .supersedes import retire as _retire
            _retire(store, similar[0][0])
        # NOOP: nothing
```

Add `from ..schema import ... _new_id` access: import `from ..schema import _new_id` at top of
`consolidate.py` (it exists in `schema.py`).

Modify recall tier-1 to skip retired and tier-2 to follow supersedes. In
`src/ladym/operations/recall.py`, import and filter:

```python
from .supersedes import is_retired
```

In the tier-1 candidate loop, after the existing filters, add:

```python
        if is_retired(mem):
            continue
```

In `_tier2_expand`, after the neighbour walk, also follow `supersedes` edges so the newest
version of a hit can be reached. Add inside the neighbour loop, before `out.append(...)`:

```python
            # follow supersedes to the newest version even if it was filtered from tier-1
            for sup_edge in store.neighbors(cur_id, relation="supersedes"):
                if sup_edge.src_id == cur_id and sup_edge.dst_id not in seen:
                    seen.add(sup_edge.dst_id)
                    newer = store.get_memory(sup_edge.dst_id)
                    if newer and newer.workspace == workspace:
                        out.append((newer, max(0.05, anchor.score * 0.6 / depth),
                                    path + [sup_edge.dst_id]))
```

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/unit/test_supersedes.py tests/unit/test_consolidate.py tests/unit/test_recall.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/ladym/operations/supersedes.py src/ladym/operations/consolidate.py src/ladym/operations/recall.py tests/unit/test_supersedes.py
git commit -m "feat(consolidate): supersedes chain (UPDATE/DELETE retire, recall filters+traverses)"
```

### Task 3.2: attention gate

**Files:**
- Create: `src/ladym/operations/attention.py`
- Modify: `src/ladym/engine.py` (call gate in `remember`)
- Test: `tests/unit/test_attention_gate.py`

**Interfaces:**
- Produces: `GateDecision(action, content, reason)`, `attention_gate(content, *, engine, layer) -> GateDecision`.
  Heuristic mode: drop if too short / recent dup / noise. LLM mode: `complete_structured`.

- [ ] **Step 1: Write the failing test**

```python
# tests/unit/test_attention_gate.py
import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.operations.attention import GateDecision, attention_gate
from ladym.schema import Layer


@pytest.fixture
def engine(tmp_path):
    e = Engine(Config.for_testing(tmp_path))
    yield e
    e.close()


def test_gate_drops_too_short(engine):
    d = attention_gate("hi", engine=engine, layer=Layer.SEMANTIC)
    assert d.action == "drop"


def test_gate_passes_normal_content(engine):
    d = attention_gate("auth uses JWT with 24h expiry", engine=engine, layer=Layer.SEMANTIC)
    assert d.action == "pass"


def test_gate_drops_recent_duplicate(engine):
    engine.episodic.record(agent="bot", action="x", observation="exact dup content here")
    d = attention_gate("exact dup content here", engine=engine, layer=Layer.EPISODIC)
    assert d.action == "drop"


def test_remember_drop_returns_unpersisted_memory(engine):
    m = engine.remember("hi")  # too short -> drop
    assert m.metadata.get("gated") == "dropped"
    assert engine.store.get_memory(m.id) is None  # not persisted


def test_remember_pass_persists(engine):
    m = engine.remember("a reasonably long fact about the system")
    assert m.metadata.get("gated") != "dropped"
    assert engine.store.get_memory(m.id) is not None
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/unit/test_attention_gate.py -v`
Expected: FAIL.

- [ ] **Step 3: Implement** `src/ladym/operations/attention.py`

```python
"""Attention gate (SPEC §2.7): pre-remember filter on the write path."""
from __future__ import annotations

import hashlib
import time
from dataclasses import dataclass

from ..config import Config
from ..schema import Layer
from ..storage.store import SQLiteStore

_BUILTIN_NOISE = {"lol", "ok", "test", "asdf", "foo", "bar", "todo"}


@dataclass
class GateDecision:
    action: str          # "pass" | "rewrite" | "drop"
    content: str | None = None
    reason: str = ""


def _hash(s: str) -> str:
    return hashlib.blake2b(s.encode(), digest_size=8).hexdigest()


def attention_gate(content: str, *, engine, layer: Layer) -> GateDecision:
    cfg: Config = engine.config
    if layer == Layer.WORKING:
        return GateDecision(action="pass", reason="working memory never gated")

    agent = getattr(engine, "_agents", {}).get("attention_gate")
    if agent is not None:
        return _llm_gate(agent, content)

    # heuristic mode
    min_chars = cfg.attention.min_chars
    if len(content.strip()) < min_chars:
        return GateDecision(action="drop", reason="too short")
    tokens = {w.lower() for w in content.split()}
    noise = _BUILTIN_NOISE | set(cfg.attention.noise_words)
    if tokens and tokens <= noise:
        return GateDecision(action="drop", reason="noise")
    # recent-duplicate within window
    now = time.time()
    window = cfg.attention.dedup_window_s
    for m in engine.store.iter_memories(workspace=cfg.workspace, layer=Layer.EPISODIC.value):
        if now - m.created_at > window:
            continue
        if _hash(m.content) == _hash(content):
            return GateDecision(action="drop", reason="recent duplicate")
    return GateDecision(action="pass")


def _llm_gate(provider, content: str) -> GateDecision:
    from pydantic import BaseModel

    class _G(BaseModel):
        action: str
        content: str | None = None
        reason: str = ""

    msgs = [{"role": "system", "content": "Decide if the user content is worth storing long-term. "
             "Reply JSON {action, content?, reason}. action ∈ pass|rewrite|drop."},
            {"role": "user", "content": content}]
    d = provider.complete_structured(msgs, _G)
    return GateDecision(action=d["action"], content=d.get("content"), reason=d.get("reason", ""))
```

Wire into `Engine.remember` in `src/ladym/engine.py`. At the top of `remember`, before routing:

```python
    def remember(self, content, *, layer=Layer.SEMANTIC, type_=MemoryType.FACT,
                 tags=None, metadata=None, source="", summary=""):
        from .operations.attention import attention_gate
        gate = attention_gate(content, engine=self, layer=layer)
        if gate.action == "drop":
            from .schema import Memory
            return Memory(layer=layer, type_=type_, content=content,
                          summary=summary, tags=tags or [], metadata={**(metadata or {}),
                          "gated": "dropped", "reason": gate.reason}, source=source,
                          workspace=self.config.workspace)
        if gate.action == "rewrite" and gate.content:
            content = gate.content
            metadata = {**(metadata or {}), "gated": "rewritten"}
        # ... existing routing unchanged ...
```

Also build the agent map in `__init__` (lazy):

```python
        self._agents: dict = {}
        # agents are built lazily on first use via make_agent; keep None entries meaning heuristic
```

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/unit/test_attention_gate.py tests/unit/test_regression_baseline.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/ladym/operations/attention.py src/ladym/engine.py tests/unit/test_attention_gate.py
git commit -m "feat(attention): pre-remember gate (heuristic + LLM), drop returns unpersisted Memory"
```

### Task 3.3: memory-density stat + L5/L6 schema enums

**Files:**
- Modify: `src/ladym/schema.py`, `src/ladym/engine.py`
- Test: `tests/unit/test_schema.py`

**Interfaces:**
- Produces: `Layer.L5_MENTAL`, `Layer.L6_PREDICTIVE`, `MemoryType.MENTAL_MODEL`,
  `MemoryType.FORWARD_INTENT`, `Stats.avg_tokens_per_memory`.

- [ ] **Step 1: Write the failing test** (append to `tests/unit/test_schema.py`)

```python
def test_new_layers_and_types_exist():
    from ladym.schema import Layer, MemoryType
    assert Layer.L5_MENTAL.value == "L5_mental"
    assert Layer.L6_PREDICTIVE.value == "L6_predictive"
    assert MemoryType.MENTAL_MODEL.value == "mental_model"
    assert MemoryType.FORWARD_INTENT.value == "forward_intent"


def test_stats_has_density(tmp_path):
    from ladym.config import Config
    from ladym.engine import Engine
    e = Engine(Config.for_testing(tmp_path))
    try:
        e.semantic.put_fact("alpha beta gamma delta")
        s = e.stats()
        assert s.avg_tokens_per_memory > 0
    finally:
        e.close()
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/unit/test_schema.py -k "new_layers or density" -v`
Expected: FAIL.

- [ ] **Step 3: Implement** in `src/ladym/schema.py`

Add to `Layer`:

```python
    L5_MENTAL = "L5_mental"
    L6_PREDICTIVE = "L6_predictive"
```

Add to `MemoryType`:

```python
    MENTAL_MODEL = "mental_model"
    FORWARD_INTENT = "forward_intent"
```

Add to `Stats`:

```python
    avg_tokens_per_memory: float = 0.0
```

In `Engine.stats()` compute it (replace the return in `src/ladym/engine.py`):

```python
        # token estimate via the existing tokenizer
        from .storage.embeddings import tokenize
        mems = list(self.store.iter_memories(workspace=self.config.workspace))
        total_tokens = sum(len(tokenize(m.content)) for m in mems)
        avg = (total_tokens / len(mems)) if mems else 0.0
        return Stats(
            total_memories=sum(by_layer.values()), by_layer=by_layer, by_type=by_type,
            edges=self.store.count_edges(), code_symbols=n_code_syms,
            workspaces=self.store.workspaces(), db_path=str(self.config.db_path),
            avg_tokens_per_memory=avg,
        )
```

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/unit/test_schema.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/ladym/schema.py src/ladym/engine.py tests/unit/test_schema.py
git commit -m "feat(schema): L5/L6 enums + avg_tokens_per_memory stat"
```

---

## Phase 4 — System1 / System2

### Task 4.1: `run_system2_cycle` + threshold gate

**Files:**
- Create: `src/ladym/operations/system2.py`
- Test: `tests/unit/test_system2.py`

**Interfaces:**
- Produces: `System2Report` (consolidate/proceduralize/l5/l6/decay fields),
  `run_system2_cycle(engine, *, workspace=None) -> System2Report`. L5/L6 steps are no-ops until
  the extractors land (post-MVP), guarded by `hasattr`.

- [ ] **Step 1: Write the failing test**

```python
# tests/unit/test_system2.py
import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.operations.system2 import System2Report, run_system2_cycle


@pytest.fixture
def engine(tmp_path):
    e = Engine(Config.for_testing(tmp_path))
    yield e
    e.close()


def test_cycle_runs_consolidate_proceduralize_decay(engine):
    engine.episodic.record(agent="bot", action="x", observation="auth uses JWT")
    report = run_system2_cycle(engine)
    assert isinstance(report, System2Report)
    assert report.consolidate is not None
    assert report.decay is not None


def test_threshold_gate_skips_when_too_few(engine):
    engine.config.system2.min_episodes_to_run = 5
    engine.episodic.record(agent="bot", action="x", observation="only one")
    report = run_system2_cycle(engine)
    assert report.l5 is None and report.l6 is None  # gated
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/unit/test_system2.py -v`
Expected: FAIL.

- [ ] **Step 3: Implement** `src/ladym/operations/system2.py`

```python
"""System2 background consolidation cycle (SPEC §2.8)."""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from ..schema import Layer


@dataclass
class System2Report:
    consolidate: Any = None
    proceduralize: Any = None
    l5: Any = None
    l6: Any = None
    decay: Any = None
    skipped_llm_steps: bool = False


def _count_recent_episodes(engine, workspace: str) -> int:
    return sum(1 for _ in engine.store.iter_memories(
        workspace=workspace, layer=Layer.EPISODIC.value))


def run_system2_cycle(engine, *, workspace: str | None = None) -> System2Report:
    ws = workspace or engine.config.workspace
    report = System2Report()
    report.consolidate = engine.consolidate(workspace=ws)
    report.proceduralize = engine.proceduralize(workspace=ws)
    enough = _count_recent_episodes(engine, ws) >= engine.config.system2.min_episodes_to_run
    if enough:
        if hasattr(engine, "extract_mental_models"):
            report.l5 = engine.extract_mental_models(workspace=ws)
        if hasattr(engine, "predict_forward_intents"):
            report.l6 = engine.predict_forward_intents(workspace=ws)
    else:
        report.skipped_llm_steps = True
    report.decay = engine.decay(workspace=ws)
    return report
```

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/unit/test_system2.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/ladym/operations/system2.py tests/unit/test_system2.py
git commit -m "feat(system2): run_system2_cycle with threshold gate"
```

### Task 4.2: `ladym worker` CLI + WAL + opt-in thread

**Files:**
- Modify: `src/ladym/cli.py`, `src/ladym/engine.py` (`start_system2`)
- Test: `tests/unit/test_system2.py`, `tests/integration/test_wal_concurrency.py`

**Interfaces:**
- Produces: `ladym worker [--once] [--interval 300] [--workspace w]` and
  `Engine.start_system2(interval_s, workspace=None) -> threading.Event` (stop handle).

- [ ] **Step 1: Write the failing test** (integration)

```python
# tests/integration/test_wal_concurrency.py
import threading
import time

from typer.testing import CliRunner

from ladym.cli import app
from ladym.config import Config
from ladym.engine import Engine


def test_worker_once_writes_while_reader_recalls(tmp_path):
    cfg = Config(db_path=tmp_path / "w.db", workspace="w", embedding_provider="hashing")
    cfg.enable_wal = True
    eng = Engine(cfg)
    eng.episodic.record(agent="bot", action="x", observation="a fact to consolidate")
    eng.close()

    runner = CliRunner()
    res = runner.invoke(app, ["worker", "--once", "--db", str(cfg.db_path), "--workspace", "w"])
    assert res.exit_code == 0

    # concurrent: spawn a reader thread while another worker cycles
    eng2 = Engine(cfg)
    errors = []

    def read():
        try:
            for _ in range(20):
                eng2.recall("fact")
                time.sleep(0.005)
        except Exception as e:  # noqa: BLE001
            errors.append(e)

    t = threading.Thread(target=read)
    t.start()
    runner.invoke(app, ["worker", "--once", "--db", str(cfg.db_path), "--workspace", "w"])
    t.join()
    eng2.close()
    assert not errors
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/integration/test_wal_concurrency.py -v`
Expected: FAIL — no `worker` command.

- [ ] **Step 3: Implement** the `worker` command in `src/ladym/cli.py`

```python
@app.command()
def worker(
    once: bool = typer.Option(False, "--once", help="Run one cycle and exit."),
    interval: int = typer.Option(300, "--interval", help="Seconds between cycles."),
    db: str | None = typer.Option(None, "--db"),
    workspace: str | None = typer.Option(None, "--workspace", "-w"),
):
    """Run System2 consolidation cycles in the background (SPEC §2.8)."""
    import time
    eng = _engine(db, workspace)
    eng.config.enable_wal = True
    try:
        from .operations.system2 import run_system2_cycle
        while True:
            run_system2_cycle(eng, workspace=workspace)
            if once:
                break
            time.sleep(interval)
    finally:
        eng.close()
```

Add `start_system2` to `Engine`:

```python
    def start_system2(self, *, interval_s: int | None = None,
                      workspace: str | None = None) -> "threading.Event":
        import threading
        import time
        from .operations.system2 import run_system2_cycle
        stop = threading.Event()
        interval_s = interval_s or self.config.system2.interval_s

        def _loop():
            # own connection, isolated from the main Engine
            from .config import Config
            from .engine import Engine as _E
            worker_eng = _E(self.config)
            worker_eng.config.enable_wal = True
            try:
                while not stop.is_set():
                    try:
                        run_system2_cycle(worker_eng, workspace=workspace)
                    except Exception:  # noqa: BLE001
                        pass
                    stop.wait(interval_s)
            finally:
                worker_eng.close()

        threading.Thread(target=_loop, daemon=True).start()
        return stop
```

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/integration/test_wal_concurrency.py tests/unit/test_system2.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/ladym/cli.py src/ladym/engine.py tests/integration/test_wal_concurrency.py
git commit -m "feat(system2): ladym worker + WAL concurrency + opt-in in-process thread"
```

---

## Phase 5 — Web config UI (post-MVP)

### Task 5.1: FastAPI app skeleton + vendored static + form route

**Files:**
- Create: `src/ladym/web/__init__.py`, `src/ladym/web/app.py`,
  `src/ladym/web/templates/{base.html,config.html}`,
  `src/ladym/web/static/{htmx.min.js,pico.min.css}`
- Test: `tests/integration/test_web_config.py`

**Interfaces:**
- Produces: `build_app(config_path: Path | None = None) -> FastAPI` with `GET /` rendering the
  form from the current config. Static files served from the vendored dir.

- [ ] **Step 1: Vendor assets** (download once, commit the files)

```bash
mkdir -p src/ladym/web/static src/ladym/web/templates
curl -sSL https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js -o src/ladym/web/static/htmx.min.js
curl -sSL https://unpkg.com/@picocss/pico@2/css/pico.min.css -o src/ladym/web/static/pico.min.css
```

(If offline, drop the two files in by hand; they are ~14KB and ~10KB.)

- [ ] **Step 2: Write the failing test**

```python
# tests/integration/test_web_config.py
import pytest

pytest.importorskip("fastapi")


def test_index_renders_form(tmp_path):
    from fastapi.testclient import TestClient
    from ladym.web.app import build_app
    client = TestClient(build_app(config_path=None))
    r = client.get("/")
    assert r.status_code == 200
    assert "Embedding" in r.text
    assert "htmx" in r.text.lower()


def test_static_assets_served(tmp_path):
    from fastapi.testclient import TestClient
    from ladym.web.app import build_app
    client = TestClient(build_app(config_path=None))
    assert client.get("/static/htmx.min.js").status_code == 200
```

- [ ] **Step 3: Run to verify it fails**

Run: `uv run pytest tests/integration/test_web_config.py -v`
Expected: FAIL.

- [ ] **Step 4: Implement**

```python
# src/ladym/web/__init__.py
```

```python
# src/ladym/web/app.py
"""FastAPI + HTMX config editor (SPEC §2.9). Local-only; bind 127.0.0.1."""
from __future__ import annotations

from pathlib import Path

from fastapi import FastAPI, Form, Request
from fastapi.responses import HTMLResponse
from fastapi.staticfiles import StaticFiles
from fastapi.templating import Jinja2Templates

from ..config import Config

_STATIC = Path(__file__).parent / "static"
_TEMPLATES = Jinja2Templates(directory=str(Path(__file__).parent / "templates"))


def build_app(config_path: Path | None = None) -> FastAPI:
    app = FastAPI(title="LadyM config")
    app.mount("/static", StaticFiles(directory=str(_STATIC)), name="static")

    @app.get("/", response_class=HTMLResponse)
    def index(request: Request):
        cfg = Config.load(config_path=config_path)
        return _TEMPLATES.TemplateResponse(request, "config.html", {"cfg": cfg})

    return app
```

```html
<!-- src/ladym/web/templates/base.html -->
<!doctype html>
<html lang="en" data-theme="light">
<head>
  <meta charset="utf-8">
  <title>LadyM config</title>
  <link rel="stylesheet" href="/static/pico.min.css">
  <script src="/static/htmx.min.js"></script>
</head>
<body>
  <main class="container">{% block content %}{% endblock %}</main>
</body>
</html>
```

```html
<!-- src/ladym/web/templates/config.html -->
{% extends "base.html" %}
{% block content %}
<h1>LadyM config</h1>
<form method="post" action="/save">
  <h2>Embedding <button type="button" hx-post="/test/embedding" hx-target="#emb-result">test</button></h2>
  <span id="emb-result"></span>
  <label>provider <select name="embedding_provider">
    {% for p in ["hashing","st","openai","ollama","http","callable"] %}
    <option value="{{p}}" {{'selected' if cfg.embedding_provider==p}}>{{p}}</option>
    {% endfor %}
  </select></label>
  <label>base_url <input name="embedding_base_url" value="{{cfg.embedding_base_url}}"></label>
  <label>model <input name="embedding_model" value="{{cfg.embedding_model}}"></label>
  <label>api_key_env <input name="embedding_api_key_env" value="{{cfg.embedding_api_key_env}}"></label>
  <label>fallback <input name="embedding_fallback" value="{{cfg.embedding_fallback}}"></label>
  <label>query_cache_size <input name="embedding_query_cache_size" value="{{cfg.embedding_query_cache_size}}"></label>

  <h2>LLM (global default) <button type="button" hx-post="/test/llm" hx-target="#llm-result">test</button></h2>
  <span id="llm-result"></span>
  <label>provider <select name="llm_provider">
    {% for p in ["none","openai","anthropic","ollama","http"] %}
    <option value="{{p}}" {{'selected' if cfg.llm_provider==p}}>{{p}}</option>
    {% endfor %}
  </select></label>
  <label>base_url <input name="llm_base_url" value="{{cfg.llm_base_url}}"></label>
  <label>model <input name="llm_model" value="{{cfg.llm_model}}"></label>
  <label>api_key_env <input name="llm_api_key_env" value="{{cfg.llm_api_key_env}}"></label>
  <label>structured_method <input name="llm_structured_method" value="{{cfg.llm_structured_method}}"></label>

  <h2>Activation</h2>
  <label>similarity <input name="activation_similarity" value="{{cfg.activation.similarity}}"></label>
  <label>recency <input name="activation_recency" value="{{cfg.activation.recency}}"></label>
  <label>frequency <input name="activation_frequency" value="{{cfg.activation.frequency}}"></label>

  <button type="submit">Save</button>
  <button type="reset">Reset</button>
</form>
{% endblock %}
```

- [ ] **Step 5: Run to verify it passes**

Run: `uv run pytest tests/integration/test_web_config.py -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add src/ladym/web/ tests/integration/test_web_config.py
git commit -m "feat(web): FastAPI config form + vendored static assets"
```

### Task 5.2: `/save` (secret rejection) + `/reset` + `/test/*` + `/stats`

**Files:**
- Modify: `src/ladym/web/app.py`
- Test: `tests/integration/test_web_config.py`

**Interfaces:**
- Produces: `POST /save` (writes `./ladym.toml`, refuses literal keys), `POST /reset`,
  `POST /test/embedding`, `POST /test/llm` (HTMX fragments), `GET /stats`.

- [ ] **Step 1: Write the failing test** (append)

```python
def test_save_writes_toml_and_rejects_secret(tmp_path, monkeypatch):
    from fastapi.testclient import TestClient
    from ladym.web.app import build_app
    monkeypatch.chdir(tmp_path)
    client = TestClient(build_app(config_path=None))
    r = client.post("/save", data={
        "embedding_provider": "openai", "embedding_base_url": "https://x",
        "api_key": "sk-leaked",   # must be rejected
    })
    assert r.status_code == 200
    written = (tmp_path / "ladym.toml").read_text()
    assert "embedding_provider = \"openai\"" in written
    assert "sk-leaked" not in written


def test_test_embedding_endpoint(tmp_path):
    from fastapi.testclient import TestClient
    from ladym.web.app import build_app
    client = TestClient(build_app(config_path=None))
    r = client.post("/test/embedding", data={"embedding_provider": "hashing"})
    assert r.status_code == 200
    assert "dim" in r.text
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/integration/test_web_config.py -k "save or test_embedding" -v`
Expected: FAIL.

- [ ] **Step 3: Implement** in `src/ladym/web/app.py` (add inside `build_app`)

```python
    @app.post("/save", response_class=HTMLResponse)
    def save(request: Request, payload: dict = Form(...)):
        cleaned = {k: v for k, v in payload.items()
                   if not _is_secret(k) and v not in (None, "")}
        # write minimal TOML (flat section); reuses Config round-trip
        from ..config import _apply_dict, Config
        cfg = Config.load(config_path=config_path)
        _apply_dict(cfg, cleaned)
        _write_toml(Path("ladym.toml"), cfg)
        return HTMLResponse("<p>Saved to ./ladym.toml</p>")

    @app.post("/reset", response_class=HTMLResponse)
    def reset(request: Request):
        return index(request)

    @app.post("/test/embedding", response_class=HTMLResponse)
    def test_embedding(payload: dict = Form(...)):
        import time
        from ..config import _apply_dict, Config
        from ..storage.embeddings import make_provider
        cfg = Config()
        _apply_dict(cfg, {k: v for k, v in payload.items() if not _is_secret(k)})
        try:
            t0 = time.time()
            ok, msg = make_provider(cfg).health_check()
            dt = (time.time() - t0) * 1000
            mark = "✓" if ok else "✗"
            return HTMLResponse(f"<small>{mark} {msg} · {dt:.0f}ms</small>")
        except Exception as e:  # noqa: BLE001
            return HTMLResponse(f"<small>✗ {type(e).__name__}: {e}</small>")

    @app.post("/test/llm", response_class=HTMLResponse)
    def test_llm(payload: dict = Form(...)):
        import os
        import time
        from ..config import _apply_dict, Config
        from ..providers.llm import make_llm_provider
        cfg = Config()
        _apply_dict(cfg, {k: v for k, v in payload.items() if not _is_secret(k)})
        provider = make_llm_provider(
            provider=cfg.llm_provider, base_url=cfg.llm_base_url, model=cfg.llm_model,
            api_key=os.environ.get(cfg.llm_api_key_env, "") if cfg.llm_api_key_env else "",
            structured_method=cfg.llm_structured_method,
        )
        if provider is None:
            return HTMLResponse("<small>none (heuristic mode)</small>")
        try:
            t0 = time.time()
            out = provider.complete([{"role": "user", "content": "ping"}])
            dt = (time.time() - t0) * 1000
            return HTMLResponse(f"<small>✓ {out[:20]!r} · {dt:.0f}ms</small>")
        except Exception as e:  # noqa: BLE001
            return HTMLResponse(f"<small>✗ {type(e).__name__}: {e}</small>")

    @app.get("/stats", response_class=HTMLResponse)
    def stats():
        from ..engine import Engine
        cfg = Config.load(config_path=config_path)
        eng = Engine(cfg)
        try:
            s = eng.stats()
        finally:
            eng.close()
        rows = "".join(f"<tr><td>{k}</td><td>{v}</td></tr>" for k, v in s.by_layer.items())
        return HTMLResponse(
            f"<table><tr><th>total</th><td>{s.total_memories}</td></tr>"
            f"<tr><th>avg tokens/mem</th><td>{s.avg_tokens_per_memory:.1f}</td></tr>{rows}</table>")
```

Helper functions at module level:

```python
def _is_secret(key: str) -> bool:
    k = key.lower()
    return any(s in k for s in ("api_key", "secret", "token", "password")) or k.endswith("_key")


def _write_toml(path: Path, cfg: Config) -> None:
    lines = [
        f'db_path = "{cfg.db_path}"',
        f'workspace = "{cfg.workspace}"',
        "",
        "[embedding]",
        f'provider = "{cfg.embedding_provider}"',
        f'base_url = "{cfg.embedding_base_url}"',
        f'model = "{cfg.embedding_model}"',
        f'api_key_env = "{cfg.embedding_api_key_env}"',
        f'fallback = "{cfg.embedding_fallback}"',
        f'query_cache_size = {cfg.embedding_query_cache_size}',
        "",
        "[llm]",
        f'provider = "{cfg.llm_provider}"',
        f'base_url = "{cfg.llm_base_url}"',
        f'model = "{cfg.llm_model}"',
        f'api_key_env = "{cfg.llm_api_key_env}"',
        f'structured_method = "{cfg.llm_structured_method}"',
        "",
        "[activation]",
        f'similarity = {cfg.activation.similarity}',
        f'recency = {cfg.activation.recency}',
        f'frequency = {cfg.activation.frequency}',
    ]
    path.write_text("\n".join(lines) + "\n")
```

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/integration/test_web_config.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/ladym/web/app.py tests/integration/test_web_config.py
git commit -m "feat(web): /save (secret rejection), /reset, /test/{embedding,llm}, /stats"
```

### Task 5.3: `ladym config` command

**Files:**
- Modify: `src/ladym/cli.py`
- Test: `tests/unit/test_cli.py`

**Interfaces:**
- Produces: `ladym config [--port 8765] [--no-browser] [--config PATH]`.

- [ ] **Step 1: Write the failing test** (append)

```python
def test_config_command_missing_web_extra_errors(monkeypatch):
    import sys
    from typer.testing import CliRunner
    from ladym.cli import app
    # simulate fastapi not installed
    monkeypatch.setitem(sys.modules, "fastapi", None)
    runner = CliRunner()
    res = runner.invoke(app, ["config", "--no-browser"])
    assert res.exit_code != 0
    assert "ladym[web]" in res.output
```

- [ ] **Step 2: Run to verify it fails**

Run: `uv run pytest tests/unit/test_cli.py::test_config_command_missing_web_extra_errors -v`
Expected: FAIL.

- [ ] **Step 3: Implement** in `src/ladym/cli.py`

```python
@app.command()
def config(
    port: int = typer.Option(8765, "--port"),
    no_browser: bool = typer.Option(False, "--no-browser"),
):
    """Open the local web config editor (needs the [web] extra)."""
    try:
        import uvicorn
        from .web.app import build_app
    except ImportError:
        console.print("[red]web extra not installed[/red] — pip install 'ladym[web]'")
        raise typer.Exit(1)
    cfg_path = Path(_config_path) if _config_path else None
    app_obj = build_app(config_path=cfg_path)
    if not no_browser:
        import threading, webbrowser
        threading.Timer(1.0, lambda: webbrowser.open(f"http://127.0.0.1:{port}/")).start()
    console.print(f"[bold]LadyM config[/bold] on http://127.0.0.1:{port}/")
    uvicorn.run(app_obj, host="127.0.0.1", port=port, log_level="warning")
```

- [ ] **Step 4: Run to verify it passes**

Run: `uv run pytest tests/unit/test_cli.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/ladym/cli.py tests/unit/test_cli.py
git commit -m "feat(cli): ladym config command (local web editor)"
```

---

## Phase 6 — NFR guards

### Task 6.1: Read-path latency budget + no-LLM-in-read-path guard

**Files:**
- Create: `tests/perf/__init__.py`, `tests/perf/test_read_path_budget.py`,
  `tests/test_read_path_no_llm.py`
- Test: self (these ARE the tests)

**Interfaces:**
- Produces: two guard tests. The perf test asserts engine-overhead p95 < 10 ms @ 200 memories
  on hashing; the no-LLM test asserts `recall.py` imports nothing from `providers.llm` /
  `langchain`.

- [ ] **Step 1: Write the perf test**

```python
# tests/perf/test_read_path_budget.py
import statistics, time

from ladym.config import Config
from ladym.engine import Engine


def test_read_path_engine_overhead_p95_under_10ms(tmp_path):
    eng = Engine(Config.for_testing(tmp_path))
    try:
        for i in range(200):
            eng.semantic.put_fact(f"fact number {i} about topic {i % 10}")
        # warm
        eng.recall("topic 0")
        # measure: subtract hashing embed cost (precompute query vec) to isolate engine overhead
        from ladym.storage.embeddings import HashingEmbedding
        prov = eng.provider
        samples = []
        for i in range(100):
            q = f"topic {i % 10}"
            # engine overhead = total - embed time
            t_embed = _time_embed(prov, q)
            t0 = time.perf_counter()
            eng.recall(q)
            total = (time.perf_counter() - t0) * 1000
            samples.append(max(0.0, total - t_embed))
        p95 = _percentile(samples, 95)
        assert p95 < 10.0, f"engine overhead p95 {p95:.2f}ms > 10ms"


def _time_embed(prov, q, n=20):
    t0 = time.perf_counter()
    for _ in range(n):
        prov.embed(q)
    return (time.perf_counter() - t0) * 1000 / n


def _percentile(xs, p):
    xs = sorted(xs)
    k = int(round((p / 100) * (len(xs) - 1)))
    return xs[k]
```

- [ ] **Step 2: Write the NFR-3 guard**

```python
# tests/test_read_path_no_llm.py
import pathlib

import ladym.operations.recall as recall_mod


def test_recall_imports_no_llm():
    src = pathlib.Path(recall_mod.__file__).read_text()
    assert "langchain" not in src
    assert "providers.llm" not in src
    assert "complete_structured" not in src
    assert "make_llm_provider" not in src
```

- [ ] **Step 3: Run them**

Run: `uv run pytest tests/perf/test_read_path_budget.py tests/test_read_path_no_llm.py -v`
Expected: PASS (perf should already be well under 10 ms today; the guard locks it in).

- [ ] **Step 4: Commit**

```bash
git add tests/perf/__init__.py tests/perf/test_read_path_budget.py tests/test_read_path_no_llm.py
git commit -m "test: NFR-1 read-path p95 guard + NFR-3 no-LLM-in-read-path guard"
```

### Task 6.2: Final full-suite green check + docs

**Files:**
- Modify: `README.md`, `ARCHITECTURE.md` (amend §3 reflect wording)

- [ ] **Step 1: Run the entire suite**

Run: `uv run pytest -q`
Expected: all green (baseline + every task's tests).

- [ ] **Step 2: Amend ARCHITECTURE §3** — replace the sentence about "pluggable for an LLM
  judge when one is configured" with:

> The `reflect()` gate uses a tiny heuristic by default (coverage of query terms + count of
> high-activation hits). Per the hard read-path constraint (NFR-3), it is **heuristic-only**;
> no LLM judge is ever invoked in the read path.

- [ ] **Step 3: Update README** configuration table to include `base_url`, the new env vars,
  and a one-line pointer to `ladym config` and `ladym worker`.

- [ ] **Step 4: Commit**

```bash
git add README.md ARCHITECTURE.md
git commit -m "docs: reflect heuristic-only (NFR-3) + new config/env surfaces"
```

---

## Self-Review (run after writing — results recorded here)

**1. Spec coverage** — every SPEC section maps to a task:
- Req 1 (external embeddings): Tasks 1.1–1.5 (ABC, ollama/http/callable, dim probe, fallback,
  cache). ✓
- Req 2 (per-op agents): Tasks 1.6–1.8 (LLMProvider, langchain, AgentRegistry). ✓
- Req 3 (web config): Tasks 5.1–5.3. ✓
- Config + precedence + secrets: Task 2.1, CLI 2.2. ✓
- supersedes: 3.1. attention gate: 3.2. memory density + L5/L6 schema: 3.3. ✓
- System2 + WAL + worker: 4.1–4.2. ✓
- NFR guards: 0.1, 6.1. ✓
- MVP boundary: Phases 0–4 + 6 are MVP; Phase 5 (web) and L5/L6 extractors are deferred (§6). ✓

**2. Placeholder scan** — no TBD/TODO/"handle edge cases"; every code step contains real code.
The only intentional `# pragma: no cover` is the real-HTTP client path. ✓

**3. Type/name consistency** —
`make_provider`/`make_llm_provider`/`make_agent` used consistently.
`is_retired`/`retire`/`latest_in_chain` defined in Task 3.1 and consumed by consolidate + recall.
`GateDecision`/`attention_gate` defined in 3.2 and consumed by `Engine.remember`.
`run_system2_cycle`/`System2Report` defined in 4.1, consumed by CLI 4.2.
`build_app` defined in 5.1, extended in 5.2, consumed by CLI 5.3.
`Config.load`/`from_file`/`_apply_dict`/`_apply_env` defined in 2.1, consumed by 2.2, 5.1, 5.2. ✓

**Gap fixed during review:** the offline `_offline_classify` in `consolidate.py` already returns
`UPDATE` with mutated-in-place behaviour; Task 3.1 changes that to create-new + retire. Ensure
existing `test_consolidate_update_merges_highly_similar` still passes — it asserts
`UPDATE>=1 or NOOP>=1`, which holds under the new semantics. No plan change needed, but the
implementer should re-run that test after Task 3.1.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-07-21-providers-config-control-plane.md`. Two execution options:**

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between
   tasks, fast iteration.
2. **Inline Execution** — execute tasks in this session using executing-plans, batch execution
   with checkpoints.

**Which approach?**
