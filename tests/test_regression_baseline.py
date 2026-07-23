"""Pins the public surface this plan must not break (NFR-4)."""
from ladym.config import Config
from ladym.engine import Engine


def test_config_defaults_unchanged():
    cfg = Config()
    assert cfg.embedding_provider == "hashing"
    assert cfg.llm_provider == "none"
    assert cfg.workspace == "default"
    assert cfg.prefer_sqlite_vec is True
    assert cfg.activation.similarity == 1.0
    assert cfg.recall.top_k_tier1 == 8


def test_engine_constructs_with_plain_config(tmp_path):
    cfg = Config(db_path=tmp_path / "x.db")
    eng = Engine(cfg)
    try:
        assert eng.provider.dim == 256
        assert eng.recall("nothing").tier_reached in (1, 2)
    finally:
        eng.close()


def test_engine_survives_configured_llm_without_extra(tmp_path, monkeypatch):
    """An LLM configured but its extra uninstalled must not crash Engine init or read
    commands (NFR-3 spirit: LLM is write-path-only). Consolidate lazily falls back to the
    heuristic classifier with a warning instead of raising.
    """
    import ladym.providers.agents as agents

    cfg = Config.for_testing(tmp_path)
    cfg.llm_provider = "openai"  # configured...
    cfg.llm_api_key = "sk-test"  # satisfy make_agent's key check so we reach the
    # simulated ImportError (missing-EXTRA path, not missing-key fail-fast).

    # ...but simulate the [llm] extra being missing.
    def _boom(**kw):
        raise ImportError("simulated: no langchain_openai")

    monkeypatch.setattr(agents, "make_llm_provider", _boom)

    eng = Engine(cfg)  # must NOT crash (no eager LLM wiring)
    try:
        eng.recall("anything")          # read path works
        eng.stats()                     # non-LLM command works
        eng.consolidate()               # lazily resolves → heuristic fallback, no crash
        assert eng._llm_classify is None  # fell back to heuristic
    finally:
        eng.close()
