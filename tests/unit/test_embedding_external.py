import pytest

from ladym.providers.embeddings_http import (
    CallableEmbedding,
    FakeHTTPClient,
    HttpEmbedding,
    OllamaEmbedding,
)


def test_ollama_embedding_posts_to_api_embeddings():
    client = FakeHTTPClient(
        responder=lambda payload: {"embedding": [0.1, 0.2, 0.3]},
        expected_path="/api/embeddings",
    )
    emb = OllamaEmbedding("http://localhost:11434", "nomic-embed-text", client=client)
    v = emb.embed("hello")
    assert v == [0.1, 0.2, 0.3]
    assert emb.dim == 3
    assert client.last_payload["model"] == "nomic-embed-text"
    assert client.last_payload["prompt"] == "hello"


def test_ollama_health_check_reports_failure():
    client = FakeHTTPClient(responder=lambda p: (_ for _ in ()).throw(ConnectionError("nope")))
    emb = OllamaEmbedding("http://localhost:11434", "x", client=client)
    ok, msg = emb.health_check()
    assert ok is False
    assert "ConnectionError" in msg


def test_http_embedding_uses_response_path():
    client = FakeHTTPClient(responder=lambda p: {"data": {"vector": [1.0, 0.0]}})
    emb = HttpEmbedding(
        "https://example.test/embed", request_template='{"input": "{text}"}',
        response_path="data.vector", dim=2, client=client,
    )
    assert emb.embed("q") == [1.0, 0.0]


def test_callable_embedding_wraps_user_function():
    emb = CallableEmbedding(fn=lambda text: [float(len(text)), 0.0], dim=2)
    assert emb.embed("abcd") == [4.0, 0.0]
    assert emb.dim == 2


def test_external_batch_matches_singletons():
    client = FakeHTTPClient(responder=lambda p: {"embedding": [0.5, 0.5]})
    emb = OllamaEmbedding("http://x", "m", client=client)
    batch = emb.embed_batch(["a", "b"])
    assert batch == [emb.embed("a"), emb.embed("b")]


# ---------- Task 1.4: dimension probe + provider routing ----------

def test_assert_dim_matches_raises_on_mismatch():
    from ladym.storage.embeddings import EmbeddingDimensionMismatch, _assert_dim_matches
    with pytest.raises(EmbeddingDimensionMismatch) as ei:
        _assert_dim_matches(stored=256, configured=128)
    assert ei.value.stored == 256
    assert ei.value.configured == 128


def test_assert_dim_matches_accepts_equal():
    from ladym.storage.embeddings import _assert_dim_matches
    _assert_dim_matches(stored=256, configured=256)  # no raise


def test_embedding_dimension_mismatch_is_provider_error():
    from ladym.storage.embeddings import (
        EmbeddingDimensionMismatch,
        EmbeddingProviderError,
    )
    assert issubclass(EmbeddingDimensionMismatch, EmbeddingProviderError)


def test_make_provider_routes_hashing():
    from ladym.config import Config
    from ladym.storage.embeddings import make_provider
    cfg = Config()
    cfg.embedding_provider = "hashing"
    cfg.embedding_dim = 256
    p = make_provider(cfg)
    assert p.dim == 256


def test_make_provider_routes_ollama_with_base_url():
    from ladym.config import Config
    from ladym.storage.embeddings import make_provider
    cfg = Config()
    cfg.embedding_provider = "ollama"
    cfg.embedding_base_url = "http://ollama-host:11434"
    cfg.embedding_model = "nomic-embed-text"
    p = make_provider(cfg)
    assert p.base_url == "http://ollama-host:11434"
    assert p.model == "nomic-embed-text"


def test_make_provider_routes_http():
    from ladym.config import Config
    from ladym.storage.embeddings import make_provider
    cfg = Config()
    cfg.embedding_provider = "http"
    cfg.embedding_base_url = "https://example.test/embed"
    cfg.embedding_dim = 4
    p = make_provider(cfg)
    assert p.base_url == "https://example.test/embed"
    assert p.dim == 4


