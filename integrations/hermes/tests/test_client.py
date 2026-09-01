"""Tests for LadymClient against the fake MCP server (tests/fake_ladym.py)."""

from __future__ import annotations

import os
import subprocess
import sys
import time
from pathlib import Path

import pytest

from ladym_hermes.ladym_client import LadymClient, LadymError, find_ladym_binary

FAKE_SERVER = Path(__file__).resolve().parent / "fake_ladym.py"


@pytest.fixture
def client(tmp_path):
    c = LadymClient(binary=str(FAKE_SERVER), db=tmp_path / "test.db", workspace="test")
    c.start()
    yield c
    c.close()


def test_initialize_handshake(client):
    assert client.server_info["name"] == "ladym"
    assert client.protocol_version == "2024-11-05"


def test_server_receives_db_and_workspace_args(tmp_path):
    # fake_ladym.py must accept the `serve --db ... --workspace ...` argv
    # without choking; a successful handshake proves it.
    c = LadymClient(binary=str(FAKE_SERVER), db=tmp_path / "w.db", workspace="ws1")
    c.start()
    try:
        assert c.ping() == {}
    finally:
        c.close()


def test_recall_parses_content_text_json(client):
    result = client.recall("deployment steps", top_k=3, workspace="test")
    assert result["query"] == "deployment steps"
    assert len(result["results"]) == 2
    assert result["results"][0]["memory"]["summary"] == "fake summary one"


def test_remember_returns_id(client):
    result = client.remember("a fact", tags=["t"], source="pytest")
    assert result["id"] == "mem-new-1"


def test_is_error_raises_ladym_error(client):
    with pytest.raises(LadymError, match="boom"):
        client.call_tool("explode", {})


def test_unknown_tool_raises_ladym_error(client):
    with pytest.raises(LadymError, match="unknown tool"):
        client.call_tool("nope", {})


def test_list_tools(client):
    names = client.list_tools()
    assert "recall" in names
    assert "forget" in names


def test_rpc_error_raises_ladym_error(client):
    with pytest.raises(LadymError, match="method not found"):
        client._rpc("no/such_method", {})


def test_server_crash_raises_ladym_error(client):
    with pytest.raises(LadymError):
        client.call_tool("die", {})
    with pytest.raises(LadymError):
        client.recall("anything")


def test_close_terminates_process(client):
    proc = client._proc
    client.close()
    assert proc.poll() is not None
    # closing twice must not raise
    client.close()


def test_call_before_start_raises(tmp_path):
    c = LadymClient(binary=str(FAKE_SERVER), db=tmp_path / "x.db")
    with pytest.raises(LadymError, match="not started"):
        c.recall("x")


def test_find_ladym_binary_explicit(tmp_path):
    exe = tmp_path / "ladym"
    exe.write_text("#!/bin/sh\n")
    exe.chmod(0o755)
    assert find_ladym_binary(explicit=str(exe)) == str(exe)


def test_find_ladym_binary_env(tmp_path, monkeypatch):
    exe = tmp_path / "ladym-env"
    exe.write_text("#!/bin/sh\n")
    exe.chmod(0o755)
    monkeypatch.setenv("LADYM_BIN", str(exe))
    assert find_ladym_binary() == str(exe)


def test_find_ladym_binary_missing(monkeypatch, tmp_path):
    monkeypatch.setenv("LADYM_BIN", str(tmp_path / "nonexistent"))
    monkeypatch.setenv("PATH", str(tmp_path))  # no ladym on PATH
    with pytest.raises(LadymError, match="not found"):
        find_ladym_binary()


def test_start_handshake_failure_cleans_up_process(tmp_path):
    env = dict(os.environ, FAKE_LADYM_DIE_ON_INIT="1")
    c = LadymClient(binary=str(FAKE_SERVER), db=tmp_path / "x.db",
                    env=env, timeout=2.0)
    with pytest.raises(LadymError):
        c.start()
    # the subprocess must not leak: reaped and dereferenced
    assert c._proc is None
    # close() after a failed start must be a no-op, not an error
    c.close()


def test_rpc_timeout_kills_server_and_fails_fast(tmp_path):
    c = LadymClient(binary=str(FAKE_SERVER), db=tmp_path / "x.db", timeout=0.5)
    c.start()
    with pytest.raises(LadymError, match="timed out"):
        c.call_tool("hang", {})
    # subsequent calls fail fast instead of queueing behind a dead server
    t0 = time.monotonic()
    with pytest.raises(LadymError):
        c.recall("anything")
    assert time.monotonic() - t0 < 0.5
    # close() on a dead client must not block
    t0 = time.monotonic()
    c.close()
    assert time.monotonic() - t0 < 2.0


def test_oversized_request_rejected_before_send(client):
    # Go server caps scanner lines at 1 MiB; the client must refuse locally
    # (with headroom) rather than kill the connection.
    with pytest.raises(LadymError, match="too large"):
        client.remember("x" * 1_000_000)
    # server is untouched and still responsive
    assert client.ping() == {}
