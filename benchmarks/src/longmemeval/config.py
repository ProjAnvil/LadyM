"""Configuration for the LongMemEval harness."""
from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import Literal


@dataclass
class BenchConfig:
    difficulty: Literal["oracle", "s", "m"] = "s"
    variant: Literal["raw", "consolidated"] = "raw"
    limit: int | None = None          # None = all 500; int = dev subset
    top_k: int = 50                   # retrieval-eval needs @50; QA uses 10 via override
    base_dir: Path = Path("benchmarks/.cache")

    def __post_init__(self):
        # Coerce str -> Path so callers (CLI / JSON / tests) can pass either
        # without crashing downstream Path-only operations (mkdir, /, etc.).
        self.base_dir = Path(self.base_dir)

    @property
    def data_dir(self) -> Path:
        return self.base_dir / "data"

    @property
    def db_dir(self) -> Path:
        return self.base_dir / "db" / self.difficulty / self.variant

    @property
    def results_dir(self) -> Path:
        return self.base_dir / "results" / self.difficulty / self.variant

    def db_path_for(self, question_id: str) -> Path:
        return self.db_dir / f"{question_id}.db"
