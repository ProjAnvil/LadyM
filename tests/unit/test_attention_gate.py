"""Tests for the attention gate (SPEC §2.7).

Covers:
* Heuristic mode (default, ``provider="none"``): drop too-short / recent-duplicate /
  noise; pass otherwise.
* LLM mode (when ``engine._agents["attention_gate"]`` is wired): structured
  pass / rewrite / drop decisions.
* ``Engine.remember`` integration: drop returns an unpersisted ``Memory`` tagged
  ``metadata={"gated": "dropped"}`` (NFR-4), pass persists normally.
"""

from __future__ import annotations

import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.operations.attention import GateDecision, attention_gate
from ladym.providers.llm import FakeLLMProvider, Message
from ladym.schema import Layer


@pytest.fixture
def engine(tmp_path):
    e = Engine(Config.for_testing(tmp_path))
    yield e
    e.close()


# ----- heuristic mode -----


def test_gate_drops_too_short(engine):
    d = attention_gate("hi", engine=engine, layer=Layer.SEMANTIC)
    assert d.action == "drop"


def test_gate_passes_normal_content(engine):
    d = attention_gate("auth uses JWT with 24h expiry", engine=engine, layer=Layer.SEMANTIC)
    assert d.action == "pass"


def test_gate_drops_noise(engine):
    # content long enough to clear min_chars but composed entirely of noise tokens
    d = attention_gate("ok ok ok lol", engine=engine, layer=Layer.SEMANTIC)
    assert d.action == "drop"
    assert d.reason == "noise"


def test_gate_drops_recent_duplicate(engine):
    engine.episodic.record(agent="bot", action="x", observation="exact dup content here")
    # We look up dup by raw content; record() renders content as "agent=... | observation=..."
    # so we pass the rendered form to match.
    dup = "agent=bot | action=x | observation=exact dup content here"
    d = attention_gate(dup, engine=engine, layer=Layer.EPISODIC)
    assert d.action == "drop"
    assert d.reason == "recent duplicate"


def test_gate_working_layer_never_gated(engine):
    d = attention_gate("x", engine=engine, layer=Layer.WORKING)
    assert d.action == "pass"
    assert d.reason == "working memory never gated"


# ----- Engine.remember integration -----


def test_remember_drop_returns_unpersisted_memory(engine):
    m = engine.remember("hi")  # too short -> drop
    assert m.metadata.get("gated") == "dropped"
    assert m.metadata.get("reason") == "too short"
    assert engine.store.get_memory(m.id) is None  # not persisted


def test_remember_pass_persists(engine):
    m = engine.remember("a reasonably long fact about the system")
    assert m.metadata.get("gated") != "dropped"
    assert engine.store.get_memory(m.id) is not None


def test_remember_working_layer_skips_gate(tmp_path):
    """L0 working memory is never gated even for short content."""
    e = Engine(Config.for_testing(tmp_path))
    try:
        # "hi" would be dropped on L1/L2/L3 but L0 bypasses the gate entirely.
        m = e.remember("hi", layer=Layer.WORKING)
        assert m.metadata.get("gated") != "dropped"
    finally:
        e.close()


# ----- LLM mode (correction: agents map must be populated) -----


def _fake_gate(structured_fn) -> FakeLLMProvider:
    return FakeLLMProvider(structured_fn=structured_fn)


def test_remember_llm_drop(tmp_path):
    """LLM mode: provider returns action=drop → unpersisted Memory with gated tag."""
    e = Engine(Config.for_testing(tmp_path))
    try:
        e._agents["attention_gate"] = _fake_gate(
            lambda msgs, schema: {"action": "drop", "content": None, "reason": "spam"}
        )
        m = e.remember("a long enough looking string that the heuristic would pass")
        assert m.metadata.get("gated") == "dropped"
        assert m.metadata.get("reason") == "spam"
        assert e.store.get_memory(m.id) is None
    finally:
        e.close()


def test_remember_llm_rewrite(tmp_path):
    """LLM mode: provider returns action=rewrite → rewritten content persisted + tagged."""
    e = Engine(Config.for_testing(tmp_path))
    try:
        e._agents["attention_gate"] = _fake_gate(
            lambda msgs, schema: {
                "action": "rewrite",
                "content": "auth uses JWT with 24h expiry",
                "reason": "cleaner",
            }
        )
        m = e.remember("auth is jwt 24 hour", layer=Layer.SEMANTIC)
        assert m.metadata.get("gated") == "rewritten"
        assert m.content == "auth uses JWT with 24h expiry"
        assert e.store.get_memory(m.id) is not None
        # The persisted row carries the rewritten content.
        assert e.store.get_memory(m.id).content == "auth uses JWT with 24h expiry"
    finally:
        e.close()


def test_remember_llm_pass(tmp_path):
    """LLM mode: provider returns action=pass → normal persist, no gated tag."""
    e = Engine(Config.for_testing(tmp_path))
    try:
        called: list[list[Message]] = []
        e._agents["attention_gate"] = _fake_gate(
            lambda msgs, schema: called.append(msgs) or {
                "action": "pass",
                "content": None,
                "reason": "worth keeping",
            }
        )
        m = e.remember("user prefers dark theme for the dashboard")
        assert m.metadata.get("gated") != "dropped"
        # "gated" only appears on drop/rewrite; pass leaves metadata clean.
        assert "gated" not in (m.metadata or {})
        assert e.store.get_memory(m.id) is not None
        assert called  # provider really was invoked
    finally:
        e.close()


def test_gate_decision_dataclass_defaults():
    d = GateDecision(action="pass")
    assert d.action == "pass"
    assert d.content is None
    assert d.reason == ""
