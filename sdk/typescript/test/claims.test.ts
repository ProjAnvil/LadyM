import test from "node:test";
import assert from "node:assert/strict";
import { LadymClient, LadymError, type Claim, type CreateClaimRequest } from "../src/index.js";

test("typed claims, evidence and error decoding", async () => {
  const requests: {path: string; body: any}[] = [];
  const fetcher: typeof fetch = async (input, init) => {
    const path = new URL(String(input)).pathname;
    requests.push({path, body: init?.body ? JSON.parse(String(init.body)) : undefined});
    if (path.endsWith("/missing")) return new Response('{"error":"memory not found"}', {status:404});
    if (path.startsWith("/api/evidence")) return new Response('{"id":"e","content":"[REDACTED]","type":"evidence"}');
    return new Response(JSON.stringify({memory:{id:"c"},revision:2,status:"verified",scope:{environment:"test",code_version:"v1",conditions:["ready"]},evidence_refs:[{memory_id:"e",kind:"test_result",fingerprint:"fp",start_rune:0,end_rune:0}]}));
  };
  const c = new LadymClient("http://unused", {fetch:fetcher, actor:"host"});
  assert.equal((await c.recordEvidence({kind:"test_result",content:"passed",outcome:"success",workspace:"w"})).id,"e");
  assert.equal((await c.archiveEvidence({kind:"observation",content:"text"})).content,"[REDACTED]");
  const request: CreateClaimRequest = {content:"works",evidence_refs:[{memory_id:"e",fingerprint:"fp",kind:"test_result",start_rune:0,end_rune:0}],scope:{environment:"test",code_version:"v1",conditions:["ready"]},workspace:"w"};
  const claim: Claim = await c.createClaim(request);
  assert.equal(claim.revision,2);
  assert.equal((await c.getClaim("c","w")).scope.code_version,"v1");
  assert.equal((await c.applyClaimFeedback("c",{expected_revision:1,action:"verify",method:"attested",evidence_refs:claim.evidence_refs,workspace:"w"})).status,"verified");
  assert.equal(typeof requests.at(-1)?.body.expected_revision,"number");
  assert.equal(requests.at(-1)?.body.evidence_refs[0].memory_id,"e");
  assert.equal(requests.at(-1)?.body.actor,"host");
  await assert.rejects(c.getClaim("missing"), (e:unknown) => e instanceof LadymError && e.status === 404);
});
