"""Web config UI integration tests (FastAPI ``TestClient``).

The whole module is skipped when the ``[web]`` extra (fastapi/uvicorn/jinja2)
isn't installed, so the default hermetic suite never requires it (NFR-2). With
the extra installed these run for real against ``build_app``.
"""

from __future__ import annotations

import os

import pytest

# Skips the entire module when fastapi is absent → default suite stays hermetic.
pytest.importorskip("fastapi")


@pytest.fixture(autouse=True)
def _isolate_config(monkeypatch, tmp_path):
    """Hermetic: ignore any project/global ``ladym.toml`` and ``LADYM_*`` env so a
    developer's local ``./ladym.toml`` can't change what ``build_app`` renders."""
    monkeypatch.chdir(tmp_path)
    monkeypatch.setenv("HOME", str(tmp_path))
    for k in list(os.environ):
        if k.startswith("LADYM_"):
            monkeypatch.delenv(k, raising=False)
    yield


def test_index_renders_form():
    from fastapi.testclient import TestClient

    from ladym.web.app import build_app

    client = TestClient(build_app(config_path=None))
    r = client.get("/")
    assert r.status_code == 200
    assert "Embedding" in r.text
    assert "htmx" in r.text.lower()


def test_static_assets_served():
    from fastapi.testclient import TestClient

    from ladym.web.app import build_app

    client = TestClient(build_app(config_path=None))
    assert client.get("/static/htmx.min.js").status_code == 200
    assert client.get("/static/pico.min.css").status_code == 200
