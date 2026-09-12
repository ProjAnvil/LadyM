"""Unit tests for ladym_client: fake transport pins method/path/payload
assembly and response parsing; no network, no subprocesses."""

from __future__ import annotations

import pytest

from ladym_client import Client, LadymError


class FakeTransport:
    """Records calls and replays queued (status, payload) responses."""

    def __init__(self, *responses):
        self.calls = []
        self.responses = list(responses)

    def __call__(self, method, path, query, body):
        self.calls.append({"method": method, "path": path, "query": query, "body": body})
        return self.responses.pop(0) if self.responses else (200, {})


def make_client(*responses):
    ft = FakeTransport(*responses)
    return Client("http://ladym.test/", transport=ft), ft


def test_ping():
    c, ft = make_client((200, {"status": "ok"}))
    assert c.ping() is None
    assert ft.calls[0]["method"] == "GET"
    assert ft.calls[0]["path"] == "/healthz"
    assert ft.calls[0]["body"] is None


def test_login():
    c, ft = make_client((200, {"username": "alice", "workspace": "acme", "admin": False, "created_at": 1.5}))
    u = c.login()
    assert (u.username, u.workspace, u.admin, u.created_at) == ("alice", "acme", False, 1.5)
    assert ft.calls[0]["body"] == {"username": "", "password": ""}


def test_remember_payload_and_result():
    c, ft = make_client((200, {"id": "m1", "hash": "abc"}))
    r = c.remember("sky is blue", tags=["fact"], workspace="ws1", source="pytest")
    assert r.id == "m1" and r.hash == "abc" and not r.dropped
    call = ft.calls[0]
    assert call["method"] == "POST" and call["path"] == "/api/remember"
    assert call["body"] == {"content": "sky is blue", "source": "pytest", "tags": ["fact"], "workspace": "ws1"}


def test_remember_gate_drop():
    c, _ = make_client((200, {"id": None, "hash": None, "gated": "dropped", "reason": "duplicate"}))
    r = c.remember("dup")
    assert r.dropped and r.reason == "duplicate" and r.id == ""


def test_recall_parsing():
    payload = {
        "query": "tea", "tier_reached": 2, "reflected_sufficient": True, "elapsed_ms": 3.2,
        "results": [
            {"score": 0.9, "tier": 2, "via": ["vector"],
             "memory": {"id": "m1", "layer": "semantic", "type": "fact",
                        "summary": "s", "content": "alice likes green tea",
                        "source": "http", "tags": ["pref"]}}
        ],
    }
    c, ft = make_client((200, payload))
    resp = c.recall("tea", top_k=5, workspace="ws1")
    assert ft.calls[0]["body"] == {"query": "tea", "top_k": 5, "workspace": "ws1", "code_only": False}
    assert resp.tier_reached == 2 and resp.reflected_sufficient and resp.elapsed_ms == 3.2
    assert len(resp.results) == 1
    r = resp.results[0]
    assert r.score == 0.9 and r.via == ["vector"]
    assert r.memory.content == "alice likes green tea" and r.memory.tags == ["pref"]


def test_recall_code_only():
    c, ft = make_client((200, {"results": []}))
    c.recall("main func", code_only=True)
    assert ft.calls[0]["body"]["code_only"] is True


def test_recall_rejects_mcp_only_knobs():
    c, _ = make_client()
    with pytest.raises(ValueError):
        c.recall("x", layers=["semantic"])
    with pytest.raises(ValueError):
        c.recall("x", min_similarity=0.5)


def test_record_event():
    c, ft = make_client((200, {"id": "e1", "layer": "episodic", "type": "event"}))
    r = c.record_event("bot", "deploy", observation="ok", outcome="done", tags=["ops"], workspace="ws1")
    assert (r.id, r.layer, r.type) == ("e1", "episodic", "event")
    assert ft.calls[0]["body"] == {
        "agent": "bot", "action": "deploy", "observation": "ok",
        "outcome": "done", "tags": ["ops"], "workspace": "ws1",
    }


def test_consolidate():
    c, ft = make_client((200, {"kept_episodes": 3, "promoted_to_semantic": 1,
                               "skipped_consolidated": 2, "actions": {"ADD": 1}}))
    r = c.consolidate(workspace="ws1", since=100.0)
    assert r.promoted_to_semantic == 1 and r.actions == {"ADD": 1}
    assert ft.calls[0]["body"] == {"workspace": "ws1", "since": 100.0}


