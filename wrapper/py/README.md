# ladym-wrapper

Python wrapper for the [ladyM](../../README.md) **Go** engine.

Go is the single source of truth — this package is a thin client that spawns
the `ladym serve` MCP server (JSON-RPC 2.0 over stdio) and exposes its tools
(`recall`, `remember`, `record_event`, `search_code`, `index_code`,
`consolidate`, `stats`, `link`, `forget`) as typed Python methods. No memory
logic lives in Python.

## Requirements

- Python ≥ 3.12, [uv](https://docs.astral.sh/uv/)
- The Go binary, resolved in this order: `binary=` argument → `LADYM_BIN`
  env var → `PATH` → repo-local `bin/ladym` (build with
  `go build -o bin/ladym ./cmd/ladym` from the repo root)

## Usage

Sync:

```python
from ladym_wrapper import LadymClient

with LadymClient() as client:
    client.remember("deploys go through Argo CD", source="notes")
    hits = client.recall("how do we deploy?")
```

Async:

```python
from ladym_wrapper import AsyncLadymClient

async with AsyncLadymClient(db="./ladym.db") as client:
    stats = await client.stats()
```

## Development

```bash
uv sync          # install deps
uv run pytest    # smoke tests (spawn the real Go binary)
```
