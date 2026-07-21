"""Two-tier retrieval with reflection gate (ARCHITECTURE.md §3).

Implements HyMem's cognitive-economy principle: a cheap tier-1 vector+activation pass answers
~70% of queries; only the ones that fail a cheap reflection check escalate to the expensive
tier-2 graph expansion + source backtracking.
"""

from __future__ import annotations

import time
from dataclasses import dataclass

from ..config import Config, RecallConfig
from ..schema import Layer, Memory, MemoryType, RecallResponse, RecallResult
from ..storage.embeddings import EmbeddingProvider, tokenize
from ..storage.store import SQLiteStore
from .activation import (
    activation_score,
    infer_query_types,
    neighbour_counts_for,
)


@dataclass
class _ReflectionVerdict:
    sufficient: bool
    coverage: float
    n_hits: int


def _reflect(query: str, hits: list[RecallResult], cfg: RecallConfig) -> _ReflectionVerdict:
    """Cheap self-check: did tier-1 surface enough high-signal context?

    Default heuristic: count distinct query tokens covered by the union of hit contents, and
    the number of hits above a small similarity floor. Pluggable — ``Engine`` can substitute
    an LLM judge when one is configured.
    """
    q_tokens = set(tokenize(query)) - {"the", "a", "an", "is", "are", "to", "of", "and", "or"}
    if not q_tokens:
        return _ReflectionVerdict(sufficient=True, coverage=1.0, n_hits=len(hits))
    corpus = " ".join(r.memory.content + " " + r.memory.summary for r in hits).lower()
    covered = sum(1 for t in q_tokens if t in corpus)
    coverage = covered / len(q_tokens)
    sufficient = len(hits) >= cfg.reflection_min_hits and coverage >= cfg.reflection_min_coverage
    return _ReflectionVerdict(sufficient=sufficient, coverage=coverage, n_hits=len(hits))


def _rank(
    *,
    query_vec: list[float],
    candidates: list[tuple[Memory, float]],
    cfg: Config,
    neighbour_counts: dict[str, int],
    query_types: list[MemoryType],
) -> list[RecallResult]:
    """Apply the ACT-R activation function to candidates and return ranked results."""
    scored: list[tuple[float, Memory, float]] = []
    for mem, sim in candidates:
        act = activation_score(
            mem,
            query_similarity=sim,
            weights=cfg.activation,
            neighbour_counts=neighbour_counts_for([mem], neighbour_counts),
            query_types=query_types,
        )
        scored.append((act, mem, sim))
    scored.sort(key=lambda t: t[0], reverse=True)
    return [RecallResult(memory=m, score=act, tier=1, via=[]) for act, m, sim in scored]


