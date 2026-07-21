"""SQLite-backed persistent store for LadyM.

One ``.db`` file holds: relational memory metadata, JSON blobs, the associative graph
(edges), code symbol projections, cross-references, incremental-index state, and (via the
``sqlite-vec`` extension) the vector index.

The store is intentionally storage-only — it knows nothing about embeddings, layers, or
activation. Higher-level modules compose it.
"""

from __future__ import annotations

import json
import sqlite3
import time
from collections.abc import Iterable, Iterator
from contextlib import contextmanager
from pathlib import Path
from typing import Any

from ..schema import Edge, Memory
from .vector_index import InMemoryVectorIndex, SQLiteVecIndex, VectorIndex

_SCHEMA = """
CREATE TABLE IF NOT EXISTS memories (
    id              TEXT PRIMARY KEY,
    layer           TEXT NOT NULL,
    type            TEXT NOT NULL,
    content         TEXT NOT NULL,
    summary         TEXT NOT NULL DEFAULT '',
    tags            TEXT NOT NULL DEFAULT '[]',
    metadata        TEXT NOT NULL DEFAULT '{}',
    source          TEXT NOT NULL DEFAULT '',
    workspace       TEXT NOT NULL DEFAULT 'default',
    created_at      REAL NOT NULL,
    updated_at      REAL NOT NULL,
    last_access_at  REAL NOT NULL,
    access_count    INTEGER NOT NULL DEFAULT 0,
    activation      REAL NOT NULL DEFAULT 0.0,
    content_hash    TEXT NOT NULL DEFAULT '',
    embedding       BLOB
);
CREATE INDEX IF NOT EXISTS idx_mem_layer_ws ON memories(layer, workspace);
CREATE INDEX IF NOT EXISTS idx_mem_type_ws ON memories(type, workspace);
CREATE INDEX IF NOT EXISTS idx_mem_ws ON memories(workspace);
CREATE INDEX IF NOT EXISTS idx_mem_source ON memories(source);
CREATE INDEX IF NOT EXISTS idx_mem_hash ON memories(content_hash);

CREATE TABLE IF NOT EXISTS edges (
    id          TEXT PRIMARY KEY,
    src_id      TEXT NOT NULL,
    relation    TEXT NOT NULL,
    dst_id      TEXT NOT NULL,
    weight      REAL NOT NULL DEFAULT 1.0,
    valid_from  REAL NOT NULL,
    valid_to    REAL,
    metadata    TEXT NOT NULL DEFAULT '{}',
    FOREIGN KEY (src_id) REFERENCES memories(id) ON DELETE CASCADE,
    FOREIGN KEY (dst_id) REFERENCES memories(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_edge_src ON edges(src_id);
CREATE INDEX IF NOT EXISTS idx_edge_dst ON edges(dst_id);

CREATE TABLE IF NOT EXISTS code_symbols (
    memory_id       TEXT PRIMARY KEY,
    file_path       TEXT NOT NULL,
    symbol_kind     TEXT NOT NULL,
    qualified_name  TEXT NOT NULL,
    signature       TEXT NOT NULL DEFAULT '',
    docstring       TEXT NOT NULL DEFAULT '',
    line_start      INTEGER NOT NULL DEFAULT 0,
    line_end        INTEGER NOT NULL DEFAULT 0,
    language        TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_sym_file ON code_symbols(file_path);
CREATE INDEX IF NOT EXISTS idx_sym_qname ON code_symbols(qualified_name);

CREATE TABLE IF NOT EXISTS code_refs (
    src_symbol  TEXT NOT NULL,
    dst_symbol  TEXT NOT NULL,
    ref_kind    TEXT NOT NULL DEFAULT 'calls'
);
CREATE INDEX IF NOT EXISTS idx_ref_src ON code_refs(src_symbol);
CREATE INDEX IF NOT EXISTS idx_ref_dst ON code_refs(dst_symbol);

CREATE TABLE IF NOT EXISTS index_state (
    file_path   TEXT PRIMARY KEY,
    body_hash   TEXT NOT NULL,
    indexed_at  REAL NOT NULL
);

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
"""


