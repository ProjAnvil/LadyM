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

### Before / after (default `dict` mode)

Baseline is `main@220f187`, re-measured **back-to-back** with the fix on the same
box within a two-minute window. That matters: absolute durations here swing by
2x with machine load, and the numbers originally published in this file
(10.6 / 13.2 / 30.9ms) were taken on a different day — they are 2–6ms off this
box's baseline and must not be compared against.

| scale | spawn p50 before | after | engine open before | after |
|---|---|---|---|---|
| 100 rows | 18.73ms | 18.48ms | 0.95ms | **0.54ms** |
| 1k rows | 23.16ms | **19.22ms** | 3.45ms | **0.54ms** |
| 10k rows | 44.95ms | **22.88ms** | 21.42ms | **0.66ms** |

- **Engine open is now flat in corpus size** — 0.54 / 0.54 / 0.66ms instead of
  0.95 / 3.45 / 21.42ms. It used to be the whole O(n) cost: `NewStore` called
  `warmIndexFromBlobs()` unconditionally, which SELECTs every embedding and
  re-upserts it into the in-memory index, once per process. Warming now happens
  on the first `VectorSearch`, and the steady-state hook write never issues one
  (resource resolution hits its exact-alias stage first).
- **The spawn win scales with corpus size, and at 100 rows it is ~2%.** That is
  the honest floor: at 100 rows the invocation is startup-bound, and the only
  thing removed is a 0.4ms warm out of ~18.7ms. The 1.3% measured difference is
  inside the noise. Don't expect the small-library number to move.
- **Resolve, record, and edges are unchanged** (resolve: 0.063 / 0.206 / 2.606ms
  after versus 0.066 / 0.214 / 2.193ms before — the 10k figure is noise, not a
  regression). The fix removed startup work; it did not make the write cheaper.
- **Ignored events are still cheap.** An unknown event exits in roughly 10ms
  because it never opens the engine.

Measured on a quieter machine in the same session, the same comparison read
14.96 → 13.18ms (100 rows), 17.04 → 12.30ms (1k), 36.91 → 14.07ms (10k), with
engine open 0.87 / 2.67 / 18.96 → 0.45 / 0.38 / 0.41ms. Same shape, different
absolute values — which is the point about comparing only against a same-box,
same-window baseline.

### CJK: the cost was a dictionary load, not per-character work

An earlier reading here said "0.1ms per character, ~348ms for a 2000-rune
prompt". That was wrong. Measured again:

| input | first call in a process | steady state |
|---|---|---|
| 2000 Han runes | 592ms | 4.7ms |
| 2000 ASCII chars | — | 0.077ms |

Splitting the 592ms: reading the two dictionary files is 5.4ms, splitting lines
is 28ms, and **building gse's cedar trie over 352k entries is ~520ms**. It is a
one-off per process, paid the first time any call touches CJK text. Steady-state
segmentation is only ~2.4µs per character.

That distinction matters: a segmentation cache cannot fix this, because the cost
is not repeat work.

`embedding.cjk_tokenizer` selects what to do about it. Both columns are
`claude/prompt-cjk` — a 3000-rune Chinese prompt truncated to the 2000-rune
ceiling — measured back-to-back in the same window.

| scale | `dict` (default) | `chars` |
|---|---|---|
| 100 rows | 635.9ms | **17.38ms** |
| 1k rows | 614.4ms | **20.37ms** |
| 10k rows | 605.2ms | **18.76ms** |

A ~35x reduction, and a Chinese prompt now costs what an English one does.

`chars` never loads the dictionary: it emits one token per rune, and `features()`
adds adjacent-pair bigrams on top. Retrieval quality does not regress — on a
40-document / 40-short-query Chinese probe the character path scored MRR 1.000
against 0.988 for the dictionary path, which matches the standing result that
character bigrams beat words at high dimensionality. Chinese BERT also tokenizes
per character.

The default stays `dict` because that is what every existing database was
written with; switching silently would make stored CJK vectors unsearchable.
The mode is recorded under the `embedding_tokenizer` meta key, so switching is
detected and routed through the same re-embed path as a dim change
(`embedding.allow_dim_change=true`, else a hard error).

### What was measured and rejected

- **Bulk-loading the vector index.** At 10k rows the warm splits into 9.1ms of
  SELECT and 8.6ms of decode+upsert, so batching could only cut ~4ms off a cost
  that no longer runs per invocation. Not built.

### Caveat on the `chars` numbers

The `chars` table above measures write latency only. The mode changes what
`storage.Tokenize` emits, and those tokens also feed recall's coverage scoring
and the per-memory token statistics, so switching modes moves those numbers for
CJK text as well. The retrieval ablation quoted above used the hashing
embedding; it did not measure `reflect`'s coverage verdict. ASCII tokenization
is identical in both modes.

Worth reading together: per-invocation cost used to be a function of how much
history you had (~19ms at 100 rows, ~45ms at 10k). It is now roughly a constant
~18–23ms. The remaining gap to the ~0.4–3ms real write is process startup, which
no amount of index work will remove — and at 100 rows that startup was already
almost all of it, which is why the fix barely registers there.

## Caveats

- Corpora are synthetic: log-normal content lengths with a mixed vocabulary
  including CJK. Real libraries mix L1/L2/L4 far more richly.
- Absolute durations are machine-specific. Compare against a baseline from the
  same box; do not compare CI numbers to a laptop.
- Cl100kBase counts are comparable across fixtures and scales, not billing
  accurate for any vendor.
- No LLM token figure appears here. The write path never calls one, and provider
  usage statistics do not exist in this codebase.
