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

Two numbers, because they answer different questions:

| metric | meaning |
|---|---|
| cold ms | first invocation on a freshly copied corpus; cold page cache |
| warm p50 ms | steady state on a hot file, i.e. what a working session pays per tool call |

The hook spawns a **fresh process per event**, so cold/warm both matter: cold is
the first call of a session, warm is every call after it.

The in-process stage breakdown (`scope`/`normalize`/`resolve`/`record`/`edges`)
excludes process startup entirely, which is how you tell process tax apart from
real write work. It is validated by `drift`: if the sum of stages deviates more
than 20% from a real `RecordOperation`, the report flags the breakdown as
untrustworthy for that scale.

## Headline findings

Measured on this repo, darwin/arm64, Go 1.27.2:

- **Process tax dominates.** A typical event costs about 30ms wall clock end to
  end while the write itself takes roughly 0.3ms. Nearly all perceived latency is
  process startup plus SQLite open, not memory work.
- **CJK segmentation is the one real hotspot.** A 2000-rune Chinese prompt costs
  about 348ms against a ~30ms baseline, and the cost is linear in character count
  at roughly 0.1ms per character. The same length in English adds almost nothing.
  Every hook payload containing Chinese pays this, and prompts are capped at 2000
  runes, so worst case is bounded but large.
- **Write cost grows with corpus size**: about 0.28ms at 100 rows, 0.37ms at
  1k, 1.7ms at 10k. Small and medium are equal within noise, so a fixed
  engine-and-schema-open cost dominates until roughly a thousand rows.
- **Ignored events are cheap.** An unknown event exits in roughly 10ms versus
  ~30ms, because it never opens the engine.

## Caveats

- Corpora are synthetic: log-normal content lengths with a mixed vocabulary
  including CJK. Real libraries mix L1/L2/L4 far more richly.
- Absolute durations are machine-specific. Compare against a baseline from the
  same box; do not compare CI numbers to a laptop.
- Cl100kBase counts are comparable across fixtures and scales, not billing
  accurate for any vendor.
- No LLM token figure appears here. The write path never calls one, and provider
  usage statistics do not exist in this codebase.
