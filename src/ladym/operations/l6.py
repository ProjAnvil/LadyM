"""L6 forward-intent prediction (SPEC §2.8).

From recent L1 episodes, predict likely next intents via the ``l6_forward_intent`` agent and
store one ``L6_predictive`` memory per intent. Each prediction carries ``metadata.valid_to``;
at the start of every run, expired predictions are retired (DELETE-style, no successor) so
stale intents drop out of recall but stay auditable.
"""
from __future__ import annotations

import time
from dataclasses import dataclass, field

from pydantic import BaseModel

from ..config import Config
from ..schema import Layer, Memory, MemoryType
from ..storage.embeddings import EmbeddingProvider
from ..storage.store import SQLiteStore
from .supersedes import is_retired, retire

_L6_LAYER = Layer.L6_PREDICTIVE.value
_L1_LAYER = Layer.EPISODIC.value
_WATERMARK = "l6_last_episode_ts"


class _Intent(BaseModel):
    intent: str
    confidence: float = 0.5
    horizon_s: float | None = None


class _Intents(BaseModel):
    intents: list[_Intent]


@dataclass
class L6PredictionReport:
    predictions: int = 0
    expired_retired: int = 0
    episodes_seen: int = 0
    watermark_updated_to: float | None = None
    details: list[dict] = field(default_factory=list)
    skipped: bool = False


def _default_prompt() -> str:
    from importlib.resources import files

    return (files("ladym.prompts") / "l6.txt").read_text()


def predict(
    store: SQLiteStore,
    embedder: EmbeddingProvider,
    *,
    cfg: Config,
    workspace: str | None = None,
    llm=None,
    prompt: str | None = None,
) -> L6PredictionReport:
    ws = workspace or cfg.workspace
    report = L6PredictionReport()
    if llm is None:
        report.skipped = True
        return report
    prompt = prompt or _default_prompt()
    now = time.time()

    # 1. expire sweep: retire predictions whose valid_to has passed.
    for m in store.iter_memories(workspace=ws, layer=_L6_LAYER):
        if is_retired(m):
            continue
        try:
            valid_to = float(m.metadata.get("valid_to", 0))
        except (TypeError, ValueError):
            continue
        if now > valid_to:
            retire(store, m)
            report.expired_retired += 1

    # 2. window: episodes newer than the watermark, capped.
    watermark = float(store.get_meta(_WATERMARK) or 0.0)
    rows = store.conn.execute(
        "SELECT * FROM memories WHERE layer = ? AND workspace = ? AND created_at > ? "
        "ORDER BY created_at ASC LIMIT ?",
        (_L1_LAYER, ws, watermark, cfg.system2.l6_max_episodes),
    ).fetchall()
    episodes = [store._row_to_memory(r) for r in rows]
    report.episodes_seen = len(episodes)
    if not episodes:
        return report

    # 3. predict.
    corpus = "\n".join(f"- {m.content}" for m in episodes)
    user_msg = (
        "Predict likely next intents from these recent episodes:\n" + corpus
    )
    msgs = [
        {"role": "system", "content": prompt},
        {"role": "user", "content": user_msg},
    ]
    out = llm.complete_structured(msgs, _Intents)
    out = out if isinstance(out, dict) else out.model_dump()
    default_horizon = cfg.system2.l6_horizon_s

    # 4. store one memory per intent.
    for it in out.get("intents", []):
        intent_text = (it.get("intent") or "").strip() if isinstance(it, dict) else ""
        if not intent_text:
            continue
        confidence = float(it.get("confidence", 0.5))
        horizon = it.get("horizon_s")
        horizon = default_horizon if horizon is None else float(horizon)
        valid_to = now + horizon
        mem = Memory(
            layer=Layer.L6_PREDICTIVE,
            type=MemoryType.FORWARD_INTENT,
            content=intent_text,
            summary=intent_text[:80],
            tags=["predicted"],
            metadata={"confidence": confidence, "horizon_s": horizon, "valid_to": valid_to},
            source="l6_predict",
            workspace=ws,
        )
        store.put_memory(mem, vector=embedder.embed(intent_text))
        report.predictions += 1
        report.details.append(
            {"intent": intent_text, "confidence": confidence, "valid_to": valid_to}
        )

    # 5. advance the watermark past the batch we just consumed.
    newest = max(m.created_at for m in episodes)
    store.set_meta(_WATERMARK, str(newest))
    report.watermark_updated_to = newest
    return report
