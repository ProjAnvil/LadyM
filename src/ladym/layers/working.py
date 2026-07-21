"""L0 — Working / Sensory memory.

A tiny in-process scratchpad mirroring the brain's working memory. Items here are volatile
and intended to be flushed into L1 (episodic) on `consolidate()` or session end.

This is also where the agent's *current* compaction summary lives (Anthropic context
engineering: compaction = the cheapest form of external memory).
"""

from __future__ import annotations

import threading
from collections import deque
from collections.abc import Iterator

from ..schema import Layer, Memory, MemoryType


class WorkingMemory:
    """Thread-safe bounded buffer of L0 notes."""

    def __init__(self, capacity: int = 64, workspace: str = "default"):
        self.capacity = capacity
        self.workspace = workspace
        self._items: deque[Memory] = deque(maxlen=capacity)
        self._lock = threading.Lock()

    def push(self, content: str, *, tags: list[str] | None = None,
             metadata: dict | None = None, source: str = "") -> Memory:
        mem = Memory(
            layer=Layer.WORKING,
            type=MemoryType.NOTE,
            content=content,
            summary=content[:80],
            tags=tags or [],
            metadata=metadata or {},
            source=source,
            workspace=self.workspace,
        )
        with self._lock:
            self._items.append(mem)
        return mem

    def __iter__(self) -> Iterator[Memory]:
        with self._lock:
            return iter(list(self._items))

    def __len__(self) -> int:
        return len(self._items)

    def drain(self) -> list[Memory]:
        """Pop and return all items (used by consolidate to flush L0 → L1)."""
        with self._lock:
            items = list(self._items)
            self._items.clear()
        return items

    def clear(self) -> None:
        with self._lock:
            self._items.clear()
