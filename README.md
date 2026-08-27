# LadyM

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go 1.26+](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Tests](https://img.shields.io/badge/tests-go%20test%20.%2F...-green.svg)](#testing)
[![MCP](https://img.shields.io/badge/MCP-compatible-purple.svg)](#mcp-server-claude-code-cursor-)
[![Storage](https://img.shields.io/badge/storage-local--first%20SQLite-success.svg)](#architecture)

**[English](README.md)** | [简体中文](README.zh-CN.md)

> A brain-inspired, multi-tier memory framework that lets LLM agents **recall** workspace
> knowledge and codebase analysis with one keyword — instead of re-`Read`-ing and
> re-`Grep`-ing the same files every turn.

LadyM caches your workspace's *understanding* — code analysis, decisions, skills, and
episodes — into a hierarchical, consolidating, decaying memory that any agent can recall
through a single keyword. Written in **Go** as a **single static binary with zero cgo**:
SQLite via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite), an in-process
brute-force cosine vector index, and [gotreesitter](https://github.com/odvcencio/gotreesitter)
(a pure-Go tree-sitter runtime) for code indexing. Exposed via **MCP**, **Claude Code
Skill**, **Go SDK**, and **CLI** — all calling the same engine so behaviour is
identical everywhere.

---

## What's New in 0.5.0

- **CJK support (Chinese / Japanese / Korean)** — CJK text tokenizes with Python-parity
  quality: the out-of-the-box per-character + adjacent-bigram mode is fully offline and
  needs zero configuration, while opt-in dictionary segmentation (gse) downloads on
  demand from the console or the admin API — LadyM never downloads anything unprompted.
  Release assets ship a `fulldict` variant with the dictionary embedded, the Dockerfile
  grows a dict data layer, and compose overlays run the whole stack dictionary-baked.
- **langchain-golang v0.6.2** — partner chat models gain `reasoning_effort` and native
  JSON mode for structured output; the LangGraph helper layer is re-exported for host
  applications.
- **Bounded consolidation cost** — processed episodes carry a `consolidated_at` stamp,
  so each System 2 cycle pays for new episodes instead of re-classifying the whole
  history; ADD verdicts store the LLM's rewritten fact rather than the raw event text;
  `consolidate` accepts a `since` bound over HTTP, MCP, and the Go SDK.

## What Was New in 0.3.0

- **Full Go rewrite** — LadyM is now one static binary with **no Python dependency and
  zero cgo**. Install with `go install` or build from source; there is nothing else to set up.
- **Pure-Go storage stack** — SQLite runs on `modernc.org/sqlite`, and the old `sqlite-vec`
  extension is replaced by an in-process brute-force cosine index ([storage/vector_index.go](storage/vector_index.go)),
  so the binary stays hermetic and cross-compiles anywhere.
- **Pure-Go tree-sitter** — code indexing uses `gotreesitter`, which loads the same parse
  tables as upstream tree-sitter without native builds.
- **One engine, every front** — the MCP server, CLI, and Go SDK all wrap the same
  `engine.Engine`; behaviour is identical in every host.

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
| 🔒 **Local-first, zero-config start** | Default `HashingEmbedding` + `llm.provider="none"` runs the moment install finishes: **no network, no model download, no API key**. One SQLite file holds everything. |
| 🧬 **Brain-inspired six layers + dual-path** | L0–L4 core store (working / episodic / semantic / procedural / associative) plus a System 2 worker that extracts **L5 mental models** and **L6 forward intents**, with a `supersedes` evolution chain so memories *evolve* instead of piling up. |
| 🔌 **Four fronts, one engine** | MCP server, Claude Code Skill, Go SDK, and CLI all call the same `Engine` — identical behaviour whether you're in Claude Code, Cursor, a script, or the terminal. |

## Quick start (30 seconds)

```bash
# 1. install (single static binary — no network or API key needed at runtime)
go install github.com/ProjAnvil/LadyM/cmd/ladym@latest

# 2. index the codebase you keep re-grepping
ladym index ./src

# 3. ask a question — get the analysis back, no Read/Grep needed
ladym recall "how does password verification work" --code

# 4. store a fact for later
ladym remember "auth uses JWT with 24h expiry" --tags auth,security
```

That's it — `ladym` is on your PATH and works fully offline.

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

### As a global CLI (recommended)

```bash
go install github.com/ProjAnvil/LadyM/cmd/ladym@latest
```

This drops a single static `ladym` binary on your Go bin path
(`$(go env GOPATH)/bin`). Requires Go 1.26+ to build; the binary itself has no
runtime dependencies.

### Build from a clone

```bash
git clone https://github.com/ProjAnvil/LadyM.git && cd LadyM
go build -o bin/ladym ./cmd/ladym
./bin/ladym stats
```

Or use the install script, which builds and copies the binary to `~/.local/bin`:

```bash
scripts/install.sh            # or: scripts/install.sh /custom/bin/dir
```

### For development

```bash
git clone https://github.com/ProjAnvil/LadyM.git && cd LadyM
go build ./...                # compile everything
go test ./...                 # full suite, fully offline
```

The module is pure Go (`github.com/ProjAnvil/LadyM`) with a handful of library
dependencies — no native toolchain needed on macOS/Linux/Windows.

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

The server speaks MCP over stdio and exposes nine tools — `recall`, `remember`,
`record_event`, `search_code`, `index_code`, `consolidate`, `stats`, `link`,
`forget` — described in [mcp/server.go](mcp/server.go). `record_event` logs an L1
episodic event that feeds the System 2 worker's consolidation (L1 → L2) and the
gated L5 / L6 extractors (record ~3+ to arm those cycles).

### Claude Code Skill

A drop-in skill is in [skills/ladym-recall.md](skills/ladym-recall.md). Copy it to your
`.claude/skills/` directory and the agent will pull workspace memory into context with a
keyword instead of re-reading files.

### Go SDK

The `ladym` package is a one-import facade over the engine
([ladym/ladym.go](ladym/ladym.go)):

```go
import "github.com/ProjAnvil/LadyM/ladym"

eng, err := ladym.NewEngine(ladym.DefaultConfig())
if err != nil {
	// handle error
}
defer eng.Close()

// index once (incremental — skips unchanged files)
report, err := eng.IndexCode("./src", false, "", nil)

// write path
eng.Remember("auth uses JWT with 24h expiry", ladym.LayerSemantic, ladym.TypeFact,
	[]string{"auth"}, nil, "sdk", "")
eng.RecordEvent("claude", "fixed login bug", "", "success", nil, nil)

// read path: two-tier retrieval, ACT-R activation ranking
resp, err := eng.Recall("how does auth work", "", 8, nil, nil, 0)
for _, r := range resp.Results {
	fmt.Printf("%.3f [%s] %s\n", r.Score, r.Memory.Layer, r.Memory.Summary)
}

// code-only shortcut
codeResp, err := eng.SearchCode("verify password", 8, "")

// cognitive operations
eng.Consolidate("", 0)          // L1 episodes → L2 facts (ADD/UPDATE/DELETE/NOOP)
eng.Proceduralize("", 0)        // recurring successful episodes → L3 playbooks
eng.Decay("", true, 0, 0)       // ACT-R base-level forgetting (dry run)
eng.Link(srcID, dstID, "depends_on") // Zettelkasten edge
```

One-shot helpers — `ladym.Recall(query, dbPath, workspace, topK)`,
`ladym.Remember(...)`, `ladym.IndexCode(...)` — open a short-lived engine, run, and
close, for scripts that don't want to manage the lifecycle.

### Python SDK (MCP wrapper)

Python apps talk to the Go engine through [`wrapper/py`](wrapper/py/) — a thin typed
client that spawns the `ladym serve` MCP server (JSON-RPC 2.0 over stdio) and exposes
the nine tools (`recall`, `remember`, `record_event`, `search_code`, `index_code`,
`consolidate`, `stats`, `link`, `forget`) as Python methods. No memory logic lives in
Python; the Go binary is the single source of truth.

```python
from ladym_wrapper import LadymClient        # sync
from ladym_wrapper import AsyncLadymClient  # async

with LadymClient() as client:
    client.remember("deploys go through Argo CD", source="notes")
    hits = client.recall("how do we deploy?")
```

The Go binary is resolved in order: `binary=` argument → `LADYM_BIN` env var → `PATH` →
repo-local `bin/ladym`. Requires Python ≥ 3.12. See
[`wrapper/py/README.md`](wrapper/py/README.md) for details. (The full 0.2.x Python
implementation is preserved on the
[`python`](https://github.com/ProjAnvil/LadyM/tree/python) branch.)

### Injecting your own langchain-golang models

If your app already configures langchain-golang chat / embedding models
(with api key, base URL, model), wrap them and pass them straight to the Engine via
`ModelRouting` — no need to re-declare credentials in LadyM's config
([adapter/adapter.go](adapter/adapter.go)):

```go
import (
	"github.com/ProjAnvil/LadyM/adapter"
	"github.com/ProjAnvil/LadyM/ladym"
	"github.com/projanvil/langchain-golang/core/modelconfig"
	"github.com/projanvil/langchain-golang/partners/openai"
)

eng, err := ladym.NewEngineWithModels(ladym.DefaultConfig(), &adapter.ModelRouting{
	Consolidate: adapter.WrapChatModel(openai.NewChatModel(
		modelconfig.WithModel("gpt-4o"),
		modelconfig.WithAPIKey(key),
		modelconfig.WithBaseURL(url),
	), ""),
	AttentionGate: adapter.WrapChatModel(openai.NewChatModel(
		modelconfig.WithModel("gpt-4o-mini"),
		modelconfig.WithAPIKey(key),
	), ""),
	Embedding: adapter.WrapEmbeddings(openai.NewEmbeddings(
		modelconfig.WithModel("text-embedding-3-small"),
		modelconfig.WithAPIKey(key),
	)),
})
```

Each of the five cognitive ops (`consolidate`, `proceduralize`,
`attention_gate`, `l5_mental_model`, `l6_forward_intent`) can take a
different model; unset ops fall back to Config.

### LangGraph

The [langgraph/](langgraph/) package integrates LadyM as a long-term memory layer for
langchain-golang LangGraph / LangChain agents. Two equivalent paths:

- **Tools** — `langgraph.CreateTools(eng, workspace, defaultTopK)` returns LangChain
  tools (`recall_memory`, `remember_fact`, `search_code`) for ReAct-style agents where
  the LLM decides when to recall/remember.
- **Nodes** — `langgraph.CreateRecallNode(eng, topK, prefix, wsFn)` /
  `langgraph.CreateRetainNode(eng, wsFn)` return graph `NodeFunc`s that inject recalled
  memory as a SystemMessage every turn and store the latest turn automatically.
  `langgraph.WorkspaceFromUserID()` gives per-user workspace isolation from the
  run-scoped `user_id`.

See [`docs/langgraph-integration.md`](docs/langgraph-integration.md) for the design
walkthrough.

### CLI

```bash
ladym index ./src                      # index a codebase (incremental)
ladym recall "auth flow"               # recall across code AND facts
ladym recall "auth flow" --code --json # code-only, machine-readable
ladym remember "..." --tags auth       # store a fact
ladym record --agent claude --action "fixed login bug" --outcome success
ladym consolidate                       # L1 → L2
ladym worker --once                     # fire the System 2 L5/L6 extractors
ladym stats                             # what's in memory
```

Run `ladym <command> --help` for flags; `ladym completion <shell>` generates shell
autocompletion. Each indexed result carries the symbol's **identity, signature,
docstring, body snippet, and source file** — plus the **callers/callees** are queryable
via the symbol graph. That is what saves the agent from re-reading.

## Configuration

Config resolves through (highest precedence first): CLI flags → `LADYM_*` env vars →
`./ladym.toml` → `~/.ladym/config.toml` → built-in defaults. `ladym --config <path>`
loads an extra TOML file on top.

| Env var | Default | Purpose |
|---|---|---|
| `LADYM_DB` | `./ladym.db` | SQLite path (one DB per project by default) |
| `LADYM_WORKSPACE` | `default` | Multi-workspace isolation in a shared DB |
| `LADYM_EMBEDDING` | `hashing` | `hashing` / `openai` / `ollama` / `http` |
| `LADYM_DICT_DIR` | `~/.ladyM/dict` | CJK dictionary dir; point at a shared volume in microservice deployments |
| `LADYM_EMBEDDING_MODEL` | (provider default) | Model name for a hosted embedding provider |
| `LADYM_EMBEDDING_BASE_URL` | (provider default) | Override embedding API base URL (OpenAI/Ollama-compatible) |
| `LADYM_EMBEDDING_API_KEY_ENV` | (none) | Name of the env var holding the embedding API key |
| `LADYM_LLM_PROVIDER` | `none` | `none` / `openai` / `anthropic` / `ollama` / `http` |
| `LADYM_LLM_BASE_URL` | (provider default) | Override LLM API base URL (OpenAI/Ollama-compatible) |
| `LADYM_LLM_MODEL` | `gpt-4o-mini` | LLM model name for the consolidation classifier |
| `LADYM_LLM_API_KEY_ENV` | (none) | Name of the env var holding the LLM API key |
| `LADYM_ENABLE_WAL` | `true` | Enable SQLite WAL journal mode (default on so multiple processes can share one db; set `false` on filesystems without WAL support) |

`base_url` support lets you point embedding and LLM calls at any OpenAI/Ollama-compatible
endpoint (e.g. vLLM, LiteLLM, local Ollama); the `http` providers let you template an
arbitrary embedding/LLM HTTP API. Finer knobs (`LADYM_EMBEDDING_TIMEOUT_S`,
`LADYM_LLM_MAX_TOKENS`, `LADYM_LLM_TEMPERATURE`, per-op overrides under `[agents]`, and
the recall/activation weights) live in TOML or on the `config.Config` struct — see
[config/config.go](config/config.go). Secret literals in TOML are rejected with a
warning; use `<name>_env` indirection or the secret store below.

### Secret store (encrypted keys at rest)

Provider API keys can be stored encrypted (AES-256-GCM) under `~/.ladyM/` instead of
being pasted into your shell rc or CI secrets:

```
ladym config set-master-key              # first time: generate a random master key
ladym config set DEEPSEEK_API_KEY sk-... # store a key (encrypted)
ladym config list                        # list names (values never echoed)
ladym config rm DEEPSEEK_API_KEY         # remove
ladym config reset-master-key <newpass>  # rotate master key; all secrets re-encrypted in place
```

Key resolution order — **LLM** providers: `Config.LLMAPIKey` plaintext field (dev escape
hatch, only honoured with `allow_plaintext_secrets`, off by default) → secret store
(`~/.ladyM/secrets.enc`) → process env var named by `api_key_env`. **Embedding**
providers skip the plaintext tier, resolving: secret store → env var. If none is set,
commands fail fast with a one-line `ConfigError` naming the env var and the fix command
(exit 1; MCP tools return a structured error instead of a stack trace).

**Security boundary:** the store guarantees *encryption at rest* — it prevents plaintext
leaking via `cat secrets.enc`, shoulder-surfing, or accidental paste into chat/logs/commits.
It does **not** protect against full `~/.ladyM/` exfiltration: the master key and ciphertext
live in the same directory (the trade-off for non-interactive MCP / background workers that
must decrypt without a passphrase prompt). For stronger isolation, keep `~/.ladyM/` on
encrypted storage and rely on OS file permissions (dir `0700`, files `0600`). **Losing
`master.key` makes all secrets unrecoverable** — back it up.

**More CLI surface:**

| Command | What it does |
|---|---|
| `ladym serve --http :8080` | HTTP data-plane API (`/api/*`, optional Basic auth) plus the embedded management console at `/` (login, memory CRUD, user admin, stats) |
| `ladym config <sub>` | Encrypted secret store: `set` / `set-master-key` / `reset-master-key` / `list` / `rm` |
| `ladym worker` | Background System 2 consolidation daemon; flags: `--once`, `--interval N` (seconds) |

## Testing

```bash
go test ./...            # full suite, fully offline
go test ./engine/ -v     # one package
go vet ./...             # lint
```

The whole suite runs **without network and without model downloads** — the default
`HashingEmbedding` is deterministic and fully offline, so CI is hermetic.

## CJK support (Chinese / Japanese / Korean)

LadyM tokenizes Chinese, Japanese, and Korean **out of the box**: per-character
tokenization plus adjacent bigrams, fully offline, zero configuration — CJK queries
never return an empty set. Dictionary-backed **word segmentation** (better recall
quality) is opt-in, and **LadyM never downloads anything on its own**: every download
is user-triggered from the console or the admin API.

### Enabling a dictionary

Four downloadable variants, all sha256-pinned to gse v1.0.2 (jsDelivr mirror first,
GitHub raw fallback):

| Variant | Content | Download |
|---|---|---|
| `zh` (default) | Chinese, simplified + traditional | 8.2 MB |
| `zh_s` | Chinese, simplified only | 4.9 MB |
| `zh_t` | Chinese, traditional only | 3.4 MB |
| `jp` | Japanese, kanji + kana (kana segments by word too) | 22.6 MB |

- **Console** (both editions): **Settings → Memory** → pick a variant → **下载词典**.
  Verified download, hot reload — no restart, no shared state.
- **API** (admin-gated): `POST /api/cjk_dict/download` with `{"dict": "zh"}`; an
  optional `"mirror_base"` points at an internal mirror for air-gapped clusters.
  `GET /api/cjk_dict` reports status + the variant enumeration; `DELETE /api/cjk_dict`
  removes the dictionary.
- **Docker**: dict-baked images — see below.

The dictionary directory defaults to `~/.ladyM/dict` and is configurable three ways
(priority high → low): the `--dict-dir` flag on `serve` / `worker` / `ladymconsole`,
the `LADYM_DICT_DIR` env var, or `dict_dir` in `ladym.toml`.

### Dict-baked images (recommended for Docker deployments)

One command builds and runs every role with the dictionary baked into the image —
**no shared volume, no downloads, no internet at runtime**:

```bash
# dev group (pg + api + worker)
docker compose -f docker-compose.dev.yml -f docker-compose.dev.dict.yml up -d --build

# enterprise three-tier (gateway + api×2 + console + worker + pg)
docker compose -f docker-compose.enterprise.yml -f docker-compose.enterprise.dict.yml up -d --build
```

How it works, and what you can tune:

- The main `Dockerfile` has a `dict` target: a thin **data layer** (dictionary files at
  `/opt/ladym/dict`, `LADYM_DICT_DIR` set in the image) on top of the regular image.
  A plain `docker build .` stays dictionary-less — the dict layer only builds on demand.
- Pick the variant in the compose override's `x-dict-build` anchor (`DICT_VARIANT:
  zh | zh_s | zh_t | jp`).
- **Air-gapped builds**: place the pinned files under the repo's `dict/` directory and
  the image builds with zero network.
- Already have a released image? Stack the layer without rebuilding:
  `docker build -f Dockerfile.dict --build-arg BASE=ladym-enterprise:v0.5.0 -t ladym-enterprise:v0.5.0-dict .`
- Dictionary refresh = rebuild the layer (base layers stay cached, seconds) + restart;
  LadyM version upgrades don't touch the dict layer.

### Microservices without baked images

Point every instance's dictionary directory at a shared volume
(`LADYM_DICT_DIR=/data/cjk-dict` — wired in the reference `docker-compose.enterprise.yml`).
**One download on any instance provisions all of them**: instances re-probe the shared
manifest and pick up new dictionaries or variant switches within ~30s, no restarts.
K8s equivalent (PVC + env + volumeMount) and the full comparison of multi-machine
patterns live in [docs/deployment.md](docs/deployment.md).

### Behavior guarantees

- **No auto-download** — the tokenization path never touches the network (a contract
  test enforces it).
- **Graceful degradation** — without a dictionary, CJK still tokenizes per character;
  similarity and recall keep working.
- **Integrity** — every file is sha256-verified before anything is written; failed or
  tampered downloads leave the existing dictionary untouched.
- **Offline binary variant** — `go build -tags fulldict` (or the side-effect import
  below) compiles the zh dictionary into the binary (~+31MB) for non-container offline
  distribution.

### Downstream Go consumers & release assets

Library consumers (go.mod) get dictionary-backed tokenization three ways — the module
version never changes:

1. **Runtime download (nothing to build differently)**: trigger via the console /
   admin API as above; hosts may also call `storage.DownloadCJKDict()` explicitly.
2. **Side-effect import (no build-script change)**:
   `import _ "github.com/ProjAnvil/LadyM/storage/fulldict"` links the embedded
   dictionary in (~+31MB) — the import edge is the switch, so uninterested consumers
   stay small.
3. **Build flag**: `-tags fulldict` — same dictionary, chosen in the build command;
   this is what the prebuilt `ladym-personal-fulldict-*` release assets use.

There is deliberately no `v0.5.0-fulldict` module version: semver reads that suffix as
a *pre-release* of v0.5.0 and it would confuse `go get`.

**Binary users**: every `v*` GitHub release carries per-platform tarballs —
`ladym-personal-*` (default), `ladym-personal-fulldict-*` (embedded dictionary),
`ladym-enterprise-*` — built by `.github/workflows/release.yml` on tag push, with a
`SHA256SUMS` file. Local equivalents: `make package-personal` / `package-fulldict` /
`package-enterprise`, or `LADYM_BUILD_TAGS=fulldict scripts/install.sh` from a clone.

## Documentation

- **[ARCHITECTURE.md](ARCHITECTURE.md)** — full design: the six memory layers, cognitive
  operations, two-tier retrieval, the ACT-R activation function, storage layout, and the
  code-indexing subsystem. *English only.*
- **[scenarios/](scenarios/)** — executable end-to-end scenarios (S01 write/recall, S03
  code index, S07 L5 mental model, S08 L6 forward intent, S09 attention gate, …) that
  double as a living spec. [scenarios/README.md](scenarios/README.md) is in Chinese.
- **[docs/](docs/)** — deep dives (code-indexing analysis, LangGraph integration) and
  the [enterprise deployment guide](docs/deployment.md) (container image, docker-compose
  reference deployment, workspace isolation, ops baseline).
- **The Go test suite** (`*_test.go` beside each package) — the executable specification.

## Status & roadmap

✅ Six-layer engine, two-tier recall, ADD/UPDATE/DELETE/NOOP consolidation,
proceduralization, decay, pure-Go tree-sitter indexer (full symbol specs for
Python/JS/TS/Go/Rust/Java/C/C++, line-window chunking for Kotlin/C#/Ruby/PHP/Swift/Scala/
Bash/Lua/SQL/HTML/CSS), MCP server, CLI, Skill, Go SDK, pluggable providers + TOML
config, System 2 background worker, L5 mental-model / L6 forward-intent extraction,
embedded management console (`ladym serve --http`, Vue 3 SPA under `console/`),
encrypted secret store.

🚧 Next: GraphRAG-style cross-file ref resolution and multi-modal episodes.

## Contributing

Contributions are welcome. The fastest way to help:

1. Run `go test ./...` (hermetic) and `go vet ./...` before submitting.
2. Add a scenario under [scenarios/](scenarios/) or a `*_test.go` beside the changed
   package for any new behaviour — they are the spec.
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
