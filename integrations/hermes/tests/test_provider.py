"""Tests for LadymMemoryProvider with an injected fake client."""

from __future__ import annotations

import json
import threading
import time
from pathlib import Path

import pytest

import hermes
from ladym_hermes.ladym_client import LadymError
from ladym_hermes.provider import LadymMemoryProvider

RECALL_PAYLOAD = {
    "query": "q",
    "results": [
        {
            "score": 0.9,
            "tier": 1,
            "via": "bm25",
            "memory": {
                "id": "m1",
                "layer": "L2",
                "type": "fact",
                "summary": "deploy via nginx",
                "content": "run deploy.sh then reload nginx",
                "source": "test",
                "tags": ["ops"],
            },
        },
        {
            "score": 0.4,
            "tier": 2,
            "via": "vector",
            "memory": {
                "id": "m2",
                "layer": "L2",
                "type": "fact",
                "summary": "db lives at /var/lib",
                "content": "sqlite file at /var/lib/ladym.db",
                "source": "test",
                "tags": [],
            },
        },
    ],
}


class FakeClient:
    """Records calls; stands in for LadymClient."""

    def __init__(self, recall_payload=None):
        self.calls = []
        self.started = False
        self.closed = False
        self.recall_payload = RECALL_PAYLOAD if recall_payload is None else recall_payload
        self.recall_error = None
        self.record_event_event = threading.Event()
        self.consolidate_event = threading.Event()
        self.recall_event = threading.Event()
        self.record_event_delay = 0.0

    def start(self):
        self.started = True

    def close(self):
        self.closed = True

    def _record(self, method, **kwargs):
        self.calls.append((method, kwargs))

    def calls_named(self, method):
        return [kw for m, kw in self.calls if m == method]

    def recall(self, query, top_k=None, workspace=None, code_only=None):
        self._record("recall", query=query, top_k=top_k, workspace=workspace)
        self.recall_event.set()
        if self.recall_error:
            raise self.recall_error
        return self.recall_payload

    def remember(self, content, tags=None, source=None, workspace=None):
        self._record("remember", content=content, tags=tags, source=source,
                     workspace=workspace)
        return {"id": "mem-1", "hash": "abc"}

    def record_event(self, agent, action, observation=None, outcome=None,
                     tags=None, workspace=None):
        self._record("record_event", agent=agent, action=action,
                     observation=observation, outcome=outcome, tags=tags,
                     workspace=workspace)
        if self.record_event_delay:
            time.sleep(self.record_event_delay)
        self.record_event_event.set()
        return {"id": "evt-1"}

    def search_code(self, query, top_k=None, workspace=None):
        self._record("search_code", query=query, top_k=top_k, workspace=workspace)
        return {"results": []}

    def index_code(self, root, force=None, languages=None, workspace=None):
        self._record("index_code", root=root, force=force, workspace=workspace)
        return {"files_indexed": 3}

    def consolidate(self, workspace=None):
        self._record("consolidate", workspace=workspace)
        self.consolidate_event.set()
        return {"promoted_to_semantic": 1}

    def forget(self, memory_id):
        self._record("forget", memory_id=memory_id)
        return {"forgotten": memory_id}


def make_provider(tmp_path, client=None, **init_kwargs):
    if client is None:
        client = FakeClient()
    provider = LadymMemoryProvider(client_factory=lambda **kw: client)
    kwargs = {"hermes_home": str(tmp_path)}
    kwargs.update(init_kwargs)
    provider.initialize("sess-1", **kwargs)
    return provider, client


# -- identity / availability -------------------------------------------------

def test_name():
    assert LadymMemoryProvider(client_factory=lambda **kw: FakeClient()).name == "ladym"


def test_is_available_true_when_binary_resolvable(monkeypatch, tmp_path):
    exe = tmp_path / "ladym"
    exe.write_text("#!/bin/sh\n")
    exe.chmod(0o755)
    monkeypatch.setenv("LADYM_BIN", str(exe))
    monkeypatch.setenv("HERMES_HOME", str(tmp_path / "empty-home"))
    p = LadymMemoryProvider(client_factory=lambda **kw: FakeClient())
    assert p.is_available() is True


def test_is_available_false_when_binary_missing(monkeypatch, tmp_path):
    monkeypatch.setenv("LADYM_BIN", str(tmp_path / "nonexistent"))
    monkeypatch.setenv("PATH", str(tmp_path))  # nothing on PATH either
    monkeypatch.setenv("HERMES_HOME", str(tmp_path / "empty-home"))
    p = LadymMemoryProvider(client_factory=lambda **kw: FakeClient())
    assert p.is_available() is False
    assert "ladym" in p.unavailable_reason().lower()


