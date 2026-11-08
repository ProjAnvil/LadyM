# R2–R4 Memory Learning and Maintenance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete R2–R4 plus real PostgreSQL parity without external services or autonomous execution.
**Architecture:** Reuse memories/edges and R1 transactions for claims, source invalidation, topic views and jobs.
Separate evidence checking, deterministic navigation, fingerprint migration and bounded worker scheduling.
**Tech Stack:** Existing Go/SQLite/pgx/pgvector, Cobra, MCP, HTTP and Go/Python/TS SDK.
**Spec:** [Remaining memory specification](../specs/2026-11-08-memory-learning-and-maintenance-design.md)

## Global Constraints

- 保留模块化单体、两种 edition、统一 Engine、零 cgo 的生产二进制。
- 不增加数据库/队列/服务、不自主执行来源中的命令、不自动导入历史 transcript。
- 不改用户现有库、主分支和其他工作区；所有新功能有回归测试。
- 验证是 caller-attested，不是模型自评或独立执行的实验结果。
- 读路径生成式 LLM 调用数为 0，tokenizer 必须纯本地且正确标单位。
- SQLite/Postgres 同契约；所有错误显式传播，不能空成功。
- 向后兼容旧 API；新能力 opt-in；不宣称未量测的 QA/token/吞吐收益。

Worktree `/Users/yuhaochen/Documents/codebase/projanvil/ladyM.worktrees/superpowers-using-superpowers-spec-subagent-plan`;
branch `agents/superpowers-using-superpowers-spec-subagent-plan`, base `44f55c0`.
Never create another worktree. Reports stay ignored in this plan's `.superpowers/sdd/` directory.
Never commit reports/temp DB/binaries/secrets. Manual edits apply_patch, modern-go guidelines before Go changes.
Every commit trailer: `Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>`.

## Review Focus

1. Stale/cyclic dependencies and bulk index deletion must not escape recursive invalidation.
2. Same-revision concurrent feedback/view/job creation must have one winner; no partial evidence/checkpoint.
3. Metadata-only trust/scope/TTL change and generic CRUD must not bypass managed verification.
4. Job cancellation/lease expiration and embedding migration may not publish from an abandoned generation.
5. UTF-8/token boundaries, quota races and cross-workspace source lookup must not leak, overspend or lie.

## File ownership and order

| Task | Unit | Shared inputs |
|---|---|---|
| 1 | schema claims, storage invalidation, Engine evidence/feedback | R1 MemoryBatch |
| 2 | HTTP/MCP/CLI/SDK and bounded opt-in archive | Task1 claim/freshness |
| 3 | topic, hybrid, bootstrap and integration | Tasks1–2 sources/contracts |
| 4 | durable staged embedding migration | Task1 mutation gates |
| 5 | contextual providers/store, persistent budget jobs | Tasks1/3/4 |
| 6 | four-variant eval, live PG/enterprise proof and release docs | all prior tasks |

Sequential implementation, independent review after every task. No nested agents.
Tests use temp databases and existing random PG DB helpers.
Dedicated test PG: 127.0.0.1:55439, user ladym_test, db ladym.
Read password from `/Users/yuhaochen/.copilot/session-state/8fed8170-c84f-406e-a143-aaa72907167a/files/pg-memory-r234/password`
only into process env DSN; never print/store password or DSN in reports.
No system service start/stop; controller owns this PostgreSQL process.

---

### Task 1: Claims, evidence, revision checking and recursive source invalidation

**Files:** Create `schema/claims.go`, `engine/claims.go`, `engine/claims_test.go`,
`storage/invalidation_test.go`; modify `schema/schema.go`, `schema/knowledge.go`,
`storage/memory_contract*.go`, `storage/store*.go`, `api/crud.go`, `operations/supersedes.go`,
`code/indexer.go` only as needed for mutation ownership, existing error tests.

