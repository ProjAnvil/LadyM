# `ladym doctor` — drop the legacy fallback, report instead

Date: 2026-10-23
Status: approved
Supersedes part of: `2026-10-23-home-dir-case-unification-design.md` (§"Legacy read fallback")

## Change of direction

The previous change shipped a read-only compatibility fallback: when
`~/.ladyM/config.toml` was absent, `Load` still read `~/.ladym/config.toml`.
That is being removed.

Rationale: LadyM has very few installations, so carrying a compatibility path
costs more than it saves. A fallback is also silent — it hides the one problem
the user actually needs to know about ("your config is in a directory nothing
reads any more"). Replace it with a diagnostic command that says so out loud.

## Design

### 1. No fallback

`config.GlobalConfigPath()` resolves `~/.ladyM/config.toml` and nothing else.
`config.LegacyDirName` stays exported, but only as the name `doctor` looks for;
no code path resolves it for loading.

### 2. `ladym doctor`

A read-only diagnostic command. No LLM calls, no writes, no network. Safe to
run anywhere, including in CI.

Checks, in output order:

| Check | Severity | Condition |
| --- | --- | --- |
| home dir | ok / warn | `~/.ladyM` missing is only a warning: a fresh install has none |
| legacy home dir | error | `~/.ladym` resolves to a *different* directory than `~/.ladyM` |
| global config | ok / warn / error | missing = warn, unparseable = error |
| database | ok / warn | resolved `db_path` missing = warn (`ladym init`) |
| legacy flat db | warn | `./ladym.db` present while the dotdir db is not |
| gitignore | warn | inside a git repo and `.ladyM/` is not ignored |
| dict dir | warn | `~/.ladyM/dict` missing (CJK degrades to per-character tokens) |

Detecting the case split portably: `os.SameFile` on the two directory stats.
On a case-insensitive filesystem both names are the same inode, so no split is
reported; on a case-sensitive one they are distinct. The check is
`legacy exists && (home missing || !SameFile(home, legacy))`, which is correct
on both.

Severity vs exit code: only `error` findings make `doctor` exit non-zero, so a
fresh install with no dictionary and no database still exits 0.

### 3. Output

Aligned two-column text:

```
LadyM doctor

  ok    home dir         ~/.ladyM
  ok    global config    ~/.ladyM/config.toml
  ok    database         /path/to/.ladyM/ladym.db
  warn  dict dir         ~/.ladyM/dict missing; CJK falls back to per-character tokens
```

`--json` emits the same findings as a list, matching the flag on `recall`.

## Non-goals

- Doctor does not fix anything. It prints the command that would fix it
  (`ladym init`, move the config) and stops.
- No migration of `~/.ladym` to `~/.ladyM`.
- No deprecation warning on `Load`.

## Acceptance

- `go build ./...`, `go vet ./...` clean.
- `go test ./...` green (22 packages).
- `make lint-comments` passes.
- Tests:
  - `GlobalConfigPath()` returns "" when only a legacy-named directory exists
    (checked with an injected `exists` view, because macOS cannot distinguish
    the two names on disk).
  - `Load` does not read a legacy-only global config.
  - doctor reports the case split when the two directories are distinct, and
    stays quiet when they are the same file.
  - doctor exits non-zero only on error-level findings.
