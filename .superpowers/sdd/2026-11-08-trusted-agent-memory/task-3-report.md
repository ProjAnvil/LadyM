# Task 3 report — canonical capture identity and safe provenance

- Date: 2026-11-08
- Worktree: `/Users/yuhaochen/Documents/codebase/projanvil/ladyM-agent-memory`
- Branch: `feat/trusted-agent-memory`
- Starting HEAD: `68df925a1990b2206b359958babbb7052d6b804c`
- Commit subject: `feat(capture): add canonical identities and trustworthy provenance`

## Status and scope

Task 3 implemented and verified in the personal edition. Both editions' production
code builds with `CGO_ENABLED=0`. The requested enterprise CLI test compilation
remains blocked by two pre-existing test references, recorded below; those files
were not changed. Live PostgreSQL is **not verified**.

No subagents/reviewers, Task 4 implementation, dependency changes, schema/Store
contract changes, transcript reads, LLM-backed reads, user hook installation,
user configuration changes, or user database modifications were performed.
The requested repository `ladym.toml` sample did not exist at the starting HEAD
or in this worktree; it was created as an explicitly tracked, legacy-default sample.
The ignored report and sample are force-added individually, not alongside other
ignored/local files. Temporary test/build inputs and command logs were kept under
this worktree and removed before committing.

## Changes and acceptance evidence

| File(s) | Change / proof |
|---|---|
| `config/config.go`, `config/capture_test.go` | `Config.CaptureIdentityMode string`, default `legacy`; `[capture].identity_mode`, environment precedence, flat CLI override support; invalid effective modes return actionable `ConfigError` from both `Load` and `FromFile`. Tests cover absent default, TOML/env canonical, env precedence, invalid env/TOML including empty TOML and case variants. |
| `hook/identity.go`, `hook/identity_test.go` | Added `ProjectIdentity` and `ToCanonicalOperation`. Absolute clean lexical identity; full SHA-256 `project:` workspace; bounded two-second `git` argument-vector execution, no shell. `git rev-parse --show-cdup` preserves symlink spelling instead of using physical `--show-toplevel`. Non-git cwd falls back to absolute cwd; missing git/invalid cwd/errors do not silently change identity. Root-relative and root-outside absolute resource paths, path marker, and unchanged non-tool resource mapping. |
| `hook/hook.go` | Normal events, including Stop, always stamp `knowledge_status=observed`. Tool events also stamp `tool_outcome=success/fail`. Old `Outcome`, public signatures, basename resources, truncation, and event mappings remain compatible. Tool-response claims cannot create `verified_at`/`task_outcome` or elevate status. |
| `cli/hook.go`, `cli/hook_test.go` | Parses payload/flags/config before identity selection; canonical opt-in only; missing payload cwd uses actual cwd; explicitly supplied `-w` wins over canonical/config/env workspace. Uses existing engine/scope recording pipeline. Tests cover nested git cwd, missing cwd, explicit workspace, persistence, and empty observer stdout. |
| `operations/resource.go`, `operations/canonical_resource_test.go` | Exact structural path helper consumes existing `Store.GetMemory`/`PutMemory` and `schema.IsUsable`; ID is full `path:` SHA-256 of `workspace + NUL + name`. Paths have no aliases or vectors and do not invoke the embedder. Invalid identity strings and unusable/conflicting existing rows return errors. Ordinary exact alias/content and vector resolution skip path entities. Equal vectors are explicitly injected into path rows in tests to prove exclusions for imported rows too. |
| `engine/scope.go`, `engine/ops_recall.go`, `engine/canonical_resource_test.go` | Recording selects the path helper only for `resource_identity=path`. Read pivots prioritize exact case-sensitive path content; ordinary alias/fuzzy candidates exclude paths. Tests prove `a/main.go`, `b/main.go`, and `A/main.go` recall only their own operation; poisoned aliases/equal vectors do not resolve unknown paths as other path entities. Existing explicit recall fallback remains intact. |
| `operations/maintain.go` | Worker alias-merge candidates exclude path entities before embedding/LLM work. An always-yes merge provider cannot merge the equal-vector path fixtures; they remain usable and distinct. T2 lifecycle implementation was reused without reimplementation. |
| `mcp/server.go`, `mcp/provenance_test.go` | Shared serializer adds workspace/created_at/updated_at and exactly the nine R1-P metadata fields. Tests preserve source ID/list/session/status/bool/time shapes and omit unknown/password/cwd/transcript/tool-input/extra-source fields. Empty metadata is an object. Existing tools, input schemas, score/tier/via/content/summary/source/tags are unchanged. |
| `docs/hooks.md`, `ladym.toml` | Document legacy default, explicit canonical opt-in, lexical roots/path IDs, explicit workspace querying, tool-vs-task outcome honesty, exact MCP whitelist, backup before switching, no automatic migration/merge, and irrecoverable legacy basename information. No harness-user configuration is automatically edited. |

