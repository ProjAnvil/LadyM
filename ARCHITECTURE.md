# LadyM

> A brain-inspired, multi-tier memory framework for LLM agents and codebase RAG.
> Implemented in Python 3.11+ with `uv`, local-first storage, MCP/Skill/SDK/CLI front-ends.

LadyM answers one pain: **agents re-read and re-grep the same files every turn**. Instead it
caches the workspace's *understanding* — code analysis, decisions, skills, episodes — into a
hierarchical, consolidating, decaying memory that any agent (MCP tool, Claude Code Skill,
Python SDK, CLI) can recall with a keyword.

The design is a synthesis of seven state-of-the-art references:

| Reference | What we borrow |
|---|---|
| [Anthropic — Effective Context Engineering](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents) | Context = finite resource; compaction, note-taking, just-in-time retrieval, progressive disclosure |
| [MemGPT / Letta](https://arxiv.org/abs/2310.08560) | OS-style memory hierarchy (working = RAM, archival = disk); agent self-manages via tool calls |
| [mem0 (arXiv 2504.19413)](https://arxiv.org/html/2504.19413v1) | Two-stage extract→update pipeline; `ADD/UPDATE/DELETE/NOOP` decisions; dual vector + graph stores; entity linking via embedding similarity |
| [A-MEM (arXiv 2502.12110)](https://arxiv.org/pdf/2502.12110) | Zettelkasten linked atomic memories; periodic consolidation prevents unbounded growth |
| [Zep / Graphiti](https://blog.getzep.com/) | Temporal validity on edges ("true from t1 to t2") |
| [**HyMem (arXiv 2602.13933)** — the "hy-memory" the user asked for](https://arxiv.org/html/2602.13933v2) | Dual-granularity storage (summary L1 + raw L2); two-tier dynamic retrieval (lightweight→deep); reflection-gated iteration; **cognitive economy** principle |
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

**The key novelty:** L2 is *one* layer that fuses general semantic facts and codebase
analysis. A code symbol and a user preference live in the same table, scored by the same
activation function, retrieved by the same recall pipeline. **Memory and codebase RAG are one
system, not two** — exactly the user's framing.

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
high-activation hits). Per the hard read-path constraint (NFR-3), it is **heuristic-only**; no
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
associative graph, and (via the `sqlite-vec` loadable extension) the vector index. No server,
no Docker, no API keys required for the default config.

```
memories(id, layer, type, content, content_vec[BLOB], summary, tags[JSON],
         metadata[JSON], source, created_at, updated_at, last_access_at,
         access_count, activation, hash)
edges(id, src_id, relation, dst_id, valid_from, valid_to, weight, metadata[JSON])
code_symbols(memory_id PK→memories, file_path, symbol_kind, qualified_name,
             signature, docstring, line_start, line_end, body_hash)
code_refs(src_symbol, dst_symbol, ref_kind)              # calls / imports / defines
index_state(file_path, body_hash, indexed_at)            # incremental re-index
```

A pure-numpy `InMemoryVectorIndex` is also provided for unit tests and tiny workspaces.

## 6. Embedding providers (pluggable)

| Provider | When to use | Deps |
|---|---|---|
| `HashingEmbedding` (default) | CI, tests, offline, deterministic | none |
| `SentenceTransformerEmbedding` | Local quality, private | `pip install ladym[local]` |
| `OpenAIEmbedding` | Best hosted quality | `pip install ladym[openai]` |

The default hashing provider makes the whole test suite runnable with **zero network and zero
model downloads** — critical for reproducible CI.

## 7. Code indexing sub-system (the codebase RAG half of L2)

```
walk repo (respect .gitignore)
    └── for each source file
          ├── hash body → skip if unchanged (incremental)
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

Supported languages (via `tree-sitter-language-pack`): Python, JavaScript, TypeScript, Go,
Rust, Java, C, C++, etc. Languages without a grammar degrade gracefully to line-based chunking.

## 8. Front-ends (how agents talk to LadyM)

All front-ends call the same `Engine` so behaviour is identical:

- **MCP server** — tools `recall`, `remember`, `search_code`, `index_code`, `consolidate`,
  `stats`, `link`, `forget`. Drop into any MCP-aware agent (Claude Code, Cursor, …).
- **Claude Code Skill** — `skills/ladym-recall.md` plus a thin Bash wrapper, so a skill can
  pull workspace memory into context with one keyword.
- **Python SDK** — `from ladym import Engine; eng.recall(...)`.
- **CLI** — `ladym recall "auth flow"`, `ladym index ./src`, `ladym remember "..."`,
  `ladym stats`, `ladym consolidate`.

## 9. Project layout

```
src/ladym/
├── config.py            # dataclass config, env overrides
├── schema.py            # pydantic models: Memory, CodeSymbol, Edge, Recall, ...
├── storage/
│   ├── store.py         # SQLiteStore: relational + json + graph + sqlite-vec
│   ├── embeddings.py    # pluggable EmbeddingProvider + Hashing default
│   └── vector_index.py  # SQLiteVecIndex + InMemoryVectorIndex
├── layers/
│   ├── working.py       # L0
│   ├── episodic.py      # L1
│   ├── semantic.py      # L2 (general + code)
│   ├── procedural.py    # L3
│   └── associative.py   # L4
├── operations/
│   ├── activation.py    # ACT-R scoring
│   ├── recall.py        # two-tier retrieval + reflect gate
│   ├── consolidate.py   # L1 → L2 with ADD/UPDATE/DELETE/NOOP
│   ├── proceduralize.py # L1 clusters → L3 playbooks
│   └── decay.py         # base-level forgetting
├── code/
│   ├── indexer.py       # tree-sitter driver
│   ├── languages.py     # grammar registry + chunking fallback
│   └── symbol_graph.py  # cross-reference extraction
├── engine.py            # orchestrator — the single entry point
├── sdk.py               # convenience facade over Engine
├── cli.py               # typer app
└── mcp/
    └── server.py        # MCP tool server
tests/
├── unit/                # one file per module
├── integration/         # end-to-end on a fixture repo
└── fixtures/sample_repo/
skills/ladym-recall.md
```

## 10. Why this design is sound (mapping back to evidence)

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

See [README.md](README.md) for usage and [tests/](tests/) for the executable specification.
