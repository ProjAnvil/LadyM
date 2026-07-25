"""Path B — LangGraph graph nodes for automatic memory injection.

Use :func:`create_recall_node` / :func:`create_retain_node` when memory should
be recalled into a SystemMessage every turn and the latest turn stored
automatically, without the LLM deciding when. Wire them into your own
``StateGraph`` (see ``docs/langgraph-integration.md``).
"""

import copy
from collections.abc import Callable
from pathlib import Path

from langchain_core.runnables import RunnableConfig

from ..config import Config
from ..engine import Engine
from ._runtime import resolve_engine, resolve_workspace


def create_recall_node(
    engine: Engine | Config | str | Path | None = None,
    *,
    top_k: int = 6,
    prefix: str = "Relevant long-term memory:",
) -> Callable[[dict, RunnableConfig | None], dict]:
    """Return a graph node that recalls against the latest message and prepends
    a SystemMessage to ``state["messages"]``. Returns ``{}`` when there are no
    hits."""
    from langchain_core.messages import SystemMessage

    eng = resolve_engine(engine)

    def recall_node(state: dict, config: RunnableConfig | None = None) -> dict:
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
    engine: Engine | Config | str | Path | None = None,
) -> Callable[[dict, RunnableConfig | None], dict]:
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

    def retain_node(state: dict, config: RunnableConfig | None = None) -> dict:
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
