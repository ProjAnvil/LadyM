/**
 * Scenario tests for the default fetch transport over a loopback node:http
 * server (hermetic — no external network), plus the timeout and the
 * browser-style btoa fallback branch.
 */

import assert from "node:assert/strict";
import { createServer, type Server } from "node:http";
import test from "node:test";

import { LadymClient, LadymError } from "../src/index.js";

interface SeenRequest {
  method: string;
  url: string;
  headers: Record<string, string | string[] | undefined>;
  body: string;
}

async function loopbackServer(
  respond: (req: SeenRequest, res: import("node:http").ServerResponse) => void,
): Promise<{ base: string; close: () => Promise<void> }> {
  const srv: Server = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      respond(
        { method: req.method ?? "", url: req.url ?? "", headers: req.headers, body: Buffer.concat(chunks).toString() },
        res,
      );
    });
  });
  await new Promise<void>((resolve) => srv.listen(0, "127.0.0.1", resolve));
  const port = (srv.address() as { port: number }).port;
  return {
    base: `http://127.0.0.1:${port}`,
    close: () => new Promise((resolve) => srv.close(() => resolve())),
  };
}

function jsonResponse(res: import("node:http").ServerResponse, status: number, body: unknown): void {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
}

test("real fetch: GET with query encoding, no auth header when unconfigured", async (t) => {
  let seen: SeenRequest | undefined;
  const { base, close } = await loopbackServer((req, res) => {
    seen = req;
    jsonResponse(res, 200, { memories: [], total: 0 });
  });
  t.after(close);

  const c = new LadymClient(base);
  const ml = await c.listMemories({ workspace: "ws 1", limit: 5 });
  assert.equal(ml.total, 0);
  const u = new URL("http://x" + seen!.url);
  assert.equal(u.pathname, "/api/memories");
  assert.equal(u.searchParams.get("workspace"), "ws 1");
  assert.equal(u.searchParams.get("limit"), "5");
  assert.equal(seen!.method, "GET");
  assert.equal(seen!.headers["authorization"], undefined);
});

test("real fetch: POST sends JSON body, content type, and Basic auth", async (t) => {
  let seen: SeenRequest | undefined;
  const { base, close } = await loopbackServer((req, res) => {
    seen = req;
    jsonResponse(res, 200, { id: "m1", hash: "h" });
  });
  t.after(close);

  const c = new LadymClient(base, { username: "alice", password: "pw" });
  const r = await c.remember("a fact with unicode: 部署", { tags: ["t"], workspace: "ws" });
  assert.equal(r.id, "m1");
  assert.equal(seen!.headers["content-type"], "application/json");
  assert.equal(seen!.headers["authorization"], "Basic " + Buffer.from("alice:pw").toString("base64"));
  const body = JSON.parse(seen!.body);
  assert.equal(body.content, "a fact with unicode: 部署");
});

test("real fetch: 401 error body maps to LadymError with status", async (t) => {
  const { base, close } = await loopbackServer((_req, res) => jsonResponse(res, 401, { error: "unauthorized" }));
  t.after(close);
  await assert.rejects(new LadymClient(base).ping(), (err: unknown) => {
    assert.ok(err instanceof LadymError);
    assert.equal((err as LadymError).status, 401);
    assert.equal((err as LadymError).message, "ladym server returned http 401: unauthorized");
    return true;
  });
});

test("real fetch: HTML error page falls back to trimmed text", async (t) => {
  const { base, close } = await loopbackServer((_req, res) => {
    res.writeHead(502, { "Content-Type": "text/html" });
    res.end("<html>Bad Gateway</html>");
  });
  t.after(close);
  await assert.rejects(new LadymClient(base).ping(), (err: unknown) => {
    assert.ok(err instanceof LadymError);
    assert.equal((err as LadymError).status, 502);
    assert.match((err as LadymError).message, /Bad Gateway/);
    return true;
  });
});

test("timeoutMs fires via AbortSignal.timeout against a silent server", async (t) => {
  const { base, close } = await loopbackServer(() => {
    // never respond
  });
  t.after(close);
  const c = new LadymClient(base, { timeoutMs: 100 });
  await assert.rejects(c.ping(), (err: unknown) => {
    assert.ok(err instanceof Error);
    assert.equal((err as Error).name, "TimeoutError");
    return true;
  });
});

test("browser branch: btoa fallback when Buffer is absent", async () => {
  const saved = (globalThis as Record<string, unknown>).Buffer;
  (globalThis as Record<string, unknown>).Buffer = undefined;
  try {
    // A stub rather than a real Response: undici's Response constructor
    // itself reads Buffer.byteLength, which the absent-Buffer simulation
    // would break before reaching the client's auth-header branch.
    const fetchFn = (async (_url: unknown, init?: RequestInit) => {
      const headers = (init?.headers ?? {}) as Record<string, string>;
      assert.equal(headers["Authorization"], "Basic " + btoa("bob:s3cret"));
      return { status: 200, text: async () => "{}" } as Response;
    }) as typeof fetch;
    await new LadymClient("http://ladym.test", { username: "bob", password: "s3cret", fetch: fetchFn }).ping();
  } finally {
    (globalThis as Record<string, unknown>).Buffer = saved;
  }
});

test("success body that is not a JSON object decodes as empty payload", async () => {
  const fetchFn = (async () => new Response("[1,2,3]", { status: 200 })) as typeof fetch;
  const c = new LadymClient("http://ladym.test", { fetch: fetchFn });
  const st = await c.stats(); // array body -> {} -> zero-value Stats
  assert.equal(st.total_memories, undefined);
});
