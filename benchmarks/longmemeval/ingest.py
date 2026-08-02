"""Ingest a LongMemEval instance's haystack into a per-instance LadyM DB."""
from __future__ import annotations
from pathlib import Path
from .config import BenchConfig


def _default_engine_factory(db_path, workspace):
    from ladym import Engine, Config
    return Engine(Config(db_path=str(db_path), workspace=workspace))


def _expected_turn_count(instance: dict) -> int:
    return sum(len(sess) for sess in instance["haystack_sessions"])


def ingest_instance(instance: dict, cfg: BenchConfig, *,
                    engine_factory=_default_engine_factory, force: bool = False) -> Path:
    """Write every haystack turn as an episodic memory. Returns the DB path.

    Skips if the DB already holds the expected turn count (unless force).
    variant=='consolidated' runs eng.consolidate() after ingest.
    """
    qid = instance["question_id"]
    db_path = cfg.db_path_for(qid)
    db_path.parent.mkdir(parents=True, exist_ok=True)
    expected = _expected_turn_count(instance)

    if db_path.exists() and not force:
        if _count_memories(engine_factory, db_path, qid) == expected:
            return db_path
        # stale/partial -> rebuild by removing
        db_path.unlink(missing_ok=True)

    eng = engine_factory(db_path=db_path, workspace=f"lme-{qid}")
    try:
        sids = instance["haystack_session_ids"]
        dates = instance["haystack_dates"]
        sessions = instance["haystack_sessions"]
        # pair sessions with their id+date, sort by date (stable)
        ordered = sorted(zip(sids, dates, sessions), key=lambda t: str(t[1]))
        for sid, date, turns in ordered:
            for turn_idx, turn in enumerate(turns):
                eng.record_event(
                    agent=turn["role"],
                    action=turn["content"],
                    observation="",
                    metadata={
                        "session_id": sid,
                        "date": str(date),
                        "turn_idx": turn_idx,
                        "doc_id": f"{sid}_{turn_idx}",
                        "has_answer": bool(turn.get("has_answer", False)),
                    },
                )
        if cfg.variant == "consolidated":
            eng.consolidate()
    finally:
        eng.close()
    return db_path


def _count_memories(engine_factory, db_path, qid) -> int:
    eng = engine_factory(db_path=db_path, workspace=f"lme-{qid}")
    try:
        return eng.stats().total_memories
    finally:
        eng.close()