def test_is_available_uses_ladym_bin_from_hermes_home_config(monkeypatch, tmp_path):
    exe = tmp_path / "bin" / "ladym"
    exe.parent.mkdir()
    exe.write_text("#!/bin/sh\n")
    exe.chmod(0o755)
    (tmp_path / "ladym.json").write_text(json.dumps({"ladym_bin": str(exe)}))
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    monkeypatch.delenv("LADYM_BIN", raising=False)
    monkeypatch.setenv("PATH", str(tmp_path / "empty-path"))  # nothing on PATH
    p = LadymMemoryProvider()
    assert p.is_available() is True


def test_is_available_false_when_config_ladym_bin_invalid(monkeypatch, tmp_path):
    (tmp_path / "ladym.json").write_text(
        json.dumps({"ladym_bin": str(tmp_path / "nonexistent")}))
    monkeypatch.setenv("HERMES_HOME", str(tmp_path))
    monkeypatch.delenv("LADYM_BIN", raising=False)
    monkeypatch.setenv("PATH", str(tmp_path / "empty-path"))
    p = LadymMemoryProvider()
    assert p.is_available() is False


# -- initialize ---------------------------------------------------------------

def test_initialize_creates_db_dir_and_starts_client(tmp_path):
    provider, client = make_provider(tmp_path)
    assert client.started
    assert (tmp_path / "ladym").is_dir()
    provider.shutdown()
    assert client.closed


def test_initialize_reads_saved_config(tmp_path):
    home = str(tmp_path)
    p = LadymMemoryProvider(client_factory=lambda **kw: FakeClient())
    p.save_config({"workspace": "ws-custom", "recall_top_k": 7}, home)
    assert json.loads((tmp_path / "ladym.json").read_text())["workspace"] == "ws-custom"

    provider, client = make_provider(tmp_path)
    provider.prefetch("how do I deploy to production?")
    recall_call = client.calls_named("recall")[0]
    assert recall_call["top_k"] == 7
    assert recall_call["workspace"] == "ws-custom"


def test_agent_workspace_kwarg_overrides_config(tmp_path):
    p = LadymMemoryProvider(client_factory=lambda **kw: FakeClient())
    p.save_config({"workspace": "ws-custom"}, str(tmp_path))
    provider, client = make_provider(tmp_path, agent_workspace="ws-kwarg")
    provider.prefetch("how do I deploy to production?")
    assert client.calls_named("recall")[0]["workspace"] == "ws-kwarg"


def test_initialize_survives_malformed_config_file(tmp_path):
    (tmp_path / "ladym.json").write_text("{not valid json")
    provider, client = make_provider(tmp_path)  # must not raise
    assert client.started
    assert provider._workspace == "hermes"  # defaults


def test_initialize_survives_invalid_config_values(tmp_path):
    (tmp_path / "ladym.json").write_text(json.dumps({
        "recall_top_k": "lots",
        "sync_turns": "maybe",
        "prefetch": "false",
        "workspace": 123,
    }))
    provider, client = make_provider(tmp_path)  # must not raise
    assert provider._recall_top_k == 5       # invalid int → default
    assert provider._sync_turns is True      # invalid bool → default
    assert provider._prefetch_enabled is False  # string "false" parsed
    assert provider._workspace == "hermes"   # non-string → default


# -- prefetch -----------------------------------------------------------------

def test_prefetch_formats_markdown_and_sets_status(tmp_path):
    provider, client = make_provider(tmp_path)
    block = provider.prefetch("how do I deploy to production?")
    assert "deploy via nginx" in block
    assert "run deploy.sh then reload nginx" in block
    assert "db lives at /var/lib" in block
    assert block.startswith("#") or "##" in block  # markdown block
    status = provider.recall_status()
    assert status is not None
    assert status.count == 2
    assert status.provider_label


def test_prefetch_trivial_prompt_returns_empty(tmp_path):
    provider, client = make_provider(tmp_path)
    assert provider.prefetch("hi") == ""
    assert provider.prefetch("ok!") == ""
    assert client.calls_named("recall") == []
    assert provider.recall_status() is None


def test_prefetch_no_results_returns_empty(tmp_path):
    client = FakeClient(recall_payload={"query": "q", "results": []})
    provider, _ = make_provider(tmp_path, client=client)
    assert provider.prefetch("anything substantive here") == ""
    assert provider.recall_status() is None


def test_prefetch_error_returns_empty_and_never_raises(tmp_path):
    client = FakeClient()
    client.recall_error = LadymError("server blew up")
    provider, _ = make_provider(tmp_path, client=client)
    assert provider.prefetch("anything substantive here") == ""


