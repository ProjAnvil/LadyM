"""Tests for the SQLiteStore: CRUD, vector index wiring, edges, code projections."""

import time

import pytest

from ladym.schema import CodeSymbol, Edge, Layer, Memory, MemoryType
from ladym.storage.embeddings import HashingEmbedding
from ladym.storage.store import SQLiteStore


@pytest.fixture
def store(tmp_db):
    s = SQLiteStore(tmp_db, dim=128, prefer_sqlite_vec=False)
    yield s
    s.close()


def _mem(content="hello", layer=Layer.WORKING, type_=MemoryType.NOTE, **kw) -> Memory:
    return Memory(layer=layer, type=type_, content=content, **kw)


def test_put_and_get_memory(store):
    m = _mem("a note", tags=["x"], metadata={"k": "v"})
    store.put_memory(m)
    got = store.get_memory(m.id)
    assert got is not None
    assert got.content == "a note"
    assert got.tags == ["x"]
    assert got.metadata == {"k": "v"}


def test_put_memory_upserts(store):
    m = _mem("original")
    store.put_memory(m)
    m.content = "updated"
    m.updated_at = time.time()
    store.put_memory(m)
    assert store.get_memory(m.id).content == "updated"


def test_put_memory_with_vector_indexes(store):
    e = HashingEmbedding(dim=128)
    m = _mem("auth flow")
    store.put_memory(m, vector=e.embed("auth flow"))
    assert len(store.vector_index) == 1


def test_delete_memory_removes_from_index(store):
    e = HashingEmbedding(dim=128)
    m = _mem("to be deleted")
    store.put_memory(m, vector=e.embed(m.content))
    assert len(store.vector_index) == 1
    store.delete_memory(m.id)
    assert len(store.vector_index) == 0
    assert store.get_memory(m.id) is None


def test_touch_memory_updates_access(store):
    m = _mem("x")
    store.put_memory(m)
    before = store.get_memory(m.id)
    store.touch_memory(m.id, now=before.last_access_at + 100)
    after = store.get_memory(m.id)
    assert after.access_count == before.access_count + 1
    assert after.last_access_at > before.last_access_at


def test_iter_memories_filters(store):
    store.put_memory(_mem("a", layer=Layer.EPISODIC, workspace="w1"))
    store.put_memory(_mem("b", layer=Layer.SEMANTIC, workspace="w2"))
    ws1 = [m.content for m in store.iter_memories(workspace="w1")]
    assert ws1 == ["a"]
    eps = [m.content for m in store.iter_memories(layer=Layer.EPISODIC.value)]
    assert eps == ["a"]


def test_find_by_hash(store):
    m = _mem("x", content_hash="abc123")
    store.put_memory(m)
    found = store.find_by_hash("abc123")
    assert found is not None
    assert found.id == m.id
    assert store.find_by_hash("nope") is None


def test_count_groups_by_layer_type(store):
    store.put_memory(_mem("a", layer=Layer.EPISODIC, type_=MemoryType.EVENT))
    store.put_memory(_mem("b", layer=Layer.EPISODIC, type_=MemoryType.EVENT))
    store.put_memory(_mem("c", layer=Layer.SEMANTIC, type_=MemoryType.FACT))
    counts = store.count()
    assert counts["L1_episodic/event"] == 2
    assert counts["L2_semantic/fact"] == 1


def test_edge_crud(store):
    a = _mem("a")
    b = _mem("b")
    store.put_memory(a)
    store.put_memory(b)
    e = Edge(src_id=a.id, relation="related_to", dst_id=b.id)
    store.put_edge(e)
    nbrs = store.neighbors(a.id)
    assert len(nbrs) == 1
    assert nbrs[0].dst_id == b.id
    # reverse lookup also works
    nbrs_b = store.neighbors(b.id)
    assert len(nbrs_b) == 1


def test_edge_cascade_delete(store):
    a = _mem("a")
    b = _mem("b")
    store.put_memory(a)
    store.put_memory(b)
    store.put_edge(Edge(src_id=a.id, relation="r", dst_id=b.id))
    assert store.count_edges() == 1
    store.delete_memory(a.id)
    # edge should be gone via FK cascade (foreign_keys pragma is ON)
    assert store.count_edges() == 0


def test_code_symbol_round_trip(store):
    m = _mem("def foo()", layer=Layer.SEMANTIC, type_=MemoryType.CODE_SYMBOL)
    store.put_memory(m)
    sym = CodeSymbol(
        memory_id=m.id,
        file_path="src/mod.py",
        symbol_kind="function",
        qualified_name="mod.foo",
        signature="foo(x: int) -> str",
        docstring="Does foo.",
        line_start=10,
        line_end=20,
        language="python",
    )
    store.put_code_symbol(sym)
    syms = store.symbols_for_file("src/mod.py")
    assert len(syms) == 1
    assert syms[0].qualified_name == "mod.foo"


def test_index_state_round_trip(store):
    assert store.get_indexed_hash("src/foo.py") is None
    store.set_indexed("src/foo.py", "deadbeef")
    assert store.get_indexed_hash("src/foo.py") == "deadbeef"
    store.set_indexed("src/foo.py", "updated", now=999.0)
    assert store.get_indexed_hash("src/foo.py") == "updated"


def test_workspaces_listing(store):
    store.put_memory(_mem("a", workspace="alpha"))
    store.put_memory(_mem("b", workspace="beta"))
    assert set(store.workspaces()) == {"alpha", "beta"}


def test_meta_roundtrip(tmp_db):
    from ladym.storage.store import SQLiteStore
    s = SQLiteStore(tmp_db, dim=8)
    assert s.get_meta("foo") is None
    s.set_meta("foo", "bar")
    assert s.get_meta("foo") == "bar"
    # reopen -> persisted
    s.close()
    s2 = SQLiteStore(tmp_db, dim=8)
    assert s2.get_meta("foo") == "bar"
    s2.close()


def test_wal_mode_when_requested(tmp_db):
    from ladym.storage.store import SQLiteStore
    s = SQLiteStore(tmp_db, dim=8, enable_wal=True)
    mode = s.conn.execute("PRAGMA journal_mode").fetchone()[0]
    assert mode == "wal"
    s.close()
