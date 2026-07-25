# LangGraph Integration (Tools + Nodes) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `ladym.langgraph` package that lets developers use ladyM as the memory layer for LangGraph/LangChain agents, via two equivalent paths — Tools (LLM-driven) and Nodes (auto per-turn injection).

**Architecture:** A new optional `[langgraph]` extra + a `src/ladym/langgraph/` subpackage (`_runtime` + `tools` + `nodes`). Zero changes to ladyM core (`engine.py` / `schema.py` / `config.py` untouched). All langgraph/langchain imports are lazy so `import ladym` is unaffected when the extra is absent.

**Tech Stack:** Python ≥3.11, ladyM core (`Engine` / `Config` / `schema`), `langgraph>=1.0`, `langchain-core>=0.3` (already declared under `[llm]`).

**Spec:** `docs/superpowers/specs/2026-07-25-langgraph-integration-design.md`

## Global Constraints

- **No core changes:** `src/ladym/engine.py`, `src/ladym/schema.py`, `src/ladym/config.py` stay byte-identical. The integration only consumes their public API.
- **Lazy imports:** no `langgraph`/`langchain_core` import at `src/ladym/__init__.py` top level, nor at subpackage module level except inside factory functions or under `TYPE_CHECKING`.
- **Offline tests only:** use `Config.for_testing(tmp_path)` (hashing embedding, `llm_provider="none"`, `prefer_sqlite_vec=False`). No network, no real LLM.
- **Test gating:** every langgraph test file starts with `pytest.importorskip("langgraph")` (or `"langchain_core"` for runtime-only tests) so a missing extra skips, not fails.
- **Style:** ruff `line-length=100`, `target-version="py311"`; follow the existing `providers/` module pattern (module docstring + `from __future__ import annotations` + `__all__`).
- **Commit policy (user preference "b"):** accumulate ALL changes on a feature branch; do NOT commit per-task. Each task ends by running its tests green (the checkpoint). Task 6 creates the branch and commits spec + plan + code + tests + docs in one go.
- **Workspace semantics:** Path A binds workspace at factory time (default `engine.config.workspace`); Path B resolves `config["configurable"]["user_id"]` per-invocation, falling back to `engine.config.workspace`.

## File Structure

| File | Responsibility |
|------|----------------|
| `src/ladym/langgraph/__init__.py` | Public re-export: `create_ladym_tools`, `create_recall_node`, `create_retain_node` |
| `src/ladym/langgraph/_runtime.py` | `resolve_engine(engine, *, workspace)` + `resolve_workspace(config, engine)` — shared by both paths, no langgraph import |
| `src/ladym/langgraph/tools.py` | Path A: `create_ladym_tools(...)` → `[recall_memory, remember_fact, search_code]` |
| `src/ladym/langgraph/nodes.py` | Path B: `create_recall_node(...)` / `create_retain_node(...)` |
| `tests/unit/langgraph/__init__.py` | Package marker |
| `tests/unit/langgraph/test_runtime.py` | Engine/workspace resolution (no langgraph dep) |
| `tests/unit/langgraph/test_tools.py` | Path A tool factory + behavior |
| `tests/unit/langgraph/test_nodes.py` | Path B node behavior |
| `tests/integration/test_langgraph_flow.py` | End-to-end minimal StateGraph (path B) |
| `docs/langgraph-integration.md` | Quickstart for both paths |
| `pyproject.toml` | New `[langgraph]` extra + add to `all` + keyword |
| `README.md` / `README.zh-CN.md` | "Use with LangGraph" section |

---

### Task 1: Package scaffold + `_runtime` + `pyproject` extra

**Files:**
- Create: `src/ladym/langgraph/__init__.py`, `src/ladym/langgraph/_runtime.py`
- Create: `tests/unit/langgraph/__init__.py`, `tests/unit/langgraph/test_runtime.py`
- Modify: `pyproject.toml` (`[project.optional-dependencies]` + `keywords`)

**Interfaces:**
- Produces: `ladym.langgraph._runtime.resolve_engine(engine, *, workspace=None) -> Engine`, `resolve_workspace(config, engine) -> str`

- [ ] **Step 1: Write the failing test** (`tests/unit/langgraph/test_runtime.py`)

