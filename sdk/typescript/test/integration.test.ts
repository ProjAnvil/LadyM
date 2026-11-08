/**
 * Integration test against a real `ladym serve --http` binary.
 *
 * Gated on LADYM_SDK_IT=1 (same spirit as the Go-side LADYM_TEST_PG_DSN
 * tests): without it the whole file skips. Requires a Go toolchain on PATH
 * to build ./cmd/ladym into a temp dir.
 */

import assert from "node:assert/strict";
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
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
  const configPath = path.join(tmp, "archive.toml");
  writeFileSync(configPath, "[archive]\nenabled=true\n");
  const proc = spawn(binary, ["--config", configPath, "serve", "--db", path.join(tmp, "it.db"), "--http", `127.0.0.1:${port}`], {
    stdio: "ignore",
  });
  t.after(() => proc.kill());
  const base = `http://127.0.0.1:${port}`;
  await waitHealthy(base, proc);
  const navigation = new LadymClient(base);
  const source = await navigation.remember("ExactTypeScriptNavigation", {workspace: "ts-topic"});
  const topic = await navigation.buildTopic({topic: "module/path", input_ids: [source.id], expected_revision: 0, workspace: "ts-topic"});
  assert.equal((await navigation.getTopic("module/path", "ts-topic")).id, topic.id);
  const hybrid = await navigation.hybridRecall({query: "ExactTypeScriptNavigation", limit: 1, workspace: "ts-topic"});
  assert.equal(hybrid.results[0]?.memory.id, source.id);
  const task = await navigation.setTaskState({id: "explicit", goal: "user goal", next_step: "next", ttl_s: 60, workspace: "ts-topic"});
  const bootstrap = await navigation.bootstrap({topic_ids: [topic.id], task_id: "explicit", conditions: ["A"], workspace: "ts-topic"});
  assert.equal(bootstrap.used, Buffer.byteLength(bootstrap.content));
  assert.ok(bootstrap.used <= 4096 && bootstrap.source_ids.includes(task.id) && bootstrap.source_ids.includes(topic.id));
  await assert.rejects(navigation.bootstrap({unit: "tokens"}), /bytes only/);

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

  const evidence = await c.recordEvidence({kind:"test_result", content:"passed", outcome:"success", workspace:"sdk-it"});
  const refs = [{memory_id:evidence.id, fingerprint:createHash("sha256").update("passed").digest("hex"), kind:"test_result" as const, start_rune:0, end_rune:0}];
  const claim = await c.createClaim({content:"works", evidence_refs:refs, workspace:"sdk-it"});
  assert.ok(claim.memory);
  const verified = await c.applyClaimFeedback(claim.memory.id, {expected_revision:1, action:"verify", method:"attested", evidence_refs:refs, workspace:"sdk-it"});
  assert.equal(verified.revision,2);
  assert.equal(verified.verification?.actor,"local");
  assert.equal((await c.getClaim(claim.memory.id,"sdk-it")).status,"verified");
  await assert.rejects(c.applyClaimFeedback(claim.memory.id,{expected_revision:1,action:"reject",reason:"bad",workspace:"sdk-it"}), (e:unknown) => e instanceof LadymError && e.status === 409);
  const archived = await c.archiveEvidence({kind:"observation",content:'{"token":"synthetic-secret"}',workspace:"sdk-it"});
  assert.ok(!archived.content.includes("synthetic-secret"));
  assert.equal(archived.metadata?.archive_marker,"ladym.archive.v1");
  await c.forget(evidence.id);
  assert.equal((await c.getClaim(claim.memory.id,"sdk-it")).memory?.content,"");
});
