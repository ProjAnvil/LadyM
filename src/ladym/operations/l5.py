"""L5 mental-model extraction (SPEC §2.8).

Incremental pass: cluster *uncovered* L2/L3 memories by embedding similarity, summarise each
cluster into one abstract mental model via the ``l5_mental_model`` agent, and link the members
with ``relation="abstracts"`` edges. A memory is "covered" once some L5 points at it, so a
re-run with no new material does no LLM work (cost-control invariant).

The periodic merge pass is added in Task 3.
"""
from __future__ import annotations

import time
from dataclasses import dataclass, field

from pydantic import BaseModel

from ..config import Config
from ..schema import Edge, Layer, Memory, MemoryType
from ..storage.embeddings import EmbeddingProvider
from ..storage.store import SQLiteStore
from .supersedes import is_retired

_L5_LAYER = Layer.L5_MENTAL.value
_ABSTRACTS = "abstracts"
# SPEC §2.8: abstract "L2 facts + L3 playbooks". Code symbols are too granular.
_ABSTRACTABLE = {
    (Layer.SEMANTIC.value, MemoryType.FACT.value),
    (Layer.SEMANTIC.value, MemoryType.NOTE.value),
    (Layer.PROCEDURAL.value, MemoryType.PLAYBOOK.value),
    (Layer.PROCEDURAL.value, MemoryType.SNIPPET.value),
}


class _MentalModel(BaseModel):
    """Structured output schema for the l5_mental_model agent."""

    title: str
    model: str


@dataclass
class L5ExtractionReport:
    new_models: int = 0
    merged_models: int = 0
    clusters: list[dict] = field(default_factory=list)
    skipped: bool = False


def _default_prompt() -> str:
    from importlib.resources import files

    return (files("ladym.prompts") / "l5.txt").read_text()


def _covered_member_ids(store: SQLiteStore, workspace: str) -> set[str]:
    """Ids of memories already abstracted by some non-retired L5 (via open ``abstracts`` edges).

    ``store.neighbors`` already filters ``valid_to IS NULL``, so edges closed by a merge
    retirement do not leak through here.
    """
    covered: set[str] = set()
    for l5 in store.iter_memories(workspace=workspace, layer=_L5_LAYER):
        if is_retired(l5):
            continue
        for e in store.neighbors(l5.id, relation=_ABSTRACTS):
            if e.src_id == l5.id:
                covered.add(e.dst_id)
    return covered


def _connected_components(
    ids: list[str], vecs: list[list[float]], threshold: float
) -> list[list[str]]:
    """Group ``ids`` by cosine similarity >= ``threshold`` (numpy Gram + pure-Python union-find)."""
    if not ids:
        return []
    import numpy as np

    mat = np.asarray(vecs, dtype=np.float64)
    norms = np.linalg.norm(mat, axis=1, keepdims=True)
    safe = np.where(norms == 0, 1.0, norms)  # avoid divide-by-zero on null vectors
    unit = mat / safe
    sim = unit @ unit.T  # cosine Gram matrix
    parent = list(range(len(ids)))

    def find(i: int) -> int:
        while parent[i] != i:
            parent[i] = parent[parent[i]]
            i = parent[i]
        return i

    def union(a: int, b: int) -> None:
        ra, rb = find(a), find(b)
        if ra != rb:
            parent[ra] = rb

    n = len(ids)
    for i in range(n):
        for j in range(i + 1, n):
            if sim[i, j] >= threshold:
                union(i, j)
    groups: dict[int, list[str]] = {}
    for i in range(n):
        groups.setdefault(find(i), []).append(ids[i])
    return list(groups.values())


def _summarise(llm, prompt: str, members: list[Memory]) -> dict | None:
    corpus = "\n".join(f"- ({m.type}) {m.content}" for m in members)
    msgs = [
        {"role": "system", "content": prompt},
        {"role": "user", "content": f"Abstract these memories into one mental model:\n{corpus}"},
    ]
    out = llm.complete_structured(msgs, _MentalModel)
    return out if isinstance(out, dict) else out.model_dump()


def _store_model(store, embedder, *, title, body, workspace, source, extra_meta) -> Memory:
    content = f"{title}: {body}" if body else title
    mem = Memory(
        layer=Layer.L5_MENTAL,
        type=MemoryType.MENTAL_MODEL,
        content=content,
        summary=title,
        tags=["mental_model"],
        metadata=extra_meta,
        source=source,
        workspace=workspace,
    )
    store.put_memory(mem, vector=embedder.embed(content))
    return mem


def extract(
    store: SQLiteStore,
    embedder: EmbeddingProvider,
    *,
    cfg: Config,
    workspace: str | None = None,
    llm=None,
    prompt: str | None = None,
) -> L5ExtractionReport:
    ws = workspace or cfg.workspace
    report = L5ExtractionReport()
    if llm is None:
        report.skipped = True
        return report
    prompt = prompt or _default_prompt()

    # candidates: uncovered abstractable L2/L3 memories
    covered = _covered_member_ids(store, ws)
    candidates = [
        m
        for m in store.iter_memories(workspace=ws)
        if m.id not in covered and (m.layer, m.type) in _ABSTRACTABLE
    ]

    by_id = {m.id: m for m in candidates}
    vecs = embedder.embed_batch([m.content for m in candidates]) if candidates else []
    components = _connected_components(
        [m.id for m in candidates], vecs, cfg.system2.l5_cluster_similarity
    )

    now = time.time()
    for comp in components:
        if len(comp) < cfg.system2.l5_min_cluster_size:
            continue
        members = [by_id[mid] for mid in comp]
        result = _summarise(llm, prompt, members)
        if not result:
            continue
        model_mem = _store_model(
            store, embedder,
            title=result.get("title", "mental model"),
            body=result.get("model", ""),
            workspace=ws, source="l5_extract",
            extra_meta={"n_members": len(members)},
        )
        for m in members:
            store.put_edge(
                Edge(src_id=model_mem.id, relation=_ABSTRACTS, dst_id=m.id, valid_from=now)
            )
        report.new_models += 1
        report.clusters.append(
            {"model_id": model_mem.id, "n_members": len(members), "action": "new"}
        )

    return report
