"""L6 forward-intent prediction (SPEC §2.8)."""

from __future__ import annotations

import time

import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.operations.l6 import L6PredictionReport, predict
from ladym.operations.supersedes import is_retired
from ladym.providers import FakeLLMProvider
from ladym.schema import Layer, Memory, MemoryType


@pytest.fixture
def engine(tmp_path):
    e = Engine(Config.for_testing(tmp_path))
    yield e
    e.close()


def _fake_intents(items):
    return FakeLLMProvider(
        structured_fn=lambda msgs, schema: {"intents": items}
    )


def test_predict_stores_one_memory_per_intent(engine):
    engine.episodic.record(agent="bot", action="deploy", observation="shipped v1")
    fake = _fake_intents([
        {"intent": "run smoke tests", "confidence": 0.9, "horizon_s": 600},
        {"intent": "open a hotfix ticket", "confidence": 0.3},
    ])
    report = predict(engine.store, engine.provider, cfg=engine.config, llm=fake)

    assert isinstance(report, L6PredictionReport)
    assert report.predictions == 2
    l6 = list(engine.store.iter_memories(workspace="test", layer=Layer.L6_PREDICTIVE.value))
    assert len(l6) == 2
    # the explicit-horizon intent keeps it; the other falls back to the config default
    by_text = {m.content: m for m in l6}
    assert by_text["run smoke tests"].metadata["horizon_s"] == 600
    assert by_text["open a hotfix ticket"].metadata["horizon_s"] == engine.config.system2.l6_horizon_s
    assert all(m.metadata["valid_to"] > time.time() for m in l6)
    assert all(m.tags == ["predicted"] for m in l6)


def test_predict_advances_watermark_and_skips_old_episodes(engine):
    engine.episodic.record(agent="bot", action="a", observation="first event")
    fake = _fake_intents([{"intent": "next", "confidence": 0.5}])
    r1 = predict(engine.store, engine.provider, cfg=engine.config, llm=fake)
    assert r1.predictions == 1
    assert r1.watermark_updated_to is not None

    # no new episodes since the watermark -> second run predicts nothing
    r2 = predict(engine.store, engine.provider, cfg=engine.config, llm=fake)
    assert r2.predictions == 0


def test_predict_retires_expired_intents(engine):
    # seed an already-expired L6 prediction directly
    expired = Memory(
        layer=Layer.L6_PREDICTIVE, type=MemoryType.FORWARD_INTENT,
        content="stale intent", summary="stale", tags=["predicted"],
        metadata={"confidence": 0.5, "horizon_s": 1.0, "valid_to": time.time() - 10},
        source="seed", workspace="test",
    )
    engine.store.put_memory(expired, vector=engine.provider.embed("stale intent"))

    engine.episodic.record(agent="bot", action="a", observation="a fresh event")
    fake = _fake_intents([])
    report = predict(engine.store, engine.provider, cfg=engine.config, llm=fake)

    assert report.expired_retired == 1
    assert is_retired(engine.store.get_memory(expired.id))


def test_predict_offline_noop_when_llm_none(engine):
    engine.episodic.record(agent="bot", action="a", observation="an event")
    report = predict(engine.store, engine.provider, cfg=engine.config, llm=None)
    assert report.skipped is True
    assert report.predictions == 0
    assert list(engine.store.iter_memories(workspace="test", layer=Layer.L6_PREDICTIVE.value)) == []
