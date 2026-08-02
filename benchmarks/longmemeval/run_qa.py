"""Phase B: RAG recall + answer-LLM -> hypothesis.jsonl."""
from __future__ import annotations

import json
from pathlib import Path

from .config import BenchConfig
from ._runtime import make_engine as _default_engine_factory, load_cfg

ABSTAIN_SCORE_FLOOR = 0.05


def _default_answer_llm():
    """Build answer-LLM from the ladyM config provider (ModelRouting-injectable).

    Uses ``load_cfg()`` so the provider comes from ``ladym.toml`` + Secret Store
    (NOT the offline ``Config()`` defaults). Returns a callable ``(system, user) -> str``.
    Verified against the real ``LangChainLLMProvider.complete(messages, **params) -> str``
    (adapter.py:42), the abstract ``LLMProvider.complete`` contract (providers/llm.py:29).
    """
    from ladym.providers import make_agent
    cfg = load_cfg()
    agent = make_agent(cfg, "consolidate")  # reuse the same op/provider as consolidate
    if agent is None:
        raise RuntimeError(
            "answer_llm not configured: set llm.provider in ladym.toml "
            "(currently 'none' / offline mode), or pass answer_llm=... explicitly."
        )
    def _call(system: str, user: str) -> str:
        msgs = [{"role": "system", "content": system}, {"role": "user", "content": user}]
        return agent.complete(msgs)
    return _call


_SYSTEM = (
    "You answer the user's question using ONLY the provided memory context. "
    "If the context does not contain the answer, say 'I don't know.'"
)
_ABSTAIN = "I don't know."


def run_qa(
    dataset: list[dict],
    cfg: BenchConfig,
    *,
    engine_factory=_default_engine_factory,
    answer_llm=None,
    top_k_context: int = 10,
) -> Path:
    """Recall memories per question, build RAG context, generate a hypothesis.

    Writes ``cfg.results_dir/"hypothesis.jsonl"`` with one line per question:
    ``{"question_id": str, "hypothesis": str}``. Abstention questions (those
    with ``_abs`` in ``question_id``) whose best recall score falls below
    :data:`ABSTAIN_SCORE_FLOOR` emit the literal ``"I don't know."`` instead of
    invoking ``answer_llm``.
    """
    if answer_llm is None:
        answer_llm = _default_answer_llm()
    cfg.results_dir.mkdir(parents=True, exist_ok=True)
    out = cfg.results_dir / "hypothesis.jsonl"
    report_path = cfg.results_dir / "run_report.json"
    failures: list[dict] = []
    with out.open("w") as f:
        for instance in dataset:
            qid = instance["question_id"]
            try:
                eng = engine_factory(db_path=cfg.db_path_for(qid), workspace=f"lme-{qid}")
                try:
                    resp = eng.recall(instance["question"], top_k=top_k_context)
                finally:
                    eng.close()
                results = resp.results or []
                best_score = results[0].score if results else 0.0
                if "_abs" in qid and best_score < ABSTAIN_SCORE_FLOOR:
                    hypothesis = _ABSTAIN
                else:
                    context = "\n".join(
                        f"[{r.memory.metadata.get('date', '')}] "
                        f"{r.memory.metadata.get('session_id', '')}: {r.memory.content}"
                        for r in results
                    ) or "(no relevant memories)"
                    user = (
                        f"Memory context:\n{context}\n\n"
                        f"Question: {instance['question']}"
                    )
                    hypothesis = answer_llm(_SYSTEM, user)
                f.write(json.dumps({"question_id": qid, "hypothesis": hypothesis}) + "\n")
            except Exception as e:
                # Per-instance fault tolerance (spec requirement): 1 bad
                # instance must not kill a 500-question run — record the
                # failure and continue. No hypothesis.jsonl line is emitted
                # for this instance.
                failures.append({
                    "question_id": qid,
                    "error": f"{type(e).__name__}: {e}",
                })
    # Always emit a run_report.json so operators can see what dropped.
    report_path.write_text(json.dumps({"failures": failures}, indent=2))
    return out