```python
"""Tests for ladym.langgraph._runtime — no langgraph import required."""
from __future__ import annotations

from pathlib import Path

import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.langgraph._runtime import resolve_engine, resolve_workspace


@pytest.fixture
def offline_config(tmp_path: Path) -> Config:
    return Config.for_testing(tmp_path)


def test_resolve_engine_accepts_engine(offline_config):
    eng = Engine(offline_config)
    try:
        assert resolve_engine(eng) is eng
    finally:
        eng.close()


def test_resolve_engine_accepts_config(offline_config):
    eng = resolve_engine(offline_config)
    try:
        assert isinstance(eng, Engine)
        assert eng.config.workspace == offline_config.workspace
    finally:
        eng.close()


def test_resolve_engine_accepts_path(tmp_path):
    db = tmp_path / "x.db"
    eng = resolve_engine(db)
    try:
        assert isinstance(eng, Engine)
        assert eng.config.db_path == db
    finally:
        eng.close()


def test_resolve_engine_accepts_none():
    eng = resolve_engine(None)
    try:
        assert isinstance(eng, Engine)
    finally:
        eng.close()


def test_resolve_engine_workspace_override_on_config(tmp_path):
    cfg = Config.for_testing(tmp_path)
    eng = resolve_engine(cfg, workspace="user-123")
    try:
        assert eng.config.workspace == "user-123"
    finally:
        eng.close()


def test_resolve_engine_ignores_workspace_when_engine_passed(tmp_path):
    eng = Engine(Config.for_testing(tmp_path))
    try:
        out = resolve_engine(eng, workspace="ignored")
        assert out is eng
        assert out.config.workspace == eng.config.workspace
    finally:
        eng.close()


def test_resolve_workspace_from_user_id(offline_config):
    eng = Engine(offline_config)
    try:
        cfg = {"configurable": {"user_id": "u-456"}}
        assert resolve_workspace(cfg, eng) == "u-456"
    finally:
        eng.close()


def test_resolve_workspace_fallback_to_engine(offline_config):
    eng = Engine(offline_config)
    try:
        assert resolve_workspace({}, eng) == eng.config.workspace
        assert resolve_workspace(None, eng) == eng.config.workspace
    finally:
        eng.close()
```

```python
# tests/unit/langgraph/__init__.py
(empty file — package marker)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pytest tests/unit/langgraph/test_runtime.py -v`
Expected: collection error / `ModuleNotFoundError: No module named 'ladym.langgraph'`

- [ ] **Step 3: Write `_runtime.py`**

```python
# src/ladym/langgraph/_runtime.py
"""Shared runtime helpers for the LangGraph integration (paths A and B).

Keeps Engine lifecycle and (for the nodes path) workspace resolution in one
place so the two paths stay consistent and have a single seam against Engine.
"""
from __future__ import annotations

import copy
from pathlib import Path
from typing import Any

from ..config import Config
from ..engine import Engine


def resolve_engine(
    engine: "Engine | Config | str | Path | None",
    *,
    workspace: str | None = None,
) -> Engine:
    """Return a ready-to-use Engine from flexible factory input.

    * ``Engine`` -> returned as-is (caller owns lifecycle); ``workspace`` is
      IGNORED — the engine's own ``config.workspace`` wins. Pass a ``Config``
      or path instead if you need a different workspace.
    * ``Config`` -> new Engine from a copy; ``workspace`` overrides ``cfg.workspace``.
    * ``str`` / ``Path`` -> ``Config(db_path=...)``; ``workspace`` honored.
    * ``None`` -> default ``Config()`` (offline hashing embedding); ``workspace`` honored.
    """
    if isinstance(engine, Engine):
        return engine
    if isinstance(engine, Config):
        cfg = copy.copy(engine)
    elif isinstance(engine, (str, Path)):
        cfg = Config(db_path=Path(engine))
    else:
        cfg = Config()
    if workspace:
        cfg.workspace = workspace
    return Engine(cfg)


def resolve_workspace(config: Any, engine: Engine) -> str:
    """Resolve the ladyM workspace for a LangGraph node invocation (path B).

    Reads ``config["configurable"]["user_id"]``; falls back to
    ``engine.config.workspace``. Typed ``Any`` so this module has no hard
    langchain dependency.
    """
    try:
        configurable = (config or {}).get("configurable", {}) or {}
    except AttributeError:
        return engine.config.workspace
    user_id = configurable.get("user_id")
    return str(user_id) if user_id else engine.config.workspace
```

