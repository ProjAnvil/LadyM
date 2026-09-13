/**
 * Unit tests for ladym-client: injected fake fetch pins method/path/payload
 * assembly and response parsing; no network, no subprocesses.
 */

import assert from "node:assert/strict";
import test from "node:test";

import { LadymClient, LadymError } from "../src/index.js";
import type { FetchFn } from "../src/index.js";

interface RecordedCall {
  url: string;
  method: string;
  headers: Record<string, string>;
  body: unknown;
  signal: AbortSignal | undefined;
}

function fakeFetch(...responses: Array<[number, unknown]>): { fetchFn: FetchFn; calls: RecordedCall[] } {
  const calls: RecordedCall[] = [];
  const fetchFn = (async (url: string | URL | Request, init?: RequestInit) => {
    const [status, payload] = responses.length > 0 ? responses.shift()! : [200, {}];
    calls.push({
      url: String(url),
      method: init?.method ?? "GET",
      headers: (init?.headers ?? {}) as Record<string, string>,
      body: typeof init?.body === "string" ? JSON.parse(init.body) : undefined,
      signal: (init?.signal ?? undefined) as AbortSignal | undefined,
    });
    return new Response(JSON.stringify(payload), { status });
  }) as FetchFn;
  return { fetchFn, calls };
}

function makeClient(...responses: Array<[number, unknown]>) {
  const { fetchFn, calls } = fakeFetch(...responses);
  const client = new LadymClient("http://ladym.test/", { fetch: fetchFn, timeoutMs: 7500 });
  return { client, calls };
}

test("ping hits GET /healthz with no body", async () => {
  const { client, calls } = makeClient([200, { status: "ok" }]);
  await client.ping();
  assert.equal(calls[0].method, "GET");
  assert.equal(calls[0].url, "http://ladym.test/healthz");
  assert.equal(calls[0].body, undefined);
});

test("login posts the configured credentials", async () => {
  const { client, calls } = makeClient([200, { username: "alice", workspace: "acme", admin: false, created_at: 1.5 }]);
  const u = await client.login();
  assert.deepEqual(u, { username: "alice", workspace: "acme", admin: false, created_at: 1.5 });
  assert.deepEqual(calls[0].body, { username: "", password: "" });
});

test("remember payload and result", async () => {
  const { client, calls } = makeClient([200, { id: "m1", hash: "abc" }]);
  const r = await client.remember("sky is blue", { tags: ["fact"], workspace: "ws1", source: "vitest" });
  assert.equal(r.id, "m1");
  assert.equal(r.dropped, false);
  assert.equal(calls[0].method, "POST");
  assert.equal(calls[0].url, "http://ladym.test/api/remember");
  assert.deepEqual(calls[0].body, { content: "sky is blue", source: "vitest", tags: ["fact"], workspace: "ws1" });
});

test("remember gate drop maps to dropped=true", async () => {
  const { client } = makeClient([200, { id: null, hash: null, gated: "dropped", reason: "duplicate" }]);
  const r = await client.remember("dup");
  assert.equal(r.dropped, true);
  assert.equal(r.reason, "duplicate");
  assert.equal(r.id, "");
});

test("recall parsing and defaults", async () => {
  const payload = {
    query: "tea", tier_reached: 2, reflected_sufficient: true, elapsed_ms: 3.2,
    results: [
      { score: 0.9, tier: 2, via: ["vector"],
        memory: { id: "m1", layer: "L2_semantic", type: "fact", summary: "s",
                  content: "alice likes green tea", source: "http", tags: ["pref"] } },
    ],
  };
  const { client, calls } = makeClient([200, payload]);
  const resp = await client.recall("tea", { topK: 5, workspace: "ws1" });
  assert.deepEqual(calls[0].body, { query: "tea", top_k: 5, workspace: "ws1", code_only: false });
  assert.equal(resp.tier_reached, 2);
  assert.equal(resp.results.length, 1);
  assert.equal(resp.results[0].memory.content, "alice likes green tea");
  assert.deepEqual(resp.results[0].via, ["vector"]);
});

test("recall code_only flag", async () => {
  const { client, calls } = makeClient([200, { results: [] }]);
  await client.recall("main func", { codeOnly: true });
  assert.equal((calls[0].body as Record<string, unknown>).code_only, true);
});

test("record_event payload", async () => {
  const { client, calls } = makeClient([200, { id: "e1", layer: "L1_episodic", type: "event" }]);
  const r = await client.recordEvent("bot", "deploy", { observation: "ok", outcome: "done", tags: ["ops"], workspace: "ws1" });
  assert.deepEqual(r, { id: "e1", layer: "L1_episodic", type: "event" });
  assert.deepEqual(calls[0].body, {
    agent: "bot", action: "deploy", observation: "ok", outcome: "done", tags: ["ops"], workspace: "ws1",
  });
});

test("consolidate payload", async () => {
  const { client, calls } = makeClient([200, { kept_episodes: 3, promoted_to_semantic: 1, skipped_consolidated: 2, actions: { ADD: 1 } }]);
  const r = await client.consolidate({ workspace: "ws1", since: 100 });
  assert.equal(r.promoted_to_semantic, 1);
  assert.deepEqual(r.actions, { ADD: 1 });
  assert.deepEqual(calls[0].body, { workspace: "ws1", since: 100 });
});

