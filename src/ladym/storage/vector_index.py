"""Vector index abstraction with two interchangeable implementations.

:class:`InMemoryVectorIndex` is a pure-Python numpy index used in tests and tiny workspaces.
:class:`SQLiteVecIndex` wraps the ``sqlite-vec`` loadable extension for production-scale
persistent storage. Both implement the same :class:`VectorIndex` ABC.
"""

from __future__ import annotations

from abc import ABC, abstractmethod

import numpy as np


class VectorIndex(ABC):
    """Insert/query/delete vectors keyed by an arbitrary id."""

    dim: int

    @abstractmethod
    def upsert(self, item_id: str, vector: list[float]) -> None: ...

    @abstractmethod
    def search(self, query: list[float], top_k: int = 10) -> list[tuple[str, float]]:
        """Return ``(id, similarity)`` pairs, most similar first."""

    @abstractmethod
    def delete(self, item_id: str) -> None: ...

    @abstractmethod
    def __len__(self) -> int: ...


class InMemoryVectorIndex(VectorIndex):
    """Brute-force numpy cosine index. Deterministic, perfect for tests."""

    def __init__(self, dim: int):
        self.dim = dim
        self._ids: list[str] = []
        self._id_to_idx: dict[str, int] = {}
        self._mat: np.ndarray | None = None  # shape (N, dim)

    def upsert(self, item_id: str, vector: list[float]) -> None:
        vec = np.asarray(vector, dtype=np.float32)
        if vec.shape[0] != self.dim:
            raise ValueError(f"vector dim {vec.shape[0]} != index dim {self.dim}")
        # L2-normalise so cosine == dot product
        n = np.linalg.norm(vec)
        if n > 0:
            vec = vec / n
        if self._mat is None:
            self._mat = np.zeros((1, self.dim), dtype=np.float32)
            self._mat[0] = vec
            self._ids.append(item_id)
            self._id_to_idx[item_id] = 0
            return
        if item_id in self._id_to_idx:
            self._mat[self._id_to_idx[item_id]] = vec
        else:
            self._mat = np.vstack([self._mat, vec[None, :]])
            idx = len(self._ids)
            self._ids.append(item_id)
            self._id_to_idx[item_id] = idx

    def search(self, query: list[float], top_k: int = 10) -> list[tuple[str, float]]:
        if self._mat is None or len(self._ids) == 0:
            return []
        q = np.asarray(query, dtype=np.float32)
        n = np.linalg.norm(q)
        if n > 0:
            q = q / n
        sims = self._mat @ q  # both normalised → dot == cosine
        k = min(top_k, len(self._ids))
        # argpartition then sort the top-k slice
        idx = np.argpartition(-sims, k - 1)[:k]
        idx = idx[np.argsort(-sims[idx])]
        return [(self._ids[i], float(sims[i])) for i in idx]

    def delete(self, item_id: str) -> None:
        if item_id not in self._id_to_idx or self._mat is None:
            return
        idx = self._id_to_idx.pop(item_id)
        last = len(self._ids) - 1
        # swap with last, then trim
        if idx != last:
            self._mat[idx] = self._mat[last]
            swapped_id = self._ids[last]
            self._ids[idx] = swapped_id
            self._id_to_idx[swapped_id] = idx
        self._ids.pop()
        self._mat = self._mat[:last] if last > 0 else None

    def __len__(self) -> int:
        return len(self._ids)


class SQLiteVecIndex(VectorIndex):
    """Persistent index backed by the ``sqlite-vec`` loadable extension.

    Each LadyM store keeps one ``vec_memories`` virtual table. We delegate to sqlite-vec for
    ANN search; sqlite-vec uses cosine distance natively when vectors are normalised.
    """

    def __init__(self, conn, dim: int):  # type: ignore[no-untyped-def]
        self.conn = conn
        self.dim = dim
        try:
            import sqlite_vec  # type: ignore

            conn.enable_load_extension(True)
            conn.load_extension(sqlite_vec.loadable_path())
            conn.enable_load_extension(False)
        except Exception as e:  # pragma: no cover - environment-specific
            raise RuntimeError(
                "Failed to load sqlite-vec. Install with: pip install sqlite-vec"
            ) from e
        conn.execute(
            f"CREATE VIRTUAL TABLE IF NOT EXISTS vec_memories USING vec0("
            f"id TEXT PRIMARY KEY, embedding float[{dim}] distance_metric=cosine)"
        )
        conn.commit()

    def upsert(self, item_id: str, vector: list[float]) -> None:
        blob = np.asarray(vector, dtype=np.float32).tobytes()
        # vec0 doesn't support upsert directly; delete-then-insert.
        self.conn.execute("DELETE FROM vec_memories WHERE id = ?", (item_id,))
        self.conn.execute(
            "INSERT INTO vec_memories(id, embedding) VALUES (?, ?)",
            (item_id, blob),
        )
        self.conn.commit()

    def search(self, query: list[float], top_k: int = 10) -> list[tuple[str, float]]:
        blob = np.asarray(query, dtype=np.float32).tobytes()
        rows = self.conn.execute(
            "SELECT id, distance FROM vec_memories "
            "WHERE embedding MATCH ? AND k = ? "
            "ORDER BY distance",
            (blob, top_k),
        ).fetchall()
        # sqlite-vec returns cosine *distance* (= 1 - similarity for normalised vectors)
        return [(r[0], max(0.0, 1.0 - r[1])) for r in rows]

    def delete(self, item_id: str) -> None:
        self.conn.execute("DELETE FROM vec_memories WHERE id = ?", (item_id,))
        self.conn.commit()

    def __len__(self) -> int:
        row = self.conn.execute("SELECT COUNT(*) FROM vec_memories").fetchone()
        return int(row[0]) if row else 0


def make_index(prefer_sqlite: bool, conn, dim: int) -> VectorIndex:  # type: ignore[no-untyped-def]
    """Factory: try sqlite-vec, fall back to in-memory (so tests never hard-fail)."""
    if prefer_sqlite:
        try:
            return SQLiteVecIndex(conn, dim)
        except Exception:
            pass
    return InMemoryVectorIndex(dim)
