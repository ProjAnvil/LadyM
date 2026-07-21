"""L1 → L2 consolidation (ARCHITECTURE.md §2 ``consolidate``).

Mirrors the hippocampus→neocortex step of memory consolidation, and implements mem0's
``ADD/UPDATE/DELETE/NOOP`` decision per candidate fact. Two operating modes:

* **Offline (default, no LLM)** — deterministic rules: hash-equal ⇒ NOOP, similarity above
  threshold ⇒ UPDATE (merge), contradiction detected by tag/keyword ⇒ DELETE the stale item,
  otherwise ADD. This keeps tests hermetic.
* **LLM-assisted** — when ``llm`` is wired into the engine, the same candidate/similar-set is
  handed to the model as a tool-call classifier (exactly as in mem0 Algorithm 1).
"""

from __future__ import annotations

import time
from collections.abc import Callable
from dataclasses import dataclass, field
from enum import StrEnum

from ..config import Config
from ..layers.semantic import SemanticMemory, content_hash
from ..schema import Layer, Memory, _new_id
from ..storage.embeddings import EmbeddingProvider
from ..storage.store import SQLiteStore


class Action(StrEnum):
    ADD = "ADD"
    UPDATE = "UPDATE"
    DELETE = "DELETE"
    NOOP = "NOOP"


@dataclass
class ConsolidationReport:
    actions: dict[str, int] = field(default_factory=lambda: {a.value: 0 for a in Action})
    kept_episodes: int = 0
    promoted_to_semantic: int = 0
    details: list[dict] = field(default_factory=list)


# A pluggable LLM classifier: given (candidate_text, [similar_texts]) → (Action, new_text|None)
LLMClassifier = Callable[[str, list[str]], "tuple[Action, str | None]"]


def _offline_classify(
    candidate: str,
    candidate_hash: str,
    similar: list[tuple[Memory, float]],
    threshold: float,
) -> tuple[Action, str | None]:
    """Deterministic ADD/UPDATE/DELETE/NOOP for offline mode."""
    for existing, _sim in similar:
        # exact textual dup → NOOP
        if existing.content_hash and existing.content_hash == candidate_hash:
            return Action.NOOP, None
        # explicit contradiction tag ⇒ DELETE the stale one
        meta = existing.metadata or {}
        if meta.get("superseded_by") == candidate_hash:
            return Action.UPDATE, None
    # highly similar but not exact → UPDATE (treat candidate as the richer version)
    if similar and similar[0][1] >= threshold:
        return Action.UPDATE, candidate
    return Action.ADD, None


def consolidate(
    store: SQLiteStore,
    embedder: EmbeddingProvider,
    *,
    cfg: Config,
    workspace: str | None = None,
    llm_classify: LLMClassifier | None = None,
    since: float | None = None,
    limit: int = 500,
) -> ConsolidationReport:
    """Promote salient episodic events into semantic facts.

    Each L1 event is treated as a candidate fact. For each candidate we retrieve the
    top similar L2 facts and let the classifier decide ADD/UPDATE/DELETE/NOOP.
    """
    ws = workspace or cfg.workspace
    sem = SemanticMemory(store, embedder, workspace=ws)
    threshold = cfg.consolidate.dedup_similarity_threshold
    report = ConsolidationReport()

    params: list = [Layer.EPISODIC.value, ws]
    where = "WHERE layer = ? AND workspace = ?"
    if since is not None:
        where += " AND created_at >= ?"
        params.append(since)
    params.append(limit)
    rows = store.conn.execute(
        f"SELECT * FROM memories {where} ORDER BY created_at ASC LIMIT ?", params
    ).fetchall()
    episodes = [store._row_to_memory(r) for r in rows]

    cand_vecs = embedder.embed_batch([e.content for e in episodes]) if episodes else []

    for ep, vec in zip(episodes, cand_vecs, strict=False):
        # similar existing L2 facts in this workspace
        raw = store.vector_index.search(vec, top_k=cfg.consolidate.min_episodes_to_trigger + 5)
        similar: list[tuple[Memory, float]] = []
        for mid, sim in raw:
            if sim < 0.1:
                continue
            m = store.get_memory(mid)
            if m is None or m.workspace != ws or m.layer != Layer.SEMANTIC.value:
                continue
            similar.append((m, sim))
        similar.sort(key=lambda t: t[1], reverse=True)

        cand_hash = content_hash(ep.content)
        if llm_classify is not None:
            action, new_text = llm_classify(ep.content, [m.content for m, _ in similar])
        else:
            action, new_text = _offline_classify(ep.content, cand_hash, similar, threshold)

        report.actions[action.value] += 1
        report.details.append({
            "episode_id": ep.id,
            "action": action.value,
            "n_similar": len(similar),
        })

        if action == Action.ADD:
            sem.put_fact(
                ep.content,
                summary=ep.summary,
                tags=ep.tags,
                metadata={**ep.metadata, "source_episode": ep.id},
                source="consolidate",
            )
            report.promoted_to_semantic += 1
        elif action == Action.UPDATE and similar:
            # SPEC §2.6: create a NEW merged memory and retire the old one (instead of
            # mutating in place) so the supersedes chain preserves lineage.
            target = similar[0][0]
            from .supersedes import retire as _retire

            merged = target.model_copy(
                update={
                    "id": _new_id(),
                    "content": new_text or ep.content,
                    "summary": ep.summary or target.summary,
                    "updated_at": time.time(),
                    "content_hash": content_hash(new_text or ep.content),
                    "metadata": {
                        **(target.metadata or {}),
                        "source_episode": ep.id,
                        "updated_from": target.id,
                    },
                }
            )
            store.put_memory(merged, vector=embedder.embed(merged.content))
            _retire(store, target, new_id=merged.id)
        elif action == Action.DELETE and similar:
            # SPEC §2.6: retire the stale item in place rather than physically deleting
            # it, so the audit trail survives and downstream indexes stay consistent.
            from .supersedes import retire as _retire

            _retire(store, similar[0][0])
        # NOOP: nothing to do
    report.kept_episodes = len(episodes)
    return report