**Interfaces:**
- `schema.EvidenceRef{MemoryID,Fingerprint,Kind string; StartRune,EndRune int}` JSON snake_case.
- `schema.ClaimScope{Environment,CodeVersion string; Conditions []string}`.
- `schema.Verification{Method,Actor string; CheckedAt float64; Evidence []EvidenceRef}`.
- `schema.Claim{Memory *Memory; Revision int64; Status string; Evidence,CounterEvidence []EvidenceRef;
  Scope ClaimScope; Verification *Verification; ValidUntil float64}`.
- `schema.KnowledgeFingerprint(*Memory) string`: SHA256 content, no access-derived fields.
- New TypeClaim/TypeEvidence/TypeTopicView/TypeMaintenanceJob/TypeTaskState.
- `storage.MemoryBatch.AbsentIDs []string`: rows required absent atomically before writes.
- `Store.InvalidateDependants(id string) (int,error)`: recursive workspace graph, same kernel
  also automatically called from all structural content/usability/trust mutations.
- `Scope.RecordEvidence(kind,content,outcome,actor string) (*schema.Memory,error)`.
- `Scope.CreateClaim(content string, refs []schema.EvidenceRef, scope schema.ClaimScope,
  status string, validUntil float64) (*schema.Claim,error)`.
- `Scope.GetClaim(id string) (*schema.Claim,error)`.
- `engine.ClaimFeedback{ExpectedRevision int64; Action,Replacement,Method,Reason,Actor string;
  Evidence []schema.EvidenceRef}` and `Scope.ApplyClaimFeedback(id string, feedback ClaimFeedback)`.
- `Scope.IsFresh(m *schema.Memory, now float64) (bool,error)` shared evaluator:
  usability/TTL, evidence fingerprints+managed revisions, no external file reads.
- Engine convenience methods take workspace last or reuse Scope; single source of business rules.
- Typed errors `ErrInvalidClaim`, `ErrMemoryNotFound` and existing ErrMemoryConflict.

- [x] **Step 1: Red tests**
  `TestClaimAttestedVerification`: inferred→verified only success typed evidence matching kind/fingerprint;
  confidence/ordinary hook cannot, rune excerpts invalid boundaries rejected.
  `TestClaimFeedbackCAS`: verify/correct/reject monotonic revision, two competitors one winner,
  source edited during feedback conflicts with no partial revised body.
  `TestRecursiveInvalidation`: source→claim→model→topic→entry; stale intermediary/cycle traversed,
  rejected remains rejected, ws foreign unaffected; injected DB failure rollback root+descendants.
  `TestBulkIndexInvalidation`: content update, code purge, retire, direct helper deletion all invalidate.
  `TestManagedGenericEditRejected`: HTTP generic content update and generic Remember can't forge verification.
  `TestEvidenceFreshness`: expiry, access-only change, metadata trust/scope revision differences.
- [x] **Step 2: Red command**
  `go test ./schema ./storage ./engine ./api ./code -run 'Test(Claim|Recursive|BulkIndex|Managed|EvidenceFreshness)' -count=1`.
- [x] **Step 3: Implement**
  Add typed JSON helpers and pure validation; metadata `claim_revision`/`claim_data` hold managed state.
  Evidence RecordEvidence stamps verification_kind/outcome and observed, single content <=65536bytes.
  Verify checks matching source metadata, scope, live refs and source guards; no model calls.
  Guard target claim by revision and snapshot; memory same ID correction invalidates children in tx.
  Shared transaction invalidation executes before edge closures and handles nonusable children,
  uses only same-workspace dependencies, increments managed revisions once.
  All Store public content/bulk/index deletion and updates go through shared kernel; must retain R1 semantics.
  Suppress stale managed claim/topic bodies in public read/list views, while Store raw access needed by audits remains.
  Add absent guard for same deterministic creation; prohibit moving existing records between workspace.
  Do not add a general event framework or run tools to “verify”.
