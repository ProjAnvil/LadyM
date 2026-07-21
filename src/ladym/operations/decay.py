"""ACT-R base-level decay / forgetting (ARCHITECTURE.md §2 ``forget``).

Items that have not been accessed recently and have low activation are candidates for
forgetting. We never physically delete code analysis (it's derived from source), only
episodic events whose activation has fallen below a floor for a sustained period.
"""

from __future__ import annotations

import time
from dataclasses import dataclass

from ..config import ActivationWeights
from ..operations.activation import recency_factor
from ..schema import Layer
from ..storage.store import SQLiteStore


@dataclass
class DecayReport:
    examined: int = 0
    forgotten: int = 0
    forgotten_ids: list[str] = None  # type: ignore[assignment]

    def __post_init__(self):
        if self.forgotten_ids is None:
            self.forgotten_ids = []


def decay(
    store: SQLiteStore,
    *,
    workspace: str | None = None,
    weights: ActivationWeights | None = None,
    max_age_s: float = 30 * 24 * 3600.0,
    activation_floor: float = 0.05,
    now: float | None = None,
    dry_run: bool = False,
) -> DecayReport:
    """Forget episodic events that have decayed below ``activation_floor``.

    Code analysis (L2 code_symbol/code_file), procedural playbooks, and associative edges are
    never auto-forgotten — they represent curated knowledge, not raw experience.
    """
    weights = weights or ActivationWeights()
    now = now if now is not None else time.time()
    report = DecayReport()
    ws_clause = ""
    params: list = [Layer.EPISODIC.value]
    if workspace is not None:
        ws_clause = " AND workspace = ?"
        params.append(workspace)
    rows = store.conn.execute(
        f"SELECT * FROM memories WHERE layer = ?{ws_clause}", params
    ).fetchall()
    for r in rows:
        report.examined += 1
        mem = store._row_to_memory(r)
        age = now - mem.last_access_at
        if age < max_age_s:
            continue
        # base-level activation = recency × weight + frequency × weight
        act = (
            weights.recency * recency_factor(mem.last_access_at, half_life_s=weights.recency_half_life_s, now=now)
            + weights.frequency * 0.0  # frequency log(1+n) folded in via activation_score elsewhere
        )
        if act < activation_floor:
            report.forgotten += 1
            report.forgotten_ids.append(mem.id)
            if not dry_run:
                store.delete_memory(mem.id)
    return report
