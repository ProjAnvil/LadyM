# LadyM

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Python 3.11+](https://img.shields.io/badge/python-3.11+-blue.svg)](https://www.python.org/)
[![Tests](https://img.shields.io/badge/tests-299-green.svg)](#testing)
[![MCP](https://img.shields.io/badge/MCP-compatible-purple.svg)](#mcp-server-claude-code-cursor-)
[![Storage](https://img.shields.io/badge/storage-local--first%20SQLite-success.svg)](#architecture)

**[English](README.md)** | [简体中文](README.zh-CN.md)

> A brain-inspired, multi-tier memory framework that lets LLM agents **recall** workspace
> knowledge and codebase analysis with one keyword — instead of re-`Read`-ing and
> re-`Grep`-ing the same files every turn.

LadyM caches your workspace's *understanding* — code analysis, decisions, skills, and
episodes — into a hierarchical, consolidating, decaying memory that any agent can recall
through a single keyword. Built with **uv + Python 3.11+**, **local-first SQLite +
sqlite-vec**, and **tree-sitter** for code indexing. Exposed via **MCP**, **Claude Code
Skill**, **Python SDK**, and **CLI** — all calling the same engine so behaviour is
identical everywhere.

---

## What's New in 0.2.1

- **Lean default install** — tree-sitter code indexing is now an optional `[codeindex]` extra.
  A bare `pip install` gives you the memory core with no native-parser baggage; add
  `'ladym[codeindex]'` only when you need `index_code` / `search_code`.
- **Inject your own langchain models** — pass already-configured `ChatOpenAI` / `OpenAIEmbeddings`
  straight to `Engine` via the new `ModelRouting` (typed per-op fields). No need to re-declare API
  keys and endpoints in ladyM's config. See [Injecting your own langchain models](#injecting-your-own-langchain-models).

---

## The problem

Today's coding agents have no long-term memory. Every turn they re-`Read` the same files
and re-`Grep` for the same symbols, burning the context window on rediscovery. Plain vector
RAG helps, but it forgets *why* a decision was made, *how* a task was done before, and
*what* the codebase actually looks like beyond flat chunks.

LadyM turns that throwaway rediscovery into **structured, evolving memory**.

## Why LadyM

| | What you get |
|---|---|
| 🧠 **Code memory + general memory in one layer** | L2 fuses tree-sitter code symbols and plain facts in a single store, scored by one activation function. Memory and codebase RAG are **one system, not two** — a unique spot no other framework occupies. |
| 🔒 **Local-first, zero-config start** | Default `HashingEmbedding` + `provider="none"` runs the moment install finishes: **no network, no model download, no API key**. One SQLite file holds everything. |
| 🧬 **Brain-inspired six layers + dual-path** | L0–L4 core store (working / episodic / semantic / procedural / associative) plus a System 2 worker that extracts **L5 mental models** and **L6 forward intents**, with a `supersedes` evolution chain so memories *evolve* instead of piling up. |
| 🔌 **Four fronts, one engine** | MCP server, Claude Code Skill, Python SDK, and CLI all call the same `Engine` — identical behaviour whether you're in Claude Code, Cursor, a script, or the terminal. |

## Quick start (30 seconds)

```bash
# 1. install (offline default — no extras, no network at runtime)
uv tool install .

# 2. index the codebase you keep re-grepping
ladym index ./src

# 3. ask a question — get the analysis back, no Read/Grep needed
ladym recall "how does password verification work" --code

# 4. store a fact for later
ladym remember "auth uses JWT with 24h expiry" --tags auth,security
```

That's it — `ladym` is on your PATH and works fully offline. Try it without installing
with `uvx --from . ladym stats`.

## How it compares

| | Local-first | Codebase RAG fused | Brain-inspired tiers | Open source | MCP/Skill/SDK/CLI |
|---|:---:|:---:|:---:|:---:|:---:|
| **LadyM** | ✅ | ✅ L2 fused | ✅ L0–L6 + System 1/2 | ✅ MIT | ✅ all four |
| **mem0** | partial | ❌ | flat + graph | ✅ | SDK |
| **Zep / Graphiti** | cloud | ❌ | temporal knowledge graph | ✅ | SDK |
| **Letta (MemGPT)** | self-host | ❌ | OS-style agent runtime | ✅ | SDK |
| **Tencent Hy-Memory** | cloud SaaS | ❌ | ✅ L1–L6 | ❌ | SDK |

LadyM borrows academic ideas from all of them (see
[ARCHITECTURE.md](ARCHITECTURE.md) §10) — its distinctive bet is **fusing codebase RAG into
a brain-inspired memory that runs fully local**.

## Architecture

```
   System 1 — online (ms)                 System 2 — offline worker (s–min)
   ┌───────────────────────────┐          ┌───────────────────────────┐
   │ L0  Working      (scratch)│          │ L5  Mental Model          │
   │ L1  Episodic     (events) │ extract  │     (cognitive frameworks)│
   │ L2  Semantic  (facts+CODE)│─────────▶│ L6  Forward Intent        │
   │ L3  Procedural  (playbook)│          │     (predictive actions)  │
   │ L4  Associative (graph)   │          └───────────────────────────┘
   └───────────────────────────┘
                 ▲
   read ◀──── recall(query) : two-tier (lightweight → deep) + reflection gate
   write ───▶ encode / consolidate / proceduralize / link / forget / decay
```

| Layer | Brain analogue | LadyM behaviour |
|---|---|---|
| L0 Working | Prefrontal cortex (working memory) | In-process bounded scratch buffer |
| L1 Episodic | Hippocampus | Time-stamped events; base-level decay |
| L2 Semantic | Neocortex | Consolidated facts **and code analysis** — one store |
| L3 Procedural | Basal ganglia | Playbooks + verified snippets, versioned |
| L4 Associative | Associative cortex | Zettelkasten edges with temporal validity |
| L5 Mental Model | (metacognition) | Cognitive frameworks abstracted from recurring episodes |
| L6 Forward Intent | (planning) | Predictive next-actions, asynchronously distilled |

Operations: `encode` (perception), `consolidate` (hippocampal replay → neocortex),
`proceduralize` (skill acquisition), `recall` (retrieval), `link` (association),
`forget` (synaptic pruning), `reflect` (metacognition).

The read path is **heuristic-only** — no LLM judge is ever invoked during recall, so it
stays fast and predictable. For the full cognitive-science provenance (what's borrowed
from MemGPT, mem0, A-MEM, Zep, HyMem, CoALA, ACT-R, and Anthropic's context-engineering
work) see **[ARCHITECTURE.md](ARCHITECTURE.md)**.

## Install

### Full install (all features)

One command pulls in every user-facing extra — LLM providers, embeddings, MCP server, and
the `ladym config` web editor:

```bash
# from a clone of this repo
uv tool install ".[all]"

# or directly from git, no clone needed
uv tool install "git+https://github.com/ProjAnvil/LadyM.git[all]"
```

### As a global CLI (recommended)

```bash
uv tool install .                # offline default — memory core only, no code indexing
uv tool install ".[codeindex]"   # + tree-sitter code indexing (index_code / search_code)
uv tool install ".[web,llm]"     # LLM providers + the `ladym config` web editor
# other extras compose the same way: [mcp] [local] [openai] [anthropic] [codeindex]
```

`.` is the hermetic core (offline-only). This drops a `ladym` executable on your PATH
(`~/.local/bin/ladym`), isolated from project venvs. Upgrade with
`uv tool install . --force --reinstall`, remove with `uv tool uninstall ladym`.

### One-off / try before you install

```bash
uvx --from . ladym stats
uvx --from . ladym remember "auth uses JWT"
uvx --from . ladym recall "auth"
```

`uvx` runs the CLI in a throwaway environment without touching your PATH.

### For development

```bash
git clone https://github.com/ProjAnvil/LadyM.git && cd ladyM
uv venv --python 3.12
uv pip install -e ".[dev]"            # core + test/lint tooling, editable (incl. [codeindex])
# optional extras stack on top:
uv pip install -e ".[mcp]"            # MCP server (for Claude Code / Cursor)
uv pip install -e ".[local]"          # sentence-transformers embeddings
uv pip install -e ".[openai]"         # OpenAI embeddings
uv pip install -e ".[llm]"            # LLM provider support (consolidation classifier)
uv pip install -e ".[web]"            # FastAPI + HTMX `ladym config` editor
```

Requires Python ≥ 3.11 (uses `enum.StrEnum`). `sqlite-vec` ships as a wheel — no native
toolchain needed on macOS/Linux/Windows. `tree-sitter` is optional via the `[codeindex]`
extra; the default install is memory-only.

## Integration

All four fronts call the same `Engine`, so behaviour is identical everywhere.

### MCP server (Claude Code, Cursor, …)

Add to your MCP client config:

```json
{
  "mcpServers": {
    "ladym": {
      "command": "ladym",
      "args": ["serve", "--db", "/absolute/path/to/ladym.db"]
    }
  }
}
```

The server exposes nine tools — `recall`, `remember`, `record_event`, `search_code`,
`index_code`, `consolidate`, `stats`, `link`, `forget` — described in
[src/ladym/mcp/server.py](src/ladym/mcp/server.py). `record_event` logs an L1 episodic
event that feeds the System 2 worker's consolidation (L1 → L2) and the gated L5 / L6
extractors (record ~3+ to arm those cycles).

### Claude Code Skill

A drop-in skill is in [skills/ladym-recall.md](skills/ladym-recall.md). Copy it to your
`.claude/skills/` directory and the agent will pull workspace memory into context with a
keyword instead of re-reading files.

### Python SDK

```python
from ladym import Engine, Config, Layer

eng = Engine(Config(db_path="ladym.db", workspace="myteam"))

# index once (incremental — skips unchanged files)
eng.index_code("./src")

# write path
eng.semantic.put_fact("auth uses JWT with 24h expiry", tags=["auth"])
eng.episodic.record(agent="claude", action="fixed login bug", outcome="success")

# read path: two-tier retrieval, ACT-R activation ranking
resp = eng.recall("how does auth work")
for r in resp.results:
    print(f"{r.score:.3f} [{r.memory.layer}] {r.memory.summary}")

# code-only shortcut
for r in eng.search_code("verify password").results:
    print(r.memory.metadata["qualified_name"], r.memory.content[:80])

# cognitive operations
eng.consolidate()         # L1 episodes → L2 facts (ADD/UPDATE/DELETE/NOOP)
eng.proceduralize()       # recurring successful episodes → L3 playbooks
eng.decay(dry_run=True)   # ACT-R base-level forgetting
eng.link(a_id, b_id, "depends_on")  # Zettelkasten edge
```

### Injecting your own langchain models

If your app already configures langchain `ChatOpenAI` / `OpenAIEmbeddings`
(with api_key, base_url, model), pass them straight to Engine via
`ModelRouting` — no need to re-declare credentials in ladyM's config:

```python
from ladym import Engine, Config, ModelRouting
from langchain_openai import ChatOpenAI, OpenAIEmbeddings

eng = Engine(Config(db_path="mem.db"), models=ModelRouting(
    consolidate=ChatOpenAI(model="gpt-4o", api_key=sk, base_url=url),
    attention_gate=ChatOpenAI(model="gpt-4o-mini", api_key=sk, base_url=url),
    embedding=OpenAIEmbeddings(model="text-embedding-3-small", api_key=sk),
))
```

Each of the five cognitive ops (`consolidate`, `proceduralize`,
`attention_gate`, `l5_mental_model`, `l6_forward_intent`) can take a
different model; unset ops fall back to Config.

### LangGraph

LadyM ships an optional LangGraph integration (install with `uv pip install "git+https://github.com/ProjAnvil/LadyM.git[langgraph]"`)
that exposes LadyM as a long-term memory layer for LangGraph / LangChain agents.
Two equivalent paths:

- **Tools** — `create_ladym_tools(engine)` returns LangChain tools (`recall_memory`,
  `remember_fact`, `search_code`) for ReAct-style agents where the LLM decides when
  to recall/remember.
- **Nodes** — `create_recall_node(engine)` / `create_retain_node(engine)` return graph
  nodes that inject recalled memory as a SystemMessage every turn and store the latest
  turn automatically (with per-user workspace isolation via `config["configurable"]["user_id"]`).

See [`docs/langgraph-integration.md`](docs/langgraph-integration.md) for full quickstarts.

### CLI

```bash
ladym index ./src                      # index a codebase (incremental)
ladym recall "auth flow"               # recall across code AND facts
ladym remember "..." --tags auth       # store a fact
ladym record --agent claude --action "fixed login bug" --outcome success
ladym consolidate                       # L1 → L2
ladym worker --once                     # fire the System 2 L5/L6 extractors
ladym stats                             # what's in memory
```

Each result carries the symbol's **identity, signature, docstring, body snippet, and
source file** — plus the **callers/callees** are queryable via the symbol graph. That is
what saves the agent from re-reading.

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `LADYM_DB` | `./ladym.db` | SQLite path (one DB per project by default) |
| `LADYM_WORKSPACE` | `default` | Multi-workspace isolation in a shared DB |
| `LADYM_EMBEDDING` | `hashing` | `hashing` / `st` / `openai` |
| `LADYM_EMBEDDING_MODEL` | (provider default) | Model name for `st` or `openai` |
| `LADYM_EMBEDDING_BASE_URL` | (provider default) | Override embedding API base URL (OpenAI/Ollama-compatible) |
| `LADYM_LLM_PROVIDER` | (none) | LLM provider name (e.g. `openai`, `ollama`) — enable via `uv pip install "git+https://github.com/ProjAnvil/LadyM.git[llm]"` |
| `LADYM_LLM_BASE_URL` | (provider default) | Override LLM API base URL (OpenAI/Ollama-compatible) |
| `LADYM_LLM_MODEL` | (provider default) | LLM model name for consolidation classifier |

`base_url` support lets you point embedding and LLM calls at any OpenAI/Ollama-compatible
endpoint (e.g. vLLM, LiteLLM, local Ollama). All values are also overridable on the
`Config` dataclass — see [src/ladym/config.py](src/ladym/config.py).

### Secret store (encrypted keys at rest)

Provider API keys can be stored encrypted (AES-256-GCM) under `~/.ladyM/` instead of
being pasted into your shell rc or CI secrets:

```
ladym config set-master-key              # first time: generate random master key
ladym config set DEEPSEEK_API_KEY sk-... # store a key (encrypted)
ladym config list                        # list names (values never echoed)
ladym config rm DEEPSEEK_API_KEY         # remove
ladym config reset-master-key <newpass>  # rotate master key; all secrets re-encrypted in place
```

Key resolution order — **LLM** providers: `Config.api_key` plaintext field (dev escape
hatch, off by default) → secret store (`~/.ladyM/secrets.enc`) → process env var named by
`api_key_env`. **Embedding** providers skip the plaintext tier, resolving: secret store →
env var. If none is set, commands fail fast with a one-line `ConfigError` naming the env
var and the fix command (exit 1; MCP tools return a structured error instead of a
traceback).

**Security boundary:** the store guarantees *encryption at rest* — it prevents plaintext
leaking via `cat secrets.enc`, shoulder-surfing, or accidental paste into chat/logs/commits.
It does **not** protect against full `~/.ladyM/` exfiltration: the master key and ciphertext
live in the same directory (the trade-off for non-interactive MCP / background workers that
must decrypt without a passphrase prompt). For stronger isolation, keep `~/.ladyM/` on
encrypted storage and rely on OS file permissions (dir `0700`, files `0600`). **Losing
`master.key` makes all secrets unrecoverable** — back it up.

**CLI extras:**

| Command | What it does | Install |
|---|---|---|
| `ladym config` | Local web config editor (FastAPI + HTMX, edits `ladym.toml`) | `uv pip install "git+https://github.com/ProjAnvil/LadyM.git[web]"` |
| `ladym worker` | Background System 2 consolidation daemon | core; flags: `--once`, `--interval N` |

## Testing

```bash
uv run pytest                  # 267 tests, ~3s, fully offline
uv run pytest tests/integration -v
uv run pytest --cov=ladym      # coverage report
uv run ruff check src/ tests/  # lint
```

The whole suite runs **without network and without model downloads** — the default
`HashingEmbedding` is deterministic and dependency-free, so CI is hermetic. The
sqlite-vec-backed path has its own regression tests in
[tests/integration/test_sqlite_vec.py](tests/integration/test_sqlite_vec.py).

## Documentation

- **[ARCHITECTURE.md](ARCHITECTURE.md)** — full design: the six memory layers, cognitive
  operations, two-tier retrieval, the ACT-R activation function, storage layout, and the
  code-indexing subsystem. *English only.*
- **[scenarios/](scenarios/)** — executable end-to-end scenarios (S01 write/recall, S03
  code index, S07 L5 mental model, S08 L6 forward intent, S09 attention gate, …) that
  double as a living spec.
- **[tests/](tests/)** — the executable specification.

## Status & roadmap

✅ Six-layer engine, two-tier recall, ADD/UPDATE/DELETE/NOOP consolidation,
proceduralization, decay, tree-sitter indexer for Python/JS/TS/Go/Rust/Java/C/C++, MCP
server, CLI, Skill, pluggable providers + TOML config, System 2 background worker, L5
mental-model / L6 forward-intent extraction, `ladym config` web editor, encrypted secret
store, 267 tests.

🚧 Next: GraphRAG-style cross-file ref resolution and multi-modal episodes.

## Contributing

Contributions are welcome. The fastest way to help:

1. Run `uv run pytest` (267 tests, hermetic) and `uv run ruff check` before submitting.
2. Add a scenario under [scenarios/](scenarios/) or a test under [tests/](tests/) for any
   new behaviour — they are the spec.
3. Keep the read path heuristic-only; route any LLM use through the System 2 worker.

For the design rationale behind any layer or operation, read
[ARCHITECTURE.md](ARCHITECTURE.md) first, then open an issue to discuss before large
changes.

## Citation

If LadyM informs your research or project, a citation is appreciated:

```bibtex
@misc{ladym2026,
  title  = {LadyM: A brain-inspired, multi-tier memory framework for LLM agents and codebase RAG},
  author = {ProjAnvil},
  year   = {2026},
  url    = {https://github.com/ProjAnvil/LadyM}
}
```

## License

[MIT](LICENSE) © ProjAnvil