Exact MCP metadata whitelist:

```text
session_id
source_episode
source_episodes
knowledge_status
evidence_missing
verified_at
action_class
tool_outcome
task_outcome
```

Legacy `hook/hook_test.go` was left unchanged and passes. Existing record APIs and
stdout-observer behavior remain intact. Trusted SDK metadata remains the existing
explicit route for verified task outcomes; no metadata input endpoint was added.
Path queries can still use the existing marked recall fallback, not a fuzzy
resolution to another path entity. MCP metadata filtering is not content redaction.

## Modern Go pre-edit gate

Before the first Go edit:

```sh
cd /Users/yuhaochen/Documents/codebase/projanvil/ladyM-agent-memory
sh '/Users/yuhaochen/Library/Application Support/Code/agentPlugins/file-Users-yuhaochen-vscode-agent-plugins-github-com-JetBrains-go-modern-guidelines-plugin/1a1685ebdb9/skills/use-modern-go/scripts/run-tool.sh' list --go-version 1.26.4
```

Exit 0; the complete unfiltered guideline list was read. New code uses the
version-appropriate `errors.AsType` and existing `cmp.Or` idioms. All edits used
`apply_patch`; Go formatting used `gofmt`. Dependencies and `go.mod`/`go.sum`
were not changed.

## TDD

The brief's named tests and CLI/engine integration tests were written before
production implementation.

### Initial red selector

All Go test/build/vet commands below ran from the specified worktree with
`CGO_ENABLED=0` and `TMPDIR="$PWD/.task3-test-data"` (created in this project).
Later full/enterprise runs also isolated HOME inside that directory while
preserving the existing `GOCACHE` and `GOPATH`; no dependencies were installed.

```sh
go test ./config ./hook ./cli ./operations ./engine ./mcp \
  -run 'Test(CaptureIdentity|Canonical|HookObserved|MCPProvenance)' -count=1
```

Exit 1. New API tests initially could not compile because the required symbols
did not exist, while engine/MCP tests produced behavioral failures:

```text
config/capture_test.go:14:22: Default().CaptureIdentityMode undefined
config/capture_test.go:26:23: cfg.CaptureIdentityMode undefined
config/capture_test.go:31:23: cfg.CaptureIdentityMode undefined
config/capture_test.go:36:23: cfg.CaptureIdentityMode undefined
hook/identity_test.go:26:22: undefined: ProjectIdentity
hook/identity_test.go:31:20: undefined: ProjectIdentity
hook/identity_test.go:35:29: undefined: ProjectIdentity
hook/identity_test.go:42:15: p.ToCanonicalOperation undefined
hook/identity_test.go:50:19: p.ToCanonicalOperation undefined
hook/identity_test.go:58:18: p.ToCanonicalOperation undefined
cli/hook_test.go:44:22: undefined: hook.ProjectIdentity
operations/canonical_resource_test.go:34:12: undefined: ResolvePathResource
operations/canonical_resource_test.go:38:12: undefined: ResolvePathResource
operations/canonical_resource_test.go:46:16: undefined: ResolvePathResource
operations/canonical_resource_test.go:50:16: undefined: ResolvePathResource
operations/canonical_resource_test.go:54:18: undefined: ResolvePathResource
--- FAIL: TestCanonicalResourceNoFuzzyMergeRecall
    exact path recall "a/main.go": one Resource, three Items, Fallback:false
--- FAIL: TestMCPProvenanceWhitelist
    whitelist = <nil>, want the nine allowed provenance entries
FAIL
```

An initial test-insertion syntax error was corrected before accepting this red
run. A subsequent formatting-time insertion error was also corrected before
green; neither was counted as behavioral regression evidence.

### Additional behavioral red and correction

