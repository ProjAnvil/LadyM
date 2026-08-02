from benchmarks.longmemeval.config import BenchConfig
from benchmarks.longmemeval import ingest
from tests.benchmarks.longmemeval.fixtures import make_mini_instance


class _FakeMemory:
    def __init__(self, metadata):
        self.metadata = metadata


class _FakeEngine:
    def __init__(self):
        self.recorded = []
        self.consolidated = False
    def record_event(self, *, agent, action, observation="", metadata=None, **kw):
        self.recorded.append({"agent": agent, "action": action, "metadata": metadata or {}})
    def consolidate(self, **kw):
        self.consolidated = True
    def stats(self):
        class S:
            total_memories = len(self.recorded)
        return S()
    def close(self):
        pass


def test_ingest_records_every_turn_in_order(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    inst = make_mini_instance()
    fake = _FakeEngine()
    ingest.ingest_instance(inst, cfg, engine_factory=lambda **kw: fake)
    assert len(fake.recorded) == 4  # 2 sessions x 2 turns
    # timestamp order: sess_1 before sess_2
    assert fake.recorded[0]["metadata"]["session_id"] == "sess_1"
    assert fake.recorded[2]["metadata"]["session_id"] == "sess_2"
    # doc_id convention
    assert fake.recorded[0]["metadata"]["doc_id"] == "sess_1_0"
    # has_answer propagated
    assert fake.recorded[0]["metadata"]["has_answer"] is True
    assert fake.recorded[1]["metadata"]["has_answer"] is False
    # raw variant must NOT consolidate
    assert fake.consolidated is False


def test_ingest_user_assistant_agent(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    fake = _FakeEngine()
    ingest.ingest_instance(make_mini_instance(), cfg, engine_factory=lambda **kw: fake)
    assert fake.recorded[0]["agent"] == "user"
    assert fake.recorded[1]["agent"] == "assistant"


def test_consolidated_variant_runs_consolidate(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="consolidated", base_dir=tmp_path)
    fake = _FakeEngine()
    # consolidated path uses a marker file, not memory-count skip
    ingest.ingest_instance(make_mini_instance(), cfg, engine_factory=lambda **kw: fake)
    assert fake.consolidated is True


def test_consolidated_skip_writes_marker_and_skips_second_call(tmp_path):
    """Carry-forward: cache-skip behaviour for the consolidated variant.

    On the first call, ingest runs and writes a `.done` marker. On the second
    call with the same config, ingest must skip WITHOUT touching the engine
    (no count probe, no record_event, no consolidate) — the marker alone guards
    re-runs because consolidation changes memory count post-ingest.
    """
    cfg = BenchConfig(difficulty="oracle", variant="consolidated", base_dir=tmp_path)
    qid = make_mini_instance()["question_id"]
    db_path = cfg.db_path_for(qid)

    fake1 = _FakeEngine()
    ingest.ingest_instance(make_mini_instance(), cfg, engine_factory=lambda **kw: fake1)
    assert fake1.consolidated is True
    # marker must exist after a successful consolidated ingest
    assert db_path.with_suffix(".done").exists()

    # second call: factory must not be invoked at all (marker-based skip)
    calls = []
    def _tracking_factory(**kw):
        calls.append(kw)
        return _FakeEngine()
    ingest.ingest_instance(make_mini_instance(), cfg, engine_factory=_tracking_factory)
    assert calls == [], "consolidated skip must not probe the engine"


def test_force_reruns_ingest_even_when_marker_exists(tmp_path):
    """Carry-forward: force=True bypasses the marker cache and re-runs."""
    cfg = BenchConfig(difficulty="oracle", variant="consolidated", base_dir=tmp_path)
    inst = make_mini_instance()
    fake1 = _FakeEngine()
    ingest.ingest_instance(inst, cfg, engine_factory=lambda **kw: fake1)
    assert fake1.consolidated is True

    # second call with force=True must re-run ingest
    fake2 = _FakeEngine()
    ingest.ingest_instance(inst, cfg, engine_factory=lambda **kw: fake2, force=True)
    assert len(fake2.recorded) == 4
    assert fake2.consolidated is True


class _BoomEngine(_FakeEngine):
    """Engine whose consolidate() always raises — simulates LLM write-path failure."""
    def consolidate(self, **kw):
        raise RuntimeError("consolidate blew up")


def test_consolidated_failure_clears_done_marker(tmp_path):
    """Important #1: if consolidate() raises during a rebuild, the `.done`
    marker must NOT be left behind pointing at a missing/empty DB. Otherwise
    the next non-force call would skip ingest and return a path to nothing,
    silently yielding empty recall (all-zero scores).
    """
    cfg = BenchConfig(difficulty="oracle", variant="consolidated", base_dir=tmp_path)
    inst = make_mini_instance()
    qid = inst["question_id"]
    db_path = cfg.db_path_for(qid)
    done_marker = db_path.with_suffix(".done")

    # Pre-place a stale marker + DB to model a prior successful run, then
    # force a rebuild that fails inside consolidate().
    db_path.parent.mkdir(parents=True, exist_ok=True)
    db_path.write_bytes(b"old")
    done_marker.touch()
    assert done_marker.exists()

    try:
        ingest.ingest_instance(
            inst, cfg, engine_factory=lambda **kw: _BoomEngine(), force=True,
        )
    except RuntimeError:
        pass

    # The marker MUST be gone — otherwise the next non-force call would skip.
    assert not done_marker.exists(), (
        "stale .done marker survived a failed rebuild -> next call returns "
        "a path to a missing DB -> silent all-zero recall"
    )
    # And the next non-force call must rebuild (not skip via the marker).
    fake = _FakeEngine()
    ingest.ingest_instance(inst, cfg, engine_factory=lambda **kw: fake)
    assert fake.consolidated is True
    assert done_marker.exists()


def test_ingest_raw_auto_rebuilds_on_embedding_dim_mismatch(tmp_path):
    """A stale DB built under another embedding dim auto-rebuilds (no --force needed).

    Simulates switching the embedding provider (e.g. hashing→ollama): the existing DB
    holds vectors of a different dim, so probing it via _count_memories raises
    EmbeddingDimensionMismatch. ingest must treat that as stale → rebuild, not fail.
    """
    from ladym.storage.embeddings import EmbeddingDimensionMismatch

    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    inst = make_mini_instance()
    # pre-create a stale DB so the skip-check probes _count_memories on it
    db_path = cfg.db_path_for(inst["question_id"])
    db_path.parent.mkdir(parents=True, exist_ok=True)
    db_path.write_bytes(b"stale")

    calls = {"n": 0}
    fake = _FakeEngine()

    def factory(**kw):
        calls["n"] += 1
        if calls["n"] == 1:
            # probing the stale DB raises (Engine reopen sees wrong dim)
            raise EmbeddingDimensionMismatch(256, 1024)
        return fake  # rebuild path uses a fresh engine

    ingest.ingest_instance(inst, cfg, engine_factory=factory)

    assert calls["n"] == 2          # probe (raised) then rebuild (succeeded)
    assert len(fake.recorded) == 4  # all turns re-ingested into the fresh engine

