# Evidence-backed memory learning

Claims record caller-attested knowledge, not independent experimental verification.
Recording evidence never executes its content, invokes a model, reads a path or URL,
or imports a transcript. Existing generic memory APIs remain unchanged; managed
claims must be changed through typed feedback, and archived evidence cannot be
edited through generic HTTP CRUD.

## Scope and attribution

Every operation runs in an `Engine.Scope`. Evidence references must resolve to
fresh, usable memories in that workspace. HTTP uses the existing Basic-auth and
workspace middleware: a non-admin's workspace overrides both body and query
workspace fields. Foreign claim IDs return 404, including feedback attempts.

HTTP evidence and feedback actors are the authenticated username, or `local`
when auth is off. A JSON `actor` cannot impersonate a principal. Local MCP and
CLI actors are fixed to `mcp` and `cli`; these labels do not attest a human's
identity. Remote CLI follows the HTTP principal. SDK host attribution
(`WithActor`, Python `actor`, TypeScript `actor`) is advisory and sent in the
body; it never overrides HTTP authentication. Embedded Go callers supply their
own host actor to Scope methods.

## HTTP / MCP contracts

Successful calls return the memory or claim object directly (no extra envelope).
Errors are `{"error":"..."}`. HTTP returns 400 for invalid inputs, disabled
archiving or quota exhaustion; 401 for missing/invalid configured authentication;
404 for missing/foreign claim targets; 409 for revision or snapshot conflicts;
500 for storage/internal errors. MCP uses the same JSON objects in text content,
with `isError=true` and the same error body on failure.

| HTTP | MCP | Request |
| --- | --- | --- |
| `POST /api/evidence` | `record_evidence` | `kind`, `content`, optional `outcome`, `workspace` |
| `POST /api/evidence/archive` | `archive_evidence` | Same evidence request; archive must be enabled |
| `POST /api/claims` | `create_claim` | `content`, optional `evidence_refs`, `scope`, `status`, `valid_until`, `workspace` |
| `GET /api/claims/{id}?workspace=...` | `get_claim` | MCP: `memory_id`, optional `workspace` |
| `POST /api/claims/{id}/feedback` | `claim_feedback` | `expected_revision`, `action`, optional `replacement`, `method`, `reason`, `evidence_refs`, `workspace`; MCP also requires `memory_id` |

New HTTP request bodies must be single JSON objects and are bounded to 1 MiB
(the content limit below is separate). Unadvertised metadata is not used to set
trust, archive ownership, expiry or verification state.

An evidence ref is:

```json
{
  "memory_id": "source-id",
  "fingerprint": "sha256-of-source-content",
  "kind": "test_result",
  "start_rune": 0,
  "end_rune": 0
}
```

The fingerprint covers the entire persisted content, including redaction for an
archive. Nonzero bounds are zero-based Unicode rune half-open ranges; `0/0`
means the entire content. Kinds are `observation`, `test_result`, `tool_result`
and `user_confirmation`. A kind must match the source's recorded kind.

Create defaults to `inferred`; `user_asserted` is the only other allowed creation
status. Scope is `{"environment":"","code_version":"","conditions":[]}`.
At most 64 refs and 32 conditions are allowed; `valid_until` is finite,
nonnegative Unix seconds, with zero meaning no claim expiry.

Feedback uses a positive integer `expected_revision`:

- `verify`: keeps the body, requires a method and at least one matching successful
  test/tool result or explicit user confirmation.
- `correct`: additionally requires a nonempty replacement; preserves the ID,
  replaces evidence and invalidates descendants atomically.
- `reject`: requires a reason, optionally accepts counter-evidence, and
  invalidates descendants.

All successful feedback increments revision. Two writes using the same revision
cannot both succeed; a source changed during verification also conflicts.
Claims return `memory`, `revision`, `status`, `evidence_refs`, `counter_evidence`,
`scope`, optional `verification` (`method`, `actor`, `checked_at`, `evidence_refs`)
and `valid_until`. Empty ref lists can be JSON null. Stale reads suppress the
body, summary and obsolete verification, rather than presenting them as trusted.
Ordinary memory reads use the shared freshness rules too; raw internal Store
reads remain available for audits.

## Opt-in archive

```toml
[archive]
enabled = true
max_entry_bytes = 65536
max_workspace_bytes = 8388608
retention_s = 2592000
```

