"""Tests for the Go runtime seam (_runtime.GoEngine / make_engine).

Hermetic: LadymClient is faked, so no Go binary / MCP subprocess is spawned.
The fake keeps per-db event storage so reopening an engine on the same db
(retrieval phase) sees what ingest wrote.
"""
import json
from types import SimpleNamespace

import pytest

from longmemeval import _runtime


class _FakeClient:
    """Duck-type of ladym_wrapper.LadymClient recording its constructor args."""

    instances = []
    stores: dict[str, list] = {}  # str(db) -> events, persists across reopens

    def __init__(self, binary=None, db=None, workspace=None, **kw):
        self.binary = binary
        self.db = db
        self.workspace = workspace
        self.events = _FakeClient.stores.setdefault(str(db), [])
        self.closed = False
        _FakeClient.instances.append(self)

    def record_event(self, agent, action, **kw):
        mid = f"mem_{len(self.events)}"
        self.events.append({"id": mid, "agent": agent, "action": action})
        return {"id": mid, "layer": "L1_episodic", "type": "event"}

    def recall(self, query, top_k=None, **kw):
        return {
            "query": query,
            "results": [
                {"score": 0.9, "memory": {"id": e["id"], "content": e["action"]}}
                for e in self.events
            ][: top_k or 8],
        }

    def consolidate(self, **kw):
        return {"kept_episodes": 0, "promoted_to_semantic": 0}

    def stats(self, **kw):
        return {"total_memories": len(self.events)}

    def close(self):
        self.closed = True


@pytest.fixture
def fake_client_cls(monkeypatch):
    _FakeClient.instances = []
    _FakeClient.stores = {}
    monkeypatch.setattr(_runtime, "LadymClient", _FakeClient)
    return _FakeClient


def test_make_engine_spawns_client_with_db_and_workspace(fake_client_cls, tmp_path):
    eng = _runtime.make_engine(tmp_path / "x.db", "ws1")
    client = fake_client_cls.instances[0]
    assert client.db == tmp_path / "x.db"
    assert client.workspace == "ws1"
    eng.close()
    assert client.closed is True


def test_make_engine_overrides_are_independent_per_call(fake_client_cls, tmp_path):
    """Two instances must not share db/workspace (no aliasing)."""
    _runtime.make_engine(tmp_path / "a.db", "wa").close()
    _runtime.make_engine(tmp_path / "b.db", "wb").close()
    dbs = [c.db for c in fake_client_cls.instances]
    wss = [c.workspace for c in fake_client_cls.instances]
    assert dbs == [tmp_path / "a.db", tmp_path / "b.db"]
    assert wss == ["wa", "wb"]


def test_record_event_captures_metadata_in_sidecar(fake_client_cls, tmp_path):
    eng = _runtime.make_engine(tmp_path / "x.db", "ws")
    eng.record_event(agent="user", action="hello", metadata={"doc_id": "s_0", "session_id": "s"})
    eng.close()
    sidecar = _runtime.sidecar_path_for(tmp_path / "x.db")
    assert sidecar.exists()
    entries = [json.loads(l) for l in sidecar.read_text().splitlines()]
    assert entries == [{"id": "mem_0", "metadata": {"doc_id": "s_0", "session_id": "s"}}]


def test_recall_reattaches_metadata_from_sidecar(fake_client_cls, tmp_path):
    eng = _runtime.make_engine(tmp_path / "x.db", "ws")
    eng.record_event(agent="user", action="hello", metadata={"doc_id": "s_0"})
    resp = eng.recall("hello", top_k=5)
    eng.close()
    assert resp.results[0].score == 0.9
    assert resp.results[0].memory.content == "hello"
    assert resp.results[0].memory.metadata == {"doc_id": "s_0"}


def test_sidecar_survives_engine_reopen(fake_client_cls, tmp_path):
    """A new engine on the same db (the retrieval phase) sees ingest metadata."""
    db = tmp_path / "x.db"
    eng = _runtime.make_engine(db, "ws")
    eng.record_event(agent="user", action="hello", metadata={"doc_id": "s_0"})
    eng.close()
    db.touch()  # the real db file exists by now (created by ladym serve)

    eng2 = _runtime.make_engine(db, "ws")
    resp = eng2.recall("hello")
    eng2.close()
    assert resp.results[0].memory.metadata == {"doc_id": "s_0"}


def test_sidecar_reset_when_db_missing(fake_client_cls, tmp_path):
    """Ingest unlinks the db before a rebuild; a stale sidecar must not alias
    old memory ids onto the fresh db."""
    db = tmp_path / "x.db"
    eng = _runtime.make_engine(db, "ws")
    eng.record_event(agent="user", action="hello", metadata={"doc_id": "s_0"})
    eng.close()
    assert _runtime.sidecar_path_for(db).exists()
    # db file absent (ingest unlinked it) -> new engine starts with empty sidecar
    eng2 = _runtime.make_engine(db, "ws")
    assert eng2._sidecar == {}
    eng2.close()


def test_recall_unknown_memory_id_gets_empty_metadata(fake_client_cls, tmp_path):
    """Memories without a sidecar entry (e.g. consolidated facts created
    server-side) surface with empty metadata (upstream consolidated caveat)."""
    eng = _runtime.make_engine(tmp_path / "x.db", "ws")
    eng._client.record_event("user", "no-meta event")  # bypass GoEngine: no sidecar entry
    resp = eng.recall("no-meta")
    eng.close()
    assert resp.results[0].memory.metadata == {}


def test_stats_exposes_total_memories(fake_client_cls, tmp_path):
    eng = _runtime.make_engine(tmp_path / "x.db", "ws")
    eng.record_event(agent="user", action="a")
    eng.record_event(agent="user", action="b")
    st = eng.stats()
    eng.close()
    assert isinstance(st, SimpleNamespace)
    assert st.total_memories == 2


def test_ingest_retrieval_roundtrip_through_seam(fake_client_cls, tmp_path):
    """End-to-end hermetic: ingest + run_retrieval through the real seam,
    verifying the doc_id/session_id plumbing the metrics depend on."""
    from lme_fixtures import make_mini_dataset
    from longmemeval import ingest, run_retrieval
    from longmemeval.config import BenchConfig

    cfg = BenchConfig(difficulty="oracle", variant="raw", top_k=50, base_dir=tmp_path)
    data = make_mini_dataset()
    for inst in data:
        ingest.ingest_instance(inst, cfg)
        cfg.db_path_for(inst["question_id"]).touch()  # db file ladym serve created

    out = run_retrieval.run_retrieval(data, cfg)
    lines = [json.loads(l) for l in out.read_text().splitlines()]
    assert [l["question_id"] for l in lines] == ["mini_1"]  # abstention skipped
    m = lines[0]["retrieval_results"]["metrics"]
    # gold turn sess_1_0 was ingested first -> ranked first by the fake
    assert m["turn"]["recall_all@5"] == 1.0
    assert m["session"]["recall_all@5"] == 1.0