```python
# src/ladym/langgraph/__init__.py
"""LangGraph integration for LadyM — two paths to use ladyM as a memory layer.

* Path A (Tools): ``create_ladym_tools`` -> LangChain BaseTools for ReAct agents.
* Path B (Nodes): ``create_recall_node`` / ``create_retain_node`` -> graph nodes
  for automatic per-turn memory injection.

Install: ``pip install 'ladym[langgraph]'``.
"""
from __future__ import annotations

# Re-exports are added incrementally as tools.py / nodes.py land (Tasks 2-3).
# Task 1 leaves this as an empty-ish module so `import ladym.langgraph` works.
__all__: list[str] = []
```

- [ ] **Step 4: Add the `[langgraph]` extra to `pyproject.toml`**

In `[project.optional-dependencies]`, add the `langgraph` key after `mcp`:
```toml
langgraph = ["langgraph>=1.0", "langchain-core>=0.3"]
```
Update the `all` aggregate (currently `["ladym[web,llm,local,mcp,openai,anthropic]"]`) to include `langgraph`:
```toml
all = ["ladym[web,llm,local,mcp,openai,anthropic,langgraph]"]
```
In the `keywords` list, add `"langgraph"`.

- [ ] **Step 5: Run test to verify it passes**

Run: `pytest tests/unit/langgraph/test_runtime.py -v`
Expected: 8 passed.

- [ ] **Step 6: Checkpoint** — run `ruff check src/ladym/langgraph tests/unit/langgraph`. Fix any findings. Do NOT commit (per Global Constraints).

---

### Task 2: Path A — `tools.py`

**Files:**
- Create: `src/ladym/langgraph/tools.py`, `tests/unit/langgraph/test_tools.py`
- Modify: `src/ladym/langgraph/__init__.py` (add re-export)

**Interfaces:**
- Consumes: `resolve_engine` from Task 1; `Engine.recall` / `Engine.remember` / `Engine.search_code`
- Produces: `create_ladym_tools(engine=None, *, workspace=None, default_top_k=8) -> list[BaseTool]`

- [ ] **Step 1: Write the failing test** (`tests/unit/langgraph/test_tools.py`)

```python
"""Tests for ladym.langgraph.tools (path A). Requires langchain_core."""
from __future__ import annotations

from pathlib import Path

import pytest

pytest.importorskip("langchain_core")

from ladym.config import Config  # noqa: E402
from ladym.langgraph import create_ladym_tools  # noqa: E402


def test_returns_three_tools(tmp_path):
    from ladym.engine import Engine
    eng = Engine(Config.for_testing(tmp_path))
    try:
        tools = create_ladym_tools(eng)
        assert {t.name for t in tools} == {"recall_memory", "remember_fact", "search_code"}
    finally:
        eng.close()


def test_recall_memory_no_hits(tmp_path):
    from ladym.engine import Engine
    eng = Engine(Config.for_testing(tmp_path))
    try:
        recall = next(t for t in create_ladym_tools(eng) if t.name == "recall_memory")
        assert recall.invoke({"query": "anything"}) == "(no hits)"
    finally:
        eng.close()


def test_remember_then_recall(tmp_path):
    from ladym.engine import Engine
    eng = Engine(Config.for_testing(tmp_path))
    try:
        tools = create_ladym_tools(eng)
        remember = next(t for t in tools if t.name == "remember_fact")
        recall = next(t for t in tools if t.name == "recall_memory")
        res = remember.invoke({"content": "auth uses JWT with 24h expiry", "tags": ["auth"]})
        assert "stored id=" in res and "gate=" in res
        out = recall.invoke({"query": "how does authentication work"})
        assert "JWT" in out
    finally:
        eng.close()


def test_workspace_isolation(tmp_path):
    # tools bound to workspace "special"
    tools_special = create_ladym_tools(Config.for_testing(tmp_path), workspace="special")
    remember = next(t for t in tools_special if t.name == "remember_fact")
    remember.invoke({"content": "user prefers dark mode"})

    # a different workspace should NOT see it
    tools_other = create_ladym_tools(Config.for_testing(tmp_path), workspace="other")
    recall_other = next(t for t in tools_other if t.name == "recall_memory")
    assert "dark mode" not in recall_other.invoke({"query": "preferences"})

    # the special-bound tools DO see it
    recall_special = next(t for t in tools_special if t.name == "recall_memory")
    assert "dark mode" in recall_special.invoke({"query": "preferences"})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pytest tests/unit/langgraph/test_tools.py -v`