- [x] **Step 4: Green**
  Red selector, full affected packages then `go test -race ./storage ./engine`.
  Live PG run same selectors using test env, enterprise storage/schema compilation.
- [x] **Step 5: Commit**
  `feat(memory): add evidence-backed claims and recursive invalidation`; report red/green and real PG proof.

### Task 2: Scoped external interfaces and quota-controlled redacted archive

**Files:** Create `api/claims.go`, `cli/claims.go`, `engine/archive.go`,
`client/golang/claims.go`, targeted tests; modify `api/api.go`, `mcp/server.go`,
`cli/cli.go`, `config/config.go`, `schema/claims.go`, `storage/memory_contract*.go`,
`storage/store.go`, `sdk/python/src/ladym_client/client.py`, `sdk/typescript/src/client.ts`,
SDK exports/tests, `ladym/ladym.go` facade if aliases needed, `docs/memory-learning.md`.

**Interfaces:**
- HTTP POST `/api/evidence`, POST `/api/claims`, GET `/api/claims/{id}`,
  POST `/api/claims/{id}/feedback`, POST `/api/evidence/archive`.
- MCP `record_evidence`, `create_claim`, `get_claim`, `claim_feedback`, `archive_evidence`,
  all JSON schemas explicit, raw metadata cannot set trusted state.
- CLI `ladym evidence record|archive`, `ladym claim create|get|feedback` with JSON structured
  --evidence refs and --expected-revision, standard db/workspace/remote flags.
- Go/Python/TS clients mirror typed requests+responses and get/feedback methods; no runtime deps.
- `config.ArchiveConfig{Enabled bool; MaxEntryBytes,MaxWorkspaceBytes int; RetentionS float64}`,
  default false/65536/8388608/2592000; TOML `[archive]` with validated positive finite bounds.
- `Scope.ArchiveEvidence(kind,content,outcome,actor string)` invokes quota-constrained atomic insert.
- `MemoryBatch.Quota *storage.MemoryQuota{Workspace string; MaxBytes int; Marker string}`,
  count text bytes of all proposed/persisted marked archive entries under writer lock.
- actor HTTP bound server principal/local, MCP/CLI fixed labels; SDK explicit host attribution.

- [x] **Step 1: Red tests**
  `TestClaimHTTPIsolationAndActor`: forced ws, nonadmin foreign GET/update reject/404,
  body actor forged ignored; auth-off local; conflict409 and invalid400.
  `TestClaimMCPAndCLIContracts`: shared shape, JSON validation, no generic metadata elevation,
  local+remote request parity, malformed refs/errors explicit.
  `TestArchiveQuotaAndRedaction`: disabled default; input65537 rejected before redact;
  secrets values absent in stored text; source fingerprint covers redacted content;
  two concurrent inserts can't exceed8MiB (small injected caps); pinned/expired rows counted;
  explicit source delete propagates descendants; new feedback on expired archive rejected.
  SDK tests numeric revisions/evidence scope and both success/error response decode.
- [x] **Step 2: Red**
  `go test ./api ./cli ./engine ./client/golang ./mcp ./config -run 'Test(Claim|Archive)' -count=1`;
  existing Python/TS SDK targeted runner, missing-symbol failure captured.
- [x] **Step 3: Implement**
  Existing auth/effectiveWS middleware remains; no endpoint bypass, source refs all scope checked.
  Extract shared redaction of token/password/API key/Authorization fields with explicit limitations.
  Quota insert checks include proposed state and same-tx concurrency, archive marker caller cannot override.
  Status/revision/error schema identical across fronts; exported type safety preserved.
  GET stale topic/claim suppresses invalid body; internal Store remains raw.
  Doc actor attestation, no execution, quotas/retention/pin, no path import and opt-in.
- [x] **Step 4: Green**
  affected package tests, full SDK suites using available Python/node tools, live PG archive quota selectors,
  enterprise production compile.
- [x] **Step 5: Commit**
  `feat(memory): expose scoped learning and bounded evidence archival`.