def test_make_provider_routes_callable_via_registry():
    from ladym.config import Config
    from ladym.storage.embeddings import make_provider, register_callable

    def my_fn(text: str) -> list[float]:
        return [float(len(text)), 0.0]

    register_callable("my-fn", my_fn)
    cfg = Config()
    cfg.embedding_provider = "callable"
    cfg.embedding_model = "my-fn"
    cfg.embedding_dim = 2
    p = make_provider(cfg)
    assert p.embed("abcd") == [4.0, 0.0]
    assert p.dim == 2


def test_make_provider_callable_unknown_raises():
    from ladym.config import Config
    from ladym.storage.embeddings import make_provider
    cfg = Config()
    cfg.embedding_provider = "callable"
    cfg.embedding_model = "does-not-exist"
    with pytest.raises(ValueError, match="no callable"):
        make_provider(cfg)


def test_make_provider_unknown_raises():
    from ladym.config import Config
    from ladym.storage.embeddings import make_provider
    cfg = Config()
    cfg.embedding_provider = "nope"
    with pytest.raises(ValueError, match="unknown embedding provider"):
        make_provider(cfg)


def test_engine_probe_persists_dim_on_empty_db(tmp_path):
    from ladym.config import Config
    from ladym.engine import Engine
    cfg = Config(db_path=tmp_path / "d.db")
    cfg.embedding_provider = "hashing"
    cfg.embedding_dim = 256
    eng = Engine(cfg)
    try:
        assert eng.store.get_meta("embedding_dim") == "256"
        assert eng.store.get_meta("embedding_provider") == "hashing"
    finally:
        eng.close()


def test_engine_reopen_with_matching_dim_is_noop(tmp_path):
    from ladym.config import Config
    from ladym.engine import Engine
    db = tmp_path / "d.db"

    cfg1 = Config(db_path=db)
    cfg1.embedding_provider = "hashing"
    cfg1.embedding_dim = 256
    eng1 = Engine(cfg1)
    eng1.close()

    # reopen with same dim → no error
    cfg2 = Config(db_path=db)
    cfg2.embedding_provider = "hashing"
    cfg2.embedding_dim = 256
    eng2 = Engine(cfg2)
    try:
        assert eng2.store.get_meta("embedding_dim") == "256"
    finally:
        eng2.close()


def test_engine_reopen_with_mismatch_raises(tmp_path):
    from ladym.config import Config
    from ladym.engine import Engine
    from ladym.storage.embeddings import EmbeddingDimensionMismatch

    db = tmp_path / "d.db"
    cfg1 = Config(db_path=db)
    cfg1.embedding_provider = "hashing"
    cfg1.embedding_dim = 256
    Engine(cfg1).close()

    # reopen at different dim → must raise
    cfg2 = Config(db_path=db)
    cfg2.embedding_provider = "hashing"
    cfg2.embedding_dim = 128
    with pytest.raises(EmbeddingDimensionMismatch):
        Engine(cfg2)


def test_engine_reopen_with_allow_dim_change_reembeds(tmp_path):
    from ladym.config import Config
    from ladym.engine import Engine

    db = tmp_path / "d.db"
    cfg1 = Config(db_path=db)
    cfg1.embedding_provider = "hashing"
    cfg1.embedding_dim = 256
    eng1 = Engine(cfg1)
    eng1.remember("hello world")  # default semantic layer
    eng1.close()

    cfg2 = Config(db_path=db)
    cfg2.embedding_provider = "hashing"
    cfg2.embedding_dim = 128
    cfg2.embedding_allow_dim_change = True
    eng2 = Engine(cfg2)
    try:
        assert eng2.store.get_meta("embedding_dim") == "128"
        # recall should still work against the re-embedded vectors
        resp = eng2.recall("hello")
        assert resp.tier_reached in (1, 2)
    finally:
        eng2.close()


def test_engine_enable_wal_is_wired(tmp_path):
    """Engine must pass enable_wal to the store without error."""
    from ladym.config import Config
    from ladym.engine import Engine
    cfg = Config(db_path=tmp_path / "d.db")
    cfg.enable_wal = True
    eng = Engine(cfg)
    try:
        mode = eng.store.conn.execute("PRAGMA journal_mode").fetchone()[0]
        assert mode == "wal"
    finally:
        eng.close()


