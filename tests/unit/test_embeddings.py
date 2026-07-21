"""Tests for the embedding providers — especially the offline HashingEmbedding."""

import math

import pytest

from ladym.storage.embeddings import HashingEmbedding, cosine_similarity, tokenize


def test_tokenize_splits_camel_and_snake():
    assert tokenize("getUserName") == ["get", "user", "name"]
    assert tokenize("get_user_name") == ["get", "user", "name"]
    assert tokenize("HTTPServer") == ["http", "server"]


def test_hashing_embedding_is_deterministic():
    e = HashingEmbedding(dim=128)
    v1 = e.embed("user authenticates with password")
    v2 = e.embed("user authenticates with password")
    assert v1 == v2
    assert len(v1) == 128


def test_hashing_embedding_is_normalized():
    e = HashingEmbedding(dim=128)
    v = e.embed("anything goes")
    norm = math.sqrt(sum(x * x for x in v))
    assert abs(norm - 1.0) < 1e-5


def test_similarity_orders_related_texts():
    e = HashingEmbedding(dim=256)
    auth_a = e.embed("authenticate user with password")
    auth_b = e.embed("user authentication via password check")
    other = e.embed("render the shopping cart icon")
    sim_close = cosine_similarity(auth_a, auth_b)
    sim_far = cosine_similarity(auth_a, other)
    assert sim_close > sim_far


def test_batch_matches_singletons():
    e = HashingEmbedding(dim=64)
    texts = ["alpha", "beta gamma", "delta"]
    batched = e.embed_batch(texts)
    singles = [e.embed(t) for t in texts]
    assert batched == singles


def test_cosine_similarity_edge_cases():
    assert cosine_similarity([0.0, 0.0], [1.0, 1.0]) == 0.0
    assert cosine_similarity([1.0, 0.0], [1.0, 0.0]) == pytest.approx(1.0)
    assert cosine_similarity([1.0, 0.0], [0.0, 1.0]) == pytest.approx(0.0)
    # different dims return 0 (defensive)
    assert cosine_similarity([1.0], [1.0, 2.0]) == 0.0


def test_hashing_health_check_ok():
    from ladym.storage.embeddings import HashingEmbedding
    ok, msg = HashingEmbedding(dim=64).health_check()
    assert ok is True
    assert isinstance(msg, str)