Expected: `ImportError: cannot import name 'create_ladym_tools'`

- [ ] **Step 3: Write `tools.py`**

```python
# src/ladym/langgraph/tools.py
"""Path A — LangChain/LangGraph tools wrapping ladyM long-term memory.

Use :func:`create_ladym_tools` when the LLM should decide when to recall /
remember (ReAct-style). The workspace is bound at factory time; for per-request
multi-user isolation use :mod:`ladym.langgraph.nodes` instead.
"""
from __future__ import annotations

from pathlib import Path
from typing import TYPE_CHECKING

from ..config import Config
from ..engine import Engine
from ._runtime import resolve_engine

if TYPE_CHECKING:
    from langchain_core.tools import BaseTool


def create_ladym_tools(
    engine: "Engine | Config | str | Path | None" = None,
    *,
    workspace: str | None = None,
    default_top_k: int = 8,
) -> "list[BaseTool]":
    """Build LangChain tools backed by ladyM memory.

    Returns ``[recall_memory, remember_fact, search_code]``. Each tool closes
    over one Engine + workspace resolved at factory time. ``workspace`` is
    honored only when ``engine`` is NOT an already-constructed ``Engine``.
    """
    from langchain_core.tools import tool  # lazy: not a hard dep of ladym core

    eng = resolve_engine(engine, workspace=workspace)
    ws = eng.config.workspace

    @tool
    def recall_memory(query: str, top_k: int = default_top_k) -> str:
        """Retrieve relevant long-term memories (facts, decisions, code, playbooks).

        Call BEFORE answering when prior context, past decisions, or how-to
        knowledge may be relevant. One line per hit, or "(no hits)".
        """
        resp = eng.recall(query, workspace=ws, top_k=top_k)
        if not resp.results:
            return "(no hits)"
        return "\n".join(
            f"[{r.memory.layer}|{r.memory.type}|{r.score:.2f}] "
            f"{r.memory.summary or r.memory.content}"
            for r in resp.results
        )

    @tool
    def remember_fact(content: str, tags: list[str] | None = None) -> str:
        """Persist a durable fact worth recalling later (user preference, key
        decision, verified answer). Do NOT store ephemeral/transactional state.

        Subject to ladyM's attention gate: low-value or duplicate content may be
        dropped or rewritten automatically.
        """
        mem = eng.remember(content, tags=tags or [], source="langgraph-tool")
        gate = mem.metadata.get("gated", "pass")
        return f"stored id={mem.id} gate={gate}"

    @tool
    def search_code(query: str, top_k: int = default_top_k) -> str:
        """Search indexed source symbols/files. Use for 'how does X work in the
        codebase' against previously ``index_code``-indexed source.
        """
        resp = eng.search_code(query, workspace=ws, top_k=top_k)
        if not resp.results:
            return "(no hits)"
        return "\n".join(
            f"{r.memory.summary} :: {r.memory.metadata.get('qualified_name', '')}"
            for r in resp.results
        )

    return [recall_memory, remember_fact, search_code]
```

Update `src/ladym/langgraph/__init__.py` — replace the body with:
```python
from .tools import create_ladym_tools

__all__ = ["create_ladym_tools"]
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pytest tests/unit/langgraph/test_tools.py -v`
Expected: 4 passed.

- [ ] **Step 5: Checkpoint** — `ruff check src/ladym/langgraph tests/unit/langgraph`. Do NOT commit.

---

### Task 3: Path B — `nodes.py`

