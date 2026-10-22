## Highlights

This release gives LadyM a **silent capture path**: Claude Code and Codex now feed memory automatically through their lifecycle hooks, without the agent spending a single token or knowing it is happening. Until now every memory write required the agent to *choose* to call a tool (`remember` / `record_event`); starting with 0.8.0, the harness itself records sessions — closing the biggest gap identified in our comparison with ai-memory: a memory system is only as good as its feed.

- **`ladym hook` — the hook carrier.** One subcommand serves both harnesses: it reads the event JSON from stdin, maps it onto a structured L1 episode through the `record_event` pipeline (controlled-vocabulary action classes, resource extraction from `tool_input`, deterministic `operates_on` edges), writes it, and exits. Observer semantics throughout: stdout stays empty, unknown events and unknown fields are tolerated (schema drift never breaks the binary), and the exit code is never 2 — it can never block or feed decisions back into the harness.
- **`ladym hook install` / `uninstall`.** One command merges the observer entries into `~/.claude/settings.json` and `~/.codex/hooks.json`. User entries and non-hook keys survive verbatim, installs are idempotent (prefix-matched), a one-time `.ladym-bak` backup is taken before the first write, and corrupt config files are refused rather than overwritten. Uninstall removes exactly our entries and nothing else.
- **Plugin market.** `plugins/ladym` (Claude Code plugin: hooks + MCP server) and `plugins/ladym-codex` (Codex plugin) ship the same wiring as installable plugins, catalogued under the `ladym-plugins` marketplace in this repository. Plugins carry no binaries — every command points at the `ladym` on your PATH, so the binary remains the single source of truth.
- **Zero-config db placement.** Hook events default to a per-project `ladym.db` (the hook process inherits the harness cwd), matching the workspace inference (event cwd's directory name). Set `LADYM_DB` for a unified library — the workspace then becomes the isolation key. Changing the db never requires reinstalling hooks.

## What's Changed

### Silent Hook Capture

- **`hook` package** — tolerant parsing of the shared Claude Code / Codex hook event shape (unknown fields ignored), workspace inference (`WorkspaceFromCwd`), tool-name → action-class mapping onto the canonical 31-word vocabulary (`MapToolActionClass`; MCP tools and unknown names stay raw for the worker's LLM backfill), and `ToOperation` for the V1 event set: `SessionStart` (`create`), `SessionEnd`, `UserPromptSubmit` (prompt observation, bounded at 2000 runes), `PostToolUse` / `PostToolUseFailure` (outcome `success` / `fail`, file-path resources from `tool_input`, tool response bounded at 500 runes), and `Stop`.
- **`ladym hook` CLI** — pipes stdin through the mapping into `Engine.Scope(workspace).RecordOperation`. Integration tests prove the full chain: event JSON → episode in the db → `operates_on` edge → retrievable via `ladym recall-ops --resource …`. The `--harness` flag (written by the installer) lands in the `Agent` field, since the two harnesses' event JSON is identical.

### Installer

- **Merge, never overwrite.** The installer reads the *whole* config document and writes it back — keys like `model` in `settings.json` survive. Entries are recognised as ours by the `ladym hook` command prefix, so `--harness` differences and future flag changes stay idempotent.
- **Safety rails.** One-time `.ladym-bak` backup (never rolled over), parent directories auto-created, corrupt JSON refused with a clear error, uninstall is a no-op when the file is absent, and a file that held nothing but our hooks is removed entirely.
- **Post-install guidance.** Claude Code: restart your session. Codex: run `/hooks` to complete the trust review (Codex binds trust to the hook definition hash). A warning is printed when `ladym` is not on `PATH`.

### Plugins & Marketplace

- `plugins/ladym/` — `.claude-plugin/plugin.json` manifest, six-event `hooks/hooks.json` (including the Claude Code-only `PostToolUseFailure`), and `.mcp.json` registering `ladym serve` as the plugin's MCP server.
- `plugins/ladym-codex/` — `.codex-plugin/plugin.json` manifest (explicit `hooks` / `mcpServers` pointers), five-event `hooks/hooks.json` with 10-second timeouts.
- `plugins/.claude-plugin/marketplace.json` — the `ladym-plugins` catalog. Add it from the `/plugin` browser or validate locally with `claude plugin validate plugins/`.
- Five static-validation tests pin the event sets, command strings, manifests, and MCP configs, so the plugin files cannot drift from the installer.

### Documentation

- **`docs/hooks.md`** — the personal-edition install guide: installation for both harnesses, db placement (per-project default vs `LADYM_DB` unified library), workspace inference, the event → memory mapping table, verification via `recall-ops`, plugin installation, troubleshooting, and the honest known-tradeoffs section.
- **`docs/research/`** — the two research notes behind this release: the ai-memory-vs-LadyM comparison and the hook-carrier selection (Go binary over Python/sh, with the verified hook mechanics of both harnesses).
- README (en/zh) and ARCHITECTURE.md updated with the new front-end and project layout.

## Compatibility

- No schema changes, no config breaking changes. Existing MCP tools, CLI commands, and SDK surfaces are untouched; `record`/`remember` keep their exact 0.7 behaviour (locked by regression tests).
- The new capture path is **opt-in**: nothing is recorded until you run `ladym hook install` or install a plugin.
- Enterprise edition is unaffected — capture is a personal-edition concern (the enterprise edition is the memory database; clients feed it).

## Known Limitations

- **No capture-side sanitization yet.** Prompts and tool outputs land as captured; a db is a local file, so the exposure surface equals your disk. Path exclusion and secret redaction are planned for the next release — tracked in `docs/hooks.md`.
- **V1 event set only.** `PreToolUse`, `PreCompact`, and notification events are not captured (deliberate: no double-writes, bounded L1 growth). Unknown events are tolerated silently and can be added later without breaking installed hooks.
- **Codex plugin manifest** follows the documented `.codex-plugin/plugin.json` shape as of this release; the plugin systems of both harnesses are still evolving quickly, so the plugin files are intentionally thin (one manifest + one hooks file each).

## Installation

Prebuilt binaries for linux/darwin (amd64/arm64) — personal, personal-fulldict, and enterprise flavours — are attached below with `SHA256SUMS`. Or build from source:

```sh
go build -o bin/ladym ./cmd/ladym
```

Then:

```sh
ladym hook install --claude   # or --codex, or --all
```

Full guide: `docs/hooks.md`.