test("stats parses wire shape", async () => {
  const payload = { total_memories: 7, by_layer: { L2_semantic: 7 }, by_type: { fact: 7 },
    edges: 2, code_symbols: 0, workspaces: ["ws1"], db_path: "/tmp/x.db", avg_tokens_per_memory: 4.5 };
  const { client } = makeClient([200, payload]);
  const st = await client.stats({ workspace: "ws1" });
  assert.equal(st.total_memories, 7);
  assert.deepEqual(st.workspaces, ["ws1"]);
});

test("link returns the edge id with default relation", async () => {
  const { client, calls } = makeClient([200, { id: "edge-1" }]);
  assert.equal(await client.link("a", "b"), "edge-1");
  assert.deepEqual(calls[0].body, { src: "a", dst: "b", relation: "related_to" });
});

test("forget posts memory_id", async () => {
  const { client, calls } = makeClient([200, { forgotten: "m1" }]);
  await client.forget("m1");
  assert.deepEqual(calls[0].body, { memory_id: "m1" });
});

test("listMemories builds query string and parses page", async () => {
  const payload = { memories: [{ id: "m1", layer: "L2_semantic", type: "fact", content: "c",
    summary: "s", source: "http", tags: [], workspace: "ws1" }], total: 1 };
  const { client, calls } = makeClient([200, payload]);
  const ml = await client.listMemories({ workspace: "ws1", layer: "semantic", limit: 10, offset: 5 });
  const u = new URL(calls[0].url);
  assert.equal(u.pathname, "/api/memories");
  assert.equal(u.searchParams.get("workspace"), "ws1");
  assert.equal(u.searchParams.get("limit"), "10");
  assert.equal(u.searchParams.get("offset"), "5");
  assert.equal(ml.total, 1);
  assert.equal(ml.memories[0].id, "m1");
});

test("listMemories defaults send no query", async () => {
  const { client, calls } = makeClient([200, { memories: [], total: 0 }]);
  await client.listMemories();
  assert.equal(calls[0].url, "http://ladym.test/api/memories");
});

test("updateMemory sends only set fields and escapes the id", async () => {
  const { client, calls } = makeClient([200, { memory: { id: "a/b c", summary: "new" } }]);
  const m = await client.updateMemory("a/b c", { summary: "new" });
  assert.equal(calls[0].method, "PUT");
  assert.equal(calls[0].url, "http://ladym.test/api/memories/a%2Fb%20c");
  assert.deepEqual(calls[0].body, { summary: "new" });
  assert.equal(m.summary, "new");
});

test("deleteMemory issues DELETE on the escaped path", async () => {
  const { client, calls } = makeClient([200, { deleted: "m1" }]);
  await client.deleteMemory("m1");
  assert.equal(calls[0].method, "DELETE");
  assert.equal(calls[0].url, "http://ladym.test/api/memories/m1");
});

for (const status of [400, 401, 500]) {
  test(`error mapping: ${status} -> LadymError with status`, async () => {
    const { client } = makeClient([status, { error: "bad things happened" }]);
    await assert.rejects(client.recall("x"), (err: unknown) => {
      assert.ok(err instanceof LadymError);
      assert.equal(err.status, status);
      assert.equal(err.message, `ladym server returned http ${status}: bad things happened`);
      return true;
    });
  });
}

test("error mapping: non-JSON error body falls back to trimmed text", async () => {
  const fetchFn = (async () => new Response("Bad Gateway", { status: 502 })) as FetchFn;
  const client = new LadymClient("http://ladym.test", { fetch: fetchFn });
  await assert.rejects(client.ping(), (err: unknown) => {
    assert.ok(err instanceof LadymError);
    assert.equal((err as LadymError).status, 502);
    assert.match((err as LadymError).message, /Bad Gateway/);
    return true;
  });
});

test("Basic auth header is sent only when a username is configured", async () => {
  const { fetchFn, calls } = fakeFetch([200, { status: "ok" }], [200, { status: "ok" }]);
  await new LadymClient("http://ladym.test", { username: "alice", password: "pw", fetch: fetchFn }).ping();
  assert.equal(calls[0].headers["Authorization"], "Basic " + Buffer.from("alice:pw").toString("base64"));
  await new LadymClient("http://ladym.test", { fetch: fetchFn }).ping();
  assert.equal(calls[1].headers["Authorization"], undefined);
});

test("timeout is applied via AbortSignal.timeout", async () => {
  const { client, calls } = makeClient([200, { status: "ok" }]);
  await client.ping();
  assert.ok(calls[0].signal instanceof AbortSignal);
  assert.equal(calls[0].signal!.aborted, false);

  const { fetchFn, calls: calls2 } = fakeFetch([200, { status: "ok" }]);
  await new LadymClient("http://ladym.test", { fetch: fetchFn, timeoutMs: 0 }).ping();
  assert.equal(calls2[0].signal, undefined);
});