### Task 3: Topic views, hybrid recall and exact-budget bootstrap

**Files:** Create `engine/topics.go`, `engine/bootstrap.go`, `operations/hybrid.go`,
`langgraph/bootstrap.go`, corresponding tests, `api/topics.go`, `cli/topics.go`;
modify schema/config/mcp/API routes/Go+Python+TS clients and docs, existing normal recall eligibility
in `operations/recall.go` and shared store filter for system exclusions.

**Interfaces:**
- `Scope.BuildTopic(topic string,inputIDs []string,expectedRevision int64) (*schema.Memory,error)`;
  `Scope.GetTopic(topic string) (*schema.Memory,error)` returns no stale usable body.
- `Scope.HybridRecall(query string,limit int) (*schema.RecallResponse,error)`.
- `engine.TokenCounter interface{Name() string; Count(string)(int,error)}`.
- `engine.BootstrapRequest{TopicIDs []string; Environment,CodeVersion,TaskID,Unit string; Limit int}`;
  `BootstrapResponse{Content,Unit,Model string; Used int; SourceIDs []string}`.
- `Scope.Bootstrap(req BootstrapRequest,counter TokenCounter) (*BootstrapResponse,error)`.
- `Scope.SetTaskState(id,goal,next string,ttlS float64) (*schema.Memory,error)` explicit goal only.
- `langgraph.CreateBootstrapNode(eng,*... workspace callback,req,counter)` exact existing NodeFunc convention;
  implement signature analogous CreateRecallNode, no LLM.
- HTTP/MCP/CLI+SDK operations topic build/get, hybrid_recall, bootstrap(bytes only), task_state set.
  Remote token unit requests rejected clearly; Go host supports exact TokenCounter.
- new `MemoryFilter.ExcludeSystem bool` excludes job/task/evidence/prediction and optional types;
  scope exclusions before vector limit, normal recall default no job/prediction injection.

- [x] **Step 1: Red**
  `TestTopicCASAndProvenance`: first expected0 absent guard, 2 concurrent winners1, update rev,
  all64ids present <=16384bytes, inputs allowed/claim verified, no unsupported inference.
  `TestTopicSourceFreshness`: content/status/scope/TTL change/Delete instantly unusable, stalebody withheld.
  `TestHybridSourceRanking`: unembedded exact code, 200 higher system decoys, perws topK, RRF60 stable;
  graph<=128, no LLM, provider errors propagate.
  `TestBootstrapCounterBudget`: full-string fake tokenizer nonadditive, limit512/max1024,
  missing/broken/negativecounter error, bytes4096/CJK invalidUTF8, no stale/inferred/foreignscope/prediction.
  `TestTaskStateTTLAndBootstrap`: explicit task goal preserved only valid TTL, never prediction.
  frontend/sdk real loopback tests every new operation, correct unit rejection.
- [x] **Step 2: Red commands**
  `go test ./engine ./operations ./langgraph ./api ./mcp ./cli -run 'Test(Topic|Hybrid|Bootstrap|TaskState)' -count=1`;
  SDK focused equivalents.
- [x] **Step 3: Implement**
  deterministic source summaries/topic links, fixed source IDs not prose hallucinations;
  source fingerprints plus revision snapshots atomicBuild.
  hybrid collect lexical/token/path exact and scoped vectors separately then RRF and bounded evidence expansion;
  no forced wiki path for symbol lookup. Freshness helper across topic/hybrid/bootstrap/GetClaim.
  Bootstrap only verified scoped claim and topic index, per-entry exact full count, no additive guesses.
  bytes default explicit and tokens only host injected, header+ID budget/rune valid.
  Use context through channels compatible existing LangGraph node; old recall4096 unchanged.
- [x] **Step 4: Green**
  full affected tests+SDK, livePG topic CAS/freshness, race topics/bootstrap.
- [x] **Step 5: Commit**
  `feat(memory): add source-grounded topics and budgeted navigation`.

