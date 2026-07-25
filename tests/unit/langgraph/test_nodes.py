"""Tests for ladym.langgraph.nodes (path B). Requires langchain_core."""
from __future__ import annotations

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
        # Direct store check: recall's hashing-embedding similarity is not
        # deterministic enough to assert against; the contract under test is
        # "the turn got persisted", which is a store-level fact.
        rows = list(eng.store.iter_memories(workspace=eng.config.workspace))
        assert len(rows) == 1
        assert "2+2" in rows[0].content
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
        # stored in workspace u-789, invisible from the default workspace.
        # Direct store check (not recall) — see test_retain_node_stores_turn.
        rows_user = list(eng.store.iter_memories(workspace="u-789"))
        assert len(rows_user) == 1
        assert "hello" in rows_user[0].content
        assert list(eng.store.iter_memories(workspace=eng.config.workspace)) == []
    finally:
        eng.close()