# ---------- Task 1.4 review fix: lazy-dim provider regression ----------

def test_ollama_embedding_dim_is_none_until_first_embed():
    """OllamaEmbedding keeps dim=None until embed() populates it (construction must not hit network)."""
    client = FakeHTTPClient(responder=lambda payload: {"embedding": [0.0, 0.0, 0.0, 0.0]})
    emb = OllamaEmbedding("http://x", "m", client=client)
    assert emb.dim is None  # lazy by design
    v = emb.embed("probe")
    assert len(v) == 4
    assert emb.dim == 4


def test_engine_ensure_provider_dim_populates_lazy_provider(tmp_path):
    """A None-dim provider must be probed by Engine init, before the store is built.

    Regression for the crash where SQLiteStore(dim=None) and the persisted
    ``meta.embedding_dim = "None"`` poisoned subsequent reopens (``int("None")`` dies).
    """
    from ladym.config import Config
    from ladym.engine import Engine

    cfg = Config(db_path=tmp_path / "d.db")
    cfg.embedding_provider = "hashing"
    cfg.embedding_dim = 256
    eng = Engine(cfg)
    try:
        # Build an OllamaEmbedding wired to a fake 4-dim client, then swap it in as the
        # Engine's provider with dim reset to None — the state it would be in right after
        # make_provider returned it. ``_ensure_provider_dim`` must repopulate dim.
        client = FakeHTTPClient(responder=lambda payload: {"embedding": [0.1, 0.2, 0.3, 0.4]})
        ollama = OllamaEmbedding("http://x", "m", client=client)
        assert ollama.dim is None
        eng.provider = ollama
        # Simulate the lazy-dim state being None at probe time.
        ollama.dim = None
        eng._ensure_provider_dim()
        assert eng.provider.dim == 4
    finally:
        eng.close()


def test_engine_init_with_lazy_dim_provider_persists_concrete_dim(tmp_path):
    """End-to-end: Engine init must persist a concrete dim even for a lazy provider.

    We wire a 4-dim OllamaEmbedding via monkeypatch of ``make_provider`` (the cleanest way
    to exercise the full init path with a fake HTTP backend) and confirm the persisted
    ``meta.embedding_dim`` is the string "4", not "None".
    """
    from ladym import engine as engine_mod
    from ladym.config import Config
    from ladym.engine import Engine

    client = FakeHTTPClient(responder=lambda payload: {"embedding": [0.1, 0.2, 0.3, 0.4]})
    fake_ollama = OllamaEmbedding("http://x", "m", client=client)
    assert fake_ollama.dim is None

    original_make_provider = engine_mod.make_provider

    def _fake_make_provider(cfg):  # noqa: ARG001
        return fake_ollama

    engine_mod.make_provider = _fake_make_provider
    try:
        cfg = Config(db_path=tmp_path / "d.db")
        cfg.embedding_provider = "ollama"
        cfg.embedding_base_url = "http://x"
        cfg.embedding_model = "m"
        eng = Engine(cfg)
        try:
            assert eng.provider.dim == 4
            assert eng.store.get_meta("embedding_dim") == "4"
        finally:
            eng.close()
    finally:
        engine_mod.make_provider = original_make_provider


def test_engine_ensure_provider_dim_raises_on_failure():
    """A provider that cannot embed must raise EmbeddingProviderError, not crash opaquely."""
    from ladym.config import Config
    from ladym.engine import Engine
    from ladym.storage.embeddings import EmbeddingProviderError

    cfg = Config()

    class _BrokenProvider:
        dim = None

        def embed(self, text):  # noqa: ARG002
            raise ConnectionError("endpoint down")

    eng = Engine.__new__(Engine)
    eng.config = cfg
    eng.provider = _BrokenProvider()
    with pytest.raises(EmbeddingProviderError, match="cannot determine embedding dimension"):
        eng._ensure_provider_dim()