**Files:**
- Create: `src/ladym/langgraph/nodes.py`, `tests/unit/langgraph/test_nodes.py`
- Modify: `src/ladym/langgraph/__init__.py` (add re-exports)

**Interfaces:**
- Consumes: `resolve_engine`, `resolve_workspace` from Task 1; `Engine.recall` / `Engine.remember`
- Produces: `create_recall_node(engine=None, *, top_k=6, prefix=...) -> Callable[[dict, Any], dict]`, `create_retain_node(engine=None) -> Callable[[dict, Any], dict]`

- [ ] **Step 1: Write the failing test** (`tests/unit/langgraph/test_nodes.py`)

```python
"""Tests for ladym.langgraph.nodes (path B). Requires langchain_core."""
from __future__ import annotations

from pathlib import Path

import pytest

pytest.importorskip("langchain_core")

from langchain_core.messages import AIMessage, HumanMessage, SystemMessage  # noqa: E402

from ladym.config import Config  # noqa: E402
from ladym.engine import Engine  # noqa: E402
from ladym.langgraph import create_recall_node, create_retain_node  # noqa: E402


def test_recall_node_no_hits_returns_empty(tmp_path):
    eng = Engine(Config.for_testing(tmp_path))
    try:
        node = create_recall_node(eng)
        assert node({"messages": [HumanMessage(content="hello")]}) == {}
    finally:
        eng.close()


def test_recall_node_injects_system_message(tmp_path):
    eng = Engine(Config.for_testing(tmp_path))
    try:
        eng.remember("auth uses JWT with 24h expiry")
        node = create_recall_node(eng)
        out = node({"messages": [HumanMessage(content="how does auth work?")]})
        msg = out["messages"][0]
        assert isinstance(msg, SystemMessage)
        assert "JWT" in msg.content
    finally:
        eng.close()


def test_recall_node_resolves_user_id(tmp_path):
    # seed a memory into workspace u-456
    cfg_w = Config.for_testing(tmp_path)
    cfg_w.workspace = "u-456"
    eng_w = Engine(cfg_w)
    eng_w.remember("user 456 likes tea")
    eng_w.close()
    # recall node on the default engine resolves user_id -> u-456
    eng = Engine(Config.for_testing(tmp_path))
    try:
        node = create_recall_node(eng)
        out = node(
            {"messages": [HumanMessage(content="what do I like?")]},
            {"configurable": {"user_id": "u-456"}},
        )
        assert "tea" in out["messages"][0].content
    finally:
        eng.close()


def test_retain_node_stores_turn(tmp_path):
    eng = Engine(Config.for_testing(tmp_path))
    try:
        node = create_retain_node(eng)
        out = node({"messages": [HumanMessage(content="what is 2+2?"), AIMessage(content="4")]})
        assert out == {}
        hits = eng.recall("arithmetic").results
        assert any("2+2" in r.memory.content or "4" in r.memory.content for r in hits)
    finally:
        eng.close()


def test_retain_node_no_messages_returns_empty(tmp_path):
    eng = Engine(Config.for_testing(tmp_path))
    try:
        assert create_retain_node(eng)({"messages": []}) == {}
    finally:
        eng.close()


def test_retain_node_resolves_user_id(tmp_path):
    eng = Engine(Config.for_testing(tmp_path))
    try:
        node = create_retain_node(eng)
        node(
            {"messages": [HumanMessage(content="hello"), AIMessage(content="hi")]},
            {"configurable": {"user_id": "u-789"}},
        )
        # stored in workspace u-789, invisible from the default workspace
        assert not eng.recall("hello").results
        hits = eng.recall("hello", workspace="u-789").results
        assert any("hello" in r.memory.content for r in hits)
    finally:
        eng.close()
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pytest tests/unit/langgraph/test_nodes.py -v`
Expected: `ImportError: cannot import name 'create_recall_node'`

- [ ] **Step 3: Write `nodes.py`**