Project-local temporary fixtures are descendants of the actual repository,
so the non-git identity fixture explicitly sets `GIT_CEILING_DIRECTORIES`
to its fixture base rather than accidentally discovering the parent worktree.

A test was then added for a non-tool payload carrying tool_input. Before the
mapping correction:

```sh
go test ./hook -run 'TestCanonicalIdentity' -count=1
```

```text
--- FAIL: TestCanonicalIdentityPaths
    non-tool event acquired resources:
    Action:session_start Resources:[other/main.go]
FAIL
FAIL github.com/ProjAnvil/LadyM/hook
```

Exit 1. Canonical mapping now changes resources only for the two tool-event
types, preserving legacy event semantics. Symlink spelling and invalid cwd,
missing git, invalid NUL resource, and missing-root cases are also exercised.

To obtain explicit behavioral proof for provenance tests initially blocked by
missing hook API symbols, the two provenance stamps were temporarily removed
and the existing test was run:

```sh
go test ./hook -run '^TestHookObservedToolOutcome$' -count=1
```

```text
--- FAIL: TestHookObservedToolOutcome
    --- FAIL: TestHookObservedToolOutcome/SessionStart
        status = <nil>, want observed
    --- FAIL: TestHookObservedToolOutcome/SessionEnd
        status = <nil>, want observed
    --- FAIL: TestHookObservedToolOutcome/UserPromptSubmit
        status = <nil>, want observed
    --- FAIL: TestHookObservedToolOutcome/Stop
        status = <nil>, want observed
    --- FAIL: TestHookObservedToolOutcome/PostToolUse
        status = <nil>, want observed
        tool_outcome = "", want "success"
    --- FAIL: TestHookObservedToolOutcome/PostToolUseFailure
        status = <nil>, want observed
        tool_outcome = "", want "fail"
FAIL
FAIL github.com/ProjAnvil/LadyM/hook
```

Exit 1. Both stamps were restored, formatted, and the final selector/full suite/
both builds/vet were rerun successfully before committing.

## Verification commands and output

### Final brief selector

```sh
go test ./config ./hook ./cli ./operations ./engine ./mcp \
  -run 'Test(CaptureIdentity|Canonical|HookObserved|MCPProvenance)' -count=1
```

Exit 0:

```text
ok github.com/ProjAnvil/LadyM/config     0.786s
ok github.com/ProjAnvil/LadyM/hook       0.731s
ok github.com/ProjAnvil/LadyM/cli        0.737s
ok github.com/ProjAnvil/LadyM/operations 0.465s
ok github.com/ProjAnvil/LadyM/engine     0.605s
ok github.com/ProjAnvil/LadyM/mcp        0.592s
```

### Brief relevant package suites

```sh
go test ./config ./hook ./cli ./operations ./engine ./mcp
```

Exit 0:

```text
ok github.com/ProjAnvil/LadyM/config     0.104s
ok github.com/ProjAnvil/LadyM/hook       0.351s
ok github.com/ProjAnvil/LadyM/cli        2.522s
ok github.com/ProjAnvil/LadyM/operations 3.089s
ok github.com/ProjAnvil/LadyM/engine     7.634s
ok github.com/ProjAnvil/LadyM/mcp        0.514s
```

### Full personal suite

Both `go test ./...` and a final uncached `go test ./... -count=1` passed.
Final uncached output (exit 0):

```text
ok github.com/ProjAnvil/LadyM/adapter          0.112s
ok github.com/ProjAnvil/LadyM/api              1.804s
ok github.com/ProjAnvil/LadyM/cli              2.804s
ok github.com/ProjAnvil/LadyM/client/golang    1.358s
ok github.com/ProjAnvil/LadyM/cmd/ladym        0.596s
ok github.com/ProjAnvil/LadyM/cmd/ladymconsole 0.333s
ok github.com/ProjAnvil/LadyM/code             0.926s
ok github.com/ProjAnvil/LadyM/config           0.125s
ok github.com/ProjAnvil/LadyM/console          0.347s
ok github.com/ProjAnvil/LadyM/engine           7.150s
?  github.com/ProjAnvil/LadyM/eval             [no test files]
ok github.com/ProjAnvil/LadyM/hook             0.425s
ok github.com/ProjAnvil/LadyM/ladym            0.513s
ok github.com/ProjAnvil/LadyM/langgraph        0.788s
ok github.com/ProjAnvil/LadyM/layers           0.547s
ok github.com/ProjAnvil/LadyM/mcp              0.531s
ok github.com/ProjAnvil/LadyM/observability    0.360s
ok github.com/ProjAnvil/LadyM/operations       3.457s
ok github.com/ProjAnvil/LadyM/providers        0.143s
ok github.com/ProjAnvil/LadyM/schema           0.327s
ok github.com/ProjAnvil/LadyM/secrets          1.066s
ok github.com/ProjAnvil/LadyM/storage          2.449s
ok github.com/ProjAnvil/LadyM/storage/fulldict 3.502s
```

