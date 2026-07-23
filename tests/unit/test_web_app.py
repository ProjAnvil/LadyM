"""Web editor secret management endpoints (Task 9).

Exercises the FastAPI + HTMX config editor's secret-management API surface
(``GET/POST/DELETE /api/secrets`` and ``POST /api/master-key``). Each test
isolates ``HOME`` to ``tmp_path`` so the real ``~/.ladyM`` is never touched.
"""

import pytest

fastapi = pytest.importorskip("fastapi")  # noqa: F841 — guards the web extra
from fastapi.testclient import TestClient  # noqa: E402

from ladym.web.app import build_app  # noqa: E402


@pytest.fixture
def client(tmp_path, monkeypatch):
    monkeypatch.setenv("HOME", str(tmp_path))
    app = build_app(config_path=None)
    return TestClient(app)


def test_secrets_empty_without_master_key(client):
    r = client.get("/api/secrets")
    assert r.status_code == 200
    assert r.json() == {"master_key_set": False, "names": []}


def test_set_master_key_then_set_kv(client, tmp_path):
    assert client.post("/api/master-key", json={"key": "p"}).status_code == 200
    assert client.post("/api/secrets", json={"name": "K", "value": "v"}).status_code == 200
    r = client.get("/api/secrets")
    assert r.json()["master_key_set"] is True
    assert r.json()["names"] == ["K"]


def test_reset_master_key(client):
    client.post("/api/master-key", json={"key": "old"})
    client.post("/api/secrets", json={"name": "K", "value": "v"})
    assert client.post("/api/master-key", json={"key": "new", "reset": True}).status_code == 200
    # value still decryptable under the new key
    from ladym.secrets import SecretStore

    assert SecretStore().get("K") == "v"


def test_set_kv_requires_master_key(client):
    """Setting a value before a master key exists must fail (require_master_key)."""
    r = client.post("/api/secrets", json={"name": "K", "value": "v"})
    assert r.status_code == 400


def test_set_kv_missing_value_is_400_not_500(client):
    """Missing ``value`` must be a client error (400), not a server bug (500).

    Regression guard: the original handler indexed ``payload["value"]`` directly,
    so a missing field raised ``KeyError`` → FastAPI 500. Validates the ``.get()``
    + explicit ``HTTPException(400)`` fix.
    """
    client.post("/api/master-key", json={"key": "p"})  # master key OK, isolates the field check
    # missing value entirely
    r = client.post("/api/secrets", json={"name": "K"})
    assert r.status_code == 400
    assert "required" in r.json()["detail"].lower()
    # missing name
    r = client.post("/api/secrets", json={"value": "v"})
    assert r.status_code == 400
    # empty value
    r = client.post("/api/secrets", json={"name": "K", "value": ""})
    assert r.status_code == 400


def test_secret_name_with_html_is_stored_verbatim_not_interpreted(client):
    """A name containing HTML markup is stored/retrieved verbatim and the list
    response carries the raw string — it must NOT be interpreted as markup.

    Server side, the API returns JSON (``json.dumps`` escapes ``<`` to ``<``
    by default in FastAPI/Starlette), so the literal name round-trips as text.
    The template-side escape (``textContent``) is covered by the corresponding
    template change; this test pins the API contract so a future regression that
    returns HTML-fragments would surface here.
    """
    client.post("/api/master-key", json={"key": "p"})
    name = "<b>x</b>"
    assert client.post("/api/secrets", json={"name": name, "value": "v"}).status_code == 200
    r = client.get("/api/secrets")
    body = r.json()
    assert body["names"] == [name]  # stored and returned verbatim
    # The raw name string is present; a script payload would be inert in JSON,
    # but assert no ``<script>`` execution contract by checking the verbatim text.
    assert name in r.text
    assert "&lt;script&gt;" not in r.text  # no double-encoding surprise


def test_values_never_returned_in_list(client):
    """``GET /api/secrets`` lists names only — values never leak through the API."""
    client.post("/api/master-key", json={"key": "p"})
    client.post("/api/secrets", json={"name": "K", "value": "super-secret-value"})
    r = client.get("/api/secrets")
    body = r.json()
    assert "values" not in body
    assert "super-secret-value" not in r.text


def test_delete_secret(client):
    client.post("/api/master-key", json={"key": "p"})
    client.post("/api/secrets", json={"name": "K", "value": "v"})
    assert client.delete("/api/secrets/K").status_code == 200
    assert client.get("/api/secrets").json()["names"] == []


def test_index_template_renders_secret_sections(client):
    """The toml editor stays intact and the secret sections are present in the page."""
    r = client.get("/")
    assert r.status_code == 200
    assert b'action="/save"' in r.content  # existing toml editor intact
    assert b"/api/master-key" in r.content  # new master-key section
    assert b"/api/secrets" in r.content  # new kv section
