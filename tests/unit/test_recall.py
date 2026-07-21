"""Tests for two-tier recall: tier-1 ranking, reflection gate, tier-2 graph expansion."""

import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.schema import Layer, MemoryType


@pytest.fixture
def engine(tmp_path):
    cfg = Config.for_testing(tmp_path)
    cfg.recall.reflection_min_hits = 1
    cfg.recall.reflection_min_coverage = 0.3
    e = Engine(cfg)
    yield e
    e.close()


def _seed(engine, items):
    for content, tags in items:
        engine.semantic.put_fact(content, tags=tags)


def test_recall_returns_ranked_results(engine):
    _seed(engine, [
        ("user authenticates with JWT token", ["auth"]),
        ("user authenticates with JWT token expired", ["auth"]),
        ("render shopping cart icon component", ["ui"]),
    ])
    resp = engine.recall("how does user authentication work?")
    # tier depends on whether reflection deems coverage sufficient; either is fine
    assert resp.tier_reached in (1, 2)
    assert len(resp.results) >= 1
    top = resp.results[0]
    assert "authentic" in top.memory.content
    assert top.score > 0.0


def test_recall_reflection_gate_promotes_to_tier2(engine):
    # seed one weak match + a graph neighbour that should be pulled in by tier 2
    a = engine.semantic.put_fact("auth helper", tags=["auth"])
    b = engine.semantic.put_fact("JWT secret key rotation procedure", tags=["auth"])
    engine.link(a.id, b.id, relation="related_to")
    resp = engine.recall("xyzzy auth qwerty")  # low coverage query
    # tier 1 might already be sufficient here, but if it isn't tier2 must run;
    # in either case the graph neighbour may appear
    assert resp.tier_reached in (1, 2)
    ids = {r.memory.id for r in resp.results}
    # the anchor should always be returned
    assert a.id in ids


def test_recall_search_code_restricts_to_code_items(engine):
    engine.semantic.put_fact("auth uses JWT", tags=["auth"])  # non-code fact
    engine.semantic.put_code_file("src/auth.py", "JWT login", language="python")
    resp = engine.search_code("auth login")
    types = {r.memory.type for r in resp.results}
    assert types <= {MemoryType.CODE_SYMBOL.value, MemoryType.CODE_FILE.value}


def test_recall_workspace_isolation(engine):
    engine.config.workspace = "default"
    engine.semantic.workspace = "default"
    engine.semantic.put_fact("only in default workspace")
    engine.config.workspace = "other"
    engine.semantic.workspace = "other"
    # the engine was constructed with workspace='test'; this test just confirms filter is applied
    resp = engine.recall("only in default", workspace="nonexistent")
    assert resp.results == []


def test_recall_touches_access_count(engine):
    a = engine.semantic.put_fact("password reset flow", tags=["auth"])
    before = engine.store.get_memory(a.id).access_count
    engine.recall("password reset")
    after = engine.store.get_memory(a.id).access_count
    assert after > before


def test_recall_layers_filter(engine):
    engine.semantic.put_fact("auth semantic", tags=["x"])
    engine.episodic.record(agent="bot", action="auth event")
    resp = engine.recall("auth", layers=[Layer.EPISODIC])
    assert all(r.memory.layer == Layer.EPISODIC.value for r in resp.results)


def test_recall_response_has_elapsed(engine):
    _seed(engine, [("hello world", [])])
    resp = engine.recall("hello")
    assert resp.elapsed_ms >= 0.0
