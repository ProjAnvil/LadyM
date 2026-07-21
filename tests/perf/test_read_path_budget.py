"""NFR-1 guard: engine-overhead portion of the read path must be < 10ms p95 @ 200 memories.

Measures total ``recall()`` time minus the ``embed(query)`` cost so we isolate the
engine overhead (vector search, activation scoring, reflection gate) from the
embedding provider cost.
"""

import time

from ladym.config import Config
from ladym.engine import Engine


def test_read_path_engine_overhead_p95_under_10ms(tmp_path):
    eng = Engine(Config.for_testing(tmp_path))
    try:
        for i in range(200):
            eng.semantic.put_fact(f"fact number {i} about topic {i % 10}")
        # warm
        eng.recall("topic 0")
        # measure: subtract hashing embed cost (precompute query vec) to isolate engine overhead
        prov = eng.provider
        samples = []
        for i in range(100):
            q = f"topic {i % 10}"
            # engine overhead = total - embed time
            t_embed = _time_embed(prov, q)
            t0 = time.perf_counter()
            eng.recall(q)
            total = (time.perf_counter() - t0) * 1000
            samples.append(max(0.0, total - t_embed))
        p95 = _percentile(samples, 95)
        assert p95 < 10.0, f"engine overhead p95 {p95:.2f}ms > 10ms"
    finally:
        eng.close()


def _time_embed(prov, q, n=20):
    t0 = time.perf_counter()
    for _ in range(n):
        prov.embed(q)
    return (time.perf_counter() - t0) * 1000 / n


def _percentile(xs, p):
    xs = sorted(xs)
    k = int(round((p / 100) * (len(xs) - 1)))
    return xs[k]
