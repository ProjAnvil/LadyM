"""Wrap vendored upstream eval scripts; produce scores.md.

The vendored scripts are CLI tools invoked via subprocess:
- print_retrieval_metrics.py <retrieval.jsonl>
- evaluate_qa.py <metric_model_short> <hyp_file> <ref_file>
- print_qa_metrics.py <eval_log> <ref_file>

Path note (verified against upstream_eval/evaluate_qa.py line 61):
    result_file = hyp_file + '.eval-results-{}'.format(metric_model_short)
where ``metric_model_short`` is the raw argv[1] passed to evaluate_qa.py.
For ``judge_model="gpt-4o"`` the suffix is therefore ``-gpt-4o`` (NOT ``-gpt4o``).
print_qa_metrics.py asserts ``autoeval_label['model'] == 'gpt-4o-2024-08-06'``,
so the judge short name MUST remain ``gpt-4o`` (model_zoo maps it to the
pinned snapshot). Never substitute ``gpt-4o-mini`` — aggregation crashes.
"""
from __future__ import annotations

import subprocess
import sys
from pathlib import Path

from .config import BenchConfig

_HERE = Path(__file__).parent / "upstream_eval"
_PY = sys.executable


def _run(cmd: list[str], *, timeout: float, label: str) -> str:
    """Run a vendored eval subprocess; surface a clear error on timeout or failure.

    The vendored judge (evaluate_qa.py) retries RateLimitError/APIError with
    exponential backoff and **no max_tries**, so a non-transient 429 (e.g.
    exhausted OpenAI quota) makes it loop forever. The timeout turns that hang
    into a fast, loud failure carrying a diagnosis instead of a silent stall.
    """
    try:
        proc = subprocess.run(
            cmd, capture_output=True, text=True, check=True, timeout=timeout
        )
    except subprocess.TimeoutExpired as e:
        partial = e.stderr or ""
        if isinstance(partial, bytes):
            partial = partial.decode(errors="replace")
        raise RuntimeError(
            f"{label} did not finish within {int(timeout)}s — likely a 429 "
            f"quota/rate-limit infinite-retry or a network stall "
            f"(check your OpenAI billing/credits). Partial stderr:\n"
            f"{partial.strip()[:2000]}"
        ) from e
    except subprocess.CalledProcessError as e:
        raise RuntimeError(
            f"{label} failed (exit {e.returncode}). stderr:\n"
            f"{(e.stderr or '').strip()[:2000]}"
        ) from e
    return proc.stdout


def run_retrieval_metrics(cfg: BenchConfig) -> str:
    """Run print_retrieval_metrics.py on retrieval.jsonl; return captured stdout."""
    retrieval = cfg.results_dir / "retrieval.jsonl"
    return _run(
        [_PY, str(_HERE / "print_retrieval_metrics.py"), str(retrieval)],
        timeout=120, label="print_retrieval_metrics.py",
    )


def run_qa_metrics(cfg: BenchConfig, dataset_path: Path, judge_model: str) -> str:
    """evaluate_qa.py then print_qa_metrics.py; return aggregated stdout."""
    hyp = cfg.results_dir / "hypothesis.jsonl"
    # Per-question budget: a normal judge call is ~3-10s, an occasional single
    # retry ~30s, so 45s/question is safe headroom. Floor 120s so even a
    # 1-question run tolerates one slow call. A quota-429 hangs every question,
    # so this still trips fast on small (smoke) runs — the typical first signal.
    n_questions = sum(1 for _ in hyp.open()) if hyp.exists() else 0
    timeout = max(120, n_questions * 45)
    # 1. judge — writes <hyp>.eval-results-<judge_model> (see module docstring).
    _run(
        [_PY, str(_HERE / "evaluate_qa.py"), judge_model, str(hyp), str(dataset_path)],
        timeout=timeout, label="evaluate_qa.py (GPT-4o judge)",
    )
    eval_log = Path(str(hyp) + f".eval-results-{judge_model}")
    # 2. aggregate
    return _run(
        [_PY, str(_HERE / "print_qa_metrics.py"), str(eval_log), str(dataset_path)],
        timeout=120, label="print_qa_metrics.py",
    )


def evaluate(cfg: BenchConfig, dataset_path: Path, *, judge_model: str = "gpt-4o") -> Path:
    """Run retrieval + QA metrics; write scores.md; return its path.

    Missing jsonls degrade gracefully to a placeholder block instead of raising,
    so the caller can invoke ``evaluate`` once per variant even if Phase A or B
    was skipped.
    """
    retrieval_block = (
        run_retrieval_metrics(cfg)
        if (cfg.results_dir / "retrieval.jsonl").exists()
        else "(no retrieval run)"
    )
    qa_block = "(no qa run)"
    if (cfg.results_dir / "hypothesis.jsonl").exists():
        qa_block = run_qa_metrics(cfg, dataset_path, judge_model)
    md = [
        f"# LongMemEval scores — {cfg.difficulty} / {cfg.variant}",
        f"_top_k(retrieval)={cfg.top_k}_\n",
        "## Retrieval (Phase A)\n```\n" + retrieval_block + "\n```\n",
        "## QA (Phase B, judge=" + judge_model + ")\n```\n" + qa_block + "\n```\n",
    ]
    out = cfg.results_dir / "scores.md"
    out.write_text("\n".join(md))
    return out
