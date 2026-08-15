from pathlib import Path

from longmemeval.config import BenchConfig


def test_dirs_derive_from_difficulty_variant(tmp_path):
    cfg = BenchConfig(difficulty="s", variant="consolidated", base_dir=tmp_path)
    assert cfg.data_dir == tmp_path / "data"
    assert cfg.db_dir == tmp_path / "db" / "s" / "consolidated"
    assert cfg.results_dir == tmp_path / "results" / "s" / "consolidated"


def test_defaults():
    cfg = BenchConfig(base_dir=Path("/tmp/x"))
    assert cfg.difficulty == "s"
    assert cfg.variant == "raw"
    assert cfg.limit is None
    assert cfg.top_k == 50


def test_db_path_per_instance(tmp_path):
    cfg = BenchConfig(base_dir=tmp_path)
    p = cfg.db_path_for("q_1")
    assert p == tmp_path / "db" / "s" / "raw" / "q_1.db"


def test_base_dir_str_is_coerced_to_path(tmp_path):
    """Minor #6: a raw str base_dir (from CLI / JSON) must not crash
    downstream Path-only operations (mkdir, /, etc.). __post_init__ coerces."""
    cfg = BenchConfig(base_dir=str(tmp_path))
    assert isinstance(cfg.base_dir, Path)
    # Path-joining must still work (would raise TypeError if str slipped through).
    assert cfg.db_path_for("q_1") == tmp_path / "db" / "s" / "raw" / "q_1.db"
    assert cfg.results_dir == tmp_path / "results" / "s" / "raw"
    # mkdir must succeed (str paths raise on .mkdir in some Python versions).
    cfg.results_dir.mkdir(parents=True, exist_ok=True)
    assert cfg.results_dir.exists()
