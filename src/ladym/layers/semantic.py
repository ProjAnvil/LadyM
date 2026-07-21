"""L2 — Semantic memory.

Consolidated facts *and* codebase analysis live here, distinguished only by ``type``.
Writes go through the consolidation primitive (``operations/consolidate.py``) so that the
``ADD/UPDATE/DELETE/NOOP`` decision from mem0 is honoured and duplicates don't accumulate.
"""

from __future__ import annotations

import hashlib
from typing import Any

from ..schema import Layer, Memory, MemoryType
from ..storage.embeddings import EmbeddingProvider
from ..storage.store import SQLiteStore


def content_hash(text: str) -> str:
    return hashlib.blake2b(text.encode(), digest_size=16).hexdigest()


class SemanticMemory:
    def __init__(self, store: SQLiteStore, embedder: EmbeddingProvider,
                 workspace: str = "default"):
        self.store = store
        self.embedder = embedder
        self.workspace = workspace

    def put_fact(self, content: str, *, summary: str = "",
                 tags: list[str] | None = None, metadata: dict[str, Any] | None = None,
                 source: str = "") -> Memory:
        """Direct fact write. Used by consolidate() and remember()."""
        mem = Memory(
            layer=Layer.SEMANTIC,
            type=MemoryType.FACT,
            content=content,
            summary=summary or content[:80],
            tags=tags or [],
            metadata=metadata or {},
            source=source,
            workspace=self.workspace,
            content_hash=content_hash(content),
        )
        vec = self.embedder.embed(content)
        self.store.put_memory(mem, vector=vec)
        return mem

    def put_code_file(self, file_path: str, summary: str, *,
                      language: str = "") -> Memory:
        mem = Memory(
            layer=Layer.SEMANTIC,
            type=MemoryType.CODE_FILE,
            content=f"{file_path}: {summary}",
            summary=summary[:120],
            tags=["code", language] if language else ["code"],
            metadata={"file_path": file_path, "language": language},
            source=file_path,
            workspace=self.workspace,
            content_hash=content_hash(file_path + "|" + summary),
        )
        self.store.put_memory(mem, vector=self.embedder.embed(mem.content))
        return mem

    def find_by_hash(self, h: str) -> Memory | None:
        return self.store.find_by_hash(h, workspace=self.workspace)
