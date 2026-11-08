# Task 2 report — honest recall and evidence-preserving lifecycle

## Scope and status

- Implemented Task 2 only, following `task-2-brief.md` and its audited atomic-publication resolution.
- Worktree: `/Users/yuhaochen/Documents/codebase/projanvil/ladyM-agent-memory`.
- Branch: `feat/trusted-agent-memory`.
- Base/Task 1 commit: `e4452f104b69cd4f6ca48563ba2dbb912f304345`.
- Commit subject: `fix(memory): preserve evidence and report recall sufficiency honestly`.
- No Task 1 storage/schema interfaces reimplemented; no Task 3 work, subagents, reviewers, merge, push, dependency changes, generated LLM calls on reads, or user database changes.
- Modern Go Guidelines CLI `list --file-path operations/recall.go` ran before Go edits; complete guideline output was read. Edits used `apply_patch`; formatting used `gofmt`.

## Files

| File | Change |
| --- | --- |
| `operations/recall.go` | Filtered error-returning vector retrieval; exact token coverage; final-result reflection; deterministic bounded traversal; eligible bridges only; one file-path map. |
| `config/config.go` | `RecallConfig.MaxExpandedNodes`, default 128, optional `[recall].max_expanded_nodes`, rejection of negative file/CLI configuration. Programmatic zero uses 128. |
| `config/config_test.go` | Default, explicit zero, override, negative file and CLI configuration coverage. |
| `operations/consolidate.go` | Live L2 fact/note targets only; exact offline NOOP, otherwise ADD; verdict validation; source/target snapshots; atomic source stamp, derived write, provenance and retirement; preservation of all old source episodes on UPDATE. |
| `operations/supersedes.go` | Delegate retirement predicate to schema; stage guarded retirement, preserve embedding, close edges and publish supersedes atomically. Public `Retire` mutates the supplied memory only after commit. |
| `operations/decay.go` | Preload evidence references once; safe deletion with age/activation cutoff; count actual deletion/protection outcomes; additive `Protected` report. |
| `operations/proceduralize.go` | Filtered usable playbook retrieval; distinguish hook tool observations from verified task success; inferred legacy playbooks; union all source IDs/edges; batch publication and update retirement. |
| `layers/procedural.go` | Pure `BuildPlaybook` constructor shared by atomic operations and existing `PutPlaybook`; existing public persistence behavior retained. |
| `operations/l5.go` | Usable clustering/coverage; pure model construction; atomic model/source/provenance/retirement batches; provider errors propagate; independent workspace merge counters. |
| `operations/l6.go` | Workspace-hashed JSON timestamp/ID cursor; lexicographic sorting and comparison; lossless checkpointing; ignore and retain legacy global keys. |
| `operations/resource.go` | Filter resource eligibility before vector top-k truncation; propagate search errors; exclude unusable exact matches. |
| `engine/ops_recall.go` | Same filtered resource search contract and usable pivot results. |
| `engine/engine.go` | Preserve legacy classifier signature; prompt explicitly binds UPDATE/DELETE/NOOP to `similar[0]`, the first live fact/note. |
| `operations/recall_contract_test.go` | Honest token/hit-count sufficiency, stable >128-node expansion, configured/zero budgets, file-map reuse, blocked unusable/out-of-scope bridges, search errors. |
| `operations/trusted_lifecycle_test.go` | Typed offline decisions, invalid verdicts, concurrent/source-conflict/SQL-fault atomicity, source retention, decay protection/races, hook/SDK provenance, L5/playbook atomic publication, workspace cursors and provider errors. |
| `operations/consolidate_provenance_test.go` | UPDATE provenance uses an explicit classifier rather than treating similarity as mutation authority. |
| `operations/pipeline_test.go` | Directly coupled parity expectations updated with positive assertions for preserved evidence, explicit errors, honest hit thresholds, workspace counters and source-safe retirement. |
| This report | Requirements, verification evidence and operational limitations. |

## Atomic-publication audit

- Consolidation builds vectors outside transactions. One `ApplyMemoryBatch` guards the pending episode, selected target and all retained source/resource snapshots, then commits the episode stamp, derived fact, provenance projections, edge closures and retirement together.
- UPDATE projects every existing `source_episode`/`source_episodes` and valid `produced_from` source onto the replacement, not just the latest episode.
- L5 extraction and merge use a pure model constructor. Their batch includes the model/vector, every source snapshot and `abstracts` edge; merge includes all old-model guards and retirements in that same batch.
- Playbooks use pure `layers.BuildPlaybook`, never persist-first `PutPlaybook` in operations. Their batch includes every source guard, source metadata, `produced_from`/resource projections and any old-playbook retirement.
- Missing/deleted/edited sources cause explicit conflicts; a real SQLite edge-insertion trigger fault rolls back all writes. Tests also inspect staged guard/write/edge sets for derived publications.
- Offline similarity no longer overwrites or retires a fact. Unequal nearby evidence is ADD; exact usable fact/note content is NOOP. Existing UPDATE parity tests now either assert ADD plus an unchanged original or explicitly supply the legacy classifier.

## Red evidence

Ran before production changes:

```sh
go test ./operations ./engine -run 'Test(RecallHonest|RecallExpansion|ConsolidateTyped|ConsolidateInvalid|ConsolidateAtomic|ConsolidateUpdateKeeps|DecayProtected|PlaybookProvenance|System2Workspace)' -count=1
```

**Exit 1, expected behavioral failures in operations; engine had no matching tests.**

