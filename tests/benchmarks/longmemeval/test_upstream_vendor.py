import importlib
import pathlib
import py_compile
import re

VENDOR_DIR = pathlib.Path("benchmarks/longmemeval/upstream_eval")
ALL_VENDORED = ["evaluate_qa", "print_qa_metrics", "print_retrieval_metrics", "eval_utils"]
# Modules imported as a module by downstream tasks — must import without argv side effects.
IMPORTABLE_MODULES = ["eval_utils", "evaluate_qa"]
# CLI tools invoked via subprocess by the evaluate wrapper (Task 11); never imported.
# Validated with py_compile (proves they parse without running their argv logic).
CLI_SCRIPTS = ["print_qa_metrics", "print_retrieval_metrics"]

# Provenance header is "@ <40-hex SHA>" (space after @); require the real SHA so a
# leftover "<SHA>" placeholder fails. @\s+ rejects decorators like @backoff.on_exception.
SHA_RE = re.compile(r"@\s+[0-9a-f]{40}")


def test_each_vendored_file_has_pin_header():
    for name in ALL_VENDORED:
        text = (VENDOR_DIR / f"{name}.py").read_text()
        assert "VENDORED from" in text, f"{name}.py missing provenance header"
        assert SHA_RE.search(text), f"{name}.py missing 40-hex-char commit SHA"


def test_importable_modules_import():
    for name in IMPORTABLE_MODULES:
        importlib.import_module(f"benchmarks.longmemeval.upstream_eval.{name}")


def test_cli_scripts_compile():
    for name in CLI_SCRIPTS:
        path = VENDOR_DIR / f"{name}.py"
        py_compile.compile(str(path), doraise=True)
