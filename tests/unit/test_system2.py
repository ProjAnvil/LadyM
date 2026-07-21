import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.operations.system2 import System2Report, run_system2_cycle


@pytest.fixture
def engine(tmp_path):
    e = Engine(Config.for_testing(tmp_path))
    yield e
    e.close()


def test_cycle_runs_consolidate_proceduralize_decay(engine):
    engine.episodic.record(agent="bot", action="x", observation="auth uses JWT")
    report = run_system2_cycle(engine)
    assert isinstance(report, System2Report)
    assert report.consolidate is not None
    assert report.decay is not None


def test_threshold_gate_skips_when_too_few(engine):
    engine.config.system2.min_episodes_to_run = 5
    engine.episodic.record(agent="bot", action="x", observation="only one")
    report = run_system2_cycle(engine)
    assert report.l5 is None and report.l6 is None  # gated
