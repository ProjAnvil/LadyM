"""Retrieval metrics matching the LongMemEval upstream schema.

Formulas attributed to vendored ``upstream_eval/eval_utils.py`` (dcg/ndcg/recall_any/recall_all).
Adapted to a doc-id-ranked interface (upstream uses integer indices into a corpus).
"""
from __future__ import annotations
import math


def recall_all(recalled_ids: list[str], gold: set[str], k: int) -> float:
    """1.0 iff every gold id appears in the top-k recalled, else 0.0."""
    if not gold:
        return 0.0
    topk = set(recalled_ids[:k])
    return 1.0 if all(g in topk for g in gold) else 0.0


def _dcg(relevances: list[float]) -> float:
    if not relevances:
        return 0.0
    total = relevances[0]
    for i, r in enumerate(relevances[1:], start=2):
        total += r / math.log2(i)
    return total


def ndcg(recalled_ids: list[str], gold: set[str], k: int) -> float:
    """Standard NDCG@k with binary relevance. Ideal = all gold ranked first."""
    if not gold:
        return 0.0
    topk = recalled_ids[:k]
    rel = [1.0 if c in gold else 0.0 for c in topk]
    ideal = [1.0] * min(len(gold), k)
    idcg = _dcg(ideal)
    if idcg == 0.0:
        return 0.0
    return _dcg(rel) / idcg


_TURN_KS = [5, 10, 50]
_SESSION_KS = [5, 10]


def build_metric_dict(
    recalled_turn_doc_ids: list[str],
    gold_turn_doc_ids: set[str],
    gold_session_ids: set[str],
    recalled_session_ids_ordered: list[str],
) -> dict:
    """Build the {"session":..., "turn":...} dict matching print_retrieval_metrics.py input."""
    turn = {}
    for k in _TURN_KS:
        turn[f"recall_all@{k}"] = recall_all(recalled_turn_doc_ids, gold_turn_doc_ids, k)
        turn[f"ndcg_any@{k}"] = ndcg(recalled_turn_doc_ids, gold_turn_doc_ids, k)
    session = {}
    for k in _SESSION_KS:
        session[f"recall_all@{k}"] = recall_all(recalled_session_ids_ordered, gold_session_ids, k)
        session[f"ndcg_any@{k}"] = ndcg(recalled_session_ids_ordered, gold_session_ids, k)
    return {"session": session, "turn": turn}
