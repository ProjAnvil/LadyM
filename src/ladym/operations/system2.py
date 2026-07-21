"""System2 background consolidation cycle (SPEC §2.8)."""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from ..schema import Layer


@dataclass
class System2Report:
    consolidate: Any = None
    proceduralize: Any = None
    l5: Any = None
    l6: Any = None
    decay: Any = None
    skipped_llm_steps: bool = False


def _count_recent_episodes(engine, workspace: str) -> int:
    return sum(
        1
        for _ in engine.store.iter_memories(
            workspace=workspace, layer=Layer.EPISODIC.value
        )
    )


def run_system2_cycle(engine, *, workspace: str | None = None) -> System2Report:
    ws = workspace or engine.config.workspace
    report = System2Report()
    report.consolidate = engine.consolidate(workspace=ws)
    report.proceduralize = engine.proceduralize(workspace=ws)
    enough = (
        _count_recent_episodes(engine, ws) >= engine.config.system2.min_episodes_to_run
    )
    if enough:
        if hasattr(engine, "extract_mental_models"):
            report.l5 = engine.extract_mental_models(workspace=ws)
        if hasattr(engine, "predict_forward_intents"):
            report.l6 = engine.predict_forward_intents(workspace=ws)
    else:
        report.skipped_llm_steps = True
    report.decay = engine.decay(workspace=ws)
    return report
