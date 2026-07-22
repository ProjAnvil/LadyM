"""L1 → L3 proceduralization (ARCHITECTURE.md §2 ``proceduralize``).

Clusters successful episodic events by similarity and, when a cluster crosses a support
threshold, writes an L3 playbook that captures the recurring procedure. CoALA's skill
library; Voyager's auto-skill authoring.
"""

from __future__ import annotations

from collections import Counter
from dataclasses import dataclass, field

from ..config import Config
from ..layers.procedural import ProceduralMemory, _playbook_content
from ..layers.semantic import content_hash
from ..schema import Layer, Memory, MemoryType
from ..storage.embeddings import EmbeddingProvider
from ..storage.store import SQLiteStore
from .consolidate import Action
from .supersedes import retire as _retire


@dataclass
class ProceduralizeReport:
    clusters_examined: int = 0
    playbooks_created: int = 0  # = ADD count (kept for backward compat)
    actions: dict[str, int] = field(
        default_factory=lambda: {"ADD": 0, "UPDATE": 0, "NOOP": 0}
    )
    details: list[dict] = None  # type: ignore[assignment]

    def __post_init__(self):
        if self.details is None:
            self.details = []


def _retrieve_existing_playbooks(
    store: SQLiteStore, candidate_vec: list[float], ws: str, top_k: int
) -> list[tuple[Memory, float]]:
    """Top similar existing L3 playbooks in this workspace (mirrors consolidate's L2 retrieval)."""
    raw = store.vector_index.search(candidate_vec, top_k=top_k)
    similar: list[tuple[Memory, float]] = []
    for mid, sim in raw:
        if sim < 0.1:
            continue
        m = store.get_memory(mid)
        if m is None or m.workspace != ws:
            continue
        if m.layer != Layer.PROCEDURAL.value or m.type != MemoryType.PLAYBOOK.value:
            continue
        similar.append((m, sim))
    similar.sort(key=lambda t: t[1], reverse=True)
    return similar


def _classify_playbook(
    candidate_hash: str,
    similar: list[tuple[Memory, float]],
    threshold: float,
) -> Action:
    """ADD/UPDATE/NOOP for a candidate playbook vs existing L3."""
    for existing, _sim in similar:
        if existing.content_hash and existing.content_hash == candidate_hash:
            return Action.NOOP
    if similar and similar[0][1] >= threshold:
        return Action.UPDATE
    return Action.ADD


def proceduralize(
    store: SQLiteStore,
    embedder: EmbeddingProvider,
    *,
    cfg: Config,
    workspace: str | None = None,
    min_cluster_size: int = 3,
    similarity_threshold: float = 0.55,
) -> ProceduralizeReport:
    ws = workspace or cfg.workspace
    proc = ProceduralMemory(store, embedder, workspace=ws)
    report = ProceduralizeReport()

    rows = store.conn.execute(
        "SELECT * FROM memories WHERE layer = ? AND workspace = ? ORDER BY created_at",
        (Layer.EPISODIC.value, ws),
    ).fetchall()
    episodes = [store._row_to_memory(r) for r in rows]
    # only successful episodes ("outcome=success") carry procedural signal
    succ = [e for e in episodes if e.metadata.get("outcome") in ("success", "ok", "done")]
    if len(succ) < min_cluster_size:
        return report

    vecs = embedder.embed_batch([e.content for e in succ])
    assigned: list[bool] = [False] * len(succ)

    for i, anchor in enumerate(succ):
        if assigned[i]:
            continue
        cluster = [anchor]
        for j in range(i + 1, len(succ)):
            if assigned[j]:
                continue
            from ..storage.embeddings import cosine_similarity
            if cosine_similarity(vecs[i], vecs[j]) >= similarity_threshold:
                cluster.append(succ[j])
                assigned[j] = True
        if len(cluster) >= min_cluster_size:
            assigned[i] = True
            report.clusters_examined += 1
            actions = [c.metadata.get("action", "do") for c in cluster]
            top_action = Counter(actions).most_common(1)[0][0]
            steps = _derive_steps(cluster)
            name = f"How to {top_action} ({len(cluster)} episodes)"
            # idempotency: check existing L3 playbooks before writing
            candidate_content = _playbook_content(name, steps)
            candidate_hash = content_hash(candidate_content)
            candidate_vec = embedder.embed(candidate_content)
            similar = _retrieve_existing_playbooks(
                store, candidate_vec, ws,
                top_k=cfg.consolidate.min_episodes_to_trigger + 5,
            )
            action = _classify_playbook(candidate_hash, similar, similarity_threshold)
            report.actions[action.value] += 1
            if action == Action.ADD:
                proc.put_playbook(
                    name=name, steps=steps,
                    preconditions=list({c.metadata.get("agent", "agent") for c in cluster}),
                    expected_outcome="success",
                    tags=[top_action],
                )
                report.playbooks_created += 1
            elif action == Action.UPDATE and similar:
                new_mem = proc.put_playbook(
                    name=name, steps=steps,
                    preconditions=list({c.metadata.get("agent", "agent") for c in cluster}),
                    expected_outcome="success",
                    tags=[top_action],
                )
                _retire(store, similar[0][0], new_id=new_mem.id)
            # NOOP: skip
            report.details.append({"action": action.value, "action_verb": top_action, "size": len(cluster)})
    return report


def _derive_steps(cluster: list) -> list[str]:
    """Heuristic: turn the distinct (action, observation) pairs of a cluster into ordered steps."""
    seen: set[str] = set()
    steps: list[str] = []
    for c in cluster:
        key = (c.metadata.get("action", ""), c.metadata.get("observation", ""))
        s = f"{key[0]}" + (f" — {key[1]}" if key[1] else "")
        if s and s not in seen:
            seen.add(s)
            steps.append(s)
    return steps
