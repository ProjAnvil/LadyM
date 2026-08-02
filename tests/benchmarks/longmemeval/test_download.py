from pathlib import Path
import json
from benchmarks.longmemeval.config import BenchConfig
from benchmarks.longmemeval import download_data


def test_download_skips_existing_valid(tmp_path, monkeypatch):
    cfg = BenchConfig(difficulty="oracle", base_dir=tmp_path)
    cfg.data_dir.mkdir(parents=True, exist_ok=True)
    # pre-place a valid-sized file
    target = cfg.data_dir / "longmemeval_oracle.json"
    payload = json.dumps([{"question_id": "x"}])
    target.write_text(payload)
    monkeypatch.setattr(download_data, "EXPECTED_SIZES",
                        {"longmemeval_oracle.json": len(payload.encode())})
    monkeypatch.setattr(download_data, "_http_get", lambda url: (_ for _ in ()).throw(AssertionError("should not download")))
    got = download_data.download(cfg)
    assert got["longmemeval_oracle.json"] == target


def test_download_rejects_wrong_size(tmp_path, monkeypatch):
    cfg = BenchConfig(difficulty="oracle", base_dir=tmp_path)
    cfg.data_dir.mkdir(parents=True, exist_ok=True)
    (cfg.data_dir / "longmemeval_oracle.json").write_text("too small")
    monkeypatch.setattr(download_data, "EXPECTED_SIZES",
                        {"longmemeval_oracle.json": 999999})
    # Avoid real network: serve wrong-sized bytes so the post-download
    # size check fires RuntimeError.
    monkeypatch.setattr(download_data, "_http_get", lambda url: b"short")
    try:
        download_data.download(cfg)
        assert False, "should have raised"
    except (ValueError, RuntimeError):
        pass
