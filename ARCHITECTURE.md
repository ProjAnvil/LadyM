# LadyM

> A brain-inspired, multi-tier memory framework for LLM agents and codebase RAG.
> Implemented in **Go** as a single static binary with zero cgo — local-first SQLite
> storage, MCP/Skill/Go SDK/CLI front-ends.

LadyM answers one pain: **agents re-read and re-grep the same files every turn**. Instead it
caches the workspace's *understanding* — code analysis, decisions, skills, episodes — into a
hierarchical, consolidating, decaying memory that any agent (MCP tool, Claude Code Skill,
Go SDK, CLI) can recall with a keyword.

The design is a synthesis of seven state-of-the-art references:

| Reference | What we borrow |
|---|---|
| [Anthropic — Effective Context Engineering](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents) | Context = finite resource; compaction, note-taking, just-in-time retrieval, progressive disclosure |
| [MemGPT / Letta](https://arxiv.org/abs/2310.08560) | OS-style memory hierarchy (working = RAM, archival = disk); agent self-manages via tool calls |
| [mem0 (arXiv 2504.19413)](https://arxiv.org/html/2504.19413v1) | Two-stage extract→update pipeline; `ADD/UPDATE/DELETE/NOOP` decisions; dual vector + graph stores; entity linking via embedding similarity |
| [A-MEM (arXiv 2502.12110)](https://arxiv.org/pdf/2502.12110) | Zettelkasten linked atomic memories; periodic consolidation prevents unbounded growth |
| [Zep / Graphiti](https://blog.getzep.com/) | Temporal validity on edges ("true from t1 to t2") |
| [**HyMem (arXiv 2602.13933)**](https://arxiv.org/html/2602.13933v2) | Dual-granularity storage (summary L1 + raw L2); two-tier dynamic retrieval (lightweight→deep); reflection-gated iteration; **cognitive economy** principle |
| [CoALA (arXiv 2309.02427)](https://arxiv.org/html/2309.02427v3) + [ACT-R/SOAR](https://advancesincognitivesystems.github.io/acs2021/data/ACS-21_paper_6.pdf) | Declarative vs procedural split; skill library; chunk activation `A_i = base + context + noise`; base-level decay |

And from codebase RAG best practice
([RepoGraph](https://arxiv.org/html/2410.14684v1),
[code-graph-rag](https://github.com/vitali87/code-graph-rag),
[CocoIndex](https://cocoindex.io/blogs/index-code-base-for-rag/)):
**tree-sitter** AST, symbol cross-references, hybrid semantic + structural retrieval, incremental
re-indexing.

---

## 1. The five memory layers

Borrowed from neuroscience's modal model + complementary learning systems + ACT-R:

```
                  ┌─────────────────────────────────────────────┐
   write path ──▶ │ L0  Working / Sensory   (context window)    │  volatile, in-process
                  │ L1  Episodic            (what/where/when)   │  time-stamped events
                  │ L2  Semantic            (facts + code)      │  consolidated knowledge
                  │ L3  Procedural          (how-to skills)     │  playbooks, verified snippets
                  │ L4  Associative         (Zettelkasten graph)│  bidirectional links
                  └─────────────────────────────────────────────┘
                                              ▲
   read path ◀── recall(query) : two-tier lightweight→deep, reflection-gated
```

| Layer | Brain analogue | Stores | TTL / decay | Write trigger |
|---|---|---|---|---|
| **L0 Working** | Prefrontal cortex / working memory | In-flight scratch notes, compaction summaries | Process lifetime | Agent calls `remember()` |
| **L1 Episodic** | Hippocampus | `(timestamp, agent, action, observation, outcome)` tuples | Base-level decay (ACT-R), ~weeks | Every notable event |
| **L2 Semantic** | Neocortex | Consolidated facts *and* **code analysis** (file summaries, symbol docs, API descriptions) | Long; replaced not deleted (temporal edges) | `consolidate()` batch + `index_code()` |
| **L3 Procedural** | Basal ganglia / motor cortex | Numbered playbooks: `preconditions → steps → expected_outcome`; verified code snippets | Long; versioned | `proceduralize()` when episodes recur |
| **L4 Associative** | Associative cortex | Edges `(src, relation, dst, valid_from, valid_to)` between any memory items | Edge-level temporal validity | Auto-link on write + explicit `link()` |

On top of the L0–L4 store, a **System 2 worker** (`ladym worker`) asynchronously extracts
**L5 mental models** (cognitive frameworks abstracted from recurring episodes) and **L6
forward intents** (predictive next-actions). Both are skipped when no LLM is configured.

**The key novelty:** L2 is *one* layer that fuses general semantic facts and codebase
analysis. A code symbol and a user preference live in the same table, scored by the same
activation function, retrieved by the same recall pipeline. **Memory and codebase RAG are one
system, not two.**

## 2. Cognitive operations

These map 1:1 to brain functions and to the SOTA primitives above.

| Operation | Brain analogue | What it does | SOTA provenance |
|---|---|---|---|
| `encode()` | Perception → working memory | Push a note/event into L0/L1 write buffer | mem0 extract |
| `consolidate()` | Hippocampal replay (sleep) | Batch L1 episodes → L2 facts via LLM `ADD/UPDATE/DELETE/NOOP`; merges duplicates | mem0 two-stage + A-MEM consolidate |
| `index_code()` | (engineered) | Walk repo, parse with tree-sitter, extract symbols, embed, store as L2 code items; incremental | RepoGraph / code-graph-rag |
| `proceduralize()` | Skill acquisition | Cluster recurring successful episodes → L3 playbook | CoALA / Voyager |
| `recall()` | Retrieval | Two-tier query (lightweight→deep), reflection-gated | HyMem |
| `link()` | Association | Add an L4 edge (Zettelkasten) | A-MEM |
| `forget()` | Synaptic pruning | Decay or explicit deletion of low-activation items | ACT-R base-level decay |
| `reflect()` | Metacognition | After recall, decide if a deeper tier / more rounds are needed | HyMem reflection module |

## 3. Two-tier retrieval (HyMem cognitive economy)

HyMem shows ~70% of queries are answerable from cheap summary retrieval; only ~30% need
expensive deep retrieval. LadyM implements the same routing:

```
recall(query):
    # Tier 1 — lightweight (always runs)
    items = vector_search(query, top_k=k_light)              # across L1+L2+L3
    items = rerank_by_activation(items, query)               # ACT-R score
    if reflect(query, items).sufficient:                     # cheap self-check
        return items

    # Tier 2 — deep (only if Tier 1 insufficient)
    expanded = graph_expand(items, hops=2)                   # L4 associative
    raw     = backtrack_to_source(expanded)                  # HyMem L1→L2 pointer
    code    = fetch_symbol_context(expanded)                 # callers/callees, file
    return rerank_by_activation(items ∪ expanded ∪ raw ∪ code, query)
```

The `reflect()` gate uses a tiny heuristic by default (coverage of query terms + count of
high-activation hits). Per the hard read-path constraint, it is **heuristic-only**; no
LLM judge is ever invoked in the read path.

## 4. Activation function (ACT-R inspired)

Every memory item carries a scalar activation that biases retrieval:

```
A_i = α·sim(query, item)                 # semantic similarity (cosine)
    + β·recency(now, last_access)        # base-level recency
    + γ·log(1 + access_count)            # base-level frequency
    + δ·graph_centrality(item)           # associative boosting (L4)
    + ε·type_boost                       # layer/type prior (e.g. code_symbol for code queries)
    + noise
```

Defaults `α=1.0, β=0.3, γ=0.2, δ=0.15, ε` per query type. Tunable per call.

## 5. Storage layout (local-first)

One SQLite file (`ladym.db`) holds everything — relational metadata, JSON blobs, the
associative graph, and the embedding vectors. No server, no Docker, no API keys required
for the default config. SQLite runs on [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)
(pure Go, no cgo); **WAL mode is on by default** and `PRAGMA busy_timeout = 10000` is pinned,
so the MCP server, the System 2 worker, and one-shot CLI commands can share one db file
across processes.

```
memories(id, layer, type, content, summary, tags[JSON], metadata[JSON],
         source, workspace, created_at, updated_at, last_access_at,
         access_count, activation, content_hash, embedding[BLOB])
edges(id, src_id, relation, dst_id, weight, valid_from, valid_to, metadata[JSON])
code_symbols(memory_id PK→memories, file_path, symbol_kind, qualified_name,
             signature, docstring, line_start, line_end, language)
code_refs(src_symbol, dst_symbol, ref_kind)              # calls / imports / defines
index_state(file_path, body_hash, indexed_at)            # incremental re-index
meta(key, value)
```

Vectors are persisted as little-endian float32 BLOBs in `memories.embedding` and warmed
into an **in-process brute-force cosine index** (`storage/vector_index.go`) when the store
opens: vectors are L2-normalised on insert, so search is one dot product per row plus a
deterministic top-k sort. This replaces the `sqlite-vec` native extension used by the
Python port, keeping the binary cgo-free; `prefer_sqlite_vec` is still accepted in config
for parity but has no effect.

## 6. Embedding providers (pluggable)

| Provider | When to use | Config |
|---|---|---|
| `hashing` (default) | CI, tests, offline, deterministic | `LADYM_EMBEDDING=hashing` |
| `openai` | Hosted quality (OpenAI-compatible endpoints) | `LADYM_EMBEDDING=openai` |
| `ollama` | Local quality, private | `LADYM_EMBEDDING=ollama` |
| `http` | Any OpenAI/Ollama-compatible HTTP endpoint | `LADYM_EMBEDDING=http` + base URL |

The default hashing provider makes the whole test suite runnable with **zero network and
zero model downloads** — critical for reproducible CI. (The Python port's
sentence-transformers provider was dropped in the Go rewrite; point `http`/`ollama` at a
local embedding server instead.)

## 7. Code indexing sub-system (the codebase RAG half of L2)

```
walk repo (respect .gitignore)
    └── for each source file
          ├── hash body → skip if unchanged (incremental, via index_state)
          ├── parse AST with tree-sitter (language auto-detected)
          ├── extract symbols: module / class / function / method
          │     with qualified_name, signature, docstring, span
          ├── extract cross-refs: calls, imports, defines  → code_refs
          ├── build per-symbol text = signature + docstring + first 40 body lines
          ├── embed text → memories(L2, type=code_symbol)
          └── file-level summary → memories(L2, type=code_file)
```

`recall("auth login flow")` then returns the relevant symbols *and* their callers/callees via
the L4 graph expansion — no `grep` or `read` needed in the agent's turn.

Parsing uses [gotreesitter](https://github.com/odvcencio/gotreesitter), a pure-Go tree-sitter
runtime that loads the same parse tables as upstream tree-sitter — full symbol extraction
for Python, JavaScript, TypeScript, Go, Rust, Java, C, C++; languages without a grammar
spec degrade gracefully to line-window chunking.

Indexing takes a **cross-process single-flight lock** (`<db>.index.lock`, `flock`
non-blocking): a second concurrent indexer fails fast with `IndexInProgressError` instead
of thrashing the same database.

## 8. Front-ends (how agents talk to LadyM)

All front-ends call the same `engine` package so behaviour is identical:

- **MCP server** (`ladym serve`, JSON-RPC 2.0 over stdio) — nine tools: `recall`,
  `remember`, `record_event`, `search_code`, `index_code`, `consolidate`, `stats`, `link`,
  `forget`. Drop into any MCP-aware agent (Claude Code, Cursor, Kimi Code, …).
- **CLI** (cobra) — `ladym recall "auth flow"`, `ladym index ./src`, `ladym remember "..."`,
  `ladym record`, `ladym consolidate`, `ladym worker`, `ladym stats`, `ladym config`.
- **Go SDK** — the `ladym` package is a one-import facade:
  `eng, _ := ladym.NewEngine(ladym.DefaultConfig()); resp, _ := eng.Recall(...)`,
  plus one-shot helpers (`ladym.Recall`, `ladym.Remember`, `ladym.IndexCode`) and
  `ladym.NewEngineWithModels` for host-model injection (`adapter.ModelRouting`,
  langchain-golang partners).
- **Claude Code Skill** — `skills/ladym-recall.md`, so a skill can pull workspace memory
  into context with one keyword.
- **LangGraph (Go)** — the `langgraph` package: `CreateTools` for ReAct-style agents,
  `CreateRecallNode`/`CreateRetainNode` for automatic per-turn memory injection, with
  per-user workspace isolation via `WorkspaceFromUserID`.
- **Management console** — `ladym serve --http` serves the embedded Vue 3 SPA
  (`console/`, go:embed'ed `console/dist`) at `/`: login, memory CRUD, user admin
  and stats against the `/api/*` data plane (optional Basic auth).

## 9. Project layout

```
github.com/ProjAnvil/LadyM
├── cmd/ladym/        CLI entry point
├── cli/              cobra CLI (remember/record/recall/index/consolidate/stats/forget/link/serve/worker/config)
├── mcp/              MCP server (JSON-RPC 2.0 over stdio)
├── api/              HTTP data plane (serve --http): /api/* endpoints, Basic auth, embedded console at /
├── console/          management console (Vue 3 + Vite; dist/ committed, go:embed'ed)
├── engine/           the Engine orchestrator — single entry point for all front-ends
├── operations/       activation, recall, consolidate, decay, proceduralize, supersedes, attention, L5, L6, system2
├── layers/           L0–L4 memory layers
├── providers/        LLM provider abstraction + per-op agent registry
├── adapter/          ModelRouting + langchain-golang model wrapping
├── langgraph/        LangGraph tools/nodes integration (Go)
├── storage/          SQLite store, in-process vector index, embedding providers
├── code/             codebase indexer (gotreesitter symbols + chunk fallback + cross-process lock)
├── config/           Config structs, TOML+env loading, secret stripping
├── secrets/          AES-256-GCM encrypted secret store (~/.ladyM/)
├── schema/           core data models
├── ladym/            SDK facade (one-import surface)
├── wrapper/py/       Python MCP client for the Go server
├── benchmarks/       LongMemEval suite driving the Go engine
├── scenarios/        executable end-to-end agent scenarios (living spec)
└── scripts/          install.sh and friends
```

`go test ./...` is hermetic: it exercises the deterministic hashing embedding and needs
no network, no model downloads, and no API keys.

## 10. Implementation notes (deviations from the Python port)

The Go rewrite is behaviourally at parity with the 0.2.x Python implementation — covered
by tests including **cross-language golden vectors** (tokenizer, `HashingEmbedding`,
`content_hash`, and the HKDF-derived AES key are byte-for-byte identical). The remaining
differences are deliberate trade-offs:

| Python (0.2.x, `python` branch) | Go (≥ 0.3.0) |
|---|---|
| `sqlite-vec` loadable extension (persistent ANN) | pure-Go `InMemoryVectorIndex` (brute-force cosine); vectors persisted as BLOBs and warmed on reopen — behaviour identical, `prefer_sqlite_vec` accepted but inert |
| tree-sitter + `tree-sitter-language-pack` (native) | gotreesitter — pure-Go runtime loading the same parse tables; same AST-level extraction, no cgo |
| sentence-transformers (`embedding.provider="st"`) | returns a clear error — use `provider="http"`/`"ollama"` against a local endpoint |
| langchain `ChatOpenAI`/`ChatAnthropic`/`ChatOllama` | [langchain-golang](https://github.com/ProjAnvil/langchain-golang) partner chat models with provider-native structured output; `llm.provider="http"` keeps the hand-rolled net/http client as an escape hatch |
| langgraph integration (`ladym.langgraph`, Python) | `langgraph` Go package: `CreateTools` / `CreateRecallNode` / `CreateRetainNode` |
| OpenAI endpoint | langchain-golang's OpenAI partner defaults to the Responses API; LadyM switches it to `/chat/completions` for parity with OpenAI-compatible servers |
| `structured_method=function_calling` on OpenAI | native json_schema response_format instead of function calling; equivalent-or-stricter |
| sqlite3 (C) | `modernc.org/sqlite` (pure Go) — preserves the "no native toolchain" property; slower for very large bulk indexing |

## 11. Why this design is sound (mapping back to evidence)

- **Layered, not flat** — every ablation in the surveyed work favours layers over monolithic
  stores: HyMem −17.4% without L2 deep layer; A-MEM worse without Zettelkasten links; CoALA
  needs the declarative/procedural split.
- **Two-tier retrieval pays off** — HyMem: 92.6% less compute, +2% accuracy vs full context.
- **Consolidation is non-negotiable** — A-MEM shows performance degrades without periodic
  graph maintenance; mem0's `ADD/UPDATE/DELETE/NOOP` is the proven primitive.
- **Code is just typed semantic memory** — RepoGraph/code-graph-rag confirm tree-sitter
  symbols + cross-ref graph + embeddings beat flat chunking for repo-level retrieval.
- **Local-first, deterministic tests** — the hashing embedding keeps the test suite hermetic,
  which is what makes "enough tests to back it up" actually feasible.

---

See [README.md](README.md) for usage; the per-package Go tests and [scenarios/](scenarios/)
are the executable specification.
