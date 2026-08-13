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
| tree-sitter + `tree-sitter-language-pack` (cgo) | pure-Go line/regex symbol extractor for Python/Go/JS/TS/Rust/Java/C/C++ (produces the same `RawSymbol` records); other languages use line-chunk fallback |
| sentence-transformers (`embedding.provider="st"`) | returns a clear error — use `provider="http"`/`"ollama"` to point at a local embedding endpoint |
| langchain `ChatOpenAI`/`ChatAnthropic`/`ChatOllama` | net/http OpenAI-compatible / Anthropic / Ollama chat clients; structured output uses JSON mode |
| langgraph integration (`langchain`/`langgraph`) | not ported (Python-only runtime); the equivalent entry points are the MCP server / CLI / SDK |
| sqlite driver | `modernc.org/sqlite` (pure Go, no cgo — preserves the "no native toolchain" property); slower than native sqlite for large bulk indexing |

`go test ./...` is hermetic: it exercises the deterministic hashing embedding and
needs no network, no model downloads, and no API keys.