Archiving defaults to **disabled**. Limits may be reduced, must be positive
integers, and cannot exceed 64 KiB per entry or 8 MiB per workspace. Retention
must be positive and finite; the default is 30 days.

The raw UTF-8 content is checked **before** redaction. Oversized input is
rejected, not silently truncated. Redacted content must also fit the entry
limit. The atomic storage writer counts the bytes of every undeleted archive
content in that workspace, including expired, pinned, stale and retired rows,
plus proposed batch writes. Quotas survive reopening the engine, and concurrent
connections cannot overfill them. Ordinary `RecordEvidence` also caps content
at 64 KiB, but is intended for short results/confirmations, not transcripts; its
total storage is not covered by the archive quota.

Best-effort redaction masks common token, password/passwd, secret, API key,
access/refresh token and Authorization fields/assignments, including Bearer and
Basic headers. It is not a comprehensive secret/PII detector: unusual field names,
free-form credentials, encoded values or complex multiline formats may escape.
Review content before submission. Only redacted archive content and its hash are
persisted; no original content or original-content digest is saved. The archive
marker and retention metadata are engine-owned.

Retention is stored as `valid_until`. Expired archives cannot support new
verification, even when pinned. Existing source-reference protection can retain
evidence for audits during safe deletion; pin/reference protection does not
remove its bytes from quota or extend trust. Expiry does not automatically free
quota or start a new cleanup service. Explicit deletion is allowed regardless
of references and recursively invalidates derived claims.

## CLI

All commands return structured JSON and support `--db`, `--workspace`/`-w`,
`--server`, `--user`, `--password`. `--db` and `--server` are mutually exclusive.
Content arguments are literal strings, not file import syntax.

```sh
ladym evidence record 'checks passed' --kind test_result --outcome success -w project
ladym evidence archive 'short redacted log' --kind observation -w project
ladym claim create 'checks pass on v1' --evidence '[{"memory_id":"SOURCE","fingerprint":"SHA256","kind":"test_result","start_rune":0,"end_rune":0}]' --scope '{"environment":"test","code_version":"v1","conditions":[]}' -w project
ladym claim get CLAIM_ID -w project
ladym claim feedback CLAIM_ID --expected-revision 1 --action verify --method caller-attested --evidence '[{"memory_id":"SOURCE","fingerprint":"SHA256","kind":"test_result","start_rune":0,"end_rune":0}]' -w project
```

## SDK methods

The three HTTP SDKs export typed evidence refs, scopes, claims, verification
and request objects, with no new runtime dependencies.

| Go HTTP client | Python | TypeScript |
| --- | --- | --- |
| `RecordEvidence(ctx, RecordEvidenceRequest)` | `record_evidence(RecordEvidenceRequest)` | `recordEvidence(RecordEvidenceRequest)` |
| `ArchiveEvidence(ctx, RecordEvidenceRequest)` | `archive_evidence(RecordEvidenceRequest)` | `archiveEvidence(RecordEvidenceRequest)` |
| `CreateClaim(ctx, CreateClaimRequest)` | `create_claim(CreateClaimRequest)` | `createClaim(CreateClaimRequest)` |
| `GetClaim(ctx, id, workspace)` | `get_claim(id, workspace="")` | `getClaim(id, workspace="")` |
| `ApplyClaimFeedback(ctx, id, ClaimFeedbackRequest)` | `apply_claim_feedback(id, ClaimFeedbackRequest)` | `applyClaimFeedback(id, ClaimFeedbackRequest)` |

Wire keys use snake_case in typed requests, including numeric revisions.
Python decodes nested refs, scopes and verification into dataclasses; TypeScript
exports matching interfaces. Non-2xx responses retain their status in Go `Error`,
Python `LadymError` and TypeScript `LadymError`.

For embedded Go, the `ladym` facade exposes `ArchiveConfig`, `EvidenceRef`,
`ClaimScope`, `Claim`, `ClaimFeedback`, `Verification` and `KnowledgeFingerprint`.
`eng.Scope(workspace)` provides `RecordEvidence`, `ArchiveEvidence`,
`CreateClaim`, `GetClaim` and `ApplyClaimFeedback`; Engine convenience methods
take workspace last. They share the same validation, freshness and transaction
logic as the external fronts.

## Source-grounded navigation