### Requested enterprise test compile: known baseline blocker

```sh
go test -tags enterprise ./config ./hook ./cli ./mcp -run '^$'
```

Exit 1:

```text
ok github.com/ProjAnvil/LadyM/config 0.311s [no tests to run]
# github.com/ProjAnvil/LadyM/cli [github.com/ProjAnvil/LadyM/cli.test]
cli/initcmd_test.go:15:2: undefined: isolateEnv
cli/initcmd_test.go:54:24: undefined: storage.NewStore
ok github.com/ProjAnvil/LadyM/hook 0.516s [no tests to run]
FAIL github.com/ProjAnvil/LadyM/cli [build failed]
ok github.com/ProjAnvil/LadyM/mcp 0.441s [no tests to run]
FAIL
```

These exact pre-existing errors were already documented in the T1/T2 ledger.
They are not Task 3 regressions and were deliberately not fixed.

### Independent enterprise compile and pure behavior gates

```sh
go test -tags enterprise ./config ./hook ./operations ./engine ./mcp -run '^$'
go test -tags enterprise ./config ./hook ./mcp \
  -run 'Test(CaptureIdentity|CanonicalIdentity|HookObserved|MCPProvenance)' -count=1
```

Both exit 0:

```text
ok github.com/ProjAnvil/LadyM/config     (cached) [no tests to run]
ok github.com/ProjAnvil/LadyM/hook       (cached) [no tests to run]
ok github.com/ProjAnvil/LadyM/operations 0.394s [no tests to run]
ok github.com/ProjAnvil/LadyM/engine     0.881s [no tests to run]
ok github.com/ProjAnvil/LadyM/mcp        (cached) [no tests to run]
ok github.com/ProjAnvil/LadyM/config     0.779s
ok github.com/ProjAnvil/LadyM/hook       0.865s
ok github.com/ProjAnvil/LadyM/mcp        0.697s
```

The storage-backed path/CLI tests keep the repository's `!enterprise` test tags.
These compile/pure gates do not claim live PostgreSQL integration coverage.

### Production builds and lint

```sh
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go build -tags enterprise ./...
CGO_ENABLED=0 go vet ./config ./hook ./cli ./operations ./engine ./mcp
git diff --check
```

All exit 0; Go commands produced no diagnostics. Both production builds and vet
were rerun after the provenance mutation proof was restored. No `-race` run was
performed because it requires enabling cgo, contrary to the zero-cgo constraint.

### PostgreSQL / Docker limitation

Availability probe:

```sh
docker info --format '{{.ServerVersion}}'
# Presence-only check of LADYM_TEST_PG_DSN; no DSN/secret values printed.
```

```text
failed to connect to the docker API at unix:///Users/yuhaochen/.orbstack/run/docker.sock;
check if the path is correct and if the daemon is running:
connect: no such file or directory
LADYM_TEST_PG_DSN is not set.
```

Docker CLI exists but its daemon is unavailable. No live PostgreSQL validation,
Docker startup, database provisioning, or skipped-as-passed PG claim was made.

## Remaining concerns / handoff

1. The pre-existing enterprise CLI test compilation blocker remains.
2. Live PostgreSQL path-record/recall behavior requires an available test DSN
   and the existing fresh-database helpers in a later validation environment.
3. Switching legacy/canonical modes is an explicit, backup-first user decision;
   no automatic old-workspace or resource merge is safe.
4. Captured content may contain sensitive text; only MCP metadata is whitelisted,
   not arbitrary observation/content text.
5. Task 4 is untouched. Its consumption of the nine-field provenance contract
   can proceed independently after its prerequisite gates.

## fix1 — reviewed Important issues (round 1/5)

