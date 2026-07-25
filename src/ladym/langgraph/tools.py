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
    engine: Engine | Config | str | Path | None = None,
    *,
    workspace: str | None = None,
    default_top_k: int = 8,
) -> list[BaseTool]:
    """Build LangChain tools backed by ladyM memory.

    Returns ``[recall_memory, remember_fact, search_code]``. Each tool closes
    over one Engine + workspace resolved at factory time. ``workspace`` is
    honored only when ``engine`` is NOT an already-constructed ``Engine``
    (an Engine's own ``config.workspace`` wins).
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
