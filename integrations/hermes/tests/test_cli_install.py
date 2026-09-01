"""Tests for `hermes ladym install` (binary bootstrap from GitHub releases).

All network access is stubbed: `install_binary` takes `fetch_json`/`download`
callables, so no real HTTP happens here.
"""

from __future__ import annotations

import argparse
import hashlib
import io
import json
import tarfile
from pathlib import Path

import pytest

from ladym_hermes import cli as cli_mod
from ladym_hermes.cli import install_binary
from ladym_hermes.ladym_client import LadymError

TAG = "v0.5.2"
FAKE_BINARY = b"#!/bin/sh\necho fake ladym\n"


def make_tarball_bytes(binary_name: str, content: bytes = FAKE_BINARY) -> bytes:
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tf:
        info = tarfile.TarInfo(binary_name)
        info.size = len(content)
        info.mode = 0o755
        tf.addfile(info, io.BytesIO(content))
    return buf.getvalue()


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def make_sums(payloads: dict) -> bytes:
    lines = [f"{sha256(data)}  {name}" for name, data in payloads.items()]
    return ("\n".join(lines) + "\n").encode()


def asset_name(tag: str, os_name: str, arch: str, fulldict: bool) -> str:
    variant = "ladym-personal-fulldict" if fulldict else "ladym-personal"
    return f"{variant}-{tag}-{os_name}-{arch}.tar.gz"


class StubNet:
    """Fake release API + downloader driven by in-memory tarballs."""

    def __init__(self, tag: str = TAG, os_name: str = "darwin",
                 arch: str = "arm64", bad_sums: bool = False):
        self.plain_name = asset_name(tag, os_name, arch, fulldict=False)
        self.full_name = asset_name(tag, os_name, arch, fulldict=True)
        self.tarballs = {
            self.plain_name: make_tarball_bytes("ladym"),
            self.full_name: make_tarball_bytes("ladym-fulldict"),
        }
        sums = dict(self.tarballs)
        if bad_sums:
            sums[self.plain_name] = b"corrupted tarball"
            sums[self.full_name] = b"corrupted tarball"
        self.payloads = {
            f"https://example.test/{name}": data
            for name, data in self.tarballs.items()
        }
        self.payloads["https://example.test/SHA256SUMS"] = make_sums(sums)
        self.meta = {
            "tag_name": tag,
            "assets": [
                {"name": name,
                 "browser_download_url": f"https://example.test/{name}"}
                for name in (*self.tarballs, "SHA256SUMS")
            ],
        }
        self.fetched: list = []
        self.downloaded: list = []

    def fetch_json(self, url):
        self.fetched.append(url)
        return self.meta

    def download(self, url, dest):
        self.downloaded.append(url)
        Path(dest).write_bytes(self.payloads[url])

    def kwargs(self):
        return {"fetch_json": self.fetch_json, "download": self.download}


# -- platform mapping ----------------------------------------------------------

def test_platform_mapping_darwin_arm64(monkeypatch):
    monkeypatch.setattr("sys.platform", "darwin")
    monkeypatch.setattr("platform.machine", lambda: "arm64")
    assert cli_mod._platform_asset_key() == ("darwin", "arm64")


def test_platform_mapping_linux_x86_64(monkeypatch):
    monkeypatch.setattr("sys.platform", "linux")
    monkeypatch.setattr("platform.machine", lambda: "x86_64")
    assert cli_mod._platform_asset_key() == ("linux", "amd64")


def test_platform_mapping_linux_aarch64(monkeypatch):
    monkeypatch.setattr("sys.platform", "linux")
    monkeypatch.setattr("platform.machine", lambda: "aarch64")
    assert cli_mod._platform_asset_key() == ("linux", "arm64")


def test_platform_windows_rejected_with_source_hint(monkeypatch):
    monkeypatch.setattr("sys.platform", "win32")
    monkeypatch.setattr("platform.machine", lambda: "AMD64")
    with pytest.raises(LadymError, match="go build"):
        cli_mod._platform_asset_key()


def test_platform_unknown_arch_rejected_with_source_hint(monkeypatch):
    monkeypatch.setattr("sys.platform", "linux")
    monkeypatch.setattr("platform.machine", lambda: "mips64")
    with pytest.raises(LadymError, match="go build"):
        cli_mod._platform_asset_key()


# -- install flow ----------------------------------------------------------------