| HTTP | MCP | Request |
| --- | --- | --- |
| `POST /api/topics` | `build_topic` | `topic`, `input_ids`, `expected_revision`, optional `workspace` |
| `GET /api/topics/{topic}?workspace=...` | `get_topic` | MCP: `topic`, optional `workspace` |
| `POST /api/hybrid_recall` | `hybrid_recall` | `query`, `limit` (1..200), optional `workspace` |
| `POST /api/bootstrap` | `bootstrap` | Optional `topic_ids`, `environment`, `code_version`, `conditions`, `task_id`, `unit`, `limit`, `workspace` |
| `POST /api/task_state` | `set_task_state` | `id`, `goal`, optional `next_step`, `ttl_s`, `workspace` |

Topic names are nonblank, at most 128 Unicode runes. Their ID is the SHA256 of
`workspace + NUL + topic`. Build accepts 1..64 unique same-workspace code
files/symbols, facts, playbooks or verified claims. It uses no LLM: the <=16KiB
body consists of literal source excerpts, complete IDs, types and scope,
marking ordinary sources **unverified** and claims **caller-attested verified**.
Metadata preserves every input fingerprint and revision; `produced_from` edges
preserve navigation. Creation requires revision 0 and atomic absence; updates
require the exact old revision. Source/trust/scope/TTL changes or deletion
immediately make derived topics unusable. Get returns ID/state but no stale body.

Hybrid independently ranks exact symbol/token/path matches (including
unembedded sources), scoped vectors and a <=128-node graph, then combines ranks
with reciprocal-rank fusion `1/(60+rank)`, ties by ID. Topic prose does not replace
exact source lookup. System records never enter lexical/vector topK. Evidence
can enter only through an explicit outgoing source reference from a claim/topic.
Ordinary recall excludes jobs, task state, evidence and predictions by default;
explicit ordinary type filters retain their existing opt-in semantics.
Embedding/storage failures propagate; no read generates LLM requests.

Bootstrap returns `{content,unit,model,used,source_ids}`. Only fresh verified
claims, explicitly requested topic IDs (brief index links), and an explicitly
named task are included. Empty claim environment/version are wildcards;
nonempty values must equal the request exactly. Every claim/source/task scope
condition must be present in caller-attested `conditions`, case-sensitively.
Generic facts, inferred claims and predictions are not automatically injected.
Task state persists the provided goal and next step; TTL defaults to 86400
seconds when omitted/zero. Negative/nonfinite TTL is rejected.

HTTP/MCP/CLI and remote SDK bootstrap are **bytes only**, default/max 4096.
Requests for remote tokens fail instead of fabricating counts. Embedded Go
`scope.Bootstrap(req,counter)` or `client.BootstrapLocal(scope,req,counter)` supports
`unit:"tokens"` with a host-owned `TokenCounter{Name() string; Count(string)(int,error)}`:
default 512, max 1024, model set to `Name()`. Every candidate and final **full
string**, including reference header and complete IDs, is counted exactly.
Excerpts shrink only at UTF-8 rune boundaries; no additive token guesses.
Missing, panicking, negative, zero-for-nonempty, erroring or unstable counters
fail. A positive host count is caller-attested, not independently tokenized.
If no complete entry fits, only the header or empty content is returned.

```bash
ladym topic build auth --input-ids SOURCE_A,SOURCE_B --expected-revision 0 -w project
ladym topic get auth -w project
ladym hybrid-recall ExactAuthSymbol --limit 8 -w project
ladym task-state set review --goal 'Review auth' --next-step 'Read SOURCE_A' --ttl-s 3600 -w project
ladym bootstrap --topic-ids TOPIC_ID --task-id review --environment test --code-version v1 --conditions TestsPass -w project
```

All commands accept the existing local `--db` or remote `--server`/authentication
options. SDK equivalents are Go `BuildTopic`, `GetTopic`, `HybridRecall`,
`Bootstrap`, `SetTaskState`; Python `build_topic`, `get_topic`, `hybrid_recall`,
`bootstrap`, `set_task_state`; TypeScript `buildTopic`, `getTopic`, `hybridRecall`,
`bootstrap`, `setTaskState`. All export matching typed request/response objects.

LangGraph hosts can add
`CreateBootstrapNode(eng,req,counter,workspaceFunc)` as a `NodeFunc`: it uses the
same scope API, adds a reference SystemMessage to the `messages` channel and
propagates errors. The existing recall node retains its 4096-byte budget.

These operations are offline navigation/contract machinery, not evidence of
real QA accuracy. Hybrid currently enumerates scoped memories to filter
transitive freshness before vector topK; large workspaces may need measurement.
No new maintenance worker, agent execution or model service is involved.