class SQLiteStore:
    """The single persistence layer for LadyM."""

    def __init__(self, db_path: Path, dim: int, prefer_sqlite_vec: bool = True,
                 enable_wal: bool = False):
        self.db_path = Path(db_path)
        self.db_path.parent.mkdir(parents=True, exist_ok=True)
        self.conn = sqlite3.connect(str(self.db_path))
        self.conn.row_factory = sqlite3.Row
        if enable_wal:
            self.conn.execute("PRAGMA journal_mode=WAL")
        self.conn.execute("PRAGMA foreign_keys = ON")
        self.conn.executescript(_SCHEMA)
        # migrate: add embedding column to pre-existing DBs (idempotent)
        cols = {r[1] for r in self.conn.execute("PRAGMA table_info(memories)")}
        if "embedding" not in cols:
            self.conn.execute("ALTER TABLE memories ADD COLUMN embedding BLOB")
            self.conn.commit()
        self.dim = dim
        # try sqlite-vec, fall back to in-memory index transparently
        self.vector_index: VectorIndex
        self._using_sqlite_vec = False
        if prefer_sqlite_vec:
            try:
                self.vector_index = SQLiteVecIndex(self.conn, dim)
                self._using_sqlite_vec = True
            except Exception:
                self.vector_index = InMemoryVectorIndex(dim)
        else:
            self.vector_index = InMemoryVectorIndex(dim)
        # when using the in-memory index, warm it from the persisted embedding BLOBs so a
        # reopened store can still answer recall queries.
        if not self._using_sqlite_vec:
            self._warm_index_from_blobs()

    # ----- lifecycle -----

    @property
    def using_sqlite_vec(self) -> bool:
        return self._using_sqlite_vec

    def _warm_index_from_blobs(self) -> None:
        """Repopulate the in-memory vector index from persisted ``embedding`` BLOBs."""
        import numpy as np

        rows = self.conn.execute("SELECT id, embedding FROM memories WHERE embedding IS NOT NULL").fetchall()
        for r in rows:
            try:
                vec = np.frombuffer(r["embedding"], dtype=np.float32).tolist()
                if len(vec) == self.dim:
                    self.vector_index.upsert(r["id"], vec)
            except Exception:
                continue

    def close(self) -> None:
        try:
            self.conn.commit()
        finally:
            self.conn.close()

    def __enter__(self) -> SQLiteStore:
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()

    @contextmanager
    def transaction(self) -> Iterator[sqlite3.Connection]:
        try:
            yield self.conn
            self.conn.commit()
        except Exception:
            self.conn.rollback()
            raise

    # ----- memory CRUD -----

    def put_memory(self, mem: Memory, vector: list[float] | None = None) -> None:
        """Insert or update a memory. If ``vector`` is given, also upsert into the index
        and persist the embedding as a BLOB so the in-memory index can be rebuilt on reopen."""
        import numpy as np

        blob = None
        if vector is not None:
            blob = np.asarray(vector, dtype=np.float32).tobytes()
        self.conn.execute(
            """INSERT INTO memories (id, layer, type, content, summary, tags, metadata,
                                     source, workspace, created_at, updated_at,
                                     last_access_at, access_count, activation, content_hash,
                                     embedding)
               VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
               ON CONFLICT(id) DO UPDATE SET
                 layer=excluded.layer, type=excluded.type, content=excluded.content,
                 summary=excluded.summary, tags=excluded.tags, metadata=excluded.metadata,
                 source=excluded.source, workspace=excluded.workspace,
                 updated_at=excluded.updated_at,
                 last_access_at=excluded.last_access_at,
                 access_count=excluded.access_count,
                 activation=excluded.activation,
                 content_hash=excluded.content_hash,
                 embedding=excluded.embedding""",
            (
                mem.id, mem.layer, mem.type, mem.content, mem.summary,
                json.dumps(mem.tags), json.dumps(mem.metadata),
                mem.source, mem.workspace, mem.created_at, mem.updated_at,
                mem.last_access_at, mem.access_count, mem.activation, mem.content_hash,
                blob,
            ),
        )
        if vector is not None:
            self.vector_index.upsert(mem.id, vector)
        self.conn.commit()

    def _row_to_memory(self, row: sqlite3.Row) -> Memory:
        return Memory(
            id=row["id"],
            layer=row["layer"],
            type=row["type"],
            content=row["content"],
            summary=row["summary"] or "",
            tags=json.loads(row["tags"] or "[]"),
            metadata=json.loads(row["metadata"] or "{}"),
            source=row["source"] or "",
            workspace=row["workspace"],
            created_at=row["created_at"],
            updated_at=row["updated_at"],
            last_access_at=row["last_access_at"],
            access_count=row["access_count"],
            activation=row["activation"],
            content_hash=row["content_hash"] or "",
        )

    def get_memory(self, mem_id: str) -> Memory | None:
        row = self.conn.execute("SELECT * FROM memories WHERE id = ?", (mem_id,)).fetchone()
        return self._row_to_memory(row) if row else None

    def delete_memory(self, mem_id: str) -> None:
        self.conn.execute("DELETE FROM memories WHERE id = ?", (mem_id,))
        self.vector_index.delete(mem_id)
        self.conn.commit()

    def touch_memory(self, mem_id: str, now: float | None = None) -> None:
        now = now if now is not None else time.time()
        self.conn.execute(
            "UPDATE memories SET last_access_at = ?, access_count = access_count + 1 "
            "WHERE id = ?",
            (now, mem_id),
        )
        self.conn.commit()

    def iter_memories(
        self,
        *,
        workspace: str | None = None,
        layer: str | None = None,
        type_: str | None = None,
    ) -> Iterator[Memory]:
        q = "SELECT * FROM memories WHERE 1=1"
        params: list[Any] = []
        if workspace is not None:
            q += " AND workspace = ?"
            params.append(workspace)
        if layer is not None:
            q += " AND layer = ?"
            params.append(layer)
        if type_ is not None:
            q += " AND type = ?"
            params.append(type_)
        cur = self.conn.execute(q, params)
        for row in cur:
            yield self._row_to_memory(row)

    def find_by_hash(self, content_hash: str, workspace: str | None = None) -> Memory | None:
        q = "SELECT * FROM memories WHERE content_hash = ?"
        params: list[Any] = [content_hash]
        if workspace is not None:
            q += " AND workspace = ?"
            params.append(workspace)
        row = self.conn.execute(q, params).fetchone()
        return self._row_to_memory(row) if row else None

    def count(self, workspace: str | None = None) -> dict[str, int]:
        out: dict[str, int] = {}
        base = "SELECT layer, type, COUNT(*) FROM memories"
        params: list[Any] = []
        if workspace is not None:
            base += " WHERE workspace = ?"
            params.append(workspace)
        base += " GROUP BY layer, type"
        for layer, type_, n in self.conn.execute(base, params):
            out[f"{layer}/{type_}"] = int(n)
        return out

    # ----- edge CRUD -----

    def put_edge(self, edge: Edge) -> None:
        self.conn.execute(
            """INSERT INTO edges (id, src_id, relation, dst_id, weight, valid_from,
                                  valid_to, metadata)
               VALUES (?,?,?,?,?,?,?,?)
               ON CONFLICT(id) DO UPDATE SET
                 src_id=excluded.src_id, relation=excluded.relation, dst_id=excluded.dst_id,
                 weight=excluded.weight, valid_from=excluded.valid_from,
                 valid_to=excluded.valid_to, metadata=excluded.metadata""",
            (
                edge.id, edge.src_id, edge.relation, edge.dst_id, edge.weight,
                edge.valid_from, edge.valid_to, json.dumps(edge.metadata),
            ),
        )
        self.conn.commit()

    def neighbors(self, mem_id: str, *, relation: str | None = None) -> list[Edge]:
        q = "SELECT * FROM edges WHERE (src_id = ? OR dst_id = ?)"
        params: list[Any] = [mem_id, mem_id]
        if relation is not None:
            q += " AND relation = ?"
            params.append(relation)
        q += " AND valid_to IS NULL"
        return [self._row_to_edge(r) for r in self.conn.execute(q, params)]

    def _row_to_edge(self, row: sqlite3.Row) -> Edge:
        return Edge(
            id=row["id"],
            src_id=row["src_id"],
            relation=row["relation"],
            dst_id=row["dst_id"],
            weight=row["weight"],
            valid_from=row["valid_from"],
            valid_to=row["valid_to"],
            metadata=json.loads(row["metadata"] or "{}"),
        )

    def count_edges(self) -> int:
        row = self.conn.execute("SELECT COUNT(*) FROM edges").fetchone()
        return int(row[0]) if row else 0

    # ----- code symbol projections -----

    def put_code_symbol(self, sym: Any) -> None:
        self.conn.execute(
            """INSERT INTO code_symbols (memory_id, file_path, symbol_kind, qualified_name,
                                         signature, docstring, line_start, line_end, language)
               VALUES (?,?,?,?,?,?,?,?,?)
               ON CONFLICT(memory_id) DO UPDATE SET
                 file_path=excluded.file_path, symbol_kind=excluded.symbol_kind,
                 qualified_name=excluded.qualified_name, signature=excluded.signature,
                 docstring=excluded.docstring, line_start=excluded.line_start,
                 line_end=excluded.line_end, language=excluded.language""",
            (
                sym.memory_id, sym.file_path, sym.symbol_kind, sym.qualified_name,
                sym.signature, sym.docstring, sym.line_start, sym.line_end, sym.language,
            ),
        )
        self.conn.commit()

    def put_code_refs(self, refs: Iterable[Any]) -> None:
        rows = [(r.src_symbol, r.dst_symbol, r.ref_kind) for r in refs]
        if not rows:
            return
        self.conn.executemany(
            "INSERT INTO code_refs (src_symbol, dst_symbol, ref_kind) VALUES (?,?,?)",
            rows,
        )
        self.conn.commit()

    def symbols_for_file(self, file_path: str) -> list[Any]:
        from ..schema import CodeSymbol  # local import to avoid cycle

        rows = self.conn.execute(
            "SELECT * FROM code_symbols WHERE file_path = ? ORDER BY line_start",
            (file_path,),
        ).fetchall()
        return [
            CodeSymbol(
                memory_id=r["memory_id"],
                file_path=r["file_path"],
                symbol_kind=r["symbol_kind"],
                qualified_name=r["qualified_name"],
                signature=r["signature"],
                docstring=r["docstring"],
                line_start=r["line_start"],
                line_end=r["line_end"],
                language=r["language"],
            )
            for r in rows
        ]

    def refs_for_symbol(self, qualified_name: str, *, direction: str = "both") -> list[Any]:
        from ..schema import CodeRef

        out: list[CodeRef] = []
        if direction in ("out", "both"):
            for r in self.conn.execute(
                "SELECT * FROM code_refs WHERE src_symbol = ?", (qualified_name,)
            ):
                out.append(CodeRef(src_symbol=r[0], dst_symbol=r[1], ref_kind=r[2]))
        if direction in ("in", "both"):
            for r in self.conn.execute(
                "SELECT * FROM code_refs WHERE dst_symbol = ?", (qualified_name,)
            ):
                out.append(CodeRef(src_symbol=r[0], dst_symbol=r[1], ref_kind=r[2]))
        return out

    # ----- index_state -----

    def get_indexed_hash(self, file_path: str) -> str | None:
        row = self.conn.execute(
            "SELECT body_hash FROM index_state WHERE file_path = ?", (file_path,)
        ).fetchone()
        return row["body_hash"] if row else None

    def set_indexed(self, file_path: str, body_hash: str, now: float | None = None) -> None:
        now = now if now is not None else time.time()
        self.conn.execute(
            """INSERT INTO index_state (file_path, body_hash, indexed_at)
               VALUES (?,?,?)
               ON CONFLICT(file_path) DO UPDATE SET
                 body_hash=excluded.body_hash, indexed_at=excluded.indexed_at""",
            (file_path, body_hash, now),
        )
        self.conn.commit()

    def workspaces(self) -> list[str]:
        rows = self.conn.execute(
            "SELECT DISTINCT workspace FROM memories ORDER BY workspace"
        ).fetchall()
        return [r[0] for r in rows]

    def get_meta(self, key: str) -> str | None:
        row = self.conn.execute("SELECT value FROM meta WHERE key = ?", (key,)).fetchone()
        return row["value"] if row else None

    def set_meta(self, key: str, value: str) -> None:
        self.conn.execute(
            "INSERT INTO meta (key, value) VALUES (?,?) "
            "ON CONFLICT(key) DO UPDATE SET value=excluded.value",
            (key, value),
        )
        self.conn.commit()
