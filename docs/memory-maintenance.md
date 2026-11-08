# Memory learning and maintenance

R2–R4 are implemented on the existing Engine/Scope, memories/edges, atomic
storage writer and System 2 worker. Personal supports SQLite and PostgreSQL;
enterprise includes PostgreSQL only. These are explicit interfaces and opt-in
maintenance, not an autonomous agent, command executor, new queue or service.
Whole-branch controller review and scoped repairs passed. Final evidence below
includes real PostgreSQL, both production editions and enabled SDK integrations.

`Engine.WithContext(ctx)` creates an immutable database-context view: cancellation
does not mutate the root or sibling views. Default and cached per-workspace L0
buffers remain shared with the root, without copying mutexes or caches; different
workspaces remain isolated. PostgreSQL query, batch, copy and transaction paths
inherit the caller context, including read transactions waiting for migration
locks or pool connections. Rollback uses a separate bounded cleanup context.

## Trust, scopes and deletion

See [memory learning](memory-learning.md) for typed evidence, claims, feedback,
archive and navigation requests. Verification is **caller-attested**: a caller
with workspace write authority supplies successful test/tool evidence or user
confirmation. LadyM does not execute a test, independently verify a result,
promote model confidence to verified, import transcripts or read referenced
paths/URLs. HTTP actor attribution follows authentication, never a body's
`actor`; local CLI/MCP labels are attribution, not authenticated human identities.

Evidence uses whole-content SHA256 and optional zero-based rune excerpts.
Verify/correct/reject require the current revision and commit with source guards.
Correction retains the claim ID, increments revision and invalidates descendants.
Touch/access accounting does not change knowledge revision.

Every read checks usability, TTL, source fingerprints and managed revisions.
Environment/code version are wildcards only when empty on the claim; otherwise
they must exactly match the request. All claim conditions must occur exactly,
case-sensitively in caller-attested Bootstrap conditions. This applies to topic
and task provenance too. Legacy knowledge remains explicitly unverified.

Content, trust, scope and usability changes recursively invalidate same-workspace
dependants through provenance edges and legacy source fields. Traversal includes
already-stale intermediaries and terminates on cycles. Explicit deletion is
synchronous, ignores archive pinning, and cannot depend on worker progress.
Public typed reads, recall, list and Bootstrap suppress obsolete managed bodies.
Internal Store reads are privileged raw audit reads, not a safe public API.
Invalidation retains source IDs and stale audit records; it is not an assertion
that every derived database byte has been physically erased.

Archives are disabled by default. Their atomic limits are 64 KiB input/persisted
text per entry and 8 MiB undeleted archive text per workspace, including expired,
stale, retired and pinned entries; defaults retain for 30 days. Best-effort
credential redaction is not complete PII removal. Pinning preserves audits,
does not extend trust or remove bytes from quota. Expiry never silently frees
capacity; explicit deletion cannot be undone by vector rollback.

## Navigation and units

`BuildTopic` explicitly creates a deterministic, revision-guarded navigation
view of 1–64 same-workspace sources, including verified claims. It preserves all
source IDs with rune-safe excerpts within 16 KiB. It does not invent call graphs,
run a model or automatically build a repository wiki. `GetTopic` never returns
a stale topic body. HybridRecall independently ranks lexical, eligible vector
and bounded structural candidates with deterministic RRF60 ordering; exact
symbols work without embeddings. Jobs/predictions cannot displace knowledge.

Bootstrap is a **reference**, not executable instructions or a privileged system
policy. Only explicitly named topics/task state and fresh verified claims are
eligible. Explicit task state preserves the caller's goal/next step with a
default 24-hour TTL; model predictions are not user authorization.

Default Bootstrap is bytes, maximum 4096; embedded token mode defaults to 512,
maximum 1024. Complete output (header, labels, IDs, separators and excerpts) is
measured, not an excerpt-only estimate. Invalid UTF-8 and malicious/invalid
counters are rejected. Token mode requires a host-owned, pure-local
`engine.TokenCounter` with meaningful `ModelName`; HTTP/MCP/CLI/HTTP SDKs support
bytes only and reject tokens. Embedded Go and LangGraph can inject a counter.
No tokenizer/model service is started. Local token counts are not billable usage.