```python
# src/ladym/langgraph/nodes.py
"""Path B — LangGraph graph nodes for automatic memory injection.

Use :func:`create_recall_node` / :func:`create_retain_node` when memory should
be recalled into a SystemMessage every turn and the latest turn stored
automatically, without the LLM deciding when. Wire them into your own
``StateGraph`` (see ``docs/langgraph-integration.md``).
"""
from __future__ import annotations

import copy
from pathlib import Path
from typing import Any, Callable

from ..config import Config
from ..engine import Engine
from ._runtime import resolve_engine, resolve_workspace


def create_recall_node(
    engine: "Engine | Config | str | Path | None" = None,
    *,
    top_k: int = 6,
    prefix: str = "Relevant long-term memory:",
) -> Callable[[dict, Any], dict]:
    """Return a graph node that recalls against the latest message and prepends
    a SystemMessage to ``state["messages"]``. Returns ``{}`` when no hits."""
    from langchain_core.messages import SystemMessage

    eng = resolve_engine(engine)

    def recall_node(state: dict, config: Any = None) -> dict:
        messages = state.get("messages", [])
        if not messages:
            return {}
        query = getattr(messages[-1], "content", str(messages[-1]))
        ws = resolve_workspace(config, eng)
        resp = eng.recall(query, workspace=ws, top_k=top_k)
        if not resp.results:
            return {}
        lines = "\n".join(f"- {r.memory.summary or r.memory.content}" for r in resp.results)
        return {"messages": [SystemMessage(content=f"{prefix}\n{lines}")]}

    return recall_node


def create_retain_node(
    engine: "Engine | Config | str | Path | None" = None,
) -> Callable[[dict, Any], dict]:
    """Return a graph node that stores the latest human+ai turn into long-term
    memory (subject to ladyM's attention gate). Returns ``{}``.

    For per-request multi-user isolation (``config["configurable"]["user_id"]``
    != engine default), a short-lived workspace-bound Engine is built per call.
    ``Engine.remember`` has no workspace parameter (core is frozen), so this is
    the cleanest no-core-change path. If it becomes a hot spot, add a
    workspace->Engine cache later.
    """
    from langchain_core.messages import AIMessage, HumanMessage

    eng = resolve_engine(engine)

    def _engine_for(ws: str) -> Engine:
        if ws == eng.config.workspace:
            return eng
        cfg = copy.copy(eng.config)
        cfg.workspace = ws
        return Engine(cfg)

    def retain_node(state: dict, config: Any = None) -> dict:
        messages = state.get("messages", [])
        human = next((m for m in reversed(messages) if isinstance(m, HumanMessage)), None)
        ai = next((m for m in reversed(messages) if isinstance(m, AIMessage)), None)
        if not human or not ai:
            return {}
        content = f"Q: {human.content}\nA: {ai.content}"
        ws = resolve_workspace(config, eng)
        local = _engine_for(ws)
        try:
            local.remember(content, source="langgraph-node")
        finally:
            if local is not eng:
                local.close()
        return {}

    return retain_node
```

Update `src/ladym/langgraph/__init__.py`:
```python
from .nodes import create_recall_node, create_retain_node
from .tools import create_ladym_tools

__all__ = [
    "create_ladym_tools",
    "create_recall_node",
    "create_retain_node",
]
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pytest tests/unit/langgraph/test_nodes.py -v`
Expected: 6 passed.

- [ ] **Step 5: Checkpoint** — `ruff check src/ladym/langgraph tests/unit/langgraph`. Do NOT commit.

---

### Task 4: End-to-end integration test (path B)

**Files:**
- Create: `tests/integration/test_langgraph_flow.py`

**Interfaces:**
- Consumes: `create_recall_node`, `create_retain_node` from Task 3; `langgraph.graph` (gated by importorskip)

- [ ] **Step 1: Write the test**

