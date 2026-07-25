"""Tests for ladym.langgraph.tools (path A). Requires langchain_core."""
from __future__ import annotations

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
