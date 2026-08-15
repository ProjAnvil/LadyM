"""Download LongMemEval data from HuggingFace with size verification."""
from __future__ import annotations
import requests
from pathlib import Path
from .config import BenchConfig

HF_BASE = "https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/main"

FILES = {
    "longmemeval_oracle.json": f"{HF_BASE}/longmemeval_oracle.json",
    "longmemeval_s_cleaned.json": f"{HF_BASE}/longmemeval_s_cleaned.json",
    "longmemeval_m_cleaned.json": f"{HF_BASE}/longmemeval_m_cleaned.json",
}

# Captured from HuggingFace `x-linked-size` header (authoritative LFS size) on
# 2026-08-02 against repo commit 98d7416c24c778c2fee6e6f3006e7a073259d48f.
# Mismatch => upstream data drifted — verify and update.
EXPECTED_SIZES: dict[str, int] = {
    "longmemeval_oracle.json": 15388478,
    "longmemeval_s_cleaned.json": 277383467,
    "longmemeval_m_cleaned.json": 2737100077,
}

# difficulty -> which file
DIFFICULTY_FILE = {"oracle": "longmemeval_oracle.json", "s": "longmemeval_s_cleaned.json", "m": "longmemeval_m_cleaned.json"}


def _http_get(url: str) -> bytes:
    r = requests.get(url, timeout=120)
    r.raise_for_status()
    return r.content


def download(cfg: BenchConfig, *, force: bool = False) -> dict[str, Path]:
    """Fetch this difficulty's JSON if missing/invalid. Returns {name: Path}."""
    cfg.data_dir.mkdir(parents=True, exist_ok=True)
    name = DIFFICULTY_FILE[cfg.difficulty]
    target = cfg.data_dir / name
    expected = EXPECTED_SIZES[name]
    if target.exists() and not force:
        if expected and target.stat().st_size == expected:
            return {name: target}
        # wrong size -> stale; fall through to re-download
    content = _http_get(FILES[name])
    if expected and len(content) != expected:
        raise RuntimeError(
            f"{name}: size {len(content)} != expected {expected}; "
            f"upstream data may have changed — verify and update EXPECTED_SIZES."
        )
    target.write_bytes(content)
    return {name: target}