def test_install_latest_plain(tmp_path):
    net = StubNet()
    result = install_binary(str(tmp_path), platform_key=("darwin", "arm64"),
                            **net.kwargs())
    dest = tmp_path / "ladym" / "bin" / "ladym"
    assert result["version"] == TAG
    assert result["fulldict"] is False
    assert dest.read_bytes() == FAKE_BINARY
    assert dest.stat().st_mode & 0o111, "binary must be executable"
    assert net.fetched[0].endswith("/releases/latest")
    assert any(net.plain_name in url for url in net.downloaded)
    cfg = json.loads((tmp_path / "ladym.json").read_text())
    assert cfg["ladym_bin"] == str(dest)


def test_install_specific_version(tmp_path):
    net = StubNet(tag="v0.5.1", os_name="linux", arch="amd64")
    result = install_binary(str(tmp_path), version="v0.5.1",
                            platform_key=("linux", "amd64"), **net.kwargs())
    assert result["version"] == "v0.5.1"
    assert net.fetched[0].endswith("/releases/tags/v0.5.1")
    assert any("v0.5.1-linux-amd64" in url for url in net.downloaded)


def test_install_fulldict_renames_binary(tmp_path):
    net = StubNet()
    result = install_binary(str(tmp_path), fulldict=True,
                            platform_key=("darwin", "arm64"), **net.kwargs())
    assert result["fulldict"] is True
    assert any(net.full_name in url for url in net.downloaded)
    dest = tmp_path / "ladym" / "bin" / "ladym"  # renamed from ladym-fulldict
    assert dest.read_bytes() == FAKE_BINARY


def test_install_sha256_mismatch_aborts(tmp_path):
    net = StubNet(bad_sums=True)
    with pytest.raises(LadymError, match="sha256"):
        install_binary(str(tmp_path), platform_key=("darwin", "arm64"),
                       **net.kwargs())
    assert not (tmp_path / "ladym" / "bin" / "ladym").exists()


def test_install_existing_binary_requires_force(tmp_path):
    net = StubNet()
    dest = tmp_path / "ladym" / "bin" / "ladym"
    dest.parent.mkdir(parents=True)
    dest.write_bytes(b"old binary")
    with pytest.raises(LadymError, match="--force"):
        install_binary(str(tmp_path), platform_key=("darwin", "arm64"),
                       **net.kwargs())
    assert dest.read_bytes() == b"old binary"
    install_binary(str(tmp_path), force=True, platform_key=("darwin", "arm64"),
                   **net.kwargs())
    assert dest.read_bytes() == FAKE_BINARY


def test_install_preserves_existing_config_keys(tmp_path):
    (tmp_path / "ladym.json").write_text(json.dumps(
        {"workspace": "ws-custom", "recall_top_k": 7}))
    net = StubNet()
    install_binary(str(tmp_path), platform_key=("darwin", "arm64"),
                   **net.kwargs())
    cfg = json.loads((tmp_path / "ladym.json").read_text())
    assert cfg["workspace"] == "ws-custom"
    assert cfg["recall_top_k"] == 7
    assert cfg["ladym_bin"].endswith("ladym/bin/ladym")


def test_install_missing_asset_errors(tmp_path):
    net = StubNet(os_name="darwin", arch="arm64")
    with pytest.raises(LadymError, match="no matching asset"):
        install_binary(str(tmp_path), platform_key=("linux", "amd64"),
                       **net.kwargs())


def test_install_network_failure_is_clean_error(tmp_path):
    import urllib.error

    def boom(url):
        raise urllib.error.URLError("connection refused")

    with pytest.raises(LadymError, match="connection refused"):
        install_binary(str(tmp_path), platform_key=("darwin", "arm64"),
                       fetch_json=boom)
    assert not (tmp_path / "ladym" / "bin" / "ladym").exists()


# -- CLI wiring --------------------------------------------------------------------

def test_register_cli_exposes_install_subcommand():
    # Mirror hermes_cli/main.py: it creates the `hermes ladym` parser itself
    # (subparsers.add_parser("ladym")) and passes THAT parser to register_cli.
    parser = argparse.ArgumentParser()
    subs = parser.add_subparsers()
    ladym_parser = subs.add_parser("ladym")
    cli_mod.register_cli(ladym_parser)
    args = parser.parse_args(
        ["ladym", "install", "--version", "v0.5.1", "--fulldict", "--force"])
    assert args.version == "v0.5.1"
    assert args.fulldict is True
    assert args.force is True
    assert callable(args.func)


def test_register_cli_status_and_bare_invocation():
    parser = argparse.ArgumentParser()
    subs = parser.add_subparsers()
    ladym_parser = subs.add_parser("ladym")
    cli_mod.register_cli(ladym_parser)
    args = parser.parse_args(["ladym", "status"])
    assert callable(args.func)
    # Bare `hermes ladym` falls back to a usage-printing handler.
    bare = parser.parse_args(["ladym"])
    assert callable(bare.func)
