"""Ingest a LongMemEval instance's haystack into a per-instance LadyM DB."""
from __future__ import annotations
from pathlib import Path
from .config import BenchConfig
from ._runtime import make_engine as _default_engine_factory


def _expected_turn_count(instance: dict) -> int:
    return sum(len(sess) for sess in instance["haystack_sessions"])


def ingest_instance(instance: dict, cfg: BenchConfig, *,
                    engine_factory=_default_engine_factory, force: bool = False) -> Path:
    """Write every haystack turn as an episodic memory. Returns the DB path.

    Skip policy (unless ``force``):
      * ``variant=='raw'``: skip if the DB already holds the expected turn count.
      * ``variant=='consolidated'``: skip iff a ``<db>.done`` marker exists —
        consolidation changes memory count post-ingest, so the raw count-check
        would be wrong here.

    ``variant=='consolidated'`` runs ``eng.consolidate()`` after ingest and
    writes the ``.done`` marker on success.
    """
    qid = instance["question_id"]
    db_path = cfg.db_path_for(qid)
    db_path.parent.mkdir(parents=True, exist_ok=True)
    expected = _expected_turn_count(instance)
    done_marker = db_path.with_suffix(".done")

    if not force:
        if cfg.variant == "consolidated" and done_marker.exists():
            return db_path
        if cfg.variant == "raw" and db_path.exists():
            from ladym.storage.embeddings import EmbeddingDimensionMismatch
            try:
                if _count_memories(engine_factory, db_path, qid) == expected:
                    return db_path
            except EmbeddingDimensionMismatch:
                # Stale DB built under a different embedding provider/dim
                # (e.g. hashing→ollama). Fall through to unlink + rebuild so a
                # provider switch doesn't require --force-ingest.
                pass
    if db_path.exists():
        db_path.unlink(missing_ok=True)
    # Also clear any stale completion marker so a failed rebuild (e.g.
    # consolidate() raising below) doesn't leave a marker pointing at a
    # missing DB — the next non-force call would otherwise skip and return
    # a path to nothing, yielding silent all-zero recall. Critical for
    # force=True on the consolidated variant; harmless on raw (no marker
    # is ever written there).
    done_marker.unlink(missing_ok=True)

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
    if cfg.variant == "consolidated":
        done_marker.touch()
    return db_path


def _count_memories(engine_factory, db_path, qid) -> int:
    eng = engine_factory(db_path=db_path, workspace=f"lme-{qid}")
    try:
        return eng.stats().total_memories
    finally:
        eng.close()