- Base: `06b710b` (`feat(capture): add canonical identities and trustworthy provenance`).
- Scope: only the two reviewed Important issues, their regression tests, and
  directly related capture documentation. No subagents, Task 4, dependencies,
  user configuration, database changes, or automatic identity migration.
- `hook/identity.go`: bounded Git discovery removes inherited repository-local
  selection variables (including `GIT_DIR`, `GIT_WORK_TREE`, `GIT_COMMON_DIR`,
  `GIT_INDEX_FILE`, object/alternate paths) and the entire `GIT_CONFIG*` family.
  Normal executable lookup/PATH, lexical symlink spelling, timeout, and errors
  remain unchanged.
- `engine/ops_recall.go`: blank validation no longer rewrites the resource query.
  Structural path matching sees its original bytes; only the subsequent ordinary
  legacy resolution trims whitespace. Existing marked recall fallback is retained.
- `hook/identity_test.go`: real Git fixtures cover nested/root identity and
  non-Git fallback with work-tree, foreign repository, common-dir, index, object,
  alternate, counted/parameter/global/system/config selection pollution.
- `engine/canonical_resource_test.go`: `main.go`, ` main.go`, and `main.go ` retain
  separate identities and return only their own operations. Unknown spaced paths
  do not pivot to another path; ordinary padded queries still resolve without
  embedding, and blank queries still return `ErrResourceRequired`.
- `docs/hooks.md`: documents environment isolation and whitespace-exact paths.
- The already-tracked report is under ignored `.superpowers`; staging used
  `git add -f` only for this explicitly requested report's fix1 append. No other
  ignored files are staged.

### Modern Go / command environment

Both commands ran before Go edits; exit 0, complete unfiltered output read:

```sh
sh '/Users/yuhaochen/Library/Application Support/Code/agentPlugins/file-Users-yuhaochen-vscode-agent-plugins-github-com-JetBrains-go-modern-guidelines-plugin/1a1685ebdb9/skills/use-modern-go/scripts/run-tool.sh' list --file-path hook/identity.go
sh '/Users/yuhaochen/Library/Application Support/Code/agentPlugins/file-Users-yuhaochen-vscode-agent-plugins-github-com-JetBrains-go-modern-guidelines-plugin/1a1685ebdb9/skills/use-modern-go/scripts/run-tool.sh' list --file-path engine/ops_recall.go
```

Changes use `slices.DeleteFunc`, `strings.Cut`, and `errors.Is` idioms. All edits
used `apply_patch`; formatting used `gofmt`. Go validation ran from this worktree:

```sh
mkdir -p .task3-fix1-data/home
export CGO_ENABLED=0 TMPDIR="$PWD/.task3-fix1-data"
export GOCACHE="$(go env GOCACHE)" GOPATH="$(go env GOPATH)"
export HOME="$PWD/.task3-fix1-data/home"
```

Scratch test inputs/logs stayed inside this worktree and were removed after
verification. No dependencies were installed.

### Red → green

Before production edits:

```sh
gofmt -w hook/identity_test.go engine/canonical_resource_test.go
go test ./hook ./engine \
  -run 'TestCanonical(IdentityPollutedGitEnv|ResourceNoFuzzyMergeRecall|RecallPreservesLegacyWhitespace)$' \
  -count=1
```

Exit 1. Output excerpts (long Git fixture paths/root mismatch details omitted):

```text
--- FAIL: TestCanonicalIdentityPollutedGitEnv (0.33s)
    --- FAIL: TestCanonicalIdentityPollutedGitEnv/work-tree (0.03s)
    --- FAIL: TestCanonicalIdentityPollutedGitEnv/foreign-repository (0.03s)
    --- FAIL: TestCanonicalIdentityPollutedGitEnv/objects (0.03s)
FAIL github.com/ProjAnvil/LadyM/hook 1.055s
--- FAIL: TestCanonicalResourceNoFuzzyMergeRecall (0.01s)
    --- FAIL: TestCanonicalResourceNoFuzzyMergeRecall/_main.go (0.00s)
    --- FAIL: TestCanonicalResourceNoFuzzyMergeRecall/main.go_ (0.00s)
    --- FAIL: TestCanonicalResourceNoFuzzyMergeRecall/_main.go_ (0.00s)
    --- FAIL: TestCanonicalResourceNoFuzzyMergeRecall/main.go__ (0.00s)
    --- FAIL: TestCanonicalResourceNoFuzzyMergeRecall/__main.go (0.00s)
--- FAIL: TestCanonicalRecallPreservesLegacyWhitespace (0.00s)
    canonical_resource_test.go:87: ordinary resolution must trim after path lookup: <nil> embed boom
FAIL github.com/ProjAnvil/LadyM/engine 0.650s
```

