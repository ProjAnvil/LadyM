"""Tests for the attention gate (SPEC §2.7).

Covers:
* Heuristic prefix (always runs): noise (pure noise vocabulary) and recent-duplicate
  (hash-exact L1 within ``dedup_window_s``) are dropped deterministically, regardless
  of whether an LLM is wired.
* Semantic layer: short content like ``"hi"`` clears the prefix and, with no LLM agent
  wired (``Config.for_testing`` default), passes; with an LLM wired it is delegated to
  the gate prompt (structured pass / rewrite / drop).
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


def test_gate_passes_short_when_no_llm(engine):
    # too-short content is no longer a heuristic drop; with no LLM agent wired
    # (Config.for_testing default), "hi" clears the heuristic prefix and passes.
    d = attention_gate("hi", engine=engine, layer=Layer.SEMANTIC)
    assert d.action == "pass"


def test_gate_passes_normal_content(engine):
    d = attention_gate("auth uses JWT with 24h expiry", engine=engine, layer=Layer.SEMANTIC)
    assert d.action == "pass"


def test_gate_drops_noise(engine):
    # pure-noise content composed entirely of `_BUILTIN_NOISE` tokens
    # NOTE: all tokens here must stay in _BUILTIN_NOISE (attention.py) for this drop assertion to hold.
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
    # pure-noise content is dropped by the heuristic prefix.
    m = engine.remember("lol ok test asdf foo")  # noise -> drop
    assert m.metadata.get("gated") == "dropped"
    assert m.metadata.get("reason") == "noise"
    assert engine.store.get_memory(m.id) is None  # not persisted


def test_remember_pass_persists(engine):
    m = engine.remember("a reasonably long fact about the system")
    assert m.metadata.get("gated") != "dropped"
    assert engine.store.get_memory(m.id) is not None


def test_remember_working_layer_skips_gate(tmp_path):
    """L0 working memory is never gated even for content the gate would drop."""
    e = Engine(Config.for_testing(tmp_path))
    try:
        # noise content would be dropped on L1/L2/L3 but L0 bypasses the gate entirely.
        m = e.remember("lol ok test asdf foo", layer=Layer.WORKING)
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
    """LLM mode: provider returns action=rewrite → rewritten content persisted + tagged.

    SPEC §2.7 also requires the pre-rewrite content to survive on
    ``metadata["original"]`` for audit / undo.
    """
    e = Engine(Config.for_testing(tmp_path))
    try:
        e._agents["attention_gate"] = _fake_gate(
            lambda msgs, schema: {
                "action": "rewrite",
                "content": "auth uses JWT with 24h expiry",
                "reason": "cleaner",
            }
        )
        original = "auth is jwt 24 hour"
        m = e.remember(original, layer=Layer.SEMANTIC)
        assert m.metadata.get("gated") == "rewritten"
        assert m.content == "auth uses JWT with 24h expiry"
        # SPEC §2.7: original content preserved on metadata.
        assert m.metadata.get("original") == original
        assert e.store.get_memory(m.id) is not None
        # The persisted row carries the rewritten content + original on metadata.
        persisted = e.store.get_memory(m.id)
        assert persisted.content == "auth uses JWT with 24h expiry"
        assert persisted.metadata.get("original") == original
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


def test_llm_gate_receives_short_content(tmp_path):
    """Short content ('hi') clears the heuristic prefix and reaches the LLM gate.

    This is the design's point: 'hi' is no longer a deterministic drop — with
    an LLM wired it is delegated to the semantic layer.
    """
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
        d = attention_gate("hi", engine=e, layer=Layer.SEMANTIC)
        assert called  # the LLM really was invoked
        assert d.action == "pass"
    finally:
        e.close()


def test_gate_decision_dataclass_defaults():
    d = GateDecision(action="pass")
    assert d.action == "pass"
    assert d.content is None
    assert d.reason == ""
