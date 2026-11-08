"""Phase B tests: run_qa emits hypothesis.jsonl with RAG answers + abstention."""
import json
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import subprocess
from threading import Event, Thread
import time

import pytest

from longmemeval.config import BenchConfig
from longmemeval import run_qa
from lme_fixtures import make_mini_dataset


class _FakeMemory:
    """Mirrors ladym.schema.Memory — only .content + .metadata are exercised."""
    def __init__(self, content, metadata=None):
        self.content = content
        self.metadata = metadata or {}


class _FakeResult:
    """Faithful to ladym.schema.RecallResult: exposes .memory + .score,
    NOT direct .content/.metadata (which would mask a regression)."""
    def __init__(self, content, score, metadata=None):
        self.memory = _FakeMemory(content, metadata)
        self.score = score


class _FakeResp:
    def __init__(self, results):
        self.results = results


class _FakeEngine:
    def __init__(self, results):
        self._r = results
    def recall(self, query, **kw):
        return _FakeResp(self._r)
    def close(self):
        pass


def test_run_qa_emits_hypothesis(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    data = make_mini_dataset()
    res = [
        _FakeResult("user said blue", 0.9, {"date": "2024-01-01", "session_id": "sess_1"}),
        _FakeResult("assistant agreed", 0.5, {"date": "2024-01-02", "session_id": "sess_1"}),
    ]
    eng = _FakeEngine(res)
    calls = []
    def fake_llm(system, user):
        calls.append(user)
        return "blue"
    out = run_qa.run_qa(data, cfg, engine_factory=lambda **kw: eng, answer_llm=fake_llm)
    lines = [json.loads(l) for l in out.read_text().splitlines()]
    assert {l["question_id"] for l in lines} == {"mini_1", "mini_2_abs"}
    hyps = {l["question_id"]: l["hypothesis"] for l in lines}
    assert hyps["mini_1"] == "blue"
    # RAG context should include the recalled content + metadata
    assert calls, "answer_llm must be invoked for non-abstaining questions"
    assert "user said blue" in calls[0]


def test_run_qa_abstention_when_low_score(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    data = make_mini_dataset()
    # abstention q with near-zero recall score -> "I don't know."
    res = [_FakeResult("irrelevant", 0.01)]
    eng = _FakeEngine(res)
    called = []
    out = run_qa.run_qa(
        data, cfg, engine_factory=lambda **kw: eng,
        answer_llm=lambda s, u: (called.append(1), "no")[1],
    )
    lines = {
        json.loads(l)["question_id"]: json.loads(l)["hypothesis"]
        for l in out.read_text().splitlines()
    }
    assert lines["mini_2_abs"] == "I don't know."
    # Empty failures list -> run_report.json still emitted.
    report = json.loads((cfg.results_dir / "run_report.json").read_text())
    assert report == {"failures": []}


def test_run_qa_per_instance_fault_tolerance(tmp_path):
    """Important #2: per-instance try/except. One bad instance must not kill
    the whole run — the OTHER instance still emits a hypothesis line, and
    the failure is recorded in run_report.json (spec requirement)."""
    import copy
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    good = make_mini_dataset()[0]                      # mini_1
    bad = copy.deepcopy(good)
    bad["question_id"] = "bad_qid"                     # non-abstention
    dataset = [good, bad]

    res = [_FakeResult("user said blue", 0.9, {"date": "d", "session_id": "s"})]

    class _RoutingFactory:
        def __init__(self):
            self.calls = 0

        def __call__(self, **kw):
            self.calls += 1
            if self.calls == 2:
                raise RuntimeError("recall blew up for bad_qid")
            return _FakeEngine(res)

    out = run_qa.run_qa(
        dataset, cfg, engine_factory=_RoutingFactory(),
        answer_llm=lambda s, u: "blue",
    )
    lines = [json.loads(l) for l in out.read_text().splitlines()]
    ids = [l["question_id"] for l in lines]
    # bad_qid is dropped; mini_1 still produced a line.
    assert ids == ["mini_1"]
    assert "bad_qid" not in ids

    report = json.loads((cfg.results_dir / "run_report.json").read_text())
    assert len(report["failures"]) == 1
    assert report["failures"][0]["question_id"] == "bad_qid"
    assert "recall blew up" in report["failures"][0]["error"]


@contextmanager
def _answer_server(monkeypatch, modes, *, trickle_duration_s=0):
    """Loopback only; trickle bytes frequently enough to defeat read timeouts."""
    trickle_interval_s = 0.025
    stop = Event()
    disconnected = Event()
    requests = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_POST(self):
            requests.append(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
            mode = modes[len(requests) - 1]
            if mode == "error":
                body = {"error": {"message": "fixture rate limit", "type": "rate_limit_error"}}
            else:
                body = {
                    "id": "fixture", "object": "chat.completion", "created": 0,
                    "model": "explicit-model",
                    "choices": [{"index": 0, "finish_reason": "stop",
                                 "message": {"role": "assistant", "content": " blue "}}],
                }
            payload = json.dumps(body).encode()
            if mode == "trickle":
                # Valid JSON whitespace keeps the declared body incomplete for
                # longer than the whole wall budget, even with instant startup.
                payload += b" " * (int(trickle_duration_s / trickle_interval_s) + 1)
            self.send_response(429 if mode == "error" else 200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.send_header("Connection", "close")
            self.end_headers()
            try:
                if mode == "trickle":
                    for byte in payload:
                        self.wfile.write(bytes([byte]))
                        self.wfile.flush()
                        if stop.wait(trickle_interval_s):
                            break
                else:
                    self.wfile.write(payload)
                    self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError):
                disconnected.set()

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = Thread(target=server.serve_forever, kwargs={"poll_interval": 0.025})
    thread.start()
    monkeypatch.setenv("OPENAI_API_KEY", "loopback-fixture-only")
    monkeypatch.setenv("OPENAI_BASE_URL", f"http://127.0.0.1:{server.server_port}/v1")
    monkeypatch.setenv("NO_PROXY", "127.0.0.1")
    monkeypatch.setenv("no_proxy", "127.0.0.1")
    try:
        yield requests, disconnected
    finally:
        stop.set()
        server.shutdown()
        server.server_close()
        thread.join(timeout=1)
        assert not thread.is_alive()


def _track_answer_processes(monkeypatch, *, startup_delay_s=0):
    processes = []
    popen = subprocess.Popen

    def tracked(command, *args, **kwargs):
        if startup_delay_s:
            command = list(command)
            assert command[1] == "-c"
            command[2] = f"import time; time.sleep({startup_delay_s!r}); " + command[2]
        process = popen(command, *args, **kwargs)
        processes.append(process)
        return process

    monkeypatch.setattr(subprocess, "Popen", tracked)
    return processes


def _assert_reaped(processes):
    assert processes
    for process in processes:
        assert process.poll() is not None
        assert all(pipe.closed for pipe in (process.stdin, process.stdout, process.stderr))


@pytest.mark.parametrize("startup_delay_s", [0, 2], ids=["cold-sdk", "delayed-cold-sdk"])
def test_live_answer_trickle_obeys_total_deadline_and_closes_socket(
    tmp_path, monkeypatch, startup_delay_s,
):
    # Removing the outer process deadline must fail even though every socket
    # read receives another byte well before its inactivity timeout.
    # Cold imports/client setup can exceed 1.5 s under parallel build/test load.
    # This allowance is part of the wall budget, not an extension in production.
    startup_allowance_s = 5
    budget = startup_allowance_s + 1
    cfg = BenchConfig(base_dir=tmp_path, live_model="explicit-model", limit=1,
                      live_budget_s=budget)
    processes = _track_answer_processes(monkeypatch, startup_delay_s=startup_delay_s)
    with _answer_server(
        monkeypatch, ["trickle"], trickle_duration_s=budget + 1,
    ) as (requests, disconnected):
        started = time.monotonic()
        answer = run_qa._default_answer_llm(cfg)
        with pytest.raises(TimeoutError, match="wall budget"):
            answer("system", "user")
        elapsed = time.monotonic() - started
        assert budget - 0.1 <= elapsed <= budget + 0.6, elapsed
        assert len(requests) == 1, "deadline must actually interrupt an in-flight request"
        assert disconnected.wait(0.6), "killed answer worker must close its HTTP socket"
        _assert_reaped(processes)
        print(f"answer startup delay={startup_delay_s:.3f}s "
              f"wall cap={budget:.3f}s measured={elapsed:.3f}s "
              f"tolerance=0.600s; worker reaped, IPC closed, HTTP peer disconnected")
        with pytest.raises(TimeoutError, match="wall budget"):
            answer("system", "user")
        assert len(processes) == len(requests) == 1


def test_live_answer_startup_timeout_does_not_send_or_restart(tmp_path, monkeypatch):
    budget = 0.5
    cfg = BenchConfig(base_dir=tmp_path, live_model="explicit-model", limit=1,
                      live_budget_s=budget)
    processes = _track_answer_processes(monkeypatch, startup_delay_s=2)
    with _answer_server(monkeypatch, ["normal"]) as (requests, _):
        started = time.monotonic()
        answer = run_qa._default_answer_llm(cfg)
        with pytest.raises(TimeoutError, match="wall budget"):
            answer("system", "user")
        elapsed = time.monotonic() - started
        assert budget - 0.1 <= elapsed <= budget + 0.6, elapsed
        assert requests == [], "startup must consume the deadline before any HTTP request"
        _assert_reaped(processes)
        with pytest.raises(TimeoutError, match="wall budget"):
            answer("system", "user")
        assert len(processes) == 1, "no worker may start after deadline exhaustion"
        assert requests == []


def test_live_answer_normal_and_provider_error_paths_are_preserved(tmp_path, monkeypatch):
    cfg = BenchConfig(base_dir=tmp_path, live_model="explicit-model", limit=2,
                      live_budget_s=10)
    data = [
        {"question_id": "bad", "question": "What color?"},
        {"question_id": "good", "question": "What color?"},
    ]
    processes = _track_answer_processes(monkeypatch)
    with _answer_server(monkeypatch, ["error", "normal"]) as (requests, _):
        out = run_qa.run_qa(
            data, cfg, engine_factory=lambda **kw: _FakeEngine([_FakeResult("blue", 0.9)]),
        )
        assert [json.loads(line) for line in out.read_text().splitlines()] == [
            {"question_id": "good", "hypothesis": "blue"},
        ]
        failures = json.loads((cfg.results_dir / "run_report.json").read_text())["failures"]
        assert len(failures) == 1
        assert failures[0]["question_id"] == "bad"
        assert "RateLimitError" in failures[0]["error"]
        assert "fixture rate limit" in failures[0]["error"]
        assert len(requests) == 2, "429 must not trigger an SDK retry"
        assert all(request["model"] == "explicit-model" for request in requests)
        assert requests[1]["messages"][1]["content"].endswith("Question: What color?")
        _assert_reaped(processes)