## Incremental jobs and budgets

```toml
[system2]
enabled = true
budgeted_enabled = true
max_candidates = 50
max_calls = 8
max_reserved_bytes = 8192
max_wall_s = 30
concurrency = 1
lease_s = 60
max_attempts = 3
```

`budgeted_enabled` defaults false; it selects the bounded pipeline in the
existing worker when System 2 is enabled. Existing compatibility behavior remains
when it is false. Embedded hosts can explicitly call
`eng.RunBudgetedCycle(ctx, workspace)` without starting a worker. Concurrency is
1–4; attempts are at most 3. Configured budgets must be positive and finite.

Only `new_episode`, `refresh_topic` and `expire_claim` are schedulable. Topic
refresh/expiry are deterministic and need zero generative calls. New-episode
extraction uses contextual structured generation, or the conservative offline
path when no LLM is configured. Jobs cannot turn source text into shell commands.

| Surface | Request/list/status |
| --- | --- |
| Engine Scope | `ScheduleMaintenance`, `ListMaintenanceJobs`, `MaintenanceStatus`, `HeartbeatMaintenance` |
| HTTP | `POST /api/maintenance/jobs`, `GET /api/maintenance/jobs`, `GET /api/maintenance/status` |
| MCP | `schedule_maintenance`, `list_maintenance_jobs`, `maintenance_status` |
| CLI | `ladym maintenance request <operation> <target-id> --input-ids SOURCE -w project`; `maintenance jobs`; `maintenance status` |
| Go/Python/TS HTTP SDK | `ScheduleMaintenance`/`schedule_maintenance`/`scheduleMaintenance`, matching list/status methods |

Requests are workspace-scoped and persist operation, target, canonical input IDs,
Args, input fingerprints/revisions and checkpoint. IDs include workspace,
operation, target, inputs and Args. Unchanged completed work remains a dedup
record; a changed input gets a different job. Jobs are exempt from Decay.

State is pending/running/done/failed/superseded. Lease acquisition is a CAS:
one owner wins, heartbeat increments revision, and a crashed owner's expired
lease permits takeover. At transaction time completion checks running state,
owner/revision, an unexpired lease, live context, every input/target snapshot and
the active migration generation. A late/expired/cancelled caller cannot publish
outputs or checkpoints. Data, graph changes and checkpoint commit together.

The deadline context reaches contextual Store views and providers. Injected
generation must implement `providers.ContextLLM`; incapable providers are
explicitly rejected, not launched in an uncancellable background goroutine.
`StructuredResult.RawOutputBytes` measures raw model text **before** parsing;
compact parsed JSON cannot reconstruct it. Each call reserves the full encoded
input plus 1024 output **bytes**; `MaxOutputBytes=1024` and API
`MaxOutputTokens=1024` are separate bounds. Provider errors and budget stops are
observable, not successful completion of pending jobs.

Cycle status is `candidate_count`, `calls`, `reserved_bytes`,
`measured_input_bytes`, `measured_output_bytes`, `completed_jobs`, `pending_jobs`,
`failed_jobs`, `elapsed_ms`, `stop_reason`, `unit="bytes"`. Timing is milliseconds;
reservations/measurements are bytes, not tokenizer or billing-token claims.

## Backup, model migration and downgrade

See [embedding migrations](embedding-migrations.md) for runnable local
status/adopt/resume/rollback/finalize commands and generation fences.

1. Back up the **whole** store before upgrading/adopting/migrating: use a
   consistent SQLite backup (including WAL as appropriate), or the deployment's
   PostgreSQL backup procedure. Migration vector backups are not full backups.
2. Identity includes provider, model, dimension and tokenizer/version. Same
   dimensions do not imply compatible models. Unknown legacy vectors need
   explicit caller-attested adoption; adoption never calls a provider.
