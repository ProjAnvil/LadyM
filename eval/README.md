# eval — hook write-path evaluation harness

Measures the LadyM hook **write** path: does a harness event actually persist,
and what does that write cost in tokens and duration.

Run it:

```bash
make eval-hook                      # builds bin/ladym, then runs the matrix
LADYM_EVAL_TRIALS=6 make eval-hook  # more trials, stabler percentiles
```

Output lands in `eval/.cache/reports/<timestamp>/{metrics.json,report.md}`.

Everything here lives behind the `eval` build tag, so the default `make test`
compiles none of it and pays no cost.

## What it measures

### Fidelity — does the write actually land?

18 fixtures drive the **real binary** (fork/exec, not an in-process call) and
assert what ended up in SQLite, read directly rather than through the engine so
the harness sees what any third-party observer would. Coverage: both harnesses,
all six registered events, both truncation ceilings, rune-based truncation on
CJK, object-shaped tool responses, resource present and absent, unknown events.

### Tokens — how much text persists per event?

Per persisted row: content plus summary plus metadata, encoded with Cl100kBase.
The vocabulary ships as compiled Go source inside `tiktoken-go/tokenizer`, so
counting needs no network and no API key.

`amplification` divides persisted tokens by the tokens the harness sent on stdin.
It runs 1.5x–2.3x for ordinary events because `layers.EpisodicMemory.Record`
writes observation and outcome into **both** Content and Metadata.

### Duration — what does the agent wait for?

| metric | meaning |
|---|---|
| spawn p50 ms | what the agent waits for: fork through process exit |
| process tax ms | spawn p50 minus the real in-process write: cost unrelated to writing memory |
| engine open ms | cost of opening the engine, paid once per invocation |

The hook spawns a **fresh process per event**, so every one of these is paid once
per tool call.

The in-process stage breakdown (`scope`/`normalize`/`resolve`/`record`/`edges`)
excludes process startup entirely, which is how you tell process tax apart from
real write work. It is validated by `drift`: if the sum of stages deviates more
than 20% from a real `RecordOperation`, the report flags the breakdown as
untrustworthy for that scale.

Note on what is *not* measured: the harness copies each corpus file immediately
before probing it, so the file is always in page cache. There is no meaningful
"cold cache" figure here, and none is reported.

## Headline findings

Measured on this repo, darwin/arm64, Go 1.27.2. `spawn` is what the agent waits
for; `real` is the write itself, measured in-process.

| scale | spawn p50 | process tax | engine open | resolve | record | real |
|---|---|---|---|---|---|---|
| 100 rows | 10.6ms | 10.3ms | 0.6ms | 0.039ms | 0.120ms | 0.26ms |
| 1k rows | 13.2ms | 12.8ms | 2.1ms | 0.128ms | 0.123ms | 0.33ms |
| 10k rows | 30.9ms | 29.3ms | 15.2ms | 1.348ms | 0.163ms | 1.59ms |

- **Process tax is essentially all of it.** At 10k rows the write takes 1.6ms
  while the agent waits 30.9ms: about 95% of perceived latency is cost that has
  nothing to do with writing memory.
- **Engine open dominates the tax, and it grows with corpus size** — 0.6ms, 2.1ms,
  then 15.2ms at 10k rows, where it is roughly half of everything the agent waits
  for. That is the signature of loading the vector index per invocation.
- **Resource resolution degrades with size** (0.039ms to 1.348ms), consistent with
  a scan over a growing index. Record and edges stay flat.
- **CJK segmentation is the one real hotspot.** A 2000-rune Chinese prompt costs
  about 348ms against a ~30ms baseline, and the cost is linear in character count
  at roughly 0.1ms per character. The same length in English adds almost nothing.
- **Ignored events are cheap.** An unknown event exits in roughly 10ms versus
  ~30ms, because it never opens the engine.

Worth reading together: per-invocation cost is not a constant. It is ~11ms on a
small library and ~31ms at 10k rows, and it will keep climbing with history.

## Caveats

- Corpora are synthetic: log-normal content lengths with a mixed vocabulary
  including CJK. Real libraries mix L1/L2/L4 far more richly.
- Absolute durations are machine-specific. Compare against a baseline from the
  same box; do not compare CI numbers to a laptop.
- Cl100kBase counts are comparable across fixtures and scales, not billing
  accurate for any vendor.
- No LLM token figure appears here. The write path never calls one, and provider
  usage statistics do not exist in this codebase.
