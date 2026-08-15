"""Synchronous facade over :class:`AsyncLadymClient`.

Runs the MCP stdio session on a private background event loop so plain
(non-async) Python code can call ladyM with ordinary blocking methods.

The session is entered and exited inside a single long-lived task on that
loop — anyio cancel scopes (used by ``mcp.client.stdio``) must exit in the
same task that entered them, so naively shipping ``__aenter__``/``__aexit__``
as separate ``run_coroutine_threadsafe`` calls fails.
"""

from __future__ import annotations

import asyncio
import threading
from typing import Any, Coroutine

from .client import AsyncLadymClient

__all__ = ["LadymClient"]


class LadymClient:
    """Blocking wrapper around ``ladym serve``.

    Usage::

        with LadymClient() as client:
            hits = client.recall("deployment steps")
    """

    def __init__(self, **kwargs: Any) -> None:
        self._loop = asyncio.new_event_loop()
        self._thread = threading.Thread(
            target=self._loop.run_forever, name="ladym-mcp", daemon=True
        )
        self._thread.start()
        self._async = AsyncLadymClient(**kwargs)

        async def start() -> None:
            self._stop = asyncio.Event()
            self._ready = self._loop.create_future()

            async def runner() -> None:
                try:
                    async with self._async:
                        self._ready.set_result(None)
                        await self._stop.wait()
                except BaseException as exc:  # noqa: BLE001 - surface to caller
                    if not self._ready.done():
                        self._ready.set_exception(exc)

            self._runner = asyncio.ensure_future(runner())
            await self._ready

        self._run(start())

    def _run(self, coro: Coroutine[Any, Any, Any]) -> Any:
        return asyncio.run_coroutine_threadsafe(coro, self._loop).result()

    def __enter__(self) -> LadymClient:
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()

    def close(self) -> None:
        async def stop() -> None:
            self._stop.set()
            await self._runner

        try:
            self._run(stop())
        finally:
            self._loop.call_soon_threadsafe(self._loop.stop)
            self._thread.join()
            self._loop.close()

    # -- memory tools (delegating to AsyncLadymClient) --

    def recall(self, query: str, **kwargs: Any) -> Any:
        return self._run(self._async.recall(query, **kwargs))

    def remember(self, content: str, **kwargs: Any) -> Any:
        return self._run(self._async.remember(content, **kwargs))

    def record_event(self, agent: str, action: str, **kwargs: Any) -> Any:
        return self._run(self._async.record_event(agent, action, **kwargs))

    def search_code(self, query: str, **kwargs: Any) -> Any:
        return self._run(self._async.search_code(query, **kwargs))

    def index_code(self, root: str, **kwargs: Any) -> Any:
        return self._run(self._async.index_code(root, **kwargs))

    def consolidate(self, **kwargs: Any) -> Any:
        return self._run(self._async.consolidate(**kwargs))

    def stats(self, **kwargs: Any) -> Any:
        return self._run(self._async.stats(**kwargs))

    def link(self, src: str, dst: str, **kwargs: Any) -> Any:
        return self._run(self._async.link(src, dst, **kwargs))

    def forget(self, memory_id: str) -> Any:
        return self._run(self._async.forget(memory_id))
