# ladym-client (Python SDK)

Python client for the LadyM HTTP data-plane (`ladym serve --http`), mirroring
`client/golang`. Zero runtime dependencies — stdlib `urllib` only; Python ≥
3.10.

## Persisted maintenance

Use `client.schedule_maintenance(ScheduleMaintenanceRequest("new_episode",
episode_id, workspace="repo"))`, `client.list_maintenance_jobs("repo")`, and
`client.maintenance_status("repo")`. Import the request/job/report dataclasses
from `ladym_client`. Requests only enqueue allowlisted operations
(`new_episode`, `refresh_topic`, `expire_claim`); an opt-in worker processes them.
Status counters are bytes, not billing tokens. Verified knowledge is not
overwritten by inferred extraction, and deletion is synchronous.

## Install

```bash
# with uv (from the repo)
uv add --path sdk/python        # or: uv pip install ./sdk/python

# plain pip
pip install ./sdk/python
```

## Quick start

```python
from ladym_client import Client

c = Client("http://127.0.0.1:8080")

c.remember("Alice likes green tea", tags=["pref"], workspace="demo")
resp = c.recall("green tea", workspace="demo")
for hit in resp.results:
    print(hit.score, hit.memory.summary or hit.memory.content)
```

## Auth

Deployments with `auth.enabled` use HTTP Basic against the users table:

```python
c = Client("http://127.0.0.1:8080", username="alice", password="s3cret")
me = c.login()          # verifies credentials, returns User
st = c.stats()          # every /api/* call carries the Basic header
```

A non-2xx response raises `LadymError` with `.status` and the server's
`{"error": ...}` message.

## API surface

`ping` · `login` · `remember` · `recall` · `record_event` · `consolidate` ·
`stats` · `link` · `forget` · `list_memories` · `update_memory` ·
`delete_memory`

Source navigation: `build_topic(BuildTopicRequest)`, `get_topic(topic, workspace)`,
`hybrid_recall(HybridRecallRequest)`, `bootstrap(BootstrapRequest)` and
`set_task_state(SetTaskStateRequest)`. Bootstrap counts the full UTF-8 message
in bytes (default/max 4096); remote token requests raise `ValueError`.
`conditions` is an explicit, case-sensitive caller-attested set. See
[navigation contracts](../../docs/memory-learning.md#source-grounded-navigation).

Notes:

- `recall` supports `top_k` / `workspace` / `code_only`; the MCP-only knobs
  (`layers`, `types`, `min_similarity`) raise `ValueError` — the HTTP
  data-plane does not expose them.
- `remember` returns a `RememberResult`; check `.dropped` for attention-gate
  drops (nothing persisted, `reason` explains why).
- For tests or custom HTTP stacks, inject `transport`: a callable
  `(method, path, query, body) -> (status, dict)`.
- HTTP `Memory.metadata` retains knowledge status and `source_episode` /
  `source_episodes`; it is not the MCP metadata whitelist. Missing status is
  legacy-compatible, not verification. Observed tool success is not a verified
  task. Explicit source deletion marks direct dependants stale with missing
  evidence; recall excludes unusable knowledge. No SDK API change is required.

## Development

```bash
cd sdk/python
uv sync
uv run pytest                  # unit tests (no network)
LADYM_SDK_IT=1 uv run pytest   # + integration leg (builds and boots a real ladym server)
```
