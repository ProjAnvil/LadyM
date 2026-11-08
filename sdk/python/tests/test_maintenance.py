from ladym_client import Client, ScheduleMaintenanceRequest


def test_maintenance_wire_contract():
    calls = []

    def transport(method, path, query, body):
        calls.append((method, path, query, body))
        if path.endswith("/status"):
            return 200, {"unit": "bytes", "calls": 1, "reserved_bytes": 2048}
        if method == "GET":
            return 200, {"jobs": [{"id": "job", "workspace": "owned", "operation": "new_episode",
                          "target_id": "episode", "state": "done", "input_ids": ["episode"],
                          "fingerprints": {"episode": "fp"}, "input_revisions": {"episode": 0},
                          "args": {}, "revision": 3, "attempt": 1, "not_before": 0, "lease_until": 60}]}
        return 200, {"id": "job", "workspace": "owned", "type": "maintenance_job"}

    client = Client("http://localhost", transport=transport)
    result = client.schedule_maintenance(ScheduleMaintenanceRequest("new_episode", "episode", ["episode"], {"mode": "literal"}, "owned"))
    assert result.id == "job"
    assert client.list_maintenance_jobs("owned", 10)[0].input_revisions == {"episode": 0}
    assert client.maintenance_status("owned").unit == "bytes"
    assert calls[0][3]["args"] == {"mode": "literal"}
    assert calls[1][2]["limit"] == 10
