"""Phase A: recall per question, emit retrieval.jsonl in upstream metric format."""
from __future__ import annotations
import json
from pathlib import Path
from .config import BenchConfig
from .metrics import build_metric_dict
from ._runtime import make_engine as _default_engine_factory


def run_retrieval(dataset: list[dict], cfg: BenchConfig, *,
                  engine_factory=_default_engine_factory) -> Path:
    cfg.results_dir.mkdir(parents=True, exist_ok=True)
    out = cfg.results_dir / "retrieval.jsonl"
    report_path = cfg.results_dir / "run_report.json"
    failures: list[dict] = []
    with out.open("w") as f:
        for instance in dataset:
            qid = instance["question_id"]
            if "_abs" in qid:           # abstention: skip retrieval eval (upstream convention)
                continue
            try:
                db_path = cfg.db_path_for(qid)
                eng = engine_factory(db_path=db_path, workspace=f"lme-{qid}")
                try:
                    resp = eng.recall(instance["question"], top_k=cfg.top_k)
                finally:
                    eng.close()
                recalled = [r.memory.metadata for r in resp.results]
                recalled_turn_doc_ids = [m.get("doc_id", "") for m in recalled]
                # session ids in first-encounter order (dedup, preserve rank)
                seen = set()
                recalled_session_ids_ordered = []
                for m in recalled:
                    sid = m.get("session_id", "")
                    if sid and sid not in seen:
                        seen.add(sid)
                        recalled_session_ids_ordered.append(sid)
                # gold: evidence turns (has_answer) + evidence sessions
                gold_turn_doc_ids = _gold_turn_doc_ids(instance)
                gold_session_ids = set(instance.get("answer_session_ids", []))
                metrics = build_metric_dict(
                    recalled_turn_doc_ids, gold_turn_doc_ids,
                    gold_session_ids, recalled_session_ids_ordered,
                )
                f.write(json.dumps({
                    "question_id": qid,
                    "retrieval_results": {"metrics": metrics},
                }) + "\n")
            except Exception as e:
                # Per-instance fault tolerance (spec requirement): 1 bad
                # instance must not kill a 500-question run — record the
                # failure and continue. No retrieval.jsonl line is emitted
                # for this instance.
                failures.append({
                    "question_id": qid,
                    "error": f"{type(e).__name__}: {e}",
                })
    # Always emit a run_report.json so operators can see what dropped.
    report_path.write_text(json.dumps({"failures": failures}, indent=2))
    return out


def _gold_turn_doc_ids(instance: dict) -> set[str]:
    ids = set()
    for sid, turns in zip(instance["haystack_session_ids"], instance["haystack_sessions"]):
        for turn_idx, turn in enumerate(turns):
            if turn.get("has_answer"):
                ids.add(f"{sid}_{turn_idx}")
    return ids
