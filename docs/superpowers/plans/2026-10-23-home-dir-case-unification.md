# Plan: home directory case unification (~/.ladyM)

Spec: `docs/superpowers/specs/2026-10-23-home-dir-case-unification-design.md`
Branch: `feat/home-dir-case-unification`

## M1 — Single source of truth in `config`

Add to `config/config.go` (near `defaultDBPath`):

- `const DirName = ".ladyM"`
- `const LegacyDirName = ".ladym"`
- `func HomeDir() string` — join(home, DirName), "." fallback on error
- `func GlobalConfigPath() string` — prefers `~/.ladyM/config.toml`, falls back
  to `~/.ladym/config.toml`, "" when neither exists

`config` cannot import `secrets` (secrets → config), so the helper belongs in
`config`. TDD: write `config/home_dir_test.go` first; it must fail to compile
before the symbols exist.

## M2 — Rewire call sites

- `config.Load`: replace the inline `filepath.Join(home, ".ladym", "config.toml")`
  block with `GlobalConfigPath()`.
- `secrets.Dir()`: return `config.HomeDir()`.
- `config.defaultDBPath()`: use `DirName`.
- `cli/initcmd.go`: use `config.DirName` for the dotdir and build the four
  gitignore spellings from it in `gitignoreIgnoresLadyM`.

## M3 — Tests

- `config/home_dir_test.go`: HomeDir default, HomeDir with unset HOME,
  GlobalConfigPath preference order, GlobalConfigPath with nothing present.
- `config/apply_and_load_test.go`: move the two global-layer fixtures
  (lines 628, 676) from `.ladym` to `.ladyM`; add `TestLoadLegacyGlobalConfig`
  asserting a legacy-only `~/.ladym/config.toml` is still read.

## M4 — Docs and comments

Sweep to `.ladyM`: `config.go:12`, `config.go:1009`, `README.md:489`,
`README.zh-CN.md:411`, `docs/deployment.md:76`, and the two
`~/.ladym/config.toml` mentions in the 2026-07-21 providers spec (add a note
that the legacy lowercase path is still read).

## M5 — Gates

- `go build ./...`
- `go vet ./...`
- `go test ./...`
- `make lint-comments`

## Discipline notes

- No rename, no migration, no dual-write. The legacy path is read-only.
- Do not add per-invocation warning output for the legacy read.
- Comments in English only.
