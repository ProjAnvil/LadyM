# Plan: drop the legacy fallback, add `ladym doctor`

Spec: `docs/superpowers/specs/2026-10-23-doctor-command-design.md`
Branch: `feat/doctor-command`

## M1 — Remove the fallback

`config.GlobalConfigPath()` resolves `~/.ladyM/config.toml` and nothing else.
Keep `config.LegacyDirName` exported; nothing loads from it any more.
TDD: flip `TestGlobalConfigPathFallsBackToLegacySpelling` into
`TestGlobalConfigPathIgnoresLegacySpelling` first and watch it go red.
Delete `TestLoadLegacyGlobalConfig`.

## M2 — `cli/doctor.go`

Seven read-only checks: home dir, legacy home dir, global config, database,
legacy flat db, gitignore, dict dir. Three statuses (ok / warn / error); only
error makes the command exit non-zero, so an uninitialized install still exits 0.
`--json` mirrors the flag on `recall`.

The case split is detected with `os.SameFile` on the two directory stats, which
is correct on both case-sensitive and case-insensitive filesystems.

Register `doctorCmd()` in `newRootCmd`.

## M3 — Tests

- `legacyHomeDirSplit` pure-function table (the split branch is unreachable from
  disk state on macOS, so the decision is tested directly).
- per-check tests for database, gitignore, legacy flat db, global config.
- end-to-end `runCmd(newRootCmd(), "doctor")` and `doctor --json`.

## M4 — Docs

README (both languages), ARCHITECTURE CLI list, `docs/deployment.md`, and a
pointer from the 2026-10-23 home-dir spec noting the fallback was removed.

## M5 — Gates

`go build ./...`, `go vet ./...`, `go test ./...`, `make lint-comments`.

## Discipline notes

- Doctor never fixes anything; it prints the command that would.
- English comments only.
