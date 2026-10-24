# recall_ops HTTP Data Plane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose the existing engine recall_ops capability (v0.7.0 `Scope.RecallOperations` / `Scope.RecordOperation`) on the HTTP data plane and in the Go SDK, so microservice (enterprise) consumers can use operation-history retrieval.

**Architecture:** Pure exposure layer. Two POST routes in `api/` mirroring the MCP `recall_ops` output shape; two new methods in `client/golang/methods.go` decoding into small local structs. Engine untouched.

**Tech Stack:** Go stdlib `net/http` (existing `api.Handler`), `client/golang` on the shared root module.

**Spec:** `docs/superpowers/specs/2026-10-24-recall-ops-http-design.md`

## Global Constraints

- No engine changes — `Scope.RecordOperation` / `Scope.RecallOperations` already exist and are tested.
- No breaking wire changes; new routes and methods only.
- Pure Go: `CGO_ENABLED=0` builds must keep working.
- Existing comments and code are not to be reworded or removed.
- Tests are offline (`config.ForTesting` + hashing provider), mirroring the existing suites.
- Version bump (v0.8.3 → v0.9.0 tag at release) happens outside this change; there is no version constant in code and no separate `client/golang` module (single root module — the spec's "client/golang minor bump" is stale).

## Deviations from the spec's literal sketches (decided)

1. **`resource` field is the resolved entity ID string or JSON `null`**, not a full memory object — the api package's documented convention is "one POST endpoint per MCP tool, mirroring … mcp/server.go", and the MCP `recall_ops` tool returns the resource as an ID (`mcp/server.go` case "recall_ops"). Clients needing the entity can follow the ID.
2. **`items` use the existing `recallResultsJSON` helper** — same shape as `POST /api/recall`; its keys (`score/tier/via/memory{...}`) match `schema.RecallResult`'s JSON tags exactly, so the client decodes items straight into `[]*schema.RecallResult`.
3. **Client return types are local structs** (`OperationsResult`, `RecordOperationResult`) — the spec's `*schema.RecallResponse` is a field mismatch (that type has `Query/Results/...`, not `Resource/Items/Fallback`).
4. **The "SDK lacks List" gap does not exist** — `Client.ListMemories` (GET /api/memories) has existed since 56e804f (2026-08-20). No work needed; spec is stale on this point.
5. **`record_operation` gate-drop response carries `reason`** in addition to `dropped` (mirrors `handleRemember`'s gated branch).

## Review Focus

- **Negative `limit`** would reach `items[:limit]` in `engine/ops_recall.go` and panic — the handler must 400 it (pinned in Task 1).
- **Non-admin forced workspace** must override the body `workspace` for both routes (pinned in Task 1).
- **`limit` cap at 100** per spec §2; 0 means engine default 10 (pinned in Task 1).
- **Gate drop** on `record_operation` returns `{"id": null, "dropped": true, "reason": ...}` (pinned in Task 1 via the `"lol"` noise trigger, mirroring `TestRememberGatedNoiseHTTP`; client side pinned with a fixture server in Task 2).
- **Empty `resources` / empty `resource`** are 400 before the engine is called (pinned in both tasks).

---

### Task 1: HTTP routes `/api/recall_ops` + `/api/record_operation`

**Files:**
- Modify: `api/api.go` (route registration after `POST /api/record_event`, handlers after `handleRecordEvent`)
- Test: `api/recall_ops_test.go` (create; `package api_test`, `//go:build !enterprise`, reuses `do`/`decodeBody`/`newTestHandler`/`newBasicAuthHandler`)

**Interfaces:**
- Consumes: `h.eng.Scope(ws).RecallOperations(engine.OperationsQuery{...}) (*engine.OperationsResponse, error)`; `h.eng.Scope(ws).RecordOperation(engine.OperationEvent{...}) (*schema.Memory, error)`; helpers `decodeBody`, `writeJSON`, `writeError`, `engineError`, `effectiveWS`, `recallResultsJSON`.
- Produces: `POST /api/recall_ops` → 200 `{"resource": <id|null>, "items": [...], "fallback": bool}`; `POST /api/record_operation` → 200 `{"id": <id|null>, "dropped": bool[, "reason"]}`. Both instrumented with their static pattern.

- [ ] **Step 1: Write the failing tests** in `api/recall_ops_test.go`:
  - `TestRecallOpsRoundTrip`: record_operation happy path (`action "updated"`, `resources ["src/a.go"]`, `outcome "success"`, `agent "tester"`) → 200, `id` non-empty, `dropped` false; recall_ops `{"resource": "src/a.go"}` → 200, `fallback` false, `resource` non-null, some `items[i].memory.id == recorded id`.
  - `TestRecallOpsValidation`: recall_ops `{}` → 400; recall_ops `{"resource":"x","limit":-1}` → 400; recall_ops `{"resource":"x","limit":500}` → 200 with at most 100 items; record_operation `{}` → 400.
  - `TestRecallOpsGateDrop`: record_operation with `observation "lol"` → 200 with `dropped` true and non-empty `reason` (mirrors `TestRememberGatedNoiseHTTP`).
  - `TestRecallOpsWorkspaceBinding`: record into workspace `ws-a` (body field, no auth); recall from `ws-b` → `fallback` true, `resource` null; recall from `ws-a` → hit. Then the auth matrix via `newBasicAuthHandler`: alice (forced `acme`) records with body workspace `not-acme` → lands in `acme` (verified by admin root recall on `acme`); nopw (forced `globex`) recalls same resource → `fallback` true.
  - `TestRecallOpsInstrumented`: one 200 + one 400 against `newTestHandler`, then scrape `/metrics` and assert `ladym_http_requests_total{endpoint="/api/recall_ops",...}` for both status classes.
- [ ] **Step 2: Run** `go test ./api/ -run 'TestRecallOps' -v` — expect FAIL (404 on both routes).
- [ ] **Step 3: Implement.** Register both routes through `h.instrument` with their static patterns (recall_ops after the `POST /api/record_event` line; record_operation beside it). Handlers follow `handleRecordEvent`'s body-decode/validation/`effectiveWS` shape: recall_ops validates `resource` non-empty and `limit >= 0` (400 otherwise), clamps `limit` to 100, calls `Scope.RecallOperations`, and serializes via `recallResultsJSON` with `resource` as ID-or-nil (mirror `mcp/server.go`); record_operation validates `resources` non-empty, defaults `agent` to `"api"` via `cmp.Or`, calls `Scope.RecordOperation`, and branches on `m.MetaString("gated") == "dropped"`. Comment the route pair: recall_ops mirrors the MCP tool; record_operation has no MCP counterpart (engine/CLI only).
- [ ] **Step 4: Run** `go test ./api/ -v` — expect PASS (whole package, existing suites included).
- [ ] **Step 5: Update `docs/deployment.md`** endpoint reference with the two new routes (one line each, matching the existing table/list style).
- [ ] **Step 6: Commit** `feat: expose recall_ops and record_operation on the HTTP data plane`.

### Task 2: Go SDK methods `RecallOperations` + `RecordOperation`

**Files:**
- Modify: `client/golang/methods.go` (append after `RecordEvent`)
- Test: `client/golang/recall_ops_test.go` (create; `package client`, `//go:build !enterprise`, reuses `newTestServer`/`asError`)

**Interfaces:**
- Consumes: Task 1's wire shapes; `c.post`; `schema.RecallResult` JSON tags.
- Produces:
  - `type OperationsQuery struct { Workspace string; Resource string; Action string; Since float64; Until float64; Outcome string; Limit int }`
  - `type OperationsResult struct { Resource string; Items []*schema.RecallResult; Fallback bool }` (json `resource`/`items`/`fallback`; Resource "" on fallback)
  - `func (c *Client) RecallOperations(ctx context.Context, q OperationsQuery) (*OperationsResult, error)`
  - `type RecordOperationInput struct { Action string; Resources []string; Observation string; Outcome string; Agent string; Tags []string }`
  - `type RecordOperationResult struct { ID string; Dropped bool; Reason string }` (json `id`/`dropped`/`reason`)
  - `func (c *Client) RecordOperation(ctx context.Context, ws string, ev RecordOperationInput) (*RecordOperationResult, error)`

- [ ] **Step 1: Write the failing tests** in `client/golang/recall_ops_test.go`:
  - `TestOperationsRoundTrip` (real `newTestServer`): RecordOperation with resources `["src/roundtrip.go"]` → ID non-empty, Dropped false; RecallOperations on that resource → Resource non-empty, Fallback false, items include the recorded id; RecallOperations on an unknown resource → Fallback true.
  - `TestOperationsWireShape` (fixture server capturing request bodies): recall_ops body has exactly keys `workspace/resource/action/since/until/outcome/limit` with sent values; record_operation body carries `workspace/action/resources/observation/outcome/agent/tags`; response fixtures decode into the two result structs (including `resource` id and `items[0].memory.id`).
  - `TestRecordOperationGated` (fixture returning `{"id":null,"dropped":true,"reason":"noise"}`): Dropped true, ID empty, Reason "noise".
  - `TestOperationsErrorMapping`: fixture returning 400 `{"error":"boom"}` for both routes → `*Error` with StatusCode 400, Message "boom" (via `asError`).
- [ ] **Step 2: Run** `go test ./client/golang/ -run 'TestOperations|TestRecordOperation' -v` — expect FAIL (undefined methods).
- [ ] **Step 3: Implement** the two methods and four types in `client/golang/methods.go` per the Interfaces block, following the file's existing doc-comment and `map[string]any` body style.
- [ ] **Step 4: Run** `go test ./client/golang/ -v` — expect PASS.
- [ ] **Step 5: Commit** `feat: add RecallOperations and RecordOperation to the Go SDK`.

### Task 3: Whole-branch verification

- [ ] **Step 1:** `CGO_ENABLED=0 go build ./... && go vet ./api/ ./client/golang/`
- [ ] **Step 2:** `go test ./...` (full suite, offline) — expect PASS.
- [ ] **Step 3:** `git log --oneline main..HEAD` shows the two commits; working tree clean except pre-existing untracked files.
