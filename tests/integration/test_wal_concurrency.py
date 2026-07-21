"""WAL concurrency integration test for Task 4.2.

Verifies the headline guarantee of the opt-in System2 worker: a reader thread
can ``recall`` while the worker process is writing, without sqlite locking
errors. This only works when the DB is opened in WAL mode (``enable_wal=True``)
BEFORE the Engine constructs its ``SQLiteStore`` — see the Task 4.2 correction.
"""

from __future__ import annotations

import threading
import time

from typer.testing import CliRunner

from ladym.cli import app
from ladym.config import Config
from ladym.engine import Engine


def _wal_config(tmp_path) -> Config:
    """A Config that opens its store in WAL mode (set BEFORE Engine build)."""
    cfg = Config(db_path=tmp_path / "w.db", workspace="w", embedding_provider="hashing")
    cfg.enable_wal = True
    return cfg


def test_worker_once_writes_while_reader_recalls(tmp_path):
    cfg = _wal_config(tmp_path)
    eng = Engine(cfg)
    eng.episodic.record(agent="bot", action="x", observation="a fact to consolidate")
    eng.close()

    runner = CliRunner()
    res = runner.invoke(
        app, ["worker", "--once", "--db", str(cfg.db_path), "--workspace", "w"]
    )
    assert res.exit_code == 0, res.output

    # Concurrent: spawn a reader thread while another worker cycles. Each
    # thread opens its OWN Engine (its own sqlite connection) — that's the
    # WAL concurrency model (writer-connection vs reader-connection). Python's
    # sqlite3 objects are thread-bound by default, so sharing one Engine
    # across threads would error for reasons unrelated to WAL.
    errors: list[Exception] = []

    def read() -> None:
        reader = Engine(cfg)
        try:
            for _ in range(20):
                reader.recall("fact")
                time.sleep(0.005)
        except Exception as e:  # noqa: BLE001
            errors.append(e)
        finally:
            reader.close()

    t = threading.Thread(target=read)
    t.start()
    runner.invoke(
        app, ["worker", "--once", "--db", str(cfg.db_path), "--workspace", "w"]
    )
    t.join()
    assert not errors


def test_start_system2_thread_ticks_and_stops_cleanly(tmp_path):
    """``Engine.start_system2`` runs in a daemon thread with its own connection.

    Starts the loop, lets it tick once or twice, sets the stop handle, joins
    briefly, and asserts no exception was raised. The worker Engine is
    isolated from the main one (its own SQLiteStore connection).
    """
    cfg = _wal_config(tmp_path)
    eng = Engine(cfg)
    eng.episodic.record(agent="bot", action="x", observation="seed episode")

    errors: list[Exception] = []

    # Patch the worker's cycle to surface exceptions instead of swallowing.
    from ladym.operations import system2 as sys2_mod

    original = sys2_mod.run_system2_cycle

    def instrumented(worker_eng, *, workspace=None):
        try:
            return original(worker_eng, workspace=workspace)
        except Exception as e:  # noqa: BLE001
            errors.append(e)
            raise

    sys2_mod.run_system2_cycle = instrumented  # type: ignore[assignment]
    try:
        stop = eng.start_system2(interval_s=0, workspace="w")
        assert isinstance(stop, threading.Event)
        # Give the daemon thread time to tick at least once.
        time.sleep(0.2)
        stop.set()
        # Brief join — daemon, so it's fine if it's not done, but it should be.
        time.sleep(0.1)
    finally:
        sys2_mod.run_system2_cycle = original  # type: ignore[assignment]
        eng.close()

    assert not errors
