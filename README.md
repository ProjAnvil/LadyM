# LadyM — Layered Agent DYnamic Memory

> A brain-inspired, multi-tier memory framework that lets LLM agents **recall** workspace
> knowledge and codebase analysis with one keyword — instead of re-`Read`-ing and re-`Grep`-ing
> the same files every turn.

Built with **uv + Python 3.11+**, **local-first SQLite + sqlite-vec**, and **tree-sitter** for
code indexing. Exposed via **MCP**, **Claude Code Skill**, **Python SDK**, and **CLI** — all
calling the same engine so behaviour is identical everywhere.

```
                ┌─────────────────────────────────────────────┐
   write ─────▶ │ L0  Working      (context window scratch)  │
                │ L1  Episodic     (what / when / why)        │
                │ L2  Semantic     (facts + CODE analysis)    │  ← one unified store
                │ L3  Procedural   (how-to playbooks)         │
                │ L4  Associative  (Zettelkasten graph)       │
                └─────────────────────────────────────────────┘
                              ▲
   read ◀──── recall(query) : two-tier (lightweight → deep) + reflection gate
```

For the full design — what's borrowed from MemGPT, mem0, A-MEM, **HyMem**, CoALA, ACT-R, and
Anthropic's context-engineering work — see **[ARCHITECTURE.md](ARCHITECTURE.md)**.

---

## Install

```bash
git clone <this-repo> && cd ladyM
uv venv --python 3.12
uv pip install -e ".[dev]"            # core + test/lint tooling
# optional extras:
uv pip install -e ".[mcp]"            # MCP server (for Claude Code / Cursor)
uv pip install -e ".[local]"          # sentence-transformers embeddings
uv pip install -e ".[openai]"         # OpenAI embeddings
uv pip install -e ".[llm]"            # LLM provider support (consolidation classifier)
uv pip install -e ".[web]"            # reserved for the upcoming web config UI (Phase 5, not yet implemented)
```

Requires Python ≥ 3.11 (uses `enum.StrEnum`). `sqlite-vec` ships as a wheel — no native
toolchain needed on macOS/Linux.

## 30-second tour

```bash
# 1. index the codebase you keep re-grepping
ladym index ./src

# 2. ask a question — get the analysis back, no Read/Grep needed
ladym recall "how does password verification work" --code
# ┏━━━━━━━┳━━━━━━━━━━━━━┳━━━━━━━━━━━━┳━━━━━━━━━━━━━━━━━━━━━━━┳━━━━━━━━━━━━━━━━┓
# ┃ score ┃ layer       ┃ type       ┃ summary               ┃ source         ┃
# ┡━━━━━━━╇━━━━━━━━━━━━━╇━━━━━━━━━━━━╇━━━━━━━━━━━━━━━━━━━━━━━╇━━━━━━━━━━━━━━━━┩
# │ 1.054 │ L2_semantic │ code_symbol│ function …verify_pass │ auth/service.py│
# │ 1.097 │ L2_semantic │ code_file  │ auth/service.py …     │ auth/service.py│
# └───────┴─────────────┴────────────┴───────────────────────┴────────────────┘

# 3. store a fact for later
ladym remember "auth uses JWT with 24h expiry" --tags auth,security

# 4. recall works across code AND facts in one call
ladym recall "auth"

# 5. see what's in memory
ladym stats
```

Each result carries the symbol's **identity, signature, docstring, body snippet, and source
file** — plus the **callers/callees** are queryable via the symbol graph. That is what saves
the agent from re-reading.

## Python SDK

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

## MCP server (Claude Code, Cursor, …)

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

The server exposes eight tools — `recall`, `remember`, `search_code`, `index_code`,
`consolidate`, `stats`, `link`, `forget` — described in
[src/ladym/mcp/server.py](src/ladym/mcp/server.py).

## Claude Code Skill

A drop-in skill is in [skills/ladym-recall.md](skills/ladym-recall.md). Copy it to your
`.claude/skills/` directory and the agent will pull workspace memory into context with a
keyword instead of re-reading files.

## How memory maps to the brain

| Layer | Brain analogue | LadyM behaviour |
|---|---|---|
| L0 Working | Prefrontal cortex (working memory) | In-process bounded scratch buffer |
| L1 Episodic | Hippocampus | Time-stamped events; base-level decay |
| L2 Semantic | Neocortex | Consolidated facts **and code analysis** — one store |
| L3 Procedural | Basal ganglia | Playbooks + verified snippets, versioned |
| L4 Associative | Associative cortex | Zettelkasten edges with temporal validity |

Operations: `encode` (perception), `consolidate` (hippocampal replay → neocortex),
`proceduralize` (skill acquisition), `recall` (retrieval), `link` (association),
`forget` (synaptic pruning), `reflect` (metacognition).

See ARCHITECTURE.md §1–§4 for the cognitive-science and SOTA provenance of each choice.

## Testing

```bash
uv run pytest                  # 103 tests, ~1s, fully offline
uv run pytest tests/integration -v
uv run pytest --cov=ladym      # coverage report
uv run ruff check src/ tests/  # lint
```

The whole suite runs **without network and without model downloads** — the default
`HashingEmbedding` is deterministic and dependency-free, so CI is hermetic. The
sqlite-vec-backed path has its own regression tests in
[tests/integration/test_sqlite_vec.py](tests/integration/test_sqlite_vec.py).

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `LADYM_DB` | `./ladym.db` | SQLite path (one DB per project by default) |
| `LADYM_WORKSPACE` | `default` | Multi-workspace isolation in a shared DB |
| `LADYM_EMBEDDING` | `hashing` | `hashing` / `st` / `openai` |
| `LADYM_EMBEDDING_MODEL` | (provider default) | Model name for `st` or `openai` |
| `LADYM_EMBEDDING_BASE_URL` | (provider default) | Override embedding API base URL (OpenAI/Ollama-compatible third parties) |
| `LADYM_LLM_PROVIDER` | (none) | LLM provider name (e.g. `openai`, `ollama`) — enable via `pip install 'ladym[llm]'` |
| `LADYM_LLM_BASE_URL` | (provider default) | Override LLM API base URL (OpenAI/Ollama-compatible third parties) |
| `LADYM_LLM_MODEL` | (provider default) | LLM model name for consolidation classifier |

`base_url` support lets you point embedding and LLM calls at any OpenAI/Ollama-compatible endpoint
(e.g. vLLM, LiteLLM, local Ollama). All values are also overridable on the `Config` dataclass — see
[src/ladym/config.py](src/ladym/config.py).

**CLI extras:**

| Command | What it does | Install |
|---|---|---|
| `ladym config` | _(planned, Phase 5 — not yet implemented)_ Local web config editor (browser UI) | `pip install 'ladym[web]'` |
| `ladym worker` | Background System2 consolidation daemon | core; flags: `--once`, `--interval N` |

## Status & roadmap

✅ Five-layer engine, two-tier recall, ADD/UPDATE/DELETE/NOOP consolidation, proceduralization,
decay, tree-sitter indexer for Python/JS/TS/Go/Rust/Java/C/C++, MCP server, CLI, Skill, 103
tests.

🚧 Next: an LLM-backed classifier for consolidation (the offline heuristic is already wired and
tested — see `Engine.attach_llm_classifier`), GraphRAG-style cross-file ref resolution,
multi-modal episodes, and a `ladym config` web editor (Phase 5 — not yet implemented).

## License

MIT.
