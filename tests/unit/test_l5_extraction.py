"""L5 mental-model extraction (SPEC §2.8) — incremental pass."""

from __future__ import annotations

import time

import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.operations.l5 import L5ExtractionReport, extract
from ladym.operations.supersedes import is_retired
from ladym.providers import FakeLLMProvider
from ladym.schema import Edge, Layer, Memory, MemoryType


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


def test_merge_collapses_similar_l5_models(engine):
    """Every Nth extract clusters existing L5 models and merges similar ones via supersedes."""

    # Two covered L2 facts, each already abstracted by its own L5 model.
    f1 = engine.semantic.put_fact("auth uses JWT")
    f2 = engine.semantic.put_fact("cache uses redis")
    now = time.time()

    def _make_l5(abstracts):
        mem = Memory(
            layer=Layer.L5_MENTAL, type=MemoryType.MENTAL_MODEL,
            content="stand-in model", summary="m", tags=["mental_model"],
            source="seed", workspace="test",
        )
        engine.store.put_memory(mem, vector=engine.provider.embed(mem.content))
        for member in abstracts:
            engine.store.put_edge(
                Edge(src_id=mem.id, relation="abstracts", dst_id=member.id, valid_from=now)
            )
        return mem

    l5a = _make_l5([f1])
    l5b = _make_l5([f2])

    # No new candidates (both facts covered) -> incremental pass is a no-op, but with
    # l5_merge_every_n_cycles=1 the merge pass runs and collapses the two models.
    engine.config.system2.l5_merge_similarity = -1.0  # force the two L5s into one cluster
    engine.config.system2.l5_merge_every_n_cycles = 1
    fake = _fake_model("Combined", "auth and cache infra")

    report = extract(engine.store, engine.provider, cfg=engine.config, llm=fake)

    assert report.new_models == 0
    assert report.merged_models == 1
    # old models retired, pointing at the merged successor
    assert is_retired(engine.store.get_memory(l5a.id))
    assert is_retired(engine.store.get_memory(l5b.id))
    assert engine.store.get_memory(l5a.id).metadata["superseded_by"]
    # exactly one non-retired L5 remains, abstracting both facts
    active = [m for m in engine.store.iter_memories(workspace="test", layer=Layer.L5_MENTAL.value)
              if not is_retired(m)]
    assert len(active) == 1
    members = engine.store.neighbors(active[0].id, relation="abstracts")
    assert {e.dst_id for e in members} == {f1.id, f2.id}
