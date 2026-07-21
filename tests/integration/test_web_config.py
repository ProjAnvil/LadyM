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


def test_save_writes_toml_and_rejects_secret(tmp_path):
    from fastapi.testclient import TestClient

    from ladym.config import Config
    from ladym.web.app import build_app

    client = TestClient(build_app(config_path=None))
    r = client.post("/save", data={
        "embedding_provider": "openai",
        "embedding_base_url": "https://x",
        "embedding_query_cache_size": "64",   # string from form → must cast to int
        "activation_similarity": "0.9",        # nested → must reshape + cast to float
        "api_key": "sk-leaked",                 # literal secret → must be rejected
    })
    assert r.status_code == 200
    written = (tmp_path / "ladym.toml").read_text()
    assert "sk-leaked" not in written
    # Round-trip (format-independent): reload the written file and confirm the
    # edit landed with correct types — not as the raw form strings.
    reloaded = Config.from_file(tmp_path / "ladym.toml")
    assert reloaded.embedding_provider == "openai"
    assert reloaded.embedding_base_url == "https://x"
    assert reloaded.embedding_query_cache_size == 64
    assert reloaded.activation.similarity == 0.9


def test_test_embedding_endpoint():
    from fastapi.testclient import TestClient

    from ladym.web.app import build_app

    client = TestClient(build_app(config_path=None))
    r = client.post("/test/embedding", data={"embedding_provider": "hashing"})
    assert r.status_code == 200
    assert "dim" in r.text


def test_test_llm_none_is_heuristic():
    from fastapi.testclient import TestClient

    from ladym.web.app import build_app

    client = TestClient(build_app(config_path=None))
    r = client.post("/test/llm", data={"llm_provider": "none"})
    assert r.status_code == 200
    assert "heuristic" in r.text.lower()


def test_reset_renders_form():
    from fastapi.testclient import TestClient

    from ladym.web.app import build_app

    client = TestClient(build_app(config_path=None))
    r = client.post("/reset")
    assert r.status_code == 200
    assert "Embedding" in r.text


def test_stats_endpoint():
    from fastapi.testclient import TestClient

    from ladym.web.app import build_app

    client = TestClient(build_app(config_path=None))
    r = client.get("/stats")
    assert r.status_code == 200
    assert "total" in r.text.lower()