3. Resume stages ordered ID-keyset batches with a unique generation. Committed
   progress survives restart. Activation verifies every eligible candidate,
   not merely the staged vector count.
4. Staging/rollback windows freeze create/content/vector/trust/scope changes;
   Touch and nonknowledge operational metadata remain allowed. Explicit deletion
   cancels staging atomically, invalidates descendants and removes deleted sources
   from backup. Rollback restores only vectors/identity, never deleted text.
5. Explicit migration retains rollback until rollback/finalize. Finalize is
   irreversible. Compatibility `embedding_allow_dim_change` uses the durable
   pipeline and generation-bound automatic finalization, including crash recovery;
   it cannot take over an explicitly retained window. Path resources stay
   structural and are never embedded.
6. Before downgrading, stop writers/the existing worker, disable opt-ins and
   resolve any migration window; restore the pre-upgrade **full backup** if an
   older binary must be used. There is no supported reverse schema/trust downgrade.
   An older reader may ignore managed types or recursive freshness and must not
   be assumed safe just because ordinary APIs still exist. Finalized vectors
   require full backup restoration or another forward migration to regain the
   prior model. Never restore a backup to resurrect explicitly deleted content
   without an independent data-retention/privacy decision.

## Offline and live evaluation

```sh
go test -count=1 ./scenarios
PYTHONPATH=benchmarks/src python -m longmemeval.learning_eval
PYTHONPATH=benchmarks/src python -m longmemeval.learning_eval \
  --edition enterprise --backend postgres
```

PostgreSQL requires an explicitly supplied `LADYM_TEST_PG_DSN`. The runner creates
random test databases on that server, not a new server or a user's memory store.
It launches only the existing Go test runner with local hashing embeddings;
no dataset download, model completion or implicit hosted-provider request occurs.

Four variants are `stateless`, `direct_source`, `r1`, `r2_topic`, with per-variant
`source_coverage`, `stale_usage`, `read_elapsed`, `context_units`, `unit="bytes"`.
Coverage is the fraction of two known persisted cross-file source roots reachable
from selected memories through the **real** source graph; stale usage is the
fraction of selected memories failing shared freshness. Direct-source reads select
the exact persisted sources; R1 uses ordinary Recall; R2 explicitly reads Bootstrap
and GetTopic. The shared roots and rejected distractor have local hashing vectors,
so R1 is not artificially starved; a separate lifecycle scenario proves exact
hybrid lookup without embeddings. Context units are UTF-8 bytes of the assembled returned reference;
read elapsed is seconds of those reader calls, excluding fixture creation and
subsequent metric calculation. Stateless receives only the task prompt.

These observations prove navigation/lifecycle contracts, **not** QA accuracy,
LLM improvements, token savings, retrieval superiority or production latency.
The existing LongMemEval live answer/judge entrypoints require explicit
`--model`, positive finite `--budget-seconds` and positive `--limit`.
The budget is **wall seconds**, not money or billable tokens. Answers disable
SDK retries and run in per-call subprocesses killed/reaped on the shared total
answer deadline, including startup, client setup, transport, SDK decoding and
shutdown (not merely read inactivity). The deadline starts at answer-callable
construction: intervening recall/context/output work consumes it but is not
interrupted; dataset loading and final reporting are not capped. OS spawn/reap
overhead is unavoidable. Provider failures retain their type/message in a
`RuntimeError` envelope; per-question fault reporting is unchanged.
The pinned judge's startup/requests/retry loop has a separate subprocess budget;
local retrieval scoring, hypothesis counting, aggregation and reporting are
outside it. See [exact benchmark boundaries](../benchmarks/README.md).
Hypotheses over the explicit
limit are rejected before judging. Credentials alone do not authorize network.
No live model measurement was run for this release.

## Acceptance evidence (B01–B14)

