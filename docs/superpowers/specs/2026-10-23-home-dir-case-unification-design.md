# Home directory case unification (~/.ladyM)

Date: 2026-10-23
Status: approved

## Problem

LadyM resolves its home directory from several places, and they do not agree on
the case of the `M`:

| Site | Resolved path |
| --- | --- |
| `config/config.go:1015` | `~/.ladym/config.toml` |
| `secrets/secrets.go:41` | `~/.ladyM/` |
| `storage/cjk_dict.go:148` | `~/.ladyM/dict` (derived from `secrets.Dir()`) |
| `config/config.go:248`, `cli/initcmd.go:49` | workspace `./.ladyM/ladym.db` |

On macOS this is invisible: APFS is case-insensitive by default, so
`~/.ladym` and `~/.ladyM` are the same inode. On case-sensitive filesystems
(Linux, CI, container images on ext4) they are two different directories, and
the global config layer is silently skipped — the user's provider, embedding
and tokenizer settings fall back to defaults with no error.

The docs disagree with each other too: `docs/deployment.md:76` says
`~/.ladym/config.toml` while lines 84/280/303 say `~/.ladyM/dict`;
`README.md:489` says lowercase while 497/517/537 say capital.

## Decision

Standardize on **`.ladyM`** (capital M).

Rejected: standardize on lowercase. The unix convention argument
(`~/.ssh`, `~/.aws`, `~/.docker`, `~/.config`) is real but aesthetic only. Three
concrete costs outweigh it:

1. **Data loss.** `./.ladyM/ladym.db` has been the default workspace database
   path since v0.8.1 (the current latest tag). On a case-sensitive filesystem,
   switching the dotdir to `.ladym` orphans every already-initialized project:
   the engine would not find the database and would create a fresh empty one,
   so the user's accumulated memories disappear.
2. **gitignore breakage.** `gitignoreIgnoresLadyM` (`cli/initcmd.go:164-176`)
   matches `.ladyM`, `.ladyM/`, `/.ladyM`, `/.ladyM/` exactly, with no case
   folding. Projects that already recorded `.ladyM/` and never re-run
   `ladym init` would stop ignoring the new lowercase directory, so the
   database gets committed.
3. **Home migration.** `~/.ladyM/` holds `master.key`, `secrets.enc` and the
   downloaded dictionary (hundreds of MB). Moving it requires either migration
   code or having users re-enter their API keys.

Cost asymmetry, which is decisive:

- Unify on `.ladyM`: **one** compatibility read (the legacy lowercase global
  config), read-only, removable after a deprecation cycle.
- Unify on lowercase: **three or more** compatibility reads (workspace db,
  secrets, dict) plus a gitignore migration, and the two-name state never ends.

## Design

1. **Single source of truth.** Add to `config`:
   - `const DirName = ".ladyM"` — used for both the home directory and the
     workspace dotdir.
   - `func HomeDir() string` — `filepath.Join(home, DirName)`, falling back to
     `.` when `os.UserHomeDir()` fails (preserves current `secrets.Dir()`
     behavior, including the `./.ladyM` fallback asserted by
     `secrets/store_internals_test.go:21`).
   - `const LegacyDirName = ".ladym"` — read-only compatibility spelling.

   `config` cannot import `secrets` (secrets imports config), so the helper
   lives in `config` and `secrets.Dir()` delegates to it.

2. **Legacy read fallback.** `config.GlobalConfigPath()` prefers
   `~/.ladyM/config.toml` and falls back to `~/.ladym/config.toml` when only
   the latter exists. Nothing writes the global config from Go, so this is a
   pure read shim — no dual-write, no migration.

3. **Call sites.**
   - `config.Load` uses `GlobalConfigPath()`.
   - `secrets.Dir()` delegates to `config.HomeDir()`.
   - `config.defaultDBPath()` and `cli/initcmd.go` use `config.DirName` for the
     workspace dotdir.
   - `gitignoreIgnoresLadyM` builds its four accepted spellings from
     `config.DirName`.

4. **Docs and comments.** Sweep `config.go:12`, `config.go:1009`,
   `README.md:489`, `README.zh-CN.md:411`, `docs/deployment.md:76`, and the
   `~/.ladym/config.toml` mentions in
   `docs/superpowers/specs/2026-07-21-providers-config-control-plane-design.md`
   to `.ladyM`. The 2026-07-21 spec is a historical record; update the path
   text and note the legacy fallback.

## Non-goals

- No directory rename, no data migration. Existing `~/.ladyM` stays where it
  is.
- No change to the workspace dotdir spelling (`.ladyM` is already the shipped
  default).
- No deprecation warning on the legacy read. `Load` runs on every CLI and MCP
  start; per-invocation output would be noise in a path that must stay quiet.
  The fallback is documented instead.

## Acceptance

- `go build ./...` and `go vet ./...` clean.
- `go test ./...` green (22 packages).
- `make lint-comments` passes (English-only comments).
- New tests:
  - `config.HomeDir()` returns `~/.ladyM`, and `./.ladyM` when HOME is unset.
  - `GlobalConfigPath()` prefers `.ladyM`, falls back to `.ladym`, returns ""
    when neither exists.
  - `Load` picks up a legacy-only `~/.ladym/config.toml`.
  - `secrets.Dir()` delegates to `config.HomeDir()`.
- Existing tests that hardcode the lowercase global dir
  (`config/apply_and_load_test.go:628,676`) move to `.ladyM`.
