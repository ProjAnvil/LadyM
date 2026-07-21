"""Pins the public surface this plan must not break (NFR-4)."""
from ladym.config import Config
from ladym.engine import Engine


def test_config_defaults_unchanged():
    cfg = Config()
    assert cfg.embedding_provider == "hashing"
    assert cfg.llm_provider is None
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
