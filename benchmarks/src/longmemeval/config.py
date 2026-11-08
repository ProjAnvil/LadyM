"""Configuration for the LongMemEval harness."""
from __future__ import annotations

from dataclasses import dataclass
import math
from pathlib import Path
from typing import Literal


@dataclass
class BenchConfig:
    difficulty: Literal["oracle", "s", "m"] = "s"
    variant: Literal["raw", "consolidated"] = "raw"
    limit: int | None = None          # None = all 500; int = dev subset
    top_k: int = 50                   # retrieval-eval needs @50; QA uses 10 via override
    base_dir: Path = Path("benchmarks/.cache")
    live_model: str | None = None
    live_budget_s: float | None = None

    def __post_init__(self):
        # Coerce str -> Path so callers (CLI / JSON / tests) can pass either
        # without crashing downstream Path-only operations (mkdir, /, etc.).
        self.base_dir = Path(self.base_dir)

    def require_live(self, model: str | None):
        if (
            not isinstance(model, str) or not model.strip()
            or self.live_budget_s is None or not math.isfinite(self.live_budget_s)
            or self.live_budget_s <= 0
            or not isinstance(self.limit, int) or isinstance(self.limit, bool)
            or self.limit <= 0
        ):
            raise ValueError("live LLM requires explicit model, positive wall budget and question limit")

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
