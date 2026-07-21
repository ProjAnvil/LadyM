"""Tests for consolidation: ADD/UPDATE/DELETE/NOOP and the LLM classifier hook."""

import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.operations.consolidate import Action


@pytest.fixture
def engine(tmp_path):
    e = Engine(Config.for_testing(tmp_path))
    yield e
    e.close()


def test_consolidate_promotes_new_episodes_as_facts(engine):
    engine.episodic.record(agent="bot", action="discovered", observation="auth uses JWT")
    engine.episodic.record(agent="bot", action="discovered", observation="cache uses redis")
    report = engine.consolidate()
    assert report.promoted_to_semantic >= 2
    assert report.actions[Action.ADD.value] >= 2


def test_consolidate_noop_on_identical_episode(engine):
    engine.episodic.record(agent="bot", action="x", observation="auth uses JWT")
    engine.consolidate()
    # the consolidated fact is now in L2
    engine.episodic.record(agent="bot", action="x", observation="auth uses JWT")
    report = engine.consolidate()
    # the second time, the identical-content episode should be a NOOP or UPDATE, not a fresh ADD
    assert report.actions[Action.ADD.value] == 0


def test_consolidate_update_merges_highly_similar(engine):
    engine.episodic.record(agent="bot", action="x", observation="auth uses JWT token")
    engine.consolidate()
    # near-duplicate that should merge via UPDATE
    engine.episodic.record(agent="bot", action="x", observation="auth uses JWT tokens")
    report = engine.consolidate()
    assert report.actions[Action.UPDATE.value] >= 1 or report.actions[Action.NOOP.value] >= 1


def test_consolidate_workspace_scoped(engine):
    engine.episodic.record(agent="bot", action="x", observation="alpha fact")
    report = engine.consolidate(workspace="nonexistent")
    assert report.kept_episodes == 0
    assert report.promoted_to_semantic == 0


def test_consolidate_llm_classifier_hook(engine):
    """When an LLM classifier is attached, its decisions override the offline heuristic."""
    calls: list[str] = []

    def classifier(candidate: str, similar: list[str]):
        calls.append(candidate)
        # force every candidate to NOOP
        return Action.NOOP, None

    engine.episodic.record(agent="bot", action="x", observation="anything new")
    engine.attach_llm_classifier(classifier)
    report = engine.consolidate()
    assert calls  # classifier was actually invoked
    assert report.actions[Action.NOOP.value] >= 1
    assert report.promoted_to_semantic == 0


def test_consolidate_since_filter(engine):
    import time
    t0 = time.time() - 1
    engine.episodic.record(agent="bot", action="x", observation="old fact")
    report = engine.consolidate(since=t0)
    assert report.kept_episodes >= 1
    # since=future excludes everything
    future = time.time() + 1000
    report2 = engine.consolidate(since=future)
    assert report2.kept_episodes == 0
