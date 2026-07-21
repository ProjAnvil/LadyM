"""Tests for the five memory layers."""

import pytest

from ladym.layers.associative import AssociativeMemory
from ladym.layers.episodic import EpisodicMemory
from ladym.layers.procedural import ProceduralMemory
from ladym.layers.semantic import SemanticMemory
from ladym.layers.working import WorkingMemory
from ladym.schema import Layer, MemoryType
from ladym.storage.embeddings import HashingEmbedding
from ladym.storage.store import SQLiteStore


@pytest.fixture
def store(tmp_db):
    s = SQLiteStore(tmp_db, dim=128, prefer_sqlite_vec=False)
    yield s
    s.close()


@pytest.fixture
def embedder():
    return HashingEmbedding(dim=128)


def test_working_memory_bounded_and_drainable():
    wm = WorkingMemory(capacity=3)
    for i in range(5):
        wm.push(f"note {i}")
    assert len(wm) == 3
    drained = wm.drain()
    assert len(drained) == 3
    assert len(wm) == 0
    assert all(m.layer == Layer.WORKING.value for m in drained)


def test_working_memory_threadsafe_iter():
    wm = WorkingMemory()
    wm.push("a")
    items = list(wm)
    assert items[0].content == "a"


def test_episodic_record_writes_event(store, embedder):
    ep = EpisodicMemory(store, embedder)
    m = ep.record(agent="claude", action="edited file", observation="fixed bug",
                  outcome="success")
    assert m.layer == Layer.EPISODIC.value
    assert m.type == MemoryType.EVENT.value
    assert m.metadata["agent"] == "claude"
    assert m.metadata["outcome"] == "success"
    # the event was embedded and indexed
    assert len(store.vector_index) == 1


def test_episodic_recent_orders_by_time(store, embedder):
    ep = EpisodicMemory(store, embedder)
    a = ep.record(agent="x", action="a")
    b = ep.record(agent="x", action="b")
    recent = ep.recent(limit=10)
    assert recent[0].id == b.id
    assert recent[-1].id == a.id


def test_semantic_put_fact_dedups_by_hash(store, embedder):
    sm = SemanticMemory(store, embedder)
    m1 = sm.put_fact("auth uses JWT")
    m2 = sm.put_fact("auth uses JWT")  # identical content → identical hash
    assert m1.content_hash == m2.content_hash
    # both inserts happened (consolidate is responsible for NOOP); hash queryable
    assert sm.find_by_hash(m1.content_hash) is not None


def test_semantic_put_code_file_tagged(store, embedder):
    sm = SemanticMemory(store, embedder)
    m = sm.put_code_file("src/auth.py", "Handles JWT login flow", language="python")
    assert m.type == MemoryType.CODE_FILE.value
    assert "code" in m.tags and "python" in m.tags
    assert m.metadata["file_path"] == "src/auth.py"


def test_procedural_put_playbook_and_snippet(store, embedder):
    pm = ProceduralMemory(store, embedder)
    pb = pm.put_playbook("Deploy", ["build", "ship", "verify"],
                         preconditions=["tests pass"])
    snip = pm.put_snippet("fib", "def fib(n): return n if n<2 else fib(n-1)+fib(n-2)")
    assert pb.layer == Layer.PROCEDURAL.value
    assert pb.type == MemoryType.PLAYBOOK.value
    assert pb.metadata["steps"] == ["build", "ship", "verify"]
    assert snip.type == MemoryType.SNIPPET.value
    assert "def fib" in snip.metadata["code"]


def test_associative_link_and_neighbors(store, embedder):
    sm = SemanticMemory(store, embedder)
    a = sm.put_fact("fact A")
    b = sm.put_fact("fact B")
    am = AssociativeMemory(store)
    am.link(a.id, b.id, relation="related_to")
    nbrs_a = am.neighbors(a.id)
    nbrs_b = am.neighbors(b.id)
    assert len(nbrs_a) == 1 and len(nbrs_b) == 1
    counts = am.neighbor_counts()
    assert counts[a.id] == 1
    assert counts[b.id] == 1


def test_associative_retire_edge(store, embedder):
    sm = SemanticMemory(store, embedder)
    a = sm.put_fact("A")
    b = sm.put_fact("B")
    am = AssociativeMemory(store)
    e = am.link(a.id, b.id, relation="r")
    am.retire(e.id)
    assert am.neighbors(a.id) == []
    # retired edges don't count towards activation boost
    assert am.neighbor_counts() == {}
