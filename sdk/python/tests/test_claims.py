import unittest

from ladym_client import Client, Claim, ClaimFeedbackRequest, ClaimScope, CreateClaimRequest, EvidenceRef, LadymError, RecordEvidenceRequest


class ClaimTests(unittest.TestCase):
    def test_typed_claims_and_errors(self):
        requests = []

        def transport(method, path, query, body):
            requests.append((method, path, query, body))
            if path == "/api/claims/missing":
                return 404, {"error": "memory not found"}
            if path.startswith("/api/evidence"):
                return 200, {"id": "e", "type": "evidence", "content": "[REDACTED]"}
            return 200, {
                "memory": {"id": "c", "content": "works"},
                "status": "verified", "revision": 2,
                "scope": {"environment": "test", "code_version": "v1", "conditions": ["ready"]},
                "evidence_refs": [{"memory_id": "e", "fingerprint": "fp", "kind": "test_result", "start_rune": 0, "end_rune": 0}],
                "verification": {"actor": "local", "method": "attested", "checked_at": 1, "evidence_refs": []},
            }

        c = Client("http://unused", transport=transport, actor="host")
        ev = c.record_evidence(RecordEvidenceRequest(kind="test_result", content="passed", outcome="success", workspace="w"))
        self.assertEqual(ev.id, "e")
        self.assertEqual(c.archive_evidence(RecordEvidenceRequest(kind="observation", content="text")).content, "[REDACTED]")
        claim = c.create_claim(CreateClaimRequest(content="works", evidence_refs=[EvidenceRef(memory_id="e", fingerprint="fp", kind="test_result")], scope=ClaimScope(environment="test"), workspace="w"))
        self.assertIsInstance(claim, Claim)
        self.assertIsInstance(claim.evidence_refs[0], EvidenceRef)
        self.assertEqual(claim.verification.actor, "local")
        self.assertEqual(c.get_claim("c", workspace="w").scope.code_version, "v1")
        self.assertEqual(c.apply_claim_feedback("c", ClaimFeedbackRequest(expected_revision=1, action="verify", evidence_refs=claim.evidence_refs, method="attested", workspace="w")).revision, 2)
        self.assertIsInstance(requests[-1][3]["expected_revision"], int)
        self.assertEqual(requests[-1][3]["evidence_refs"][0]["memory_id"], "e")
        self.assertEqual(requests[-1][3]["actor"], "host")
        with self.assertRaises(LadymError) as raised:
            c.get_claim("missing")
        self.assertEqual(raised.exception.status, 404)


if __name__ == "__main__":
    unittest.main()
