"""Verify the run modules load ladyM config from ladym.toml (not bare Config() defaults)."""
from pathlib import Path


def test_load_cfg_uses_config_load(monkeypatch):
    import ladym
    from benchmarks.longmemeval import _runtime

    sentinel = ladym.Config()
    monkeypatch.setattr(
        ladym.Config, "load",
        classmethod(lambda cls, *a, **k: sentinel),
    )
    assert _runtime.load_cfg() is sentinel


def test_make_engine_loads_config_then_overrides_per_instance(monkeypatch, tmp_path):
    """make_engine must (a) use Config.load, (b) override db_path/workspace per call."""
    import ladym
    from benchmarks.longmemeval import _runtime

    loaded = {}
    sentinel = ladym.Config()  # bare, no network — just a marker for "what load returned"
    monkeypatch.setattr(
        ladym.Config, "load",
        classmethod(lambda cls, *a, **k: (loaded.__setitem__("called", True), sentinel)[1]),
    )
    built = {}

    class _FakeEngine:
        def __init__(self, cfg):
            built["cfg"] = cfg

    monkeypatch.setattr(ladym, "Engine", _FakeEngine)

    _runtime.make_engine(tmp_path / "x.db", "ws1")

    # (a) Config.load was used — the loaded config is what's passed to Engine
    assert loaded.get("called") is True
    assert built["cfg"] is sentinel
    # (b) per-instance override applied on top of the loaded config
    assert built["cfg"].db_path == tmp_path / "x.db"
    assert built["cfg"].workspace == "ws1"


def test_make_engine_overrides_are_independent_per_call(monkeypatch, tmp_path):
    """Two instances must not share the override (no aliasing of db_path/workspace)."""
    import ladym
    from benchmarks.longmemeval import _runtime

    # Return a FRESH Config each call so the override can't leak between instances.
    monkeypatch.setattr(
        ladym.Config, "load",
        classmethod(lambda cls, *a, **k: ladym.Config()),
    )
    configs = []

    class _RecEngine:
        def __init__(self, cfg):
            configs.append(cfg)

    monkeypatch.setattr(ladym, "Engine", _RecEngine)

    _runtime.make_engine(tmp_path / "a.db", "wa")
    _runtime.make_engine(tmp_path / "b.db", "wb")

    assert len(configs) == 2
    assert configs[0].db_path == tmp_path / "a.db"
    assert configs[0].workspace == "wa"
    assert configs[1].db_path == tmp_path / "b.db"
    assert configs[1].workspace == "wb"
