import importlib
import pathlib

VENDORED = ["evaluate_qa", "print_qa_metrics", "print_retrieval_metrics", "eval_utils"]


def test_each_vendored_file_has_pin_header():
    d = pathlib.Path("benchmarks/longmemeval/upstream_eval")
    for name in VENDORED:
        text = (d / f"{name}.py").read_text()
        assert "VENDORED from" in text, f"{name}.py missing provenance header"
        assert "@" in text, f"{name}.py missing commit SHA"


def test_modules_import():
    for name in VENDORED:
        importlib.import_module(f"benchmarks.longmemeval.upstream_eval.{name}")
