/**
 * Integration test against a real `ladym serve --http` binary.
 *
 * Gated on LADYM_SDK_IT=1 (same spirit as the Go-side LADYM_TEST_PG_DSN
 * tests): without it the whole file skips. Requires a Go toolchain on PATH
 * to build ./cmd/ladym into a temp dir.
 */

import assert from "node:assert/strict";
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { LadymClient, LadymError } from "../src/index.js";

// Resolved from the compiled location (dist-test/test/), one level deeper
// than the source tree.
const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../../..");
const RUN_IT = process.env.LADYM_SDK_IT === "1";

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.listen(0, "127.0.0.1", () => {
      const port = (srv.address() as { port: number }).port;
      srv.close(() => resolve(port));
    });
    srv.on("error", reject);
  });
}

async function waitHealthy(base: string, proc: ChildProcess, timeoutMs = 15_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (proc.exitCode !== null) throw new Error("ladym serve exited before becoming healthy");
    try {
      const resp = await fetch(base + "/healthz");
      if (resp.status === 200) return;
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error("ladym serve did not become healthy in 15s");
}

test("full chain against a real ladym serve --http", { skip: !RUN_IT && "set LADYM_SDK_IT=1 to run" }, async (t) => {
  const tmp = mkdtempSync(path.join(tmpdir(), "ladym-sdk-ts-it-"));
  const binary = path.join(tmp, "ladym");
  execFileSync("go", ["build", "-o", binary, "./cmd/ladym"], { cwd: REPO_ROOT });

  const port = await freePort();
  const proc = spawn(binary, ["serve", "--db", path.join(tmp, "it.db"), "--http", `127.0.0.1:${port}`], {
    stdio: "ignore",
  });
  t.after(() => proc.kill());
  const base = `http://127.0.0.1:${port}`;
  await waitHealthy(base, proc);

  const c = new LadymClient(base);
  await c.ping();

  const r = await c.remember("the quixplotron deploy pin is 42", { tags: ["it"], workspace: "sdk-it" });
  assert.equal(r.dropped, false);
  assert.ok(r.id);

  const ev = await c.recordEvent("sdk-bot", "boot", { observation: "sdk integration booted", workspace: "sdk-it" });
  assert.equal(ev.layer, "L1_episodic");

  const resp = await c.recall("quixplotron deploy pin", { topK: 5, workspace: "sdk-it" });
  assert.ok(resp.results.some((h) => h.memory.content.includes("quixplotron")));

  const st = await c.stats({ workspace: "sdk-it" });
  assert.ok(st.total_memories >= 2);

  const ml = await c.listMemories({ workspace: "sdk-it" });
  assert.ok(ml.total >= 2 && ml.memories.some((m) => m.id === r.id));

  const updated = await c.updateMemory(r.id, { summary: "deploy pin fact" });
  assert.equal(updated.summary, "deploy pin fact");

  const edgeId = await c.link(r.id, ev.id);
  assert.ok(edgeId);

  await c.forget(r.id);
  const resp2 = await c.recall("quixplotron deploy pin", { topK: 5, workspace: "sdk-it" });
  assert.ok(!resp2.results.some((h) => h.memory.id === r.id));

  await assert.rejects(c.deleteMemory(r.id), (err: unknown) => {
    assert.ok(err instanceof LadymError);
    assert.equal((err as LadymError).status, 404);
    return true;
  });
});
