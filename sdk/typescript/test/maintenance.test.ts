import {test} from "node:test";
import assert from "node:assert/strict";
import {LadymClient} from "../src/client.js";

test("maintenance request/list/status preserve scoped typed byte contracts", async () => {
  const calls: Array<{url: string, body: unknown}> = [];
  const client = new LadymClient("http://localhost", {fetch: async (input, init) => {
    const url = String(input);
    calls.push({url, body: init?.body ? JSON.parse(String(init.body)) : undefined});
    const data = url.includes("/status") ? {unit: "bytes", reserved_bytes: 2048} :
      init?.method === "POST" ? {id: "job"} : {jobs: [{id: "job", input_revisions: {episode: 0}, state: "done"}]};
    return new Response(JSON.stringify(data), {status: 200});
  }});
  assert.equal((await client.scheduleMaintenance({operation: "new_episode", target_id: "episode", input_ids: ["episode"], args: {mode: "literal"}, workspace: "owned"})).id, "job");
  assert.deepEqual((await client.listMaintenanceJobs("owned", 10))[0].input_revisions, {episode: 0});
  assert.equal((await client.maintenanceStatus("owned")).unit, "bytes");
  assert.ok(calls[1].url.includes("workspace=owned"));
  assert.ok(calls[1].url.includes("limit=10"));
});