### Task 4: Durable embedding fingerprint migration and rollback

**Files:** Create `schema/migration.go`, `storage/migration.go`,
`storage/migration_sqlite.go`, `storage/migration_postgres.go`, `engine/migration.go`,
`cli/migrate.go`, tests; modify Store/schema initialization, config/provider fingerprint,
Engine.enforceEmbeddingDim/Tokenizer/reembedAll, HTTP/SDK migration status/control only
if code-local facade needed; update directly related migration docs.

**Interfaces:**
- `schema.EmbeddingIdentity{Provider,Model,Tokenizer string; Dim int}` canonical SHA fingerprint.
- `schema.MigrationStatus{State,ActiveFingerprint,TargetFingerprint,Cursor string; Processed int; RollbackAvailable bool}`.
- `Store.BeginEmbeddingMigration(target EmbeddingIdentity) error`;
  `StageEmbeddingMigration(id string,vector []float32,expectedFingerprint string) error`;
  `ActivateEmbeddingMigration() error`; `RollbackEmbeddingMigration() error`;
  `FinalizeEmbeddingMigration() error`; `EmbeddingMigrationStatus() (*MigrationStatus,error)`.
- `Store.GetEmbedding(id string)([]float32,error)` safe copy reuse.
- Engine explicit `MigrateEmbeddings(ctx,... target, embedder,batchSize, keepRollback)` resumes;
  CLI `ladym migrate embeddings --status|--resume|--rollback|--finalize|--adopt`.
- injection identity from optional provider `Identity() EmbeddingIdentity`;
  unknown injected identity explicit adoption required for existing storedvectors,
  no same-dim auto equivalence. Old tests fresh injected DB initialization compatible.
- Context cancellation/finite batch check per staging; Task5 extends store provider context.

- [x] **Step 1: Red**
  `TestMigrationFingerprintAndResume`: same dim different model fails normally, explicit switch,
  failure after2 staged rows restart resumes only remaining IDs, target mismatch not inheritcursor.
  `TestMigrationAtomicActivateRollback`: faulty activation rollback all vectors/meta/index,
  previous active readable until activate, mutation blocked during stage/rollbackwindow,
  rollback restores exactly, finalize clears backup then writes permitted.
  `TestMigrationPathAndLegacyAdopt`: path no vectors/provider calls, missingoldidentity explicit adopt,
  compatibleallowDim automatic path finalizes writes, no wholesale silent lossvectors.
  both backend suites + enterprise compile, errors.CloseDB propagated.
- [x] **Step 2: Red**
  `go test ./storage ./engine ./cli -run 'TestMigration' -count=1`.
- [x] **Step 3: Implement**
  staged/old vector tables and metadata durable under R1 mutation tx lock;
  stagevector+cursoratomic; DB triggers/gate enforce writefreeze evenrawSQL.
  activation transaction replaces embedding dimension+index+meta and flipsgeneration;
  PostgreSQL ALTER vector column done only with staged safe representations.
  no error-discarding RebuildVectorIndex in new migration flow.
  explicit entry opens storage without normal Engine fingerprint refusal;
  autoallowDim path canFinalize to retain R1 write compatibility.
  distinguish content metadata edits and Touch (Touchallowed); ordinary queries coherent.
  applyrollback restoringidentity not just vectors; backupretained until explicitfinalize.
- [x] **Step 4: Green**
  fullstorage/engine/cli, livePG migration failure/resume/activation/rollback,
  concurrentread/race, zero-cgoenterprisebuild.
- [x] **Step 5: Commit**
  `feat(storage): make embedding migrations resumable and reversible`.

### Task 5: Contextual provider calls and fenced budgeted maintenance jobs

**Files:** Create `schema/jobs.go`, `engine/maintenance.go`, `providers/budget.go`,
`storage/context.go`, targeted context/job tests; modify provider HTTP/langchain adapters,
Store context plumbing/transactions, Engine.System2 workers, cli/worker.go,
config and observability, API/MCP/CLI/SDK maintenance status/request as needed.