```python
"""End-to-end: a minimal LangGraph using ladyM nodes (path B). Offline."""
from __future__ import annotations

from pathlib import Path

import pytest

pytest.importorskip("langgraph")

from langchain_core.messages import AIMessage, HumanMessage  # noqa: E402
from langgraph.checkpoint.memory import InMemorySaver  # noqa: E402
from langgraph.graph import END, START, MessagesState, StateGraph  # noqa: E402

from ladym.config import Config  # noqa: E402
from ladym.engine import Engine  # noqa: E402
from ladym.langgraph import create_recall_node, create_retain_node  # noqa: E402


def _fake_agent_node(state, config=None):
    # Offline stand-in for the user's LLM node — echoes the last user message.
    last = state["messages"][-1].content
    return {"messages": [AIMessage(content=f"echo: {last}")]}


def test_path_b_graph_recalls_and_retains(tmp_path: Path):
    eng = Engine(Config.for_testing(tmp_path))
    try:
        eng.remember("The deploy command is `ladym deploy`.")

        builder = StateGraph(MessagesState)
        builder.add_node("recall", create_recall_node(eng))
        builder.add_node("agent", _fake_agent_node)
        builder.add_node("retain", create_retain_node(eng))
        builder.add_edge(START, "recall")
        builder.add_edge("recall", "agent")
        builder.add_edge("agent", "retain")
        builder.add_edge("retain", END)
        graph = builder.compile(checkpointer=InMemorySaver())

        result = graph.invoke(
            {"messages": [HumanMessage(content="how do I deploy?")]},
            config={"configurable": {"thread_id": "t1"}},
        )

        sys_msgs = [m for m in result["messages"] if m.type == "system"]
        assert any("ladym deploy" in m.content for m in sys_msgs)

        hits = eng.recall("deploy").results
        assert any("echo" in r.memory.content or "ladym deploy" in r.memory.content for r in hits)
    finally:
        eng.close()
```

- [ ] **Step 2: Run test**

Run: `pytest tests/integration/test_langgraph_flow.py -v`
Expected: 1 passed. (If langgraph is not installed, it skips — verify with `pytest -v --co` that the file is collected then skipped, OR install the extra locally: `pip install -e '.[langgraph]'` inside the project venv to run it for real.)

- [ ] **Step 3: Checkpoint** — do NOT commit.

---

### Task 5: Documentation

**Files:**
- Create: `docs/langgraph-integration.md`
- Modify: `README.md`, `README.zh-CN.md`

- [ ] **Step 1: Write `docs/langgraph-integration.md`**

Cover these sections (prose + the code blocks shown):

1. **Install**: `pip install 'ladym[langgraph]'`
2. **Which path?** — a 4-row table: Tools (LLM decides, ReAct) vs Nodes (auto per-turn injection), with the recommendation logic from the spec.
3. **Path A — Tools**: full quickstart using `create_ladym_tools` + `langgraph.prebuilt.create_react_agent`. Show multi-turn usage. Note: workspace is factory-bound; for per-user isolation use Path B.
4. **Path B — Nodes**: full quickstart building a `StateGraph(MessagesState)` with `recall → agent → retain`. Show the per-user `config={"configurable": {"user_id": ...}}` pattern.
5. **Indexing your codebase first**: a short block calling `eng.index_code("./src")` so `search_code` / recall return code hits.
6. **How attention gate interacts**: explain that `remember_fact` / `retain_node` both go through ladyM's attention gate (drop / rewrite / pass), and how to read `gate=` in the tool output.

Use the exact code blocks from the spec's "用法" sections verbatim.

- [ ] **Step 2: Add a "Use with LangGraph" section to `README.md`** (after the existing usage section). Content:

```markdown
## Use with LangGraph

LadyM ships an optional LangGraph integration (install with `pip install 'ladym[langgraph]'`)
that exposes ladyM as a long-term memory layer for LangGraph / LangChain agents.
Two equivalent paths:

- **Tools** — `create_ladym_tools(engine)` returns LangChain tools (`recall_memory`,
  `remember_fact`, `search_code`) for ReAct-style agents where the LLM decides when
  to recall/remember.
- **Nodes** — `create_recall_node(engine)` / `create_retain_node(engine)` return graph
  nodes that inject recalled memory as a SystemMessage every turn and store the latest
  turn automatically (with per-user workspace isolation via `config["configurable"]["user_id"]`).

See [`docs/langgraph-integration.md`](docs/langgraph-integration.md) for full quickstarts.
```

- [ ] **Step 3: Add the equivalent section to `README.zh-CN.md`** (translated):

