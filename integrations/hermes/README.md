# ladym — Hermes Agent memory provider

Hermes Agent (NousResearch) memory-provider plugin backed by
[ladyM](../../README.md), a brain-inspired multi-layer memory engine (single Go
binary). The plugin talks to `ladym serve` — a newline-delimited JSON-RPC 2.0
MCP stdio server — via a small synchronous client written against the Python
standard library only. **Zero third-party dependencies.**

## Install

0. Prerequisite — the `ladym` binary. Either let the plugin install it
   (after step 1): `hermes ladym install` (add `--fulldict` for the embedded
   CJK dictionary, recommended for Chinese users), or make it resolvable
   manually (any of):
   - download a release asset from
     `https://github.com/ProjAnvil/LadyM/releases` and put it on `PATH`, or
   - `go build -o bin/ladym ./cmd/ladym` from the ladyM repo / `go install`, or
   - set `LADYM_BIN=/path/to/ladym`, or set `ladym_bin` in the plugin config.
1. Install the plugin — no manual file copying needed, pick one:
   - **Hermes plugin manager (recommended):**
     `hermes plugins install ProjAnvil/ladyM/integrations/hermes`
     clones the repo and installs this subdirectory to
     `$HERMES_HOME/plugins/ladym/` (the plugin name comes from `plugin.yaml`).
   - **pip / entry points:** install the package into the Hermes venv —
     ```sh
     uv pip install --python ~/.hermes/hermes-agent/venv/bin/python3 \
       "git+https://github.com/ProjAnvil/ladyM.git#subdirectory=integrations/hermes"
     ```
     Hermes discovers the provider automatically via the
     `hermes_agent.memory_providers` entry point (`ladym = ladym_hermes:register`);
     nothing lands in the plugins directory.
2. Enable and verify (same for both):
   `hermes memory setup` → choose `ladym` → `hermes memory status`.
3. Developers working on this repo can instead symlink the directory:
   `ln -s /path/to/ladyM/integrations/hermes ~/.hermes/plugins/ladym`
   (or copy it to `plugins/memory/ladym/` in a hermes-agent checkout).

## Configuration

Non-secret config lives in `<hermes_home>/ladym.json`, written by
`hermes memory setup` (via `save_config`) or by hand:

| key           | type    | default   | meaning                                        |
| ------------- | ------- | --------- | ---------------------------------------------- |
| `ladym_bin`   | text    | (resolve) | explicit path to the `ladym` binary            |
| `workspace`   | text    | `hermes`  | ladyM workspace name                            |
| `recall_top_k`| integer | `5`       | max memories injected per turn via prefetch    |
| `sync_turns`  | boolean | `true`    | record each conversation turn as an L1 episode |
| `prefetch`    | boolean | `true`    | recall + inject memories before each turn      |

Binary resolution order: `ladym_bin` config → `LADYM_BIN` env → `PATH`.

All state (the SQLite DB) lives under `<hermes_home>/ladym/ladym.db`, so
Hermes profiles are isolated and `hermes backup` covers the database
(`backup_paths()` is empty on purpose).

## Behavior

- **prefetch** — before each turn, recalls up to `recall_top_k` memories for
  the user prompt and injects them as a markdown block. Trivial prompts
  (`hi`, `ok`, …) and recall failures never block a turn: they inject nothing.
- **sync_turn** — after each turn, records an L1 episodic event
  (`agent=hermes`, `action=conversation_turn`, truncated observation/outcome)
  on a daemon thread. Skipped for non-primary `agent_context` (subagent /
  cron / flush) so sub-agents never pollute the user's store.
- **on_session_end / on_pre_compress** — triggers `consolidate` (L1 → L2
  promotion) in the background.
- **recall_status** — surfaces a `🧠 ladyM recalled N memories` indicator.

## Tools exposed to the model

`ladym_recall`, `ladym_remember`, `ladym_record_event`, `ladym_search_code`,
`ladym_index_code`, `ladym_forget` (OpenAI function-calling schemas; see
`provider.get_tool_schemas()`).

## CLI

- `hermes ladym install [--version vX.Y.Z] [--fulldict] [--force]` — download
  the ladym binary from GitHub releases into `$HERMES_HOME/ladym/bin/`,
  verify its SHA-256 against the release `SHA256SUMS`, and write `ladym_bin`
  into `ladym.json`. `--fulldict` installs the variant with the embedded CJK
  dictionary.
- `hermes ladym status` — shows binary resolution, effective config, and live
  store stats when the DB exists.

## Development

Layout: the importable package lives in `src/ladym_hermes/` (provider, stdio
client, CLI); the directory root keeps only the Hermes directory-plugin glue
(`__init__.py` + `cli.py` shims, `plugin.yaml`) so the same directory works
both as a Hermes directory plugin and as a pip-installable package
(`pyproject.toml`, hatchling, zero runtime dependencies).

Tests (pytest, stdlib fakes only — no Hermes install needed; a fake
`agent.memory_provider` module is injected by `tests/conftest.py`):

```sh
python3 -m venv integrations/hermes/.venv
integrations/hermes/.venv/bin/pip install pytest build hatchling
go build -o bin/ladym ./cmd/ladym   # only needed for the e2e smoke test
integrations/hermes/.venv/bin/python -m pytest integrations/hermes/tests -v
```

`tests/test_e2e.py` is skipped automatically when `bin/ladym` is absent;
`tests/test_packaging.py` builds the wheel and verifies the
`hermes_agent.memory_providers` entry point end to end (skipped if
`build`/`hatchling` are missing from the venv).
