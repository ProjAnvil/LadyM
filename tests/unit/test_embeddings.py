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


def test_health_check_failure_branch_returns_false_with_exception_name():
    """The ABC default ``health_check`` catches ``embed`` failures and reports them.

    Covers the ``except`` branch in ``EmbeddingProvider.health_check``: a provider
    whose ``embed`` raises must surface ``ok=False`` with a message containing the
    exception type, so the web UI's "test embedding" button can render a useful
    diagnostic instead of crashing.
    """
    from ladym.storage.embeddings import EmbeddingProvider

    class _BrokenProvider(EmbeddingProvider):
        dim = 8

        def embed(self, text: str):
            raise RuntimeError("boom: endpoint unreachable")

    ok, msg = _BrokenProvider().health_check()
    assert ok is False
    assert "RuntimeError" in msg
    assert "boom" in msg


# ---------- Task 8: OpenAI embedding secret-store support ----------


def test_openai_embedding_accepts_api_key():
    """OpenAIEmbedding must accept an explicit ``api_key`` and forward it to OpenAI().

    Symmetric to the LLM make_agent path (Task 4): when a user later configures an
    OpenAI embedding, the key resolved three-tier by ``make_provider`` should reach
    the underlying client rather than rely solely on the openai lib's env default.
    """
    from ladym.storage.embeddings import OpenAIEmbedding
    emb = OpenAIEmbedding(model="text-embedding-3-small", base_url=None, api_key="sk-x")
    assert emb._model == "text-embedding-3-small"


def test_make_provider_openai_missing_key_raises(tmp_path, monkeypatch):
    """When the openai embedding provider has no key in any of the three tiers,
    ``make_provider`` must fail fast with ConfigError naming the env var + fix cmd.
    """
    from ladym.config import Config
    from ladym.errors import ConfigError
    from ladym.storage.embeddings import make_provider
    c = Config()
    c.embedding_provider = "openai"
    c.embedding_api_key_env = "NO_SUCH_KEY"
    monkeypatch.delenv("NO_SUCH_KEY", raising=False)
    monkeypatch.setattr("ladym.storage.embeddings.get_store", lambda: _Empty())
    with pytest.raises(ConfigError, match="NO_SUCH_KEY"):
        make_provider(c)


class _Empty:
    def get(self, name):
        return None