The commands below use the dedicated supplied pgvector server at
`127.0.0.1:55439`; credentials are environment-only and never recorded here.
`internal/testutil.PGDatabase` uses random databases and a fresh
`context.WithTimeout(context.Background(),10*time.Second)` for cleanup, reporting
cleanup errors. A regression verifies DROP after the test context is cancelled.
The pre-existing storage/operations wrapper helpers delegate to that one helper.
No service was started/stopped or user memory database changed.

`P` = `CGO_ENABLED=0 go test -count=1 ./...`;
`E` = `CGO_ENABLED=0 go test -tags enterprise -count=1 ./...`, both with live DSN.
Counts below are conservative passing **leaf** cases explicitly labelled
`/postgres`, never skips or parent tests; other live-PG-only leaves are excluded.
Selectors identify overlapping contract families, not disjoint totals.

| Acceptance | Contract / executable evidence | P / E PG leaves |
| --- | --- | --- |
| B01 | Claim attestation, feedback CAS and source/trust races: `TestClaim{AttestedVerification,FeedbackCAS,SourceChangedDuringFeedback,ScopeAndTrustChanges}` in P/E | 8 / 8 |
| B02 | Recursive/bulk/managed invalidation and `TestTrustedMemoryRecursiveDelete` in P/E | 15 / 15 |
| B03 | Rune/evidence freshness and `TestTrustedMemoryTransitiveExpiry` before maintenance in P/E | 11 / 11 |
| B04 | Archive defaults/redaction/persisted quota/retention/pinning: `TestArchive*` in P/E | 4 / 4 |
| B05 | HTTP/MCP/CLI/local Go/HTTP Go claims and actor/workspace tests in P/E; full Python/TS SDK gates below | 7 / 7 |
| B06 | `TestTopic*`, repeated corrected task/navigation in P/E | 14 / 14 |
| B07 | `TestHybrid*`, exact nonembedded source and real four-reader fixture in P/E | 6 / 6 |
| B08 | `TestBootstrap*`, transitive expiry; host counter/UTF-8/scope byte/token bounds in P/E | 7 / 7 |
| B09 | `TestMaintenance*` lease/crash/checkpoint/input dedup/attempts; E2E restart/budget in P/E | 32 / 32 |
| B10 | Context/raw output/reservation/call/wall budgets and provider rejection in P/E | 32 / 32 |
| B11 | Migration generation/resume/rollback/finalize/identity and CJK tokenizer tests in P/E | 45 / 45 |
| B12 | `TestTrustedMemoryVariants` and `pytest benchmarks/tests/test_learning_variants.py` | 1 / 1 |
| B13 | Both full live-PG editions plus cancelled-context cleanup regression; total labelled PG leaves | 230 / 198, plus 39 new PG-only context leaves in each edition |
| B14 | Full editions; race seven packages; zero-CGO production/dependency boundaries; short eval; full SDK/benchmarks; `make lint-comments` | see final gate summary |

SQLite-only CLI init/legacy-file tests are correctly `!enterprise` tagged, not
silently skipped. Four previously SQLite-only Engine CJK cases and one-shot Go
SDK operation round trips now run explicit PostgreSQL variants. All critical
Engine migration suites also run enterprise PG, including two simultaneously
live hashing Engines. Existing excluded-edition subtest skips and helper-process
skips are listed separately in release evidence; none count as acceptance passes.

Final controller verification after whole-branch repairs:

