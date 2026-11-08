# Task 1 implementation report

## Scope and disposition

- Worktree: `/Users/yuhaochen/Documents/codebase/projanvil/ladyM-agent-memory`.
- Branch: `feat/trusted-agent-memory`; starting commit: `73a763918b982e7eaf41bb4ac0719cf80a2334b6`.
- Implemented Task 1 only: schema/storage contracts, consistency, atomic mutation/deletion, related tests and this report.
- Task 1 brief and its relevant R1-S/R1-C/R1-E/global spec requirements governed the changes. No later-task implementation, subagents, reviewers, main merge or push.
- Go version, dependencies and edition selection are unchanged. Both production editions build with `CGO_ENABLED=0`.
- Local SQLite behavior, required race tests and personal full suite pass. PostgreSQL implementation and gated tests compile, but live PostgreSQL behavior is **not verified**.

## Implementation

### Shared contracts

- Added `schema.IsRetired` with the existing `superseded_by`/truthy `superseded` semantics.
- Added `schema.IsUsable`: non-nil, non-retired, neither stale nor rejected, and `evidence_missing` is not boolean true. Missing status remains usable legacy knowledge.
- Added the requested `MemoryFilter`, `MemoryGuard`, `MemoryWrite`, `MemoryBatch`, `SearchMemories`, `ApplyMemoryBatch`, `DeleteMemorySafely`, `EvidenceReferences` and errors.Is-compatible `ErrMemoryConflict`.
- Knowledge CAS compares ID/content/summary/tags/metadata/source/workspace/layer/type/hash/UpdatedAt. CreatedAt, embedding and access bookkeeping are excluded. Metadata is JSON-roundtripped before comparison, including typed string lists and large-integer-to-float roundtrip behavior.
- Batch updates preserve existing LastAccessAt/AccessCount/Activation, so an ignored Touch does not subsequently get overwritten. Standalone PutMemory retains its existing full-upsert bookkeeping behavior.
- Invalid vectors, invalid batch inputs and unserializable metadata fail explicitly before mutation. Invalid deletion cutoffs cannot accidentally select explicit deletion.
- No public transaction callback or general database revision framework was introduced. Shared transaction plumbing is private to storage.

### Retrieval and SQLite consistency

- New retrieval uses a consistent eligibility/vector snapshot and filters before top-k truncation. Similarity ties use stable ID ordering.
- SQLite retains lazy vector loading. Its persistent generation is advanced by insert/delete/actual embedding-change triggers, including direct SQL changes. Identical embedding assignments, Touch, metadata/stat writes and graph-only writes do not advance it.
- Refresh samples generation and vectors in one read transaction and replaces the index, removing deleted entries. Query/scan/blob/index errors propagate rather than producing empty success.
- Eligibility metadata is read without copying embedding blobs on each search. The internal rebuild counter verifies bookkeeping does not rebuild vectors; the coordinated refresh hook proves an intervening commit cannot permanently poison the generation snapshot.
- A delayed local writer cannot patch a newer warmed snapshot with an unversioned stale vector.
- Legacy VectorSearch remains unfiltered across workspaces/types/statuses and logs errors that its old signature cannot return. Direct index calls no longer panic on invalid queries/topK.
- PostgreSQL uses a MATERIALIZED filtered candidate set and exact similarity/ID ordering, not HNSW preselection. Zero-norm query/stored vectors retain similarity-zero behavior.

### Atomic mutation and evidence deletion

- SQLite reserves its writer transaction before eligibility/CAS reads. PostgreSQL locks the mutation tables, then guard/source/write/edge endpoint rows in stable ID order.
- Guards, writes, incident-edge closures at one timestamp and replacement edges commit together. Missing/stale evidence or guard changes return conflict; database failures roll back writes, retirement, completion stamps, edge closures and vectors.
- PreserveEmbedding explicitly retains an existing embedding; without it a nil vector follows PutMemory's NULL semantics.
- Safe deletion and EvidenceReferences use the same direct dependency rule: usable same-workspace dependants through active produced_from/abstracts edges or source_episode/source_episodes lists. Self-references, retired/unusable dependants, malformed scalar lists and forward-intent provenance do not protect evidence.
- Automatic deletion rechecks both LastAccessAt and references under the mutation lock. Reference-first commits protect evidence; deletion-first makes subsequent guarded publication conflict. Ordinary Store writes/edges preflight unavailable evidence too.
- Explicit DeleteMemory delegates safe explicit deletion, atomically marks usable direct dependants stale/evidence_missing, preserves their existing provenance metadata and removes the source. Missing IDs remain no-ops. Transitive invalidation is not implemented in R1.
- SQLite adds a narrowly scoped, trigger-maintained legacy-source lookup table/index, with a one-time transactional metadata backfill. PostgreSQL adds scalar/GIN metadata-source indexes. Deletion candidate queries use keyed source lookups rather than per-event all-memory JSON scans.

## TDD evidence

All Go edits were preceded by the supplied Modern Go Guidelines CLI `list --go-version 1.26`; its complete output was read.

Initial red:

```text
go test ./storage ./schema -run 'Test(SearchMemories|SQLiteSearch|MemoryBatch|SafeDelete)' -count=1
FAIL: schema IsUsable/IsRetired undefined; storage SearchMemories/MemoryFilter,
      refresh seam and other requested contracts missing.
```

