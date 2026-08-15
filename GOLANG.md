# LadyM — Go port

This branch carries a from-scratch Go rewrite of LadyM (the Python multi-tier
memory + codebase-RAG framework). The Python sources on `main` are left
untouched; the Go module lives alongside them here.

```
github.com/ProjAnvil/LadyM
├── cmd/ladym/main.go      CLI entry point
├── cli/                   cobra CLI (remember/record/recall/index/consolidate/stats/forget/link/serve/worker/config)
├── mcp/                   MCP server (JSON-RPC 2.0 over stdio)
├── web/                   local config editor (net/http + html/template)
├── engine/                the Engine orchestrator
├── operations/            activation, recall, consolidate, decay, proceduralize, supersedes, attention, L5, L6, system2
├── layers/                L0–L4 memory layers
├── providers/             LLM provider abstraction + per-op agent registry
├── storage/               SQLite store, vector index, embedding providers
├── code/                  codebase indexer (symbol extraction + chunk fallback)
├── config/                Config structs, TOML+env loading, secret stripping
├── secrets/               AES-256-GCM encrypted secret store
├── schema/                core data models
└── ladym/                 SDK facade (one-import surface)
```

## Build & test

```bash
go build ./...            # build every package
go build -o bin/ladym ./cmd/ladym
go install ./cmd/ladym    # installs a `ladym` binary on your GOPATH
go test ./...             # hermetic — no network, no model downloads
go vet ./...
```

## Usage

```bash
bin/ladym remember "auth uses JWT with 24h expiry" --tags auth,security
bin/ladym index ./src
bin/ladym recall "how does authentication work" --code
bin/ladym record --agent claude --action "fixed login bug" --outcome success
bin/ladym consolidate
bin/ladym worker --once
bin/ladym stats
bin/ladym serve           # MCP stdio server
```

The Go SDK mirrors the Python `from ladym import Engine, Config` surface:

```go
import ladym "github.com/ProjAnvil/LadyM/ladym"

eng, _ := ladym.NewEngine(ladym.DefaultConfig())
defer eng.Close()
eng.IndexCode("./src", false, "", nil)
resp, _ := eng.Recall("how does auth work", "", 0, nil, nil, 0)
```

One-shot helpers and host-model injection are also available:

```go
// one-shot (opens + closes a short-lived engine)
resp, _ := ladym.Recall("how does auth work", "mem.db", "team", 5)
ladym.Remember("auth uses JWT", "mem.db", "team", []string{"auth"}, "sdk")
ladym.IndexCode("./src", "mem.db", "team", false)

// inject your own providers (ModelRouting ≈ Python's langchain injection)
eng, _ := ladym.NewEngineWithModels(cfg, &ladym.ModelRouting{
    Embedding: myEmbeddingProvider,
    Consolidate: myLLMProvider,
})

// inject langchain-golang models directly (mirrors Python's langchain injection)
eng, _ = ladym.NewEngineWithModels(cfg, &ladym.ModelRouting{
    Embedding:   adapter.WrapEmbeddings(openai.NewEmbeddings(...)),
    Consolidate: adapter.WrapChatModel(anthropic.NewChatModel(...), "function_calling"),
})
```

LangGraph hosts can wire ladyM in as a memory layer via the `langgraph`
package (mirrors Python's `ladym.langgraph`):

```go
// Path A — tools for ReAct-style agents (agents.CreateAgent)
tools, _ := ladymgraph.CreateTools(eng, "team", 8)

// Path B — graph nodes for automatic per-turn memory injection
g.AddNode("recall", ladymgraph.CreateRecallNode(eng, 6, "", nil))
g.AddNode("retain", ladymgraph.CreateRetainNode(eng, nil))
```

## Parity

The following behaviour is ported 1:1 and covered by tests, including
**cross-language golden vectors** (tokenizer, `HashingEmbedding`, `content_hash`,
and the HKDF-derived AES key are byte-for-byte identical to the Python output):

- Schema (`Layer`, `MemoryType`, `Memory`, `Edge`, `CodeSymbol`, `CodeRef`, `RecallResult`, `RecallResponse`, `Stats`) and JSON field names.
- Config: 4-layer precedence (defaults → `~/.ladym/config.toml` → `./ladym.toml` → `--config`), env overlay, deep merge, secret-literal stripping, deprecated-key rename.
- Secret store: HKDF-SHA256 → AES-256-GCM, atomic writes, master-key lifecycle, reset.
- Storage: identical SQLite schema, in-memory cosine vector index warmed from BLOBs on reopen.
- Embeddings: hashing (default), Ollama, generic HTTP, OpenAI-compatible, callable registry, LRU query cache.
- Layers L0–L4 and operations: ACT-R activation, two-tier recall + reflection gate, ADD/UPDATE/DELETE/NOOP consolidation, supersedes chain, decay, proceduralization, attention gate, L5/L6, System 2 worker.
- Code indexer: language detection, incremental/force indexing, skip-dir pruning, symbol + cross-ref extraction, chunk fallback.
- Front-ends: CLI (cobra), MCP stdio server (9 tools), web config editor, encrypted secret store.

## Documented deviations

These are deliberate trade-offs of the Go port:

| Python | Go port |
|---|---|
| `sqlite-vec` loadable extension (persistent ANN) | pure-Go `InMemoryVectorIndex` (brute-force cosine); vectors still persisted as BLOBs and warmed on reopen — behaviour identical, `prefer_sqlite_vec` accepted but inert |
| tree-sitter + `tree-sitter-language-pack` (cgo) | [gotreesitter](https://github.com/odvcencio/gotreesitter) — a pure-Go tree-sitter runtime that loads the same parse tables as upstream tree-sitter (206 grammars). Same AST-level symbol extraction, no cgo |
| sentence-transformers (`embedding.provider="st"`) | returns a clear error — use `provider="http"`/`"ollama"` to point at a local embedding endpoint |
| langchain `ChatOpenAI`/`ChatAnthropic`/`ChatOllama` | [langchain-golang](https://github.com/ProjAnvil/langchain-golang) partner chat models (`openai`/`anthropic`/`ollama` kinds) with provider-native structured output (real JSON Schema: OpenAI json_schema, Anthropic tool use, Ollama format schema); `llm.provider="http"` keeps the legacy hand-rolled net/http client as an escape hatch |
| langgraph integration (`ladym.langgraph`) | `langgraph` package on langchain-golang: `CreateTools` (Path A) and `CreateRecallNode`/`CreateRetainNode` (Path B) |
| OpenAI endpoint | langchain-golang's OpenAI partner defaults to the Responses API; ladyM switches it to `/chat/completions` for parity with Python's `ChatOpenAI` and OpenAI-compatible servers |
| `structured_method=function_calling` on OpenAI | langchain-golang's OpenAI partner implements structured output via the native json_schema response_format rather than function calling; behavior is equivalent-or-stricter |
| sqlite driver | `modernc.org/sqlite` (pure Go, no cgo — preserves the "no native toolchain" property); slower than native sqlite for large bulk indexing |

`go test ./...` is hermetic: it exercises the deterministic hashing embedding and
needs no network, no model downloads, and no API keys.
