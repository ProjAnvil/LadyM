"""Run four offline readers against real local Engine APIs and provenance.

These are fixture observations, not QA accuracy, LLM gains or billing tokens.
No model, service or network request is created by this runner.
"""

import argparse
import json
import os
from pathlib import Path
import subprocess
import uuid


def run_learning_eval(*, edition="personal", backend="sqlite"):
    if edition not in {"personal", "enterprise"}:
        raise ValueError("edition must be personal or enterprise")
    if backend not in {"sqlite", "postgres"}:
        raise ValueError("backend must be sqlite or postgres")
    if edition == "enterprise" and backend == "sqlite":
        raise ValueError("enterprise excludes SQLite")
    if backend == "postgres" and not os.environ.get("LADYM_TEST_PG_DSN"):
        raise ValueError("PostgreSQL requires explicit LADYM_TEST_PG_DSN")
    root = Path(__file__).resolve().parents[3]
    scratch = root / ".superpowers" / "sdd" / "learning-eval"
    scratch.mkdir(parents=True, exist_ok=True)
    report = scratch / f"{uuid.uuid4().hex}.json"
    env = os.environ.copy()
    env.update(
        LADYM_LEARNING_BACKEND=backend,
        LADYM_LEARNING_REPORT=str(report.relative_to(root)),
        TMPDIR=str(scratch),
        CGO_ENABLED="0",
    )
    command = ["go", "test", "-count=1", "-run", "^TestTrustedMemoryVariants$"]
    if edition == "enterprise":
        command += ["-tags", "enterprise"]
    command += ["./scenarios"]
    try:
        result = subprocess.run(
            command, cwd=root, env=env, capture_output=True, text=True, timeout=180,
        )
        if result.returncode:
            # Do not echo subprocess diagnostics that might contain credentials.
            raise RuntimeError("real Engine evaluation failed; run ./scenarios directly")
        rows = json.loads(report.read_text())
        return {
            "measurement": "offline_engine_fixture",
            "edition": edition,
            "backend": backend,
            "read_elapsed_unit": "seconds",
            "generative_calls": 0,
            "variants": rows,
        }
    finally:
        report.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--edition", choices=["personal", "enterprise"], default="personal")
    parser.add_argument("--backend", choices=["sqlite", "postgres"], default="sqlite")
    args = parser.parse_args()
    print(json.dumps(run_learning_eval(edition=args.edition, backend=args.backend), indent=2))


if __name__ == "__main__":
    main()