**Interfaces:**
- `providers.ContextLLMProvider`: `CompleteStructuredContext(ctx context.Context,messages []Message,
  schema JSONSchema,options CallOptions)(map[string]any,error)`;
  `CallOptions{MaxOutputTokens int}` separate fromreservedbytes.
- `storage.ContextEmbeddingProvider`: EmbedContext(ctx,text), EmbedBatchContext.
- `Store.WithContext(ctx context.Context) Store`: immutable context-scoped backendview;
  no global mutablectx, propagation toSQL queries/tx wait/cancellation.
- `schema.MaintenanceJob{ID,Workspace,Operation,TargetID,State,Owner,ErrorCode string;
  InputIDs []string; Fingerprints map[string]string; Revision int64; Attempt int;
  NotBefore,LeaseUntil float64; Checkpoint map[string]any}`.
- `Scope.ScheduleMaintenance(operation,target string,inputIDs []string,args map[string]any) (*Memory,error)`.
- `Engine.RunBudgetedCycle(ctx context.Context,workspace string) (*schema.MaintenanceReport,error)`.
- `config.System2.BudgetedEnabled bool`, `MaxCandidates=50`, `MaxCalls=8`,
  `MaxReservedBytes=8192`, `MaxWallS=30`, `Concurrency=1` (1..4), `LeaseS=60`, `MaxAttempts=3`.
- actual budget counters stored/report, `budget_stop_reason` distinct from jobdone;
  new low-cardinality observability job/cycle metrics.
- `Scope.ListMaintenanceJobs(limit int)` and data-plane status seam, no arbitrarycodeexecution.

- [x] **Step 1: Red**
  `TestMaintenanceDedupAndLease`: jobID args/sources/revisions, repeated unmodifiedcycle zero provider,
  concurrent workers one lease winner, expiredcrash takeover, latepublisher blocked owner token.
  `TestMaintenanceAtomicCheckpoint`: jobdone+output+cursor or none on DBfault;
  failedattempt retry<=3; done not Decay; systemitems noRecall.
  `TestMaintenanceBudgets`: >50candidate/>8calls/>8192reservedbytes/>30s wall;
  concurrency reservation atomic, outputtooLarge noPublish; unit labels accurate.
  `TestMaintenanceCancellation`: delayed real httptestproviderctx cancels, DBwriterwait respectsctx,
  old injectedprovider rejected rather than goroutinefakecancel; shutdownjoinroutingpreserved.
  `TestStoredEmbeddingReuse`: content/fingerprintunchanged L5/proc no reEmbed.
  `TestRichExtractionTarget`: structured target_id/action/evidence_ids guard exact valid target,
  no old verified overwrites by inferred; jobs update explicit topicinputs not entirewiki.
- [x] **Step 2: Red**
  `go test ./providers ./storage ./engine ./operations ./cli ./config ./observability -run 'Test(Maintenance|StoredEmbedding|RichExtraction)' -count=1`.
- [x] **Step 3: Implement**
  jobs backed by memories, deterministic identity includes target+canonicalargs/sortedsources,
  state/ownerCAS uses atomicbatches; same ownerfence checkpointcommit guards.
  schedule backlog/currentstaleviews/expiredclaims incrementally, budgetsstop leavespending.
  optinworker branches RunBudgetedCycle, legacy behaviorunchanged; new workers preservehostrouting.
  provideroptionsctx realI/O; oldLLM noctx rejects inbudgetedmode, noleakedgoroutines.
  process deterministic stale/expiry/topic jobs and newepisode richclassification with explicitID.
  charge complete serializedmessages/schema beforecall plus byte outputreservation, honoroutputtokencap,
  countedafterreply; reserve acrossworkers percycle sharedsyncstate, units not conflated.
  completion atomic with output; no readingjob as evidence; synchronous deletion never delayed.
  store.GetEmbedding reuse lifecycle vector scans; if absent fallback Embed, fingerprint mismatch migration error.
  low-cardinality metrics op/state not workspace/IDs/text; status exposesreportswithunit.
