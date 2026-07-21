"""L5 mental-model extraction (SPEC §2.8) — incremental pass."""

from __future__ import annotations

import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.operations.l5 import L5ExtractionReport, extract
from ladym.providers import FakeLLMProvider
from ladym.schema import Layer


@pytest.fixture
def engine(tmp_path):
    e = Engine(Config.for_testing(tmp_path))
    yield e
    e.close()


def _fake_model(title, body):
    return FakeLLMProvider(structured_fn=lambda msgs, schema: {"title": title, "model": body})


def test_extract_creates_model_and_abstracts_edges(engine):
    # Three similar L2 facts; force a single cluster with threshold 0.0.
    for c in ["auth uses JWT", "auth uses JWT tokens", "authentication via JWT"]:
        engine.semantic.put_fact(c)
    engine.config.system2.l5_cluster_similarity = -1.0
    engine.config.system2.l5_min_cluster_size = 2

    report = extract(engine.store, engine.provider, cfg=engine.config,
                     llm=_fake_model("Auth", "Authentication is JWT-based"))

    assert isinstance(report, L5ExtractionReport)
    assert report.new_models == 1
    # one L5 mental model exists
    l5 = list(engine.store.iter_memories(workspace="test", layer=Layer.L5_MENTAL.value))
    assert len(l5) == 1
    # it abstracts all three facts
    members = engine.store.neighbors(l5[0].id, relation="abstracts")
    assert len(members) == 3
    assert all(e.src_id == l5[0].id for e in members)


def test_extract_is_idempotent(engine):
    for c in ["auth uses JWT", "auth uses JWT tokens", "authentication via JWT"]:
        engine.semantic.put_fact(c)
    engine.config.system2.l5_cluster_similarity = -1.0
    engine.config.system2.l5_min_cluster_size = 2
    fake = _fake_model("Auth", "Authentication is JWT-based")

    r1 = extract(engine.store, engine.provider, cfg=engine.config, llm=fake)
    assert r1.new_models == 1
    # second run: every fact is now covered -> no new candidates -> no new models
    r2 = extract(engine.store, engine.provider, cfg=engine.config, llm=fake)
    assert r2.new_models == 0
    l5 = list(engine.store.iter_memories(workspace="test", layer=Layer.L5_MENTAL.value))
    assert len(l5) == 1  # still just the one from r1


def test_extract_below_min_cluster_size_creates_nothing(engine):
    engine.semantic.put_fact("only one lonely fact here")
    engine.semantic.put_fact("a second unrelated fact about cats")
    engine.config.system2.l5_cluster_similarity = -1.0
    engine.config.system2.l5_min_cluster_size = 3  # clusters of <3 are skipped

    report = extract(engine.store, engine.provider, cfg=engine.config,
                     llm=_fake_model("X", "Y"))
    assert report.new_models == 0
    assert list(engine.store.iter_memories(workspace="test", layer=Layer.L5_MENTAL.value)) == []


def test_extract_offline_noop_when_llm_none(engine):
    for c in ["auth uses JWT", "auth uses JWT tokens", "authentication via JWT"]:
        engine.semantic.put_fact(c)
    report = extract(engine.store, engine.provider, cfg=engine.config, llm=None)
    assert report.skipped is True
    assert report.new_models == 0
    assert list(engine.store.iter_memories(workspace="test", layer=Layer.L5_MENTAL.value)) == []
