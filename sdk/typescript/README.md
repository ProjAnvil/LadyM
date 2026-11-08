# ladym-client (TypeScript SDK)

TypeScript client for the LadyM HTTP data-plane (`ladym serve --http`),
mirroring `client/golang` and `sdk/python`. Zero runtime dependencies — global
`fetch` only; Node ≥ 20 or browsers. ESM build with `.d.ts`.

## Persisted maintenance

`scheduleMaintenance({operation: "new_episode", target_id: episodeId,
input_ids: [episodeId], workspace: "repo"})`, `listMaintenanceJobs("repo")`, and
`maintenanceStatus("repo")` mirror the scoped HTTP endpoints. Request, job and
report types are exported. Only `new_episode`, `refresh_topic`, and `expire_claim`
are allowed; requests enqueue work for the opt-in worker. Report reservations
and measurements are **bytes**, separate from the provider API token cap.
Inferred extraction cannot overwrite verified knowledge; deletion remains synchronous.

## Install

```bash
npm install ./sdk/typescript   # from the repo (private package, not on npm)
```

## Quick start

```ts
import { LadymClient } from "ladym-client";

const c = new LadymClient("http://127.0.0.1:8080");

await c.remember("Alice likes green tea", { tags: ["pref"], workspace: "demo" });
const resp = await c.recall("green tea", { workspace: "demo" });
for (const hit of resp.results) {
  console.log(hit.score, hit.memory.summary || hit.memory.content);
}
```

## Auth

Deployments with `auth.enabled` use HTTP Basic against the users table:

```ts
const c = new LadymClient("http://127.0.0.1:8080", { username: "alice", password: "s3cret" });
const me = await c.login();   // verifies credentials, returns User
const st = await c.stats();   // every /api/* call carries the Basic header
```

A non-2xx response throws `LadymError` with `.status` and the server's
`{"error": ...}` message.

## API surface

`ping` · `login` · `remember` · `recall` · `recordEvent` · `consolidate` ·
`stats` · `link` · `forget` · `listMemories` · `updateMemory` · `deleteMemory`

Source navigation: `buildTopic(BuildTopicRequest)`, `getTopic(topic, workspace)`,
`hybridRecall(HybridRecallRequest)`, `bootstrap(BootstrapRequest)` and
`setTaskState(SetTaskStateRequest)`. Bootstrap counts the full UTF-8 message in
bytes (default/max 4096); remote token requests reject without token estimates.
`conditions` is an explicit, case-sensitive caller-attested set. See
[navigation contracts](../../docs/memory-learning.md#source-grounded-navigation).

Notes:

- `recall` supports `topK` / `workspace` / `codeOnly`; the MCP-only knobs
  (`layers` / `types` / `min_similarity`) are intentionally absent from the
  signature — the HTTP data-plane does not expose them.
- `remember` returns `RememberResult`; check `.dropped` for attention-gate
  drops (nothing persisted, `reason` explains why).
- `recordEvent` results carry the schema layer constant (e.g. `"L1_episodic"`).
- Timeout defaults to 30s via `AbortSignal.timeout` (`timeoutMs: 0` disables).
- For tests or custom stacks, inject `fetch` via the constructor options.
- HTTP `Memory.metadata` retains knowledge status and `source_episode` /
  `source_episodes`; it is not the MCP metadata whitelist. Missing status is
  legacy-compatible, not verification. Observed tool success is not a verified
  task. Explicit source deletion marks direct dependants stale with missing
  evidence; recall excludes unusable knowledge. No SDK API change is required.

## Development

```bash
cd sdk/typescript
npm install
npm test                       # build + unit tests (no network)
LADYM_SDK_IT=1 npm test        # + integration leg (builds and boots a real ladym server)
```
