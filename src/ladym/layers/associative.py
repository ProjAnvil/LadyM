"""L4 — Associative memory (Zettelkasten graph).

Bidirectional edges between memories. Used both for explicit linking (the agent calls
``link()``) and for retrieval-time graph expansion (the deep tier of recall hops along these
edges). Edges have Zep-style temporal validity (``valid_from`` / ``valid_to``) so
contradictions can be retired without being deleted.
"""

from __future__ import annotations

import time
from collections import Counter

from ..schema import Edge
from ..storage.store import SQLiteStore


class AssociativeMemory:
    def __init__(self, store: SQLiteStore):
        self.store = store

    def link(self, src_id: str, dst_id: str, relation: str = "related_to",
             *, weight: float = 1.0, metadata: dict | None = None,
             valid_from: float | None = None, valid_to: float | None = None) -> Edge:
        edge = Edge(
            src_id=src_id,
            relation=relation,
            dst_id=dst_id,
            weight=weight,
            metadata=metadata or {},
            valid_from=valid_from if valid_from is not None else time.time(),
            valid_to=valid_to,
        )
        self.store.put_edge(edge)
        return edge

    def neighbors(self, mem_id: str, *, relation: str | None = None) -> list[Edge]:
        return self.store.neighbors(mem_id, relation=relation)

    def neighbor_counts(self) -> dict[str, int]:
        """Return ``{memory_id: neighbour_count}`` for spreading-activation scoring."""
        rows = self.store.conn.execute(
            "SELECT src_id AS id FROM edges WHERE valid_to IS NULL "
            "UNION ALL "
            "SELECT dst_id AS id FROM edges WHERE valid_to IS NULL"
        ).fetchall()
        c: Counter = Counter(r["id"] for r in rows)
        return dict(c)

    def retire(self, edge_id: str, *, when: float | None = None) -> None:
        """Mark an edge no longer current (sets ``valid_to``)."""
        when = when if when is not None else time.time()
        self.store.conn.execute(
            "UPDATE edges SET valid_to = ? WHERE id = ?", (when, edge_id)
        )
        self.store.conn.commit()