| Gate | Actual result (skips not passes) |
| --- | --- |
| P, live PG, zero CGO | 24 tested packages, 1797 passing leaves, 230 labelled PG leaves plus 39 new PG-only context leaves; one helper-process test skipped; eval requires its separate tag |
| E, live PG, zero CGO | 21 tested packages, 751 passing leaves, 198 labelled PG leaves plus 39 new PG-only context leaves; 15 excluded-SQLite subtests skipped with live PG alternatives; code/eval/langgraph/layers have no enabled default enterprise tests |
| `go test -race -count=1 ./storage ./engine ./operations ./api ./hook ./langgraph ./providers`, live PG | Seven packages, 1222 passing leaves, 210 labelled PG leaves plus 39 new PG-only context leaves, zero skips |
| `CGO_ENABLED=0 go build` personal CLI, enterprise CLI and enterprise console | All three builds passed; module metadata confirms CGO=0; no runtime/cgo or cgo SQLite dependency; enterprise excludes modernc and CLI console embed; console binary includes its console |
| `CGO_ENABLED=0 go test -short -tags eval ./eval` | Passed; short mode is not the live/full performance benchmark |
| Full Python SDK, `LADYM_SDK_IT=1` with locked dev environment | 34 passed, 6 subtests passed, zero skips; coverage 100%; real local HTTP server integrations enabled |
| Full TypeScript SDK `LADYM_SDK_IT=1 npm test` | Build passed, 31 passed, zero skips; real local HTTP server integration enabled |
| Full benchmark `python -m pytest benchmarks/tests` | 69 passed, zero skips; includes independent startup and in-flight hard-deadline tests |
| `make lint-comments`, `git diff --check` | Passed; stable linter run performed after scratch-producing tests completed |

All three formerly optional SDK external-process integration tests were enabled
against self-contained temporary loopback servers; none was counted from a skip.
No live hosted LLM measurement was performed or claimed as QA evidence. Every reader variant
was also measured separately on personal SQLite, personal PG and enterprise PG:
coverage `0/1/1/1`, stale usage `0/0/0/0`, context bytes `49/78/78/704`.
These fixture values are not expected production costs or accuracy gains.

The final Go implementation was verified at `764f607`; `39d77e9` changes only
the startup-safe deadline regression. Controller reran complete personal and
enterprise live-PG suites, race, short eval, all SDK integrations, benchmarks,
zero-CGO builds and comment lint. Whole-branch review found and repaired
PostgreSQL BeginTx context propagation and shared L0 buffers. A loaded test
diagnostic separated cold SDK startup timeout from genuine in-flight trickling;
production deadlines still include startup. Removing the outer deadline fails
the regression, even with the larger test-only startup allowance.

## Controller decisions and costs

| Decision | Reason | Cost/trade-off |
| --- | --- | --- |
| Verification is caller-attested; no command execution | Traceable evidence, not model confidence | Hosts must provide honest results; no independent experiment proof |
| Topics are explicit deterministic source views | Preserve every source and avoid hallucinated wiki synthesis | Less automatic cross-document synthesis |
| Worker reservations are bytes, API token cap separate | No assumed model tokenizer or false billing claims | Conservative call capacity |
| Explicit delete aborts staging and purges backup rows | Privacy deletion must not wait or resurrect | Migration can need rework |
| Real PG uses a dedicated password-protected loopback cluster | Do not touch existing databases/services | Missing PostgreSQL/pgvector tooling was installed locally |
| Stage/activate use a unique generation acquired atomically | Prevent begin/status and restarted-runner races | Additional Store fence methods |
| Structural/trust metadata writes freeze; Touch/delete remain allowed | Retain valid rollback evidence | Authoring pause until finalize/rollback |
| Unknown injected vectors require Identity plus adopt/migrate | Same dimension does not prove embedding equivalence | Host version attribution required |
| Recovery is local standalone Go/CLI | Must open storage even when normal Engine refuses identity | Operator requires local access |
| Legacy migration provider calls are synchronous with boundary cancellation checks | No fake background cancellation | Legacy in-flight calls may not be interruptible; budget mode requires contextual providers |
| PG vector searches lock before snapshots during activation | Avoid non-MVCC-safe column rewrite holes | Brief activation/read blocking |
| In-repository temp fixtures use a Git-discovery boundary | Hook root discovery sanitizes inherited Git controls | Test-only isolation marker |
| Automatic cutoff deletion cannot use explicit-delete exception | Unattended maintenance must not abort migrations | Cleanup waits for migration completion |

The session branch/worktree is preserved, not merged or pushed. Dedicated test
servers and named validation scratch are removed after final verification;
production user databases/configuration are not migrated automatically.