Additional regression reds, followed by implementation and green reruns:

- `TestSQLiteSearchDelayedLocalMutation`: a delayed post-commit vector poisoned the latest cached snapshot (similarity 1 instead of 0).
- `TestMemoryBatchRoundtripAndBookkeeping`: large numeric metadata roundtrip incorrectly caused a guard conflict.
- `TestSafeDeleteEvidenceSnapshotParity`: self/cross-workspace provenance was incorrectly counted by the protection snapshot.
- `TestKnowledgeEligibility`: string `"false"` evidence_missing was incorrectly treated as boolean true.
- `TestSafeDeleteInvalidCutoff`: NaN cutoff incorrectly performed explicit deletion.
- `TestSafeDeleteIgnoresMalformedLegacyList`: scalar source_episodes produced a SQLite malformed-JSON error.
- `TestSafeDeleteSQLiteLegacyDependencyIndex`: keyed legacy-source table was absent.

The database-fault tests inject real SQLite/PG trigger failures after writes/closures and during deletion; they assert rollback of persisted state rather than mocked transaction behavior. Concurrency tests cover competing source consumption and both in-flight reference/deletion orderings. Migration tests preserve vector laziness while backfilling old metadata references.

Existing tests were updated only for the specified changed behavior:

- Warm-once forever became generation-aware observation of direct SQL inserts.
- A nil-vector PutMemory upsert now removes its NULLed vector from retrieval/cache.
- Unscannable refresh rows fail explicitly instead of being silently skipped.
- Pre-embedding migration fixtures drop the newly added embedding trigger before simulating an old schema.

No concrete fake changes were required; existing Store-embedding wrappers still compile.

## Final verification

All commands ran in the requested worktree. Go commands used `TMPDIR="$PWD/.test-tmp"` so test/build scratch files remained inside the project; that scratch directory was removed after validation.

| Command | Result |
| --- | --- |
| `go test ./storage ./schema -run 'Test(SearchMemories\|SQLiteSearch\|MemoryBatch\|SafeDelete)' -count=1` | PASS |
| `go test ./storage ./schema -count=1` | PASS |
| `go test -race ./storage -run 'Test(SQLiteSearch\|MemoryBatch\|SafeDelete)' -count=1` | PASS |
| `go test -tags enterprise ./storage -run '^$'` | PASS compilation |
| `go test ./... -count=1` | PASS complete personal suite |
| `go test ./... -run '^$'` | PASS personal package/test compilation |
| `CGO_ENABLED=0 go test ./storage ./schema -run '^$'` | PASS compilation |
| `CGO_ENABLED=0 go test -tags enterprise ./storage -run '^$'` | PASS compilation |
| `CGO_ENABLED=0 go build ./...` | PASS personal production build |
| `CGO_ENABLED=0 go build -tags enterprise ./...` | PASS enterprise production build |
| `git diff --check` | PASS |
| `go test -tags enterprise ./storage -run 'Test(SearchMemories\|MemoryBatch\|SafeDelete)' -count=1 -v` | 12 PostgreSQL backend subtests explicitly SKIP: LADYM_TEST_PG_DSN not set |

Personal backend-parametrized tests likewise ran SQLite and skipped their PostgreSQL legs through the existing gate. Fresh PostgreSQL database helpers are retained; no shared/user database was used.

## Concerns and verification limits

1. **Live PostgreSQL remains a verification gap.** LADYM_TEST_PG_DSN is unset and the supplied environment has no usable Docker daemon. No live PG search, locking, trigger-fault rollback, metadata index or race claim is made. Controller should run the shared gate against an isolated live PG+pgvector server.
2. **Expanded enterprise all-tests compilation exposes an unchanged baseline test issue:** `go test -tags enterprise ./... -run '^$'` fails at `cli/initcmd_test.go:15` (`isolateEnv` undefined) and `:54` (`storage.NewStore` undefined). Base 73a7639 already contains this untagged file and these references; isolateEnv and SQLiteStore/NewStore are !enterprise. No CLI files were changed. Required enterprise storage compilation and both enterprise/personal production builds pass.
3. PostgreSQL uses coarse table mutation locks to close the legacy-metadata reference predicate race, in addition to stable row locks. This favors correctness and serializes writers, including bookkeeping; lock contention was not benchmarked.
4. Exact PostgreSQL retrieval and SQLite in-memory exact scoring prioritize correctness over ANN-scale latency. No p95, QA or token-saving claim is made. Existing HNSW schema is retained but the new exact query does not depend on it.
5. Source-index migration performs one transactional metadata backfill on first installation, not on every open. Invalid legacy JSON can make that migration fail explicitly rather than silently omitting dependencies.
6. Guard/CAS and unavailable-evidence publication guarantees apply to supported Store mutation APIs. Arbitrary external SQL can bypass Store validation; external vector changes and legacy metadata reference edits nevertheless participate in the SQLite persistent indexes.
7. No correctness architectural blocker was identified for Task 1. Controller review remains outstanding; later tasks must adopt these contracts without treating this report as verification of later-task behavior.

## Commit

Requested subject: `fix(storage): make memory retrieval and mutations consistent`.
The commit includes only Task 1 schema/storage/test changes and this report, with the requested Copilot coauthor trailer. No merge or push is performed.