def test_stats():
    payload = {"total_memories": 7, "by_layer": {"semantic": 7}, "by_type": {"fact": 7},
               "edges": 2, "code_symbols": 0, "workspaces": ["ws1"], "db_path": "/tmp/x.db",
               "avg_tokens_per_memory": 4.5}
    c, _ = make_client((200, payload))
    st = c.stats("ws1")
    assert st.total_memories == 7 and st.workspaces == ["ws1"] and st.avg_tokens_per_memory == 4.5


def test_link():
    c, ft = make_client((200, {"id": "edge-1", "src": "a", "dst": "b", "relation": "related_to"}))
    assert c.link("a", "b") == "edge-1"
    assert ft.calls[0]["body"] == {"src": "a", "dst": "b", "relation": "related_to"}


def test_forget():
    c, ft = make_client((200, {"forgotten": "m1"}))
    assert c.forget("m1") is None
    assert ft.calls[0]["body"] == {"memory_id": "m1"}


def test_list_memories_query_and_parse():
    payload = {"memories": [{"id": "m1", "layer": "semantic", "type": "fact", "content": "c",
                             "summary": "s", "source": "http", "tags": [], "workspace": "ws1"}],
               "total": 1}
    c, ft = make_client((200, payload))
    ml = c.list_memories(workspace="ws1", layer="semantic", limit=10, offset=5)
    call = ft.calls[0]
    assert call["method"] == "GET" and call["path"] == "/api/memories"
    assert call["query"] == {"workspace": "ws1", "layer": "semantic", "limit": "10", "offset": "5"}
    assert ml.total == 1 and ml.memories[0].id == "m1"


def test_list_memories_defaults_send_no_query():
    c, ft = make_client((200, {"memories": [], "total": 0}))
    c.list_memories()
    assert ft.calls[0]["query"] == {}


def test_list_memories_layer_and_type_filters():
    c, ft = make_client((200, {"memories": [], "total": 0}))
    c.list_memories(layer="semantic", type="fact")
    assert ft.calls[0]["query"] == {"layer": "semantic", "type": "fact"}


def test_update_memory_partial_patch():
    c, ft = make_client((200, {"memory": {"id": "m1", "content": "new"}}))
    out = c.update_memory("m1", content="new")
    call = ft.calls[0]
    assert call["method"] == "PUT" and call["path"] == "/api/memories/m1"
    assert call["body"] == {"content": "new"}  # unset fields omitted
    assert out["content"] == "new"


def test_update_memory_escapes_id():
    c, ft = make_client((200, {"memory": {}}))
    c.update_memory("a/b c", tags=["x"])
    assert ft.calls[0]["path"] == "/api/memories/a%2Fb%20c"


def test_delete_memory():
    c, ft = make_client((200, {"deleted": "m1"}))
    assert c.delete_memory("m1") is None
    assert ft.calls[0]["method"] == "DELETE" and ft.calls[0]["path"] == "/api/memories/m1"


@pytest.mark.parametrize("status", [400, 401, 500])
def test_error_mapping(status):
    c, _ = make_client((status, {"error": "bad things happened"}))
    with pytest.raises(LadymError) as ei:
        c.recall("x")
    assert ei.value.status == status
    assert ei.value.message == "bad things happened"
    assert f"http {status}" in str(ei.value)


def test_error_non_dict_body():
    c, _ = make_client((502, {}))
    with pytest.raises(LadymError) as ei:
        c.ping()
    assert ei.value.status == 502


def test_auth_header_sent_with_default_transport_shape():
    # Assert header construction without network: stub urllib at the Request level.
    import base64

    import ladym_client.client as mod

    seen = {}

    class FakeResponse:
        status = 200

        def read(self):
            return b'{"status":"ok"}'

        def __enter__(self):
            return self

        def __exit__(self, *a):
            return False

    def fake_urlopen(req, timeout=None):
        seen["auth"] = req.headers.get("Authorization")
        seen["timeout"] = timeout
        return FakeResponse()

    orig = mod.urllib.request.urlopen
    mod.urllib.request.urlopen = fake_urlopen
    try:
        c = Client("http://ladym.test", username="alice", password="pw", timeout=7.5)
        c.ping()
        assert seen["auth"] == "Basic " + base64.b64encode(b"alice:pw").decode()
        assert seen["timeout"] == 7.5

        seen.clear()
        Client("http://ladym.test").ping()
        assert seen["auth"] is None
    finally:
        mod.urllib.request.urlopen = orig
