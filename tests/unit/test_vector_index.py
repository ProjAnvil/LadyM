"""Tests for both VectorIndex implementations."""

import pytest

from ladym.storage.embeddings import HashingEmbedding
from ladym.storage.vector_index import InMemoryVectorIndex


def _build_index(items: dict[str, str], dim: int = 128) -> InMemoryVectorIndex:
    e = HashingEmbedding(dim=dim)
    idx = InMemoryVectorIndex(dim=dim)
    for k, v in items.items():
        idx.upsert(k, e.embed(v))
    return idx


def test_search_returns_empty_when_index_empty():
    idx = InMemoryVectorIndex(dim=64)
    assert idx.search([0.1] * 64, top_k=5) == []


def test_search_returns_most_similar_first():
    idx = _build_index(
        {
            "a": "user authentication with password",
            "b": "user authentication with password",
            "c": "render shopping cart",
        }
    )
    e = HashingEmbedding(dim=128)
    q = e.embed("how does user authentication work?")
    hits = idx.search(q, top_k=3)
    ids = [h[0] for h in hits]
    assert ids[0] in {"a", "b"}
    assert "c" not in ids[:2]


def test_upsert_replaces_existing():
    idx = _build_index({"a": "alpha"}, dim=32)
    e = HashingEmbedding(dim=32)
    idx.upsert("a", e.embed("completely different content"))
    assert len(idx) == 1
    hits = idx.search(e.embed("completely different content"), top_k=1)
    assert hits[0][0] == "a"


def test_delete_removes_item():
    idx = _build_index({"a": "alpha", "b": "beta", "c": "gamma"}, dim=32)
    assert len(idx) == 3
    idx.delete("b")
    assert len(idx) == 2
    # search should still work and not return b
    e = HashingEmbedding(dim=32)
    hits = idx.search(e.embed("alpha"), top_k=5)
    assert all(h[0] != "b" for h in hits)


def test_delete_nonexistent_is_noop():
    idx = _build_index({"a": "alpha"}, dim=32)
    idx.delete("zzz")
    assert len(idx) == 1


def test_dim_mismatch_raises():
    idx = InMemoryVectorIndex(dim=8)
    with pytest.raises(ValueError):
        idx.upsert("x", [0.1] * 16)


def test_swap_on_delete_preserves_search_correctness():
    """Regression: the swap-with-last delete must not corrupt remaining vectors."""
    idx = _build_index(
        {
            "a": "alpha authentication",
            "b": "beta authentication",
            "c": "gamma authentication",
            "d": "delta rendering",
        },
        dim=64,
    )
    # delete the middle items to trigger swap path
    idx.delete("b")
    idx.delete("a")
    e = HashingEmbedding(dim=64)
    hits = idx.search(e.embed("authentication"), top_k=4)
    # every remaining hit must reference authentication, never 'delta rendering' first
    assert hits[0][0] in {"c"}
    assert "d" not in [h[0] for h in hits][:1]
