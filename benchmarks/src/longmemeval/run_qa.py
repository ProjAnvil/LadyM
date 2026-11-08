"""Phase B: RAG recall + answer-LLM -> hypothesis.jsonl."""
from __future__ import annotations

import json
from pathlib import Path
import subprocess
import sys
import time

from .config import BenchConfig
from ._runtime import make_engine as _default_engine_factory

ABSTAIN_SCORE_FLOOR = 0.05


def _answer_worker():
    """One isolated SDK call; errors cross IPC with their source type/message."""
    payload = json.load(sys.stdin)
    try:
        from openai import OpenAI

        with OpenAI(max_retries=0) as client:
            remaining = payload["deadline"] - time.monotonic()
            if remaining <= 0:
                raise TimeoutError("live answer wall budget exhausted")
            resp = client.chat.completions.create(
                model=payload["model"],
                messages=payload["messages"],
                temperature=0,
                timeout=remaining,
            )
            result = {"answer": resp.choices[0].message.content.strip()}
    except Exception as error:
        # Forward rather than suppress errors; SDK exceptions carry live HTTP
        # objects, so use a plain envelope rather than trying to pickle them.
        result = {"error_type": type(error).__name__, "error": str(error)}
    json.dump(result, sys.stdout)


def _default_answer_llm(cfg: BenchConfig):
    """Build an explicitly selected, wall-budgeted OpenAI-compatible answer LLM.

    The original Python harness reused ladyM's configured LLM provider; the Go
    engine's MCP surface exposes no LLM completion tool, so the answer model is
    configured here via environment variables instead:

      * ``OPENAI_API_KEY``   — required (read by the ``openai`` client)
      * ``OPENAI_BASE_URL``  — optional, for OpenAI-compatible endpoints
    Model, wall budget and question limit must be provided in BenchConfig.
    Environment credentials alone never authorize a request. Retries are disabled.
    One shared deadline starts here; recall/context/output work between calls
    consumes it but is not itself interrupted. Each live call includes worker
    startup, SDK import/client setup, transport, decoding and client shutdown.
    Provider failures become RuntimeError with the original type and message.

    Returns a callable ``(system, user) -> str``. Injected ``answer_llm=`` tests
    remain in-process and offline; deadline regressions use loopback HTTP only.
    """
    cfg.require_live(cfg.live_model)
    deadline = time.monotonic() + cfg.live_budget_s

    def _call(system: str, user: str) -> str:
        payload = json.dumps({
            "deadline": deadline,
            "model": cfg.live_model,
            "messages": [
                {"role": "system", "content": system},
                {"role": "user", "content": user},
            ],
        })
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError("live answer wall budget exhausted")
        # SDK timeouts limit inactivity, not total wall time. A fresh interpreter
        # avoids unsafe forks of threaded clients; startup/client reuse cost is
        # deliberately traded for cancellation. subprocess.run kills AND reaps
        # on timeout and closes IPC pipes: no abandoned worker thread/socket.
        try:
            proc = subprocess.run(
                [sys.executable, "-c",
                 "from longmemeval.run_qa import _answer_worker; _answer_worker()"],
                input=payload, capture_output=True, text=True, check=True,
                timeout=remaining,
            )
        except subprocess.TimeoutExpired as error:
            raise TimeoutError("live answer wall budget exhausted") from error
        except subprocess.CalledProcessError as error:
            raise RuntimeError(
                f"live answer worker failed (exit {error.returncode}): "
                f"{error.stderr.strip()}"
            ) from error
        result = json.loads(proc.stdout)
        if "error_type" in result:
            message = f"{result['error_type']}: {result['error']}"
            if result["error_type"] == "TimeoutError":
                raise TimeoutError(message)
            raise RuntimeError(message)
        return result["answer"]

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
        cfg.require_live(cfg.live_model)
        dataset = dataset[:cfg.limit]
        answer_llm = _default_answer_llm(cfg)
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
