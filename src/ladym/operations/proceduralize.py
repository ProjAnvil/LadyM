"""L1 → L3 proceduralization (ARCHITECTURE.md §2 ``proceduralize``).

Clusters successful episodic events by similarity and, when a cluster crosses a support
threshold, writes an L3 playbook that captures the recurring procedure. CoALA's skill
library; Voyager's auto-skill authoring.
"""

from __future__ import annotations

from dataclasses import dataclass

from ..config import Config
from ..layers.procedural import ProceduralMemory
from ..schema import Layer
from ..storage.embeddings import EmbeddingProvider
from ..storage.store import SQLiteStore


@dataclass
class ProceduralizeReport:
    clusters_examined: int = 0
    playbooks_created: int = 0
    details: list[dict] = None  # type: ignore[assignment]

    def __post_init__(self):
        if self.details is None:
            self.details = []


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
            # derive a playbook name from the most common action verb
            actions = [c.metadata.get("action", "do") for c in cluster]
            from collections import Counter
            top_action = Counter(actions).most_common(1)[0][0]
            steps = _derive_steps(cluster)
            proc.put_playbook(
                name=f"How to {top_action} ({len(cluster)} episodes)",
                steps=steps,
                preconditions=list({c.metadata.get("agent", "agent") for c in cluster}),
                expected_outcome="success",
                tags=[top_action],
            )
            report.playbooks_created += 1
            report.details.append({"action": top_action, "size": len(cluster)})
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
