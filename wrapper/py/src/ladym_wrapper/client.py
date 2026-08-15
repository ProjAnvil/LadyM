"""Async ladyM client over MCP stdio.

Spawns the Go ``ladym serve`` subprocess and exposes its MCP tools as typed
async methods. The Go binary is the single source of truth; this package is
a thin client only — no memory logic lives here.
"""

from __future__ import annotations

import json
import os
import shutil
from contextlib import AsyncExitStack
from pathlib import Path
from typing import Any

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

__all__ = ["AsyncLadymClient", "LadymError", "find_ladym_binary"]


class LadymError(RuntimeError):
    """Raised when the ladyM MCP server reports a tool error."""


def find_ladym_binary(explicit: str | os.PathLike[str] | None = None) -> str:
    """Resolve the ``ladym`` Go binary.

    Order: explicit argument → ``LADYM_BIN`` env var → ``PATH`` → the
    repo-local ``bin/ladym`` build.
    """
    candidates: list[str | None] = [
        str(explicit) if explicit else None,
        os.environ.get("LADYM_BIN"),
        shutil.which("ladym"),
        str(Path(__file__).resolve().parents[4] / "bin" / "ladym"),
    ]
    for candidate in candidates:
        if candidate and Path(candidate).is_file():
            return candidate
    raise LadymError(
        "ladym binary not found; build it with `go build -o bin/ladym ./cmd/ladym`, "
        "or set LADYM_BIN / pass binary="
    )


class AsyncLadymClient:
    """Async context manager wrapping ``ladym serve`` (JSON-RPC over stdio).

    Usage::

        async with AsyncLadymClient() as client:
            hits = await client.recall("deployment steps")
    """

    def __init__(
        self,
        binary: str | os.PathLike[str] | None = None,
        db: str | os.PathLike[str] | None = None,
        workspace: str | None = None,
        extra_args: list[str] | None = None,
    ) -> None:
        args = ["serve"]
        if db is not None:
            args += ["--db", str(db)]
        if workspace is not None:
            args += ["--workspace", workspace]
        args += list(extra_args or [])
        self._params = StdioServerParameters(
            command=find_ladym_binary(binary), args=args
        )
        self._stack = AsyncExitStack()
        self._session: ClientSession | None = None

    async def __aenter__(self) -> AsyncLadymClient:
        read, write = await self._stack.enter_async_context(stdio_client(self._params))
        self._session = await self._stack.enter_async_context(ClientSession(read, write))
        await self._session.initialize()
        return self

    async def __aexit__(self, *exc: Any) -> None:
        await self._stack.aclose()

    async def _call(self, tool: str, **kwargs: Any) -> Any:
        if self._session is None:
            raise LadymError("client not started; use `async with`")
        arguments = {k: v for k, v in kwargs.items() if v is not None}
        result = await self._session.call_tool(tool, arguments)
        if result.is_error:
            raise LadymError(f"{tool} failed: {result.content}")
        if result.structured_content is not None:
            # MCP backward-compat wrapping: when the server declares no
            # outputSchema (the Go `ladym serve` doesn't), the SDK wraps the
            # text payload as {"result": <raw text>}. Unwrap it and parse the
            # JSON text the server actually sent.
            sc = result.structured_content
            if set(sc) == {"result"} and isinstance(sc["result"], str):
                try:
                    return json.loads(sc["result"])
                except json.JSONDecodeError:
                    return sc["result"]
            return sc
        for block in result.content:
            if block.type == "text":
                try:
                    return json.loads(block.text)
                except json.JSONDecodeError:
                    return block.text
        return None

    # -- memory tools (mirror `ladym serve` tools/list) --

    async def recall(
        self,
        query: str,
        top_k: int | None = None,
        workspace: str | None = None,
        code_only: bool | None = None,
    ) -> Any:
        return await self._call(
            "recall", query=query, top_k=top_k, workspace=workspace, code_only=code_only
        )

    async def remember(
        self,
        content: str,
        source: str | None = None,
        tags: list[str] | None = None,
        workspace: str | None = None,
    ) -> Any:
        return await self._call(
            "remember", content=content, source=source, tags=tags, workspace=workspace
        )

    async def record_event(
        self,
        agent: str,
        action: str,
        observation: str | None = None,
        outcome: str | None = None,
        tags: list[str] | None = None,
        workspace: str | None = None,
    ) -> Any:
        return await self._call(
            "record_event",
            agent=agent,
            action=action,
            observation=observation,
            outcome=outcome,
            tags=tags,
            workspace=workspace,
        )

    async def search_code(
        self,
        query: str,
        top_k: int | None = None,
        workspace: str | None = None,
    ) -> Any:
        return await self._call(
            "search_code", query=query, top_k=top_k, workspace=workspace
        )

    async def index_code(
        self,
        root: str,
        force: bool | None = None,
        languages: list[str] | None = None,
        workspace: str | None = None,
    ) -> Any:
        return await self._call(
            "index_code", root=root, force=force, languages=languages, workspace=workspace
        )

    async def consolidate(self, workspace: str | None = None) -> Any:
        return await self._call("consolidate", workspace=workspace)

    async def stats(self, workspace: str | None = None) -> Any:
        return await self._call("stats", workspace=workspace)

    async def link(
        self, src: str, dst: str, relation: str | None = None
    ) -> Any:
        return await self._call("link", src=src, dst=dst, relation=relation)

    async def forget(self, memory_id: str) -> Any:
        return await self._call("forget", memory_id=memory_id)
