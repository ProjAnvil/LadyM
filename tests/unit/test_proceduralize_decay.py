"""Tests for proceduralize (L1→L3) and decay (forgetting)."""

import time

import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.schema import Layer, MemoryType


@pytest.fixture
def engine(tmp_path):
    e = Engine(Config.for_testing(tmp_path))
    yield e
    e.close()


def test_proceduralize_creates_playbook_from_recurring_episodes(engine):
    # 3 similar successful episodes → cluster threshold met
    for i in range(3):
        engine.episodic.record(
            agent="bot", action="deploy",
            observation=f"ran deploy script number {i}",
            outcome="success",
        )
    report = engine.proceduralize(min_cluster_size=3)
    assert report.playbooks_created >= 1
    # the playbook is now retrievable from L3
    resp = engine.recall("how to deploy", types=[MemoryType.PLAYBOOK])
    assert any(r.memory.layer == Layer.PROCEDURAL.value for r in resp.results)


def test_proceduralize_ignores_failed_episodes(engine):
    for i in range(5):
        engine.episodic.record(
            agent="bot", action="deploy",
            observation=f"deploy {i}", outcome="failure",
        )
    report = engine.proceduralize(min_cluster_size=3)
    assert report.playbooks_created == 0


def test_proceduralize_below_threshold_no_op(engine):
    engine.episodic.record(agent="bot", action="deploy", observation="d1", outcome="success")
    engine.episodic.record(agent="bot", action="deploy", observation="d2", outcome="success")
    report = engine.proceduralize(min_cluster_size=3)
    assert report.playbooks_created == 0


def test_decay_removes_old_episodic_items(engine):
    # insert an old episodic event by backdating it after creation
    m = engine.episodic.record(agent="bot", action="old", observation="long ago")
    # backdate last_access so it's well beyond the half-life
    engine.store.conn.execute(
        "UPDATE memories SET last_access_at = ? WHERE id = ?",
        (time.time() - 10 * 365 * 24 * 3600, m.id),
    )
    engine.store.conn.commit()
    report = engine.decay(max_age_s=1.0, activation_floor=0.5)
    assert report.forgotten >= 1
    assert engine.store.get_memory(m.id) is None


def test_decay_preserves_recent_items(engine):
    engine.episodic.record(agent="bot", action="recent", observation="just now")
    report = engine.decay(max_age_s=30 * 24 * 3600)
    assert report.forgotten == 0


def test_decay_dry_run_does_not_delete(engine):
    m = engine.episodic.record(agent="bot", action="old", observation="long ago")
    engine.store.conn.execute(
        "UPDATE memories SET last_access_at = ? WHERE id = ?",
        (time.time() - 10 * 365 * 24 * 3600, m.id),
    )
    engine.store.conn.commit()
    report = engine.decay(max_age_s=1.0, activation_floor=0.5, dry_run=True)
    assert report.forgotten >= 1
    # dry run → still present
    assert engine.store.get_memory(m.id) is not None


def test_decay_never_touches_code_or_procedural(engine):
    engine.semantic.put_code_file("src/x.py", "module summary", language="python")
    engine.procedural.put_playbook("p", ["a"])
    # backdate everything
    old = time.time() - 10 * 365 * 24 * 3600
    engine.store.conn.execute("UPDATE memories SET last_access_at = ?", (old,))
    engine.store.conn.commit()
    report = engine.decay(max_age_s=1.0, activation_floor=0.99)
    # the L2/L3 items remain despite being very old
    assert report.forgotten == 0


def test_proceduralize_idempotent_same_episodes(engine):
    """Same batch of L1 → two proceduralize calls must not duplicate L3 (content_hash NOOP)."""
    for _ in range(3):
        engine.episodic.record(
            agent="bot", action="deploy",
            observation="ran deploy.sh",  # identical → identical cluster/playbook content
            outcome="success",
        )
    r1 = engine.proceduralize(min_cluster_size=3)
    assert r1.actions["ADD"] == 1
    assert r1.playbooks_created == 1

    # second call on the same L1 → NOOP, no new playbook
    r2 = engine.proceduralize(min_cluster_size=3)
    assert r2.actions["NOOP"] == 1
    assert r2.actions["ADD"] == 0
    assert r2.playbooks_created == 0

    # exactly one L3 playbook in the store
    resp = engine.recall("deploy", types=[MemoryType.PLAYBOOK])
    playbooks = [r for r in resp.results if r.memory.layer == Layer.PROCEDURAL.value]
    assert len(playbooks) == 1
