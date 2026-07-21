"""L1 — Episodic memory.

Time-stamped events ``(agent, action, observation, outcome)`` — the hippocampal fast-write
store. Items here are *raw* and decay over time; consolidation promotes the salient ones to
L2 (semantic) and clusters of successful ones to L3 (procedural).
"""

from __future__ import annotations

from typing import Any

from ..schema import Layer, Memory, MemoryType
from ..storage.embeddings import EmbeddingProvider
from ..storage.store import SQLiteStore


class EpisodicMemory:
    def __init__(self, store: SQLiteStore, embedder: EmbeddingProvider,
                 workspace: str = "default"):
        self.store = store
        self.embedder = embedder
        self.workspace = workspace

    def record(self, *, agent: str, action: str, observation: str = "",
               outcome: str = "", tags: list[str] | None = None,
               metadata: dict[str, Any] | None = None) -> Memory:
        """Append an episodic event. ``content`` is a rendered sentence for embedding."""
        parts = [f"agent={agent}", f"action={action}"]
        if observation:
            parts.append(f"observation={observation}")
        if outcome:
            parts.append(f"outcome={outcome}")
        content = " | ".join(parts)
        meta = dict(metadata or {})
        meta.setdefault("agent", agent)
        meta.setdefault("action", action)
        if observation:
            meta["observation"] = observation
        if outcome:
            meta["outcome"] = outcome

        mem = Memory(
            layer=Layer.EPISODIC,
            type=MemoryType.EVENT,
            content=content,
            summary=f"{agent}: {action}",
            tags=tags or [],
            metadata=meta,
            source=agent,
            workspace=self.workspace,
        )
        vec = self.embedder.embed(content)
        self.store.put_memory(mem, vector=vec)
        return mem

    def recent(self, limit: int = 50) -> list[Memory]:
        rows = self.store.conn.execute(
            "SELECT * FROM memories WHERE layer = ? AND workspace = ? "
            "ORDER BY created_at DESC LIMIT ?",
            (Layer.EPISODIC.value, self.workspace, limit),
        ).fetchall()
        return [self.store._row_to_memory(r) for r in rows]
