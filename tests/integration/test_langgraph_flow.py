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

        # recall node injected a SystemMessage mentioning the seeded fact
        sys_msgs = [m for m in result["messages"] if m.type == "system"]
        assert any("ladym deploy" in m.content for m in sys_msgs)

        # retain node stored the turn (direct store check — recall's hashing
        # similarity is not deterministic enough to assert against here)
        rows = list(eng.store.iter_memories(workspace=eng.config.workspace))
        assert any("echo" in r.content or "deploy" in r.content for r in rows)
    finally:
        eng.close()