- `TestRecallHonestTier2AndTokens`: cat matched concatenate; absent token incorrectly sufficient after tier 2.
- `TestRecallExpansionBudgetAndScope`: 166 expanded nodes; unusable/cross-workspace bridges leaked.
- `TestConsolidateTypedOffline`: resources/code/stale/retired matches produced NOOP; unequal fact produced UPDATE.
- `TestConsolidateInvalidVerdictPending`: unknown and targetless verdicts accepted; invalid episodes stamped.
- `TestConsolidateAtomicSourceConflict`: both classifiers won and created two facts; source edit published unsupported evidence; SQL edge fault left partial publication.
- `TestConsolidateUpdateKeepsAllSources`: prior source metadata and provenance lost.
- `TestDecayProtectedLegacyAndDeletion`: referenced legacy episode counted/deleted.
- `TestPlaybookProvenanceAndHookVerification`: no inferred/verified status or complete source projections; tool-only hook accepted.
- `TestSystem2WorkspaceCursor`: global watermark suppressed both workspaces; independent counters absent; L5 provider error swallowed.

Also ran separately before production changes:

```sh
go test ./operations -run 'Test(DerivedModelAndPlaybookAtomic|RecallSearchErrors)' -count=1
```

**Exit 1.** Search failure swallowed; extraction/merge/playbook creation/update left partial derived publications and/or premature retirement after missing-source and SQL edge faults.

## Iteration evidence

- Initial `go test ./operations ./engine ./config` exposed 11 coupled old top-level expectations: UPDATE-by-similarity/superseded-match behavior, targetless classifier behavior, dishonest one-hit Chinese sufficiency, swallowed L5 errors, global merge counters, retirement of an absent item, and negative-top-k panic expectations. Each was replaced by an assertion of the new specified behavior, not by removing assertions.
- A strengthened restart test initially failed because its fixture changed `CreatedAt` after INSERT; Task 1 correctly preserves stored creation time on upsert. The fixture now assigns equal fractional timestamps **before insertion**, and checks exact prompt-consumption order, timestamp/ID round-trip and restart behavior.
- A temporary nested test-declaration syntax error was corrected before the expanded suite ran.
- Additional coverage checks all staged derived guards, edited sources, hook-event-name-only detection, SDK task-only verification, configured budgets, one file-map scan and access/reference races during decay.

## Final green evidence

All commands below completed with exit 0:

```sh
go test ./operations ./engine -run 'Test(RecallHonest|RecallExpansion|ConsolidateTyped|ConsolidateInvalid|ConsolidateAtomic|ConsolidateUpdateKeeps|DecayProtected|PlaybookProvenance|System2Workspace)' -count=1
go test ./operations -run 'Test(DerivedModelAndPlaybookAtomic|RecallSearchErrors)' -count=1
go test ./operations ./engine ./config
go test ./operations ./engine ./config ./layers
go test ./layers ./storage
go test -race ./operations ./engine -run 'Test(ConsolidateAtomic|System2Workspace|RecallExpansion)' -count=1
go test -race ./operations ./engine ./config ./layers
go test -tags enterprise ./operations ./engine ./config -run '^$'
go test -tags enterprise ./operations ./engine ./config ./layers -run '^$'
CGO_ENABLED=0 go test ./operations ./engine ./config ./layers -run '^$'
CGO_ENABLED=0 go test -tags enterprise ./operations ./engine ./config -run '^$'
go test ./...
git diff --check
```

- Full relevant race run: operations 21.465s, engine 25.227s, config 1.362s, layers 2.446s; no race reports.
- Latest required race selector: operations 2.806s; engine compiled with no selector matches.
- Latest full personal suite: all 22 packages with tests passed; `eval` has no tests.
- Every production `.VectorSearch(` call is gone: a repository-wide search excluding tests returned no matches. Task 1's compatibility method remains unchanged.
- Workspace cursor test consumes 5 equal-timestamp episodes per workspace in batches `2,2,1,0`, across actual store reopen, in exact timestamp/ID order, once each. Both legacy global keys remain untouched.
- Fixture databases and scratch logs are removed; only task source/tests/report are staged for the requested commit.

## Concerns and limits

1. **No live PostgreSQL verification.** `LADYM_TEST_PG_DSN` is unset; Docker/PostgreSQL are unavailable in the supplied environment. Enterprise package compilation is proven, not live PG locking, rollback or runtime behavior. SQLite-only regression fixtures are excluded under enterprise.
2. **Independent enterprise CLI baseline failures remain out of scope.** The requested enterprise operations/engine/config compile gate passed. The independently broken enterprise CLI tests were not fixed or represented as passing; a full enterprise runtime suite was not run.
3. The node budget counts non-anchor candidates examined, including rejected candidates, so eligible output can be smaller than 128. Workspace/layer/type/usability constraints prevent rejected candidates from acting as bridges.
4. Recall access bookkeeping intentionally remains best-effort, preserving its previous public behavior; storage/search/provider and lifecycle publication errors propagate.
5. L6 prediction writes and its metadata checkpoint are still separate commits. Provider/checkpoint errors propagate; retry after a partial prediction write can duplicate forecasts. Task 2 requires lossless cursor batching/restart behavior, not a new cursor CAS/storage interface or atomic forecast transaction.
6. No new verification authority was added: model/fact derivations are inferred; playbooks are verified only when all explicitly trusted inputs have `task_outcome=success` and `knowledge_status=verified`. Tool observations alone cannot qualify.
