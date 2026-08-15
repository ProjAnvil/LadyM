"""Hermetic tests for AsyncLadymClient._call result unwrapping.

The Go `ladym serve` declares no outputSchema, so the MCP SDK wraps text
payloads as structured_content={"result": <raw JSON text>}. _call must unwrap
and parse it. No subprocess is spawned: the session is faked.
"""
from __future__ import annotations

import json
from types import SimpleNamespace

import pytest

from ladym_wrapper.client import AsyncLadymClient


def _text_block(text: str):
    return SimpleNamespace(type="text", text=text)


class _FakeSession:
    def __init__(self, result):
        self._result = result

    async def call_tool(self, tool, arguments):
        return self._result


def _client_with(result) -> AsyncLadymClient:
    client = AsyncLadymClient.__new__(AsyncLadymClient)
    client._session = _FakeSession(result)
    return client


@pytest.mark.anyio
async def test_unwraps_mcp_result_compat_wrapper() -> None:
    payload = {"total_memories": 3, "by_layer": {"L1_episodic": 3}}
    result = SimpleNamespace(
        is_error=False,
        structured_content={"result": json.dumps(payload)},
        content=[_text_block(json.dumps(payload))],
    )
    got = await _client_with(result)._call("stats")
    assert got == payload


@pytest.mark.anyio
async def test_unwrap_leaves_non_json_text_as_string() -> None:
    result = SimpleNamespace(
        is_error=False,
        structured_content={"result": "plain text"},
        content=[_text_block("plain text")],
    )
    got = await _client_with(result)._call("stats")
    assert got == "plain text"


@pytest.mark.anyio
async def test_real_structured_content_passes_through() -> None:
    payload = {"id": "abc", "layer": "L1_episodic"}
    result = SimpleNamespace(
        is_error=False, structured_content=payload, content=[]
    )
    got = await _client_with(result)._call("record_event")
    assert got == payload


@pytest.mark.anyio
async def test_falls_back_to_text_content_when_no_structured() -> None:
    payload = {"forgotten": "abc"}
    result = SimpleNamespace(
        is_error=False,
        structured_content=None,
        content=[_text_block(json.dumps(payload))],
    )
    got = await _client_with(result)._call("forget")
    assert got == payload