def test_prefetch_disabled_by_config(tmp_path):
    p = LadymMemoryProvider(client_factory=lambda **kw: FakeClient())
    p.save_config({"prefetch": False}, str(tmp_path))
    provider, client = make_provider(tmp_path)
    assert provider.prefetch("anything substantive here") == ""
    assert client.calls_named("recall") == []


# -- queue_prefetch ------------------------------------------------------------

def test_queue_prefetch_recalls_in_background_and_prefetch_consumes_cache(tmp_path):
    provider, client = make_provider(tmp_path)
    provider.queue_prefetch("how do I deploy to production?")
    assert client.recall_event.wait(timeout=5)  # background recall happened
    block = provider.prefetch("how do I deploy to production?")
    assert "deploy via nginx" in block
    assert provider.recall_status().count == 2
    # prefetch consumed the cache instead of issuing a second recall
    assert len(client.calls_named("recall")) == 1


def test_queue_prefetch_skips_trivial_prompt(tmp_path):
    provider, client = make_provider(tmp_path)
    provider.queue_prefetch("ok!")
    provider.queue_prefetch("hi")
    time.sleep(0.2)
    assert client.calls_named("recall") == []


def test_prefetch_first_turn_without_queue_still_recalls(tmp_path):
    provider, client = make_provider(tmp_path)
    block = provider.prefetch("how do I deploy to production?")
    assert "deploy via nginx" in block
    assert provider.recall_status().count == 2


def test_prefetch_ignores_cache_for_different_query(tmp_path):
    provider, client = make_provider(tmp_path)
    provider.queue_prefetch("how do I deploy to production?")
    assert client.recall_event.wait(timeout=5)
    provider.prefetch("how do I deploy to production?")
    block = provider.prefetch("what database does it use?")
    assert block != ""
    assert len(client.calls_named("recall")) == 2


# -- sync_turn -----------------------------------------------------------------

def test_sync_turn_records_event_in_background(tmp_path):
    provider, client = make_provider(tmp_path)
    client.record_event_delay = 0.3
    t0 = time.monotonic()
    provider.sync_turn("how do I deploy?", "run deploy.sh")
    elapsed = time.monotonic() - t0
    assert elapsed < 0.3, "sync_turn must be non-blocking"
    assert client.record_event_event.wait(timeout=5)
    call = client.calls_named("record_event")[0]
    assert call["agent"] == "hermes"
    assert call["action"] == "conversation_turn"
    assert "deploy" in call["observation"]
    assert "deploy.sh" in call["outcome"]
    assert "hermes" in call["tags"]


def test_sync_turn_truncates_long_content(tmp_path):
    provider, client = make_provider(tmp_path)
    provider.sync_turn("u" * 5000, "a" * 5000)
    assert client.record_event_event.wait(timeout=5)
    call = client.calls_named("record_event")[0]
    assert len(call["observation"]) < 5000
    assert len(call["outcome"]) < 5000


def test_sync_turn_skips_non_primary_context(tmp_path):
    provider, client = make_provider(tmp_path, agent_context="subagent")
    provider.sync_turn("hello there", "hi back")
    assert not client.record_event_event.wait(timeout=1)
    assert client.calls_named("record_event") == []
    # reads still work in non-primary contexts
    assert provider.prefetch("how do I deploy to production?") != ""


def test_sync_turn_disabled_by_config(tmp_path):
    p = LadymMemoryProvider(client_factory=lambda **kw: FakeClient())
    p.save_config({"sync_turns": False}, str(tmp_path))
    provider, client = make_provider(tmp_path)
    provider.sync_turn("hello there", "hi back")
    assert not client.record_event_event.wait(timeout=1)


# -- session hooks -------------------------------------------------------------

def test_on_session_end_consolidates_in_background(tmp_path):
    provider, client = make_provider(tmp_path)
    provider.on_session_end([{"role": "user", "content": "bye"}])
    assert client.consolidate_event.wait(timeout=5)


def test_on_pre_compress_returns_empty_and_consolidates(tmp_path):
    provider, client = make_provider(tmp_path)
    out = provider.on_pre_compress([{"role": "user", "content": "old"}])
    assert out == ""
    assert client.consolidate_event.wait(timeout=5)


# -- tools ---------------------------------------------------------------------

