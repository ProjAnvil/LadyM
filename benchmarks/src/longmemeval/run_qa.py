"""Phase B: RAG recall + answer-LLM -> hypothesis.jsonl."""
from __future__ import annotations

import json
from pathlib import Path

from .config import BenchConfig
from ._runtime import make_engine as _default_engine_factory

ABSTAIN_SCORE_FLOOR = 0.05


def _default_answer_llm():
    """Build the answer-LLM from environment configuration (OpenAI-compatible).

    The original Python harness reused ladyM's configured LLM provider; the Go
    engine's MCP surface exposes no LLM completion tool, so the answer model is
    configured here via environment variables instead:

      * ``OPENAI_API_KEY``   — required (read by the ``openai`` client)
      * ``OPENAI_BASE_URL``  — optional, for OpenAI-compatible endpoints
      * ``LME_ANSWER_MODEL`` — answer model, default ``gpt-4o-mini``

    Returns a callable ``(system, user) -> str``. Tests pass ``answer_llm=``
    explicitly and never hit the network.
    """
    import os

    from openai import OpenAI

    model = os.environ.get("LME_ANSWER_MODEL", "gpt-4o-mini")
    client = OpenAI()  # reads OPENAI_API_KEY / OPENAI_BASE_URL from the env

    def _call(system: str, user: str) -> str:
        resp = client.chat.completions.create(
            model=model,
            messages=[
                {"role": "system", "content": system},
                {"role": "user", "content": user},
            ],
            temperature=0,
        )
        return resp.choices[0].message.content.strip()

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
