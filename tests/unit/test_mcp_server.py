"""Tests for the MCP server — exercised via the FastMCP tool registry, not over stdio.

We grab each registered tool function and call it directly against an in-memory Engine, which
avoids needing a live MCP transport. This is the same pattern MCP's own tests use.
"""

import json

import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.mcp.server import build_server

pytest.importorskip("mcp")


def _tools(server):
    """Pull the underlying tool callables off a FastMCP server."""
    # FastMCP exposes registered tools via the tool manager; reach in for testing.
    tm = server._tool_manager  # type: ignore[attr-defined]
    # In recent MCP SDKs each entry is a Tool object wrapping the callable in ``.fn``;
    # older versions stored the bare function. Support both.
    out = {}
    for name, entry in tm._tools.items():  # type: ignore[attr-defined]
        out[name] = entry.fn if hasattr(entry, "fn") else entry
    return out


@pytest.fixture
def server_with_engine(tmp_path):
    eng = Engine(Config.for_testing(tmp_path))
    server = build_server(engine=eng)
    tools = _tools(server)
    yield server, tools, eng
    eng.close()


def test_mcp_remember_and_recall(server_with_engine):
    _, tools, eng = server_with_engine
    out = json.loads(tools["remember"]("auth uses JWT", tags=["auth"]))
    assert "id" in out

    out = json.loads(tools["recall"]("how does auth work"))
    assert out["query"] == "how does auth work"
    assert any("auth" in r["memory"]["content"] for r in out["results"])


def test_mcp_search_code(server_with_engine):
    _, tools, eng = server_with_engine
    fixture = __import__("pathlib").Path(__file__).resolve().parent.parent / "fixtures" / "sample_repo"
    eng.index_code(fixture)
    out = json.loads(tools["search_code"]("verify password"))
    assert out["results"]
    assert any("password" in r["memory"]["content"] for r in out["results"])


def test_mcp_stats(server_with_engine):
    _, tools, eng = server_with_engine
    eng.semantic.put_fact("hello")
    out = json.loads(tools["stats"]())
    assert out["total_memories"] >= 1


def test_mcp_link_and_forget(server_with_engine):
    _, tools, eng = server_with_engine
    a = eng.semantic.put_fact("A")
    b = eng.semantic.put_fact("B")
    out = json.loads(tools["link"](a.id, b.id, "depends_on"))
    assert out["src"] == a.id and out["dst"] == b.id
    out2 = json.loads(tools["forget"](a.id))
    assert out2["forgotten"] == a.id


def test_mcp_consolidate(server_with_engine):
    _, tools, eng = server_with_engine
    eng.episodic.record(agent="bot", action="x", observation="jwt rotates weekly")
    out = json.loads(tools["consolidate"]())
    assert out["kept_episodes"] >= 1
    assert out["actions"]["ADD"] >= 1


def test_mcp_index_code(server_with_engine):
    _, tools, eng = server_with_engine
    fixture = __import__("pathlib").Path(__file__).resolve().parent.parent / "fixtures" / "sample_repo"
    out = json.loads(tools["index_code"](str(fixture)))
    assert out["files_indexed"] >= 2
    assert out["symbols_written"] >= 4