def test_tool_schemas_openai_format(tmp_path):
    provider, _ = make_provider(tmp_path)
    schemas = provider.get_tool_schemas()
    names = {s["name"] for s in schemas}
    assert names == {
        "ladym_recall", "ladym_remember", "ladym_record_event",
        "ladym_search_code", "ladym_index_code", "ladym_forget",
    }
    for s in schemas:
        assert s["description"]
        assert s["parameters"]["type"] == "object"


def test_handle_tool_call_recall(tmp_path):
    provider, client = make_provider(tmp_path)
    out = json.loads(provider.handle_tool_call("ladym_recall", {"query": "deploy", "top_k": 2}))
    assert out["results"][0]["memory"]["summary"] == "deploy via nginx"
    assert client.calls_named("recall")[0]["top_k"] == 2


def test_handle_tool_call_remember(tmp_path):
    provider, client = make_provider(tmp_path)
    out = json.loads(provider.handle_tool_call(
        "ladym_remember", {"content": "fact", "tags": ["x"], "source": "agent"}))
    assert out["id"] == "mem-1"
    call = client.calls_named("remember")[0]
    assert call["content"] == "fact"
    assert call["tags"] == ["x"]


def test_handle_tool_call_record_event(tmp_path):
    provider, client = make_provider(tmp_path)
    out = json.loads(provider.handle_tool_call(
        "ladym_record_event",
        {"agent": "hermes", "action": "test", "observation": "o", "outcome": "r"}))
    assert out["id"] == "evt-1"


def test_handle_tool_call_search_and_index(tmp_path):
    provider, client = make_provider(tmp_path)
    assert json.loads(provider.handle_tool_call("ladym_search_code", {"query": "foo"})) == {"results": []}
    out = json.loads(provider.handle_tool_call("ladym_index_code", {"root": "/repo", "force": True}))
    assert out["files_indexed"] == 3
    assert client.calls_named("index_code")[0]["force"] is True


def test_handle_tool_call_forget(tmp_path):
    provider, client = make_provider(tmp_path)
    out = json.loads(provider.handle_tool_call("ladym_forget", {"memory_id": "m9"}))
    assert out["forgotten"] == "m9"


def test_handle_tool_call_unknown_tool(tmp_path):
    provider, _ = make_provider(tmp_path)
    out = json.loads(provider.handle_tool_call("ladym_nope", {}))
    assert "error" in out


def test_handle_tool_call_error_returns_error_json(tmp_path):
    client = FakeClient()
    client.recall_error = LadymError("boom")
    provider, _ = make_provider(tmp_path, client=client)
    out = json.loads(provider.handle_tool_call("ladym_recall", {"query": "x"}))
    assert "error" in out
    assert "boom" in out["error"]


# -- config schema ---------------------------------------------------------------

def test_config_schema(tmp_path):
    provider, _ = make_provider(tmp_path)
    schema = provider.get_config_schema()
    keys = {f["key"] for f in schema}
    assert keys == {"ladym_bin", "workspace", "recall_top_k", "sync_turns", "prefetch"}
    by_key = {f["key"]: f for f in schema}
    assert not any(f.get("secret") for f in schema)
    assert by_key["workspace"]["default"] == "hermes"
    assert by_key["recall_top_k"]["default"] == 5
    assert by_key["recall_top_k"]["type"] == "integer"
    assert by_key["sync_turns"]["type"] == "boolean"
    assert by_key["sync_turns"]["default"] is True
    assert by_key["prefetch"]["type"] == "boolean"
    assert by_key["prefetch"]["default"] is True


def test_backup_paths_empty(tmp_path):
    provider, _ = make_provider(tmp_path)
    assert provider.backup_paths() == []


# -- hermes plugin loading ------------------------------------------------------

def test_register_hook_registers_provider():
    class FakeCtx:
        def __init__(self):
            self.providers = []

        def register_memory_provider(self, provider):
            self.providers.append(provider)

    ctx = FakeCtx()
    hermes.register(ctx)
    assert len(ctx.providers) == 1
    # The directory shim re-exports whatever class its register() used, so
    # compare against the shim's own symbol (it may be a distinct module
    # instance from the top-level ladym_hermes package).
    assert isinstance(ctx.providers[0], hermes.LadymMemoryProvider)
    assert ctx.providers[0].name == "ladym"


def test_wheel_package_register_hook():
    """The pip-installed form (ladym_hermes) exposes the same register()."""
    import ladym_hermes

    class FakeCtx:
        def __init__(self):
            self.providers = []

        def register_memory_provider(self, provider):
            self.providers.append(provider)

    ctx = FakeCtx()
    ladym_hermes.register(ctx)
    assert len(ctx.providers) == 1
    assert isinstance(ctx.providers[0], LadymMemoryProvider)
    assert ctx.providers[0].name == "ladym"