def recall(
    store: SQLiteStore,
    embedder: EmbeddingProvider,
    query: str,
    *,
    cfg: Config,
    workspace: str | None = None,
    top_k: int | None = None,
    layers: list[Layer] | None = None,
    types: list[MemoryType] | None = None,
    min_similarity: float = 0.0,
) -> RecallResponse:
    """The full two-tier retrieval pipeline."""
    start = time.time()
    ws = workspace or cfg.workspace
    rcfg = cfg.recall
    k1 = top_k or rcfg.top_k_tier1
    query_vec = embedder.embed(query)
    query_types = types or infer_query_types(query)

    # ---- Tier 1: vector search + activation rerank ----
    raw_hits = store.vector_index.search(query_vec, top_k=max(k1 * 3, k1))
    cand: list[tuple[Memory, float]] = []
    for mem_id, sim in raw_hits:
        if sim < min_similarity:
            continue
        mem = store.get_memory(mem_id)
        if mem is None or mem.workspace != ws:
            continue
        if layers is not None and Layer(mem.layer) not in layers:
            continue
        if types is not None and MemoryType(mem.type) not in types:
            continue
        cand.append((mem, sim))

    neighbour_counts = store.associative_neighbour_counts() if hasattr(store, "associative_neighbour_counts") else {}

    tier1 = _rank(
        query_vec=query_vec,
        candidates=cand,
        cfg=cfg,
        neighbour_counts=neighbour_counts,
        query_types=query_types,
    )[:k1]

    verdict = _reflect(query, tier1, rcfg)
    if verdict.sufficient or not rcfg.enable_tier2:
        _commit_access(store, [r.memory.id for r in tier1])
        return RecallResponse(
            query=query,
            results=tier1,
            tier_reached=1,
            reflected_sufficient=verdict.sufficient,
            elapsed_ms=(time.time() - start) * 1000.0,
        )

    # ---- Tier 2: graph expansion + source backtracking ----
    expanded = _tier2_expand(store, tier1, cfg, ws)
    # merge: tier-2 results inherit tier=2; original tier1 items keep tier=1
    by_id: dict[str, RecallResult] = {r.memory.id: r for r in tier1}
    for mem, sim, via in expanded:
        if mem.id in by_id:
            continue
        if layers is not None and Layer(mem.layer) not in layers:
            continue
        if types is not None and MemoryType(mem.type) not in types:
            continue
        act = activation_score(
            mem,
            query_similarity=sim,
            weights=cfg.activation,
            neighbour_counts=neighbour_counts_for([mem], neighbour_counts),
            query_types=query_types,
        )
        by_id[mem.id] = RecallResult(memory=mem, score=act, tier=2, via=via)
    merged = sorted(by_id.values(), key=lambda r: r.score, reverse=True)[: (top_k or rcfg.top_k_tier2)]

    _commit_access(store, [r.memory.id for r in merged])
    return RecallResponse(
        query=query,
        results=merged,
        tier_reached=2,
        reflected_sufficient=True,
        elapsed_ms=(time.time() - start) * 1000.0,
    )


def _tier2_expand(
    store: SQLiteStore,
    tier1: list[RecallResult],
    cfg: Config,
    workspace: str,
) -> list[tuple[Memory, float, list[str]]]:
    """Walk the associative graph from tier-1 anchors, plus backtrack to source context."""
    out: list[tuple[Memory, float, list[str]]] = []
    seen: set[str] = {r.memory.id for r in tier1}
    qvec_cache: dict[str, list[float]] = {}

    for anchor in tier1:
        frontier = [(anchor.memory.id, 1, [anchor.memory.id])]
        while frontier:
            cur_id, depth, path = frontier.pop(0)
            if depth > cfg.recall.graph_hops:
                continue
            for edge in store.neighbors(cur_id):
                other_id = edge.dst_id if edge.src_id == cur_id else edge.src_id
                if other_id in seen:
                    continue
                seen.add(other_id)
                other = store.get_memory(other_id)
                if other is None or other.workspace != workspace:
                    continue
                # similarity to query, lazily embedding
                if other_id not in qvec_cache:
                    qvec_cache[other_id] = [0.0]  # placeholder; we don't have query vec here
                out.append((other, max(0.05, anchor.score * 0.5 / depth), path + [other_id]))
                frontier.append((other_id, depth + 1, path + [other_id]))

    # backtrack: for code symbols in tier1/expanded, pull their file memory too
    for mem_id in list(seen):
        mem = store.get_memory(mem_id)
        if mem and mem.type == MemoryType.CODE_SYMBOL.value:
            file_meta = mem.metadata.get("file_path")
            if not file_meta:
                continue
            for m in store.iter_memories(workspace=workspace, type_=MemoryType.CODE_FILE.value):
                if m.metadata.get("file_path") == file_meta and m.id not in seen:
                    seen.add(m.id)
                    out.append((m, 0.1, [mem_id, m.id]))
    return out


def _commit_access(store: SQLiteStore, ids: list[str]) -> None:
    """Side-effect: bump access_count / last_access_at on recalled items (drives decay)."""
    now = time.time()
    for mid in ids:
        store.touch_memory(mid, now=now)
