"""Regression test for the sqlite-vec cosine distance metric.

Earlier versions of the SQLiteVecIndex used the default L2 distance, which produced wrong
similarity rankings (file-level memories outranked exact symbol matches because the L2→cosine
conversion was incorrect). This test pins the correct behaviour end-to-end through the real
sqlite-vec path.
"""

from __future__ import annotations

import os

import pytest

from ladym import Config, Engine
from ladym.storage.store import SQLiteStore

pytestmark = pytest.mark.skipif(
    os.environ.get("LADYM_NO_SQLITE_VEC") == "1",
    reason="sqlite-vec explicitly disabled in this env",
)


@pytest.fixture
def sv_store(tmp_path):
    """A store that MUST use sqlite-vec (not the in-memory fallback)."""
    s = SQLiteStore(tmp_path / "sv.db", dim=128, prefer_sqlite_vec=True)
    if not s.using_sqlite_vec:
        s.close()
        pytest.skip("sqlite-vec not loadable in this environment")
    yield s
    s.close()


def test_sqlite_vec_returns_high_similarity_for_near_identical_text(sv_store):
    from ladym.storage.embeddings import HashingEmbedding

    e = HashingEmbedding(dim=128)
    from ladym.schema import Layer, Memory, MemoryType

    m1 = Memory(layer=Layer.SEMANTIC, type=MemoryType.FACT, content="verify user password")
    m2 = Memory(layer=Layer.SEMANTIC, type=MemoryType.FACT,
                content="verify_user_password function definition")
    sv_store.put_memory(m1, vector=e.embed(m1.content))
    sv_store.put_memory(m2, vector=e.embed(m2.content))

    hits = sv_store.vector_index.search(e.embed("verify password"), top_k=5)
    assert hits
    top_id, top_sim = hits[0]
    # cosine similarity must be well above 0 for matching content (the original bug gave ~0)
    assert top_sim > 0.3, f"cosine similarity too low: {top_sim}"


def test_sqlite_vec_orders_symbol_above_unrelated_file(tmp_path):
    """The exact regression: a symbol whose name matches the query must rank above an
    unrelated file-level memory."""
    cfg = Config(db_path=tmp_path / "e2e.db", workspace="regression")
    eng = Engine(cfg)
    if not eng.store.using_sqlite_vec:
        eng.close()
        pytest.skip("sqlite-vec not loadable")
    try:
        from pathlib import Path
        fixture = Path(__file__).resolve().parent.parent / "fixtures" / "sample_repo"
        eng.index_code(fixture)
        resp = eng.search_code("verify password")
        # at least one code_symbol hit must be present (not just code_file)
        assert any(r.memory.type == "code_symbol" for r in resp.results)
        # and verify_password specifically must be in the top 3
        top_contents = " ".join(r.memory.content for r in resp.results[:3])
        assert "verify_password" in top_contents
    finally:
        eng.close()
