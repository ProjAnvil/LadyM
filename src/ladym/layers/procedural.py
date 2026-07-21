"""L3 — Procedural memory.

Stores reusable playbooks ``preconditions → steps → expected_outcome`` plus verified code
snippets. CoALA frames procedural memory as the agent's "skill library"; Voyager is the
canonical example of agents writing new skills here.
"""

from __future__ import annotations

from ..schema import Layer, Memory, MemoryType
from ..storage.embeddings import EmbeddingProvider
from ..storage.store import SQLiteStore


class ProceduralMemory:
    def __init__(self, store: SQLiteStore, embedder: EmbeddingProvider,
                 workspace: str = "default"):
        self.store = store
        self.embedder = embedder
        self.workspace = workspace

    def put_playbook(self, name: str, steps: list[str], *,
                     preconditions: list[str] | None = None,
                     expected_outcome: str = "",
                     tags: list[str] | None = None) -> Memory:
        body = {
            "name": name,
            "preconditions": preconditions or [],
            "steps": steps,
            "expected_outcome": expected_outcome,
        }
        content = name + "\n" + "\n".join(f"{i+1}. {s}" for i, s in enumerate(steps))
        mem = Memory(
            layer=Layer.PROCEDURAL,
            type=MemoryType.PLAYBOOK,
            content=content,
            summary=name,
            tags=(tags or []) + ["playbook"],
            metadata=body,
            source="proceduralize",
            workspace=self.workspace,
        )
        self.store.put_memory(mem, vector=self.embedder.embed(content))
        return mem

    def put_snippet(self, title: str, code: str, *, language: str = "python",
                    tags: list[str] | None = None) -> Memory:
        mem = Memory(
            layer=Layer.PROCEDURAL,
            type=MemoryType.SNIPPET,
            content=f"{title}\n```{language}\n{code}\n```",
            summary=title,
            tags=(tags or []) + ["snippet", language],
            metadata={"language": language, "code": code, "title": title},
            workspace=self.workspace,
        )
        self.store.put_memory(mem, vector=self.embedder.embed(mem.content))
        return mem
