# Spec: recall_ops on the HTTP data plane (`/api/recall_ops`) + Go SDK completion

Status: proposal (written by the woca side, 2026-10-24, against LadyM v0.8.3)
Scope: api/ + client/golang/ + sdk/python/ + sdk/typescript/ (SDK parity optional per release)
Non-goals: no engine changes — `Scope.RecordOperation` / `Scope.RecallOperations` already exist (v0.7.0); this spec only exposes them over the data plane.

## 1. Why

The recall_ops capability (operation-history retrieval: resource × action pivot,
L4 `operates_on` graph join, ACT-R ranking) is reachable today via the embedded
engine, the MCP tool, and the CLI — but not over the HTTP data plane. Consumers
of the microservice deployment (woca's enterprise worker talks to LadyM purely
over HTTP via `client/golang`) cannot ask "how did I last operate on X?". The
Go SDK also lacks a `List` method even though `GET /api/memories` exists
(since v0.6.x), so callers assemble their own requests.

## 2. HTTP surface

Two routes in `api/`, following the existing handler conventions
(`h.instrument` wrapper, JSON bodies, workspace-scoped):

```
POST /api/recall_ops
  {"workspace": "...", "resource": "src/a.go",       // required
   "action": "update",                                // optional, canonical word
   "since": 0, "until": 0,                            // optional, unix seconds
   "outcome": "success",                              // optional
   "limit": 10}                                       // optional, default 10, cap 100
→ 200 {"resource": {...}|null, "items": [RecallResult...], "fallback": bool}
→ 400 when "resource" is empty (mirror engine.ErrResourceRequired)

POST /api/record_operation
  {"workspace": "...", "action": "update",            // action_raw; engine normalizes
   "resources": ["src/a.go", ...],                    // required non-empty
   "observation": "...", "outcome": "success",
   "agent": "woca",                                   // optional, default "api"
   "tags": [...]}
→ 200 {"id": "...", "dropped": false}                 // dropped=true on gate drop
→ 400 when "resources" is empty
```

Read path binds the workspace exactly like `POST /api/recall` does today
(per-request `engine.Scope` view — v0.6.0 semantics; no global mutex).

Instrumentation: both handlers go through `h.instrument` so they join the
Prometheus request metrics.

## 3. Go SDK (`client/golang`)

```go
// OperationsQuery mirrors the POST /api/recall_ops body.
type OperationsQuery struct {
    Workspace string
    Resource  string
    Action    string
    Since     float64
    Until     float64
    Outcome   string
    Limit     int
}

func (c *Client) RecallOperations(ctx context.Context, q OperationsQuery) (*schema.RecallResponse, error)
func (c *Client) RecordOperation(ctx context.Context, ws string, ev RecordOperationInput) (*RecordResult, error)

// Also close the List gap: GET /api/memories exists but the SDK has no method.
func (c *Client) List(ctx context.Context, ws string, opts ListOptions) ([]*schema.Memory, error)
```

Wire shapes follow the existing `methods.go` conventions (`post`, query
params, `schema.RecallResponse` passthrough). Error mapping: 400 → typed
client error as today.

## 4. Auth & multi-tenancy

Same rules as the existing data-plane verbs: Basic auth (users table), the
workspace key is caller-supplied (`woca:{tenant}:{user}:{workspace}` style)
— recall_ops introduces no new tenancy semantics.

## 5. Tests (match the repo's current suites)

- api: handler tests — happy path, empty resource → 400, empty resources on
  record → 400, workspace binding (an item recorded in ws A is not visible
  from ws B), instrumented metrics counters increment.
- client/golang: round-trip against httptest fixtures (request body shape,
  response decode, error mapping), following client_test.go's table style.
- Offline only: engine-level behavior is already covered by v0.7.0 suites.

## 6. woca-side consumption (after the tag ships)

`internal/memory/remote.go` implements `OperationEngine` (woca's capability
interface) over the new client methods; the `GET /api/memory/operations`
endpoint flips from 501 to live for enterprise. No woca interface changes —
the capability is already asserted, the remote engine just starts implementing it.

## 7. Versioning

Feature-additive: root module minor bump; client/golang minor bump (new
methods only). No breaking wire changes.
