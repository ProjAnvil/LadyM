import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.operations import system2 as system2_module
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


def test_start_system2_stops_after_max_consecutive_errors(engine, monkeypatch):
    """Repeatedly failing cycles must stop the background worker thread after
    ``config.system2.max_consecutive_errors`` failures (so a stale index /
    misconfigured LLM is visible, not invisible)."""
    import threading
    import time

    # Make every cycle blow up. ``Engine.start_system2`` reads this symbol at
    # call-time from the module, so monkeypatching it here is enough.
    def _raising_cycle(_eng, *, workspace=None):
        raise RuntimeError("simulated cycle failure")

    monkeypatch.setattr(system2_module, "run_system2_cycle", _raising_cycle)

    # Small budget + tiny interval so the test terminates quickly.
    engine.config.system2.max_consecutive_errors = 2
    # Snapshot the thread count BEFORE start_system2 spins up the worker so
    # we can detect the worker thread exiting on its own.
    pre_alive = threading.active_count()
    stop = engine.start_system2(interval_s=0)
    # Give the worker time to fail twice and break out of its loop. The thread
    # count should drop back to ``pre_alive`` once it does.
    deadline = time.monotonic() + 5.0
    while time.monotonic() < deadline:
        if threading.active_count() <= pre_alive:
            break
        time.sleep(0.05)
    # The worker thread must have exited on its own (we did NOT set the stop
    # event). If the bounded-stop were removed, the worker would loop forever
    # and ``active_count`` would stay elevated → this assert fails.
    assert threading.active_count() <= pre_alive, (
        "system2 worker thread did not stop after max_consecutive_errors "
        "failures — bounded-stop logic is missing or broken"
    )
    # Sanity: the stop event was never set by the test (the worker broke out
    # on its own). ``is_set()`` is False.
    assert not stop.is_set()


def test_start_system2_recovers_after_single_error(engine, monkeypatch):
    """A transient failure followed by success must reset the consecutive-error
    counter so the worker keeps running."""
    import threading
    import time

    calls = {"n": 0}

    def _flaky_cycle(_eng, *, workspace=None):
        calls["n"] += 1
        if calls["n"] == 1:
            raise RuntimeError("one-off")
        return System2Report()

    monkeypatch.setattr(system2_module, "run_system2_cycle", _flaky_cycle)

    engine.config.system2.max_consecutive_errors = 2
    pre_alive = threading.active_count()
    stop = engine.start_system2(interval_s=0)
    # Let at least 3 cycles run so we observe: fail, succeed, succeed.
    deadline = time.monotonic() + 5.0
    while time.monotonic() < deadline and calls["n"] < 3:
        time.sleep(0.05)
    assert calls["n"] >= 3, "worker did not progress past the first failure"
    # The worker must still be alive — if the counter had not reset, the
    # worker would have stopped after cycle 2 (the first success would be
    # miscounted as failure #2). ``active_count > pre_alive`` proves the
    # thread is still running and the counter reset worked.
    assert threading.active_count() > pre_alive, (
        "system2 worker thread exited early — consecutive-error counter did "
        "not reset after a successful cycle"
    )
    stop.set()
