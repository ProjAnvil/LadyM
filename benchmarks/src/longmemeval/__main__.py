"""CLI: python -m longmemeval <command> [opts]"""
from __future__ import annotations
import argparse
import json
import sys
from .config import BenchConfig


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    p = argparse.ArgumentParser(prog="longmemeval")
    p.add_argument("command", choices=["ingest", "retrieve", "qa", "evaluate"])
    p.add_argument("--difficulty", default="s", choices=["oracle", "s", "m"])
    p.add_argument("--variant", default="raw", choices=["raw", "consolidated"])
    p.add_argument("--limit", type=int, default=None)
    p.add_argument("--top-k", type=int, default=50)
    p.add_argument("--force-ingest", action="store_true")
    return p.parse_args(argv)


def _load_dataset(cfg: BenchConfig):
    from .download_data import download, DIFFICULTY_FILE
    name = DIFFICULTY_FILE[cfg.difficulty]
    path = download(cfg)[name]
    data = json.loads(path.read_text())
    if cfg.limit:
        data = data[: cfg.limit]
    return data, path


def main(argv: list[str] | None = None) -> None:
    args = parse_args(argv)
    cfg = BenchConfig(difficulty=args.difficulty, variant=args.variant,
                      limit=args.limit, top_k=args.top_k)
    if args.command == "ingest":
        from .ingest import ingest_instance
        data, _ = _load_dataset(cfg)
        failures: list[dict] = []
        for inst in data:
            try:
                ingest_instance(inst, cfg, force=args.force_ingest)
                print(f"ingested {inst['question_id']}")
            except Exception as e:
                # Per-instance fault tolerance (spec requirement): log the
                # failure and keep going so 1 bad instance doesn't kill a
                # 500-instance run.
                msg = f"{type(e).__name__}: {e}"
                print(f"FAILED {inst['question_id']}: {msg}")
                failures.append({"question_id": inst["question_id"], "error": msg})
        cfg.results_dir.mkdir(parents=True, exist_ok=True)
        report_path = cfg.results_dir / "ingest_report.json"
        report_path.write_text(json.dumps({"failures": failures}, indent=2))
        print(
            f"ingested {len(data) - len(failures)}/{len(data)} "
            f"({len(failures)} failures -> {report_path})"
        )
    elif args.command == "retrieve":
        from .run_retrieval import run_retrieval
        data, _ = _load_dataset(cfg)
        out = run_retrieval(data, cfg)
        print(f"wrote {out}")
    elif args.command == "qa":
        from .run_qa import run_qa
        data, _ = _load_dataset(cfg)
        out = run_qa(data, cfg)
        print(f"wrote {out}")
    elif args.command == "evaluate":
        _, dataset_path = _load_dataset(cfg)
        from .evaluate import evaluate
        try:
            out = evaluate(cfg, dataset_path)
        except RuntimeError as e:
            # evaluate._run() wraps subprocess timeout / non-zero-exit (incl. the
            # vendored judge's 429 infinite-retry) into a RuntimeError with a
            # diagnosis. Surface it instead of a bare traceback + exit non-zero.
            sys.stderr.write(f"[evaluate] {e}\n")
            sys.exit(1)
        print(f"wrote {out}")


if __name__ == "__main__":
    main()