- [x] **Step 4: Green**
  full relevantpackages, livePG job race/lease/DBctxcancel, requiredrace, enterprisebuild,
  existinglegacyworkerfailovertestsunchanged.
- [x] **Step 5: Commit**
  `feat(worker): add fenced incremental maintenance with explicit budgets`.

### Task 6: End-to-end evaluation, dual-edition live PG verification and documentation

**Files:** Create `scenarios/trusted_memory_test.go`, `benchmarks/tests/test_learning_variants.py`,
`benchmarks/src/longmemeval/learning_eval.py`, `docs/memory-maintenance.md`;
update main spec status/readmes/architecture/Makefile testselector only if necessary,
enterprise-incompatible baseline test files correctly tagged/PG-parameterized,
SDK docs/fidelity tests/observability reports.

**Interfaces:** Offline evaluation outputs four variant names stateless/direct_source/r1/r2_topic
and pervariant source_coverage/stale_usage/read_elapsed/context_units/unit.
No fakeQAaccuracy. Existing liveLLM runner model/budget/limit explicit; no implicitnetwork.

- [x] **Step 1: Red tests**
  exact source, cross-file topic, verifiedcorrectionrepeatedtask, recursiveDelete,
  expiry, samewsview/job restart and modelmigration scenario;
  assert fourvariantreportshape and metrics not billingtokens.
  enterprise tests currently SQLite-onlyfail must become correctlybuild-tagged or PG variant;
  tests don't silentlyskip contract covered by livePG alternate.
- [x] **Step 2: Red commands**
  `go test -count=1 ./scenarios`; `pytest benchmarks/tests/test_learning_variants.py`;
  `go test -tags enterprise ./...` with localtestDSN capture baseline failures before corrections.
- [x] **Step 3: Implement**
  fixture eval runs real Engine APIs and source graph, labels timingsobservations not accuracygains.
  document attestationboundary, quotas, scope, tokenprovider, jobslease/ctx,
  backup/migrationadopt/resume/rollback/finalize, optin defaults, downgrade compatibility,
  all remainingfeature surfaces & no newglobalservice. Keep clear limitations if measurementsnotrun.
  Add exact acceptance B01–B14 matrix with commands/livePG counts.
- [x] **Step 4: Full gates**
  with dedicatedPG DSN:
  `CGO_ENABLED=0 go test -count=1 ./...`;
  `CGO_ENABLED=0 go test -tags enterprise -count=1 ./...`;
  `go test -race -count=1 ./storage ./engine ./operations ./api ./hook ./langgraph ./providers`;
  personal+enterpriseproductionbuilds & dependencyexclusions;
  `go test -short -tags eval ./eval`;
  fullPython/TS SDK andbenchmarks; `make lint-comments`.
  If docs/testtool missing choose smallest existing runner, no secretreports.
- [x] **Step 5: Commit**
  `test(memory): verify learning lifecycle and dual-edition maintenance`;
  report all actual gates, no skips as pass; controller performs wholebranchreview then freshfinalgates.

## Coverage and preflight

T1 B01–03, T2 B04–05, T3 B06–08, T4 B11, T5 B09–10, T6 B12–14.
Sourcegraph/tx contracts shared T1/T2/T3/T4/T5, sequential edits and singular shared helper.
No R2–R4 deferred implementation here: opt-in/explicit interface is shipped, not autoexecuted.
Task6 producer/runtime tests cover integration after earlier unitgates.
Plan/spec audits apply section7 decisions. No invalid privacy/verification shortcut accepted.

## Binding plan-audit corrections

These override abbreviated signatures above and must be carried into each task brief.

