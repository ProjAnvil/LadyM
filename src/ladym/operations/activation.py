"""ACT-R-inspired activation function (ARCHITECTURE.md §4).

Activation drives recall ranking. Each term corresponds to a well-studied cognitive effect:

* ``similarity``    — how semantically close the item is to the query
* ``recency``       — base-level learning: recently accessed items are easier to recall
* ``frequency``     — base-level learning: frequently accessed items are easier to recall
* ``graph``         — associative spreading activation (L4 neighbours boost the item)
* ``type_boost``    — a query-type prior (e.g. code queries boost code_symbol items)
"""

from __future__ import annotations

import math
import time
from collections.abc import Iterable

from ..config import ActivationWeights
from ..schema import Memory, MemoryType


def recency_factor(last_access_at: float, *, half_life_s: float, now: float | None = None) -> float:
    """Exponential decay: 1.0 right after access, 0.5 after ``half_life_s`` seconds."""
    now = now if now is not None else time.time()
    age = max(0.0, now - last_access_at)
    return math.pow(0.5, age / half_life_s)


def frequency_factor(access_count: int) -> float:
    """Diminishing-returns log curve, ACT-R base-level form ``log(1 + n)``."""
    return math.log(1.0 + max(0, access_count))


def type_boost_for_query(
    mem: Memory, *, query_types: Iterable[MemoryType] | None, weight: float
) -> float:
    """Boost items whose type matches a query-type prior."""
    if not query_types:
        return 0.0
    target = {t.value for t in query_types}
    return weight if mem.type in target else 0.0


def graph_factor(mem: Memory, neighbour_counts: dict[str, int], weight: float) -> float:
    """Spreading activation: items with more (current) graph neighbours get a small boost."""
    n = neighbour_counts.get(mem.id, 0)
    return weight * math.log(1.0 + n)


def activation_score(
    mem: Memory,
    *,
    query_similarity: float,
    weights: ActivationWeights,
    neighbour_counts: dict[str, int] | None = None,
    query_types: Iterable[MemoryType] | None = None,
    now: float | None = None,
) -> float:
    """Compute the full activation score for a memory given a query context."""
    return (
        weights.similarity * max(0.0, query_similarity)
        + weights.recency * recency_factor(mem.last_access_at, half_life_s=weights.recency_half_life_s, now=now)
        + weights.frequency * frequency_factor(mem.access_count)
        + weights.graph * graph_factor(mem, neighbour_counts or {}, 1.0)
        + type_boost_for_query(mem, query_types=query_types, weight=weights.type_boost)
    )


def infer_query_types(query: str) -> list[MemoryType]:
    """Heuristic: detect whether a query is asking about code, in which case boost
    code_symbol/code_file items. Pluggable — replaced by an LLM classifier when one is wired."""
    q = query.lower()
    code_signals = (
        "function", "class", "method", "def ", "import", "module", "api", "endpoint",
        "variable", "implement", "where is", "where defined", "signature", "call",
    )
    if any(s in q for s in code_signals):
        return [MemoryType.CODE_SYMBOL, MemoryType.CODE_FILE]
    if any(s in q for s in ("how to", "how do i", "steps", "procedure", "playbook")):
        return [MemoryType.PLAYBOOK]
    return []


def neighbour_counts_for(mems: Iterable[Memory], counts: dict[str, int]) -> dict[str, int]:
    """Project a ``{memory_id: neighbour_count}`` map onto just the given memories."""
    ids = {m.id for m in mems}
    return {mid: counts.get(mid, 0) for mid in ids}


__all__ = [
    "activation_score",
    "frequency_factor",
    "graph_factor",
    "infer_query_types",
    "neighbour_counts_for",
    "recency_factor",
    "type_boost_for_query",
]
