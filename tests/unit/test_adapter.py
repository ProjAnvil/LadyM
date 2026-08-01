"""Tests for the langchain adapter classes + ModelRouting."""

from ladym.adapter import LangChainEmbeddingAdapter, ModelRouting


class FakeEmbeddings:
    """Duck-typed stand-in for langchain Embeddings — no langchain needed."""

    def embed_query(self, text: str) -> list[float]:
        return [1.0, 2.0, 3.0]

    def embed_documents(self, texts: list[str]) -> list[list[float]]:
        return [self.embed_query(t) for t in texts]


def test_embedding_adapter_embed_and_dim_probe():
    adapter = LangChainEmbeddingAdapter(FakeEmbeddings())
    assert adapter.dim is None
    vec = adapter.embed("hello")
    assert vec == [1.0, 2.0, 3.0]
    assert adapter.dim == 3  # probed on first embed


def test_embedding_adapter_embed_batch():
    adapter = LangChainEmbeddingAdapter(FakeEmbeddings())
    adapter.embed("warm")  # set dim
    out = adapter.embed_batch(["a", "b"])
    assert out == [[1.0, 2.0, 3.0], [1.0, 2.0, 3.0]]


def test_embedding_adapter_health_check():
    adapter = LangChainEmbeddingAdapter(FakeEmbeddings())
    ok, msg = adapter.health_check()
    assert ok is True
    assert "dim=3" in msg


def test_model_routing_defaults_none():
    r = ModelRouting()
    assert r.consolidate is None
    assert r.attention_gate is None
    assert r.proceduralize is None
    assert r.l5_mental_model is None
    assert r.l6_forward_intent is None
    assert r.embedding is None


def test_model_routing_fields_match_named_ops():
    """Field names must equal NAMED_OPS strings (getattr(op) resolves)."""
    from ladym.providers.agents import NAMED_OPS

    fields = {"consolidate", "proceduralize", "attention_gate",
              "l5_mental_model", "l6_forward_intent"}
    assert set(NAMED_OPS) == fields