1. **Task1 freshness:** public shared evaluator is
   `storage.IsFresh(store Store,m *schema.Memory,now float64)(bool,error)`;
   Scope delegates. Wire ordinary operations.Recall, Engine.List, typed reads, hybrid and bootstrap,
   no operations→Engine cycle. Tests must show transitive TTL expiry before any maintenance cycle.
   Stale managed bodies never exposed by public list/read.
2. **Task3 conditions:** BootstrapRequest additionally has `Conditions []string`.
   Empty claim environment/version wildcard; nonempty equals request exactly.
   Every claim condition must appear exactly/case-sensitively in caller-attested Conditions.
   Missing condition excludes the claim; topic/task filtering follows same contract.
3. **Task4 migration:** add `Store.MigrationCandidates(afterID string,limit int)([]*schema.Memory,error)`,
   ordered ID lexical keyset for embedded rows. MigrationStatus.Generation unique UUID;
   StageEmbeddingMigration adds captured generation arg in addition to target fingerprint.
   Activation verifies every eligible candidate ID staged for that generation/target,
   not merely vector count. Old late stages cannot survive abort/newgeneration.
   Freeze content/vector/create and managed trust/scope; Touch and nonknowledge opmeta allowed.
   Explicit deletion always allowed, atomically cancels pending migration, removes deleted source
   from rollback backups, recursively invalidates descendants and never restores deleted text.
4. **Task5 fence:** completion transaction guards running state/owner/revision,
   LeaseUntil > transaction-time now, live ctx, every input/target snapshot and active migrationgeneration.
   Test expired lease without takeover plus source edited during I/O. No outputs/checkpoints published.
   Persist Args and InputRevisions alongside existing job fields; heartbeat renews revision.
5. **Task5 measurement:** ContextLLM return is `providers.StructuredResult{Data map[string]any; RawOutputBytes int}`,
   CallOptions includes MaxOutputBytes and MaxOutputTokens. Measure/cap raw model text before parsing;
   parsed compact map cannot reconstruct raw bytes; incapable provider rejected.
   Per-call reserve fullencodedinput +1024outputbytes, MaxOutputBytes1024, outputAPItokens1024 separate.
   Cycle report fields candidate_count/calls/reserved_bytes/measured_input_bytes/measured_output_bytes/
   completed_jobs/pending_jobs/failed_jobs/elapsed_ms/stop_reason with unit="bytes".
6. **Task5 ownership:** add operations/decay.go, operations/l5.go and operations/proceduralize.go;
   exempt jobs fromDecay, reuse GetEmbedding when content/fingerprint unchanged.
7. **Task6 and each test:** add shared `internal/testutil/pgtest.go` isolatedPG helper,
   imports pgx/config only (no storage cycle). Every critical new storage/claim/topic/archive/migration/job
   suite explicitly runs SQLite and PG, including enterprise PGlegs. Report nonzero PGcases.
   Fix storage/pg_gate_test.go and duplicated operations helper cleanup to fresh
   context.WithTimeout(context.Background(),10*time.Second), use t.Errorf on error.
   Env alone is not PGproof; no t.Context after test cancellation in cleanup.

All seven readiness blockers resolved by these binding corrections; no scope deferral.

## Completed execution

All six tasks passed independent spec/quality review. Task1 freshness, Task2 Python
null conditions, Task3 domain-separated task identity and Task4 migration intent
repairs passed scoped re-review. Task6 live-answer deadlines and startup-safe proof
also passed scoped review. Whole-branch context/Working-memory repair `764f607`
passed final review; `39d77e9` changes only validated deadline tests.

Final code and tests passed complete personal/enterprise suites against real PG,
seven-package race, short eval, production builds/dependency checks, enabled
Python/TS SDK integrations, 69 benchmark tests and comment lint.
See [actual acceptance and decisions](../../memory-maintenance.md).
No R2–R4 implementation is deferred; hosted model quality/cost measurement remains
explicitly unperformed rather than claimed. No merge/push or user DB migration.
