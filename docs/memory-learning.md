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
