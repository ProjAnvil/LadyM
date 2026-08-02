"""CLI: python -m benchmarks.longmemeval <command> [opts]"""
from __future__ import annotations
import argparse
import json
import subprocess
import sys
from .config import BenchConfig


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    p = argparse.ArgumentParser(prog="benchmarks.longmemeval")
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
        for inst in data:
            ingest_instance(inst, cfg, force=args.force_ingest)
            print(f"ingested {inst['question_id']}")
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
        except subprocess.CalledProcessError as e:
            # evaluate() uses capture_output=True+check=True internally; surface
            # the captured stderr so operators can see why the vendored judge /
            # metrics script failed instead of a bare non-zero exit.
            sys.stderr.write(e.stderr or "")
            sys.stderr.write(f"\n[evaluate] {e}\n")
            sys.exit(e.returncode)
        print(f"wrote {out}")


if __name__ == "__main__":
    main()