The Git failures showed inherited selection producing incorrect root/workspace
values, including non-Git fallback. The path failures showed padded filenames
returning `main.go` operations and unknown padded paths incorrectly reporting
`Fallback:false`. The ordinary direct-lookup test exposed the need to trim only
after structural matching.

After production edits and `gofmt -w hook/identity.go engine/ops_recall.go`,
the identical focused command exited 0:

```text
ok github.com/ProjAnvil/LadyM/hook   0.793s
ok github.com/ProjAnvil/LadyM/engine 0.514s
```

### Related suites and caller regression gate

```sh
go test ./config ./hook ./cli ./operations ./engine ./mcp -count=1
```

Exit 0:

```text
ok github.com/ProjAnvil/LadyM/config     0.302s
ok github.com/ProjAnvil/LadyM/hook       0.651s
ok github.com/ProjAnvil/LadyM/cli        2.551s
ok github.com/ProjAnvil/LadyM/operations 2.817s
ok github.com/ProjAnvil/LadyM/engine     6.876s
ok github.com/ProjAnvil/LadyM/mcp        0.640s
```

```sh
go test ./... -count=1
```

Exit 0; all personal-edition package suites passed:

```text
ok github.com/ProjAnvil/LadyM/adapter          0.633s
ok github.com/ProjAnvil/LadyM/api              2.363s
ok github.com/ProjAnvil/LadyM/cli              3.013s
ok github.com/ProjAnvil/LadyM/client/golang    1.819s
ok github.com/ProjAnvil/LadyM/cmd/ladym        0.543s
ok github.com/ProjAnvil/LadyM/cmd/ladymconsole 0.345s
ok github.com/ProjAnvil/LadyM/code             1.291s
ok github.com/ProjAnvil/LadyM/config           0.147s
ok github.com/ProjAnvil/LadyM/console          0.382s
ok github.com/ProjAnvil/LadyM/engine           7.393s
?  github.com/ProjAnvil/LadyM/eval             [no test files]
ok github.com/ProjAnvil/LadyM/hook             1.174s
ok github.com/ProjAnvil/LadyM/ladym            0.680s
ok github.com/ProjAnvil/LadyM/langgraph        1.266s
ok github.com/ProjAnvil/LadyM/layers           1.344s
ok github.com/ProjAnvil/LadyM/mcp              0.535s
ok github.com/ProjAnvil/LadyM/observability    0.369s
ok github.com/ProjAnvil/LadyM/operations       3.995s
ok github.com/ProjAnvil/LadyM/providers        1.031s
ok github.com/ProjAnvil/LadyM/schema           0.948s
ok github.com/ProjAnvil/LadyM/secrets          1.010s
ok github.com/ProjAnvil/LadyM/storage          2.421s
ok github.com/ProjAnvil/LadyM/storage/fulldict 3.526s
```

### Enterprise and lint

```sh
go test -tags enterprise ./config ./hook ./cli ./mcp -run '^$'
```

Exit 1; unchanged pre-existing CLI test compilation blocker:

```text
ok github.com/ProjAnvil/LadyM/config (cached) [no tests to run]
# github.com/ProjAnvil/LadyM/cli [github.com/ProjAnvil/LadyM/cli.test]
cli/initcmd_test.go:15:2: undefined: isolateEnv
cli/initcmd_test.go:54:24: undefined: storage.NewStore
ok github.com/ProjAnvil/LadyM/hook 0.434s [no tests to run]
FAIL github.com/ProjAnvil/LadyM/cli [build failed]
ok github.com/ProjAnvil/LadyM/mcp (cached) [no tests to run]
FAIL
```

```sh
go test -tags enterprise ./hook -run '^TestCanonicalIdentity' -count=1
go build -tags enterprise ./hook ./engine
go vet ./hook ./engine
git diff --check
```

All exit 0. Hook behavior output:

```text
ok github.com/ProjAnvil/LadyM/hook 0.942s
```

Build, vet, and diff check produced no diagnostics. Live PostgreSQL and race
validation are not claimed; the existing enterprise CLI blocker is not fixed.
