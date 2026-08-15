"""Runtime seam — per-instance engines backed by the Go ladyM binary.

The original harness (``benchmarks/longmemeval`` on the Python ``main`` branch)
constructed a Python ``Engine`` per LongMemEval instance. Here each per-instance
engine is a :class:`GoEngine`: a thin duck-type adapter over
:class:`ladym_wrapper.LadymClient`, spawned against a per-instance db +
workspace so every instance stays isolated. ``make_engine`` is the single
injection point used by ``ingest`` / ``run_retrieval`` / ``run_qa`` — swap it
(via the ``engine_factory=`` parameters) to test the harness hermetically.

Known capability gap (Go MCP server, ``mcp/server.go``):
  * ``record_event`` accepts **no metadata** argument, and
  * ``recall`` results omit ``memory.metadata``
    (``recallResultsJSON`` serializes id/layer/type/summary/content/source/tags
    only),

even though the Go engine stores metadata internally
(``engine.Engine.RecordEvent(..., metadata map[string]any)``). The harness needs
``session_id`` / ``date`` / ``doc_id`` per ingested turn for the retrieval
metrics, so :class:`GoEngine` keeps an append-only **sidecar** JSONL file next
to the db (``<db>.meta.jsonl``) mapping ``memory_id -> metadata``: the id
returned by ``record_event`` is recorded at ingest time, and ``recall``
re-attaches metadata by memory id. Memories created server-side without going
through this client (e.g. L2 facts produced by ``consolidate``) have no sidecar
entry and surface with empty metadata — matching the upstream harness's
documented consolidated-variant caveat (consolidated facts carry no
doc_id/session_id).
"""
from __future__ import annotations

import json
from pathlib import Path
from types import SimpleNamespace

from ladym_wrapper import LadymClient, LadymError

__all__ = ["GoEngine", "LadymError", "make_engine", "sidecar_path_for"]


def sidecar_path_for(db_path) -> Path:
    """Sidecar metadata file for a per-instance db (``x.db`` -> ``x.meta.jsonl``)."""
    return Path(db_path).with_suffix(".meta.jsonl")


class _Memory:
    """Duck-type of the Python harness's ``ladym.schema.Memory``."""

    def __init__(self, content: str, metadata: dict):
        self.content = content
        self.metadata = metadata


class _RecallResult:
    """Duck-type of ``ladym.schema.RecallResult``: exposes .memory + .score."""

    def __init__(self, memory: _Memory, score: float):
        self.memory = memory
        self.score = score


class _RecallResponse:
    def __init__(self, results: list[_RecallResult]):
        self.results = results


class GoEngine:
    """Per-instance engine backed by ``ladym serve`` (MCP stdio).

    Duck-types the Python ``Engine`` surface the harness uses:
    ``record_event`` / ``recall`` / ``consolidate`` / ``stats`` / ``close``.
    """

    def __init__(self, db_path, workspace: str, *, binary=None):
        self._db_path = Path(db_path)
        self._workspace = workspace
        self._sidecar_path = sidecar_path_for(db_path)
        # A missing db means ingest unlinked it for a rebuild — any stale
        # sidecar from the previous build must be discarded so old memory ids
        # can't alias onto the fresh db. Checked BEFORE spawning the client,
        # because `ladym serve` creates the db file on startup.
        db_existed = self._db_path.exists()
        self._sidecar: dict[str, dict] = {}
        if db_existed and self._sidecar_path.exists():
            with self._sidecar_path.open() as f:
                for line in f:
                    line = line.strip()
                    if line:
                        entry = json.loads(line)
                        self._sidecar[entry["id"]] = entry["metadata"]
        self._sidecar_fh = self._sidecar_path.open("w")
        for mid, meta in self._sidecar.items():
            self._sidecar_fh.write(json.dumps({"id": mid, "metadata": meta}) + "\n")
        self._sidecar_fh.flush()
        try:
            self._client = LadymClient(
                binary=binary, db=self._db_path, workspace=workspace
            )
        except Exception:
            self._sidecar_fh.close()
            raise

    # -- engine surface used by the harness --

    def record_event(self, *, agent, action, observation="", metadata=None, **kw):
        res = self._client.record_event(
            agent=agent, action=action, observation=observation or None
        )
        mid = res.get("id") if isinstance(res, dict) else None
        if mid and metadata:
            self._sidecar[mid] = dict(metadata)
            self._sidecar_fh.write(json.dumps({"id": mid, "metadata": dict(metadata)}) + "\n")
            self._sidecar_fh.flush()
        return res

    def recall(self, query, top_k: int = 8, **kw) -> _RecallResponse:
        resp = self._client.recall(query, top_k=top_k)
        results = []
        for r in (resp or {}).get("results", []):
            mem = r.get("memory", {})
            meta = self._sidecar.get(mem.get("id"), {})
            results.append(
                _RecallResult(_Memory(mem.get("content", ""), meta), r.get("score", 0.0))
            )
        return _RecallResponse(results)

    def consolidate(self, **kw):
        return self._client.consolidate()

    def stats(self):
        st = self._client.stats() or {}
        return SimpleNamespace(total_memories=st.get("total_memories", 0))

    def close(self):
        try:
            self._client.close()
        finally:
            self._sidecar_fh.close()


def make_engine(db_path, workspace, *, binary=None) -> GoEngine:
    """Build a per-instance engine on the Go binary (one ``ladym serve`` per db)."""
    return GoEngine(db_path, workspace, binary=binary)