```markdown
## 与 LangGraph 配合使用

LadyM 提供可选的 LangGraph 集成（安装：`pip install 'ladym[langgraph]'`），把 ladyM
作为 LangGraph / LangChain agent 的长期记忆层。两条等价路径：

- **Tools（工具）** — `create_ladym_tools(engine)` 返回 LangChain 工具（`recall_memory`、
  `remember_fact`、`search_code`），适合 LLM 自主决定何时存取的 ReAct 型 agent。
- **Nodes（节点）** — `create_recall_node(engine)` / `create_retain_node(engine)` 返回图节点，
  每轮自动把相关记忆作为 SystemMessage 注入、并自动存下本轮对话（通过
  `config["configurable"]["user_id"]` 支持按用户隔离 workspace）。

完整示例见 [`docs/langgraph-integration.md`](docs/langgraph-integration.md)。
```

- [ ] **Step 4: Checkpoint** — do NOT commit.

---

### Task 6: Full verification + commit (feature branch)

- [ ] **Step 1: Install the extra locally and run the new tests for real**

```
pip install -e '.[langgraph]'
pytest tests/unit/langgraph tests/integration/test_langgraph_flow.py -v
```
Expected: 8 (runtime) + 4 (tools) + 6 (nodes) + 1 (integration) = 19 passed.

- [ ] **Step 2: Run the FULL test suite to confirm no regressions**

```
pytest -q
```
Expected: green (the langgraph tests run when the extra is installed; otherwise skip). Compare the pre/post pass count to ensure nothing else broke.

- [ ] **Step 3: Lint the whole package**

```
ruff check src tests
```
Expected: clean.

- [ ] **Step 4: Create the feature branch and commit everything**

```bash
git checkout -b feat/langgraph-integration
git add docs/superpowers/specs/2026-07-25-langgraph-integration-design.md \
        docs/superpowers/plans/2026-07-25-langgraph-integration.md \
        docs/langgraph-integration.md \
        src/ladym/langgraph \
        tests/unit/langgraph tests/integration/test_langgraph_flow.py \
        pyproject.toml README.md README.zh-CN.md
git commit -m "$(cat <<'EOF'
feat(langgraph): add Tools + Nodes integration as optional extra

Adds `ladym.langgraph` subpackage exposing ladyM as a LangGraph memory layer:
- Path A: create_ladym_tools() -> LangChain BaseTools (recall/remember/search_code)
- Path B: create_recall_node/create_retain_node graph nodes with per-user workspace

Zero core changes; langgraph/langchain-core imports stay lazy behind a new
[langgraph] extra. Includes offline unit + integration tests and docs.

Spec: docs/superpowers/specs/2026-07-25-langgraph-integration-design.md
EOF
)"
```

- [ ] **Step 5: Report** — paste the commit hash and the `pytest -q` summary line to the user.

---

## Self-Review

**1. Spec coverage:**
- Two paths (Tools + Nodes) → Tasks 2, 3 ✓
- Shared runtime (workspace/engine resolution) → Task 1 ✓
- `[langgraph]` extra + `all` aggregate + keyword → Task 1 ✓
- Offline tests + importorskip → Tasks 1–4 ✓
- Multi-user (`user_id → workspace`) → test_runtime, test_nodes (recall + retain), integration ✓
- Workspace factory-binding (path A) → test_tools `test_workspace_isolation` ✓
- Attention gate passthrough → test_tools asserts `gate=` in output ✓
- Docs (quickstart + README en/zh) → Task 5 ✓
- "No core changes" → Global Constraints + all tasks only consume public Engine API ✓
- BaseStore NOT done, async NOT done → explicit non-goals, no task (correct) ✓

**2. Placeholder scan:** all code blocks contain runnable code; no "TODO"/"TBD"/"add error handling". Doc task (Task 5) gives exact section list + verbatim code/README snippets to drop in. ✓

**3. Type consistency:**
- `resolve_engine(engine, *, workspace=None)` — same signature in Task 1 def, Task 2 call, Task 3 call ✓
- `resolve_workspace(config, engine)` — same in Task 1 def, Task 3 nodes ✓
- `create_ladym_tools(engine=None, *, workspace=None, default_top_k=8)` — same in Task 2 def + spec ✓
- `create_recall_node(engine=None, *, top_k=6, prefix=...)`, `create_retain_node(engine=None)` — same in Task 3 def + spec ✓
- Tool names `recall_memory` / `remember_fact` / `search_code` — same across tools.py, test_tools, README ✓

No issues found.
