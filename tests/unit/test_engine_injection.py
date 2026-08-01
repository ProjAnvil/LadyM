"""Tests for Engine's ModelRouting injection (LLM per-op + embedding)."""

from ladym.adapter import LangChainEmbeddingAdapter, LangChainLLMProvider, ModelRouting
from ladym.config import Config
from ladym.engine import Engine
from ladym.storage.embeddings import HashingEmbedding


class FakeEmbeddings:
    def embed_query(self, text):
        return [0.5, 0.5]

    def embed_documents(self, texts):
        return [self.embed_query(t) for t in texts]


def test_injected_llm_wrapped_not_rebuilt(tmp_path):
    """_make_agent wraps the injected model instead of rebuilding from config."""
    sentinel = object()  # stands in for a BaseChatModel
    eng = Engine(Config.for_testing(tmp_path), models=ModelRouting(consolidate=sentinel))
    try:
        provider = eng._make_agent("consolidate")
        assert isinstance(provider, LangChainLLMProvider)
        assert provider._cm is sentinel  # the injected object, not a config rebuild
    finally:
        eng.close()


def test_injected_embedding_takes_over_provider(tmp_path):
    eng = Engine(Config.for_testing(tmp_path), models=ModelRouting(embedding=FakeEmbeddings()))
    try:
        assert isinstance(eng.provider, LangChainEmbeddingAdapter)
        assert eng.provider.dim == 2  # probed
        assert eng.provider.embed("x") == [0.5, 0.5]
    finally:
        eng.close()


def test_no_injection_falls_back_to_config(tmp_path):
    """Without models=, Engine uses config-driven providers (back-compat)."""
    eng = Engine(Config.for_testing(tmp_path))
    try:
        assert isinstance(eng.provider, HashingEmbedding)  # default offline embedding
        assert eng._make_agent("consolidate") is None  # config has no LLM → None
    finally:
        eng.close()


def test_uninjected_op_falls_back(tmp_path):
    """An op not set in ModelRouting falls back to make_agent(config, op)."""
    sentinel = object()
    eng = Engine(Config.for_testing(tmp_path), models=ModelRouting(consolidate=sentinel))
    try:
        # consolidate is injected; proceduralize is not → config path → None
        assert eng._make_agent("consolidate")._cm is sentinel
        assert eng._make_agent("proceduralize") is None
    finally:
        eng.close()
