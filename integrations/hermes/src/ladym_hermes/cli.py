"""Hermes CLI integration for the ladyM memory provider.

Subcommands:
  status  — binary resolution, effective config, live store stats.
  install — download the ladym binary from GitHub releases (with optional
            embedded CJK dictionary) and wire it into ladym.json.
"""

from __future__ import annotations

import hashlib
import json
import logging
import platform
import shutil
import sys
import tarfile
import tempfile
import urllib.request
from pathlib import Path
from typing import Any, Callable, Dict, Optional, Tuple

from .ladym_client import LadymClient, LadymError, find_ladym_binary
from .provider import LadymMemoryProvider, default_hermes_home, load_config

logger = logging.getLogger(__name__)

GITHUB_REPO = "ProjAnvil/LadyM"
RELEASES_API = f"https://api.github.com/repos/{GITHUB_REPO}/releases"

_SOURCE_BUILD_HINT = (
    "no prebuilt binary for this platform; build from source with "
    "`go build -o bin/ladym ./cmd/ladym` from the ladyM repo and set LADYM_BIN"
)


def register_cli(subparser) -> None:
    """Register the ``ladym`` subcommand tree onto the given argparse subparsers."""
    status = subparser.add_parser(
        "status", help="Show ladyM memory provider status"
    )
    status.add_argument(
        "--hermes-home",
        default=None,
        help="HERMES_HOME directory (default: $HERMES_HOME or ~/.hermes)",
    )
    status.set_defaults(func=_cmd_status)

    install = subparser.add_parser(
        "install", help="Download the ladym binary from GitHub releases"
    )
    install.add_argument(
        "--hermes-home",
        default=None,
        help="HERMES_HOME directory (default: $HERMES_HOME or ~/.hermes)",
    )
    install.add_argument(
        "--version", default=None, metavar="vX.Y.Z",
        help="Release tag to install (default: latest)",
    )
    install.add_argument(
        "--fulldict", action="store_true",
        help="Install the variant with the embedded CJK dictionary "
             "(recommended for Chinese users; +31MB)",
    )
    install.add_argument(
        "--force", action="store_true",
        help="Overwrite an existing installed binary",
    )
    install.set_defaults(func=_cmd_install)


# -- status ------------------------------------------------------------------------


def _cmd_status(args) -> int:
    provider = LadymMemoryProvider()
    hermes_home = args.hermes_home or default_hermes_home()
    # Single safe read: malformed ladym.json logs a warning, no traceback.
    cfg = load_config(hermes_home)
    config_path = Path(hermes_home) / "ladym.json"

    print("ladym memory provider")
    configured_bin = cfg.get("ladym_bin")
    try:
        binary = find_ladym_binary(
            configured_bin if isinstance(configured_bin, str) else None
        )
        print(f"  binary:   {binary}")
    except LadymError:
        print(f"  binary:   unavailable — {provider.unavailable_reason()}")
        return 0
    if config_path.is_file():
        print(f"  config:   {config_path}")
        for key, value in cfg.items():
            print(f"    {key}: {value}")
    else:
        print(f"  config:   (none — defaults; {config_path})")

    db = Path(hermes_home) / "ladym" / "ladym.db"
    if not db.is_file():
        return 0
    client = LadymClient(binary=binary, db=str(db))
    try:
        client.start()
        stats = client.stats()
        print(f"  stats:    {json.dumps(stats)}")
    except LadymError as exc:
        print(f"  stats:    unavailable — {exc}")
    finally:
        client.close()
    return 0


# -- install -------------------------------------------------------------------------


def _platform_asset_key() -> Tuple[str, str]:
    """Map the current platform to a (goos, goarch) release-asset key."""
    if sys.platform == "darwin":
        os_name = "darwin"
    elif sys.platform.startswith("linux"):
        os_name = "linux"
    else:
        raise LadymError(f"unsupported platform {sys.platform!r}: {_SOURCE_BUILD_HINT}")
    machine = platform.machine().lower()
    arch = {"x86_64": "amd64", "amd64": "amd64",
            "arm64": "arm64", "aarch64": "arm64"}.get(machine)
    if arch is None:
        raise LadymError(f"unsupported architecture {machine!r}: {_SOURCE_BUILD_HINT}")
    return os_name, arch


def _fetch_json(url: str) -> Dict[str, Any]:
    """GET a JSON document (GitHub release metadata)."""
    req = urllib.request.Request(url, headers={"User-Agent": "ladym-hermes-plugin"})
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read().decode("utf-8"))


def _download(url: str, dest: Path) -> None:
    """Download a URL to a local file."""
    req = urllib.request.Request(url, headers={"User-Agent": "ladym-hermes-plugin"})
    with urllib.request.urlopen(req, timeout=300) as resp, open(dest, "wb") as fh:
        shutil.copyfileobj(resp, fh)


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _parse_sha256sums(text: str) -> Dict[str, str]:
    sums = {}
    for line in text.splitlines():
        parts = line.split()
        if len(parts) == 2:
            sums[parts[1].lstrip("*")] = parts[0]
    return sums


def install_binary(
    hermes_home: str,
    version: Optional[str] = None,
    fulldict: bool = False,
    force: bool = False,
    *,
    fetch_json: Optional[Callable[[str], Dict[str, Any]]] = None,
    download: Optional[Callable[[str, Path], None]] = None,
    platform_key: Optional[Tuple[str, str]] = None,
) -> Dict[str, Any]:
    """Download and install the ladym binary from GitHub releases.

    Installs to ``<hermes_home>/ladym/bin/ladym`` and records ``ladym_bin``
    in ``<hermes_home>/ladym.json`` (preserving other keys). The network
    seams (``fetch_json``/``download``) are injectable for tests.
    """
    fetch_json = fetch_json or _fetch_json
    download = download or _download
    os_name, arch = platform_key or _platform_asset_key()

    bin_dir = Path(hermes_home) / "ladym" / "bin"
    dest = bin_dir / "ladym"
    if dest.exists() and not force:
        raise LadymError(f"{dest} already exists; pass --force to overwrite")

    api_url = (f"{RELEASES_API}/tags/{version}" if version
               else f"{RELEASES_API}/latest")
    try:
        meta = fetch_json(api_url)
    except LadymError:
        raise
    except Exception as exc:
        raise LadymError(f"failed to fetch release metadata: {exc}") from exc
    tag = meta.get("tag_name") or version
    if not tag:
        raise LadymError("release metadata has no tag_name")

    variant = "ladym-personal-fulldict" if fulldict else "ladym-personal"
    name = f"{variant}-{tag}-{os_name}-{arch}.tar.gz"
    assets = {a.get("name"): a.get("browser_download_url")
              for a in meta.get("assets", [])}
    if name not in assets or "SHA256SUMS" not in assets:
        raise LadymError(
            f"no matching asset {name!r} in release {tag}; "
            f"available: {sorted(a for a in assets if a)}"
        )

    inner_binary = "ladym-fulldict" if fulldict else "ladym"
    with tempfile.TemporaryDirectory() as td:
        tarball = Path(td) / name
        sums_path = Path(td) / "SHA256SUMS"
        try:
            download(assets[name], tarball)
            download(assets["SHA256SUMS"], sums_path)
        except LadymError:
            raise
        except Exception as exc:
            raise LadymError(f"download failed: {exc}") from exc

        expected = _parse_sha256sums(sums_path.read_text()).get(name)
        actual = _sha256_file(tarball)
        if expected is None:
            raise LadymError(f"{name} not listed in SHA256SUMS of release {tag}")
        if actual != expected:
            raise LadymError(
                f"sha256 mismatch for {name}: expected {expected}, got {actual}; "
                f"aborting install"
            )

        bin_dir.mkdir(parents=True, exist_ok=True)
        with tarfile.open(tarball, "r:gz") as tf:
            member = next(
                (m for m in tf.getmembers()
                 if m.isfile() and Path(m.name).name == inner_binary),
                None,
            )
            if member is None:
                raise LadymError(f"{inner_binary} not found inside {name}")
            src = tf.extractfile(member)
            assert src is not None
            with open(dest, "wb") as out:
                shutil.copyfileobj(src, out)
        dest.chmod(0o755)

    cfg = load_config(hermes_home)
    cfg["ladym_bin"] = str(dest)
    config_path = Path(hermes_home) / "ladym.json"
    config_path.write_text(json.dumps(cfg, indent=2) + "\n")

    return {"version": tag, "path": str(dest), "fulldict": fulldict}


def _cmd_install(args) -> int:
    hermes_home = args.hermes_home or default_hermes_home()
    try:
        result = install_binary(
            hermes_home,
            version=args.version,
            fulldict=args.fulldict,
            force=args.force,
        )
    except LadymError as exc:
        print(f"ladym install failed: {exc}")
        return 1
    print("ladym binary installed")
    print(f"  version:  {result['version']}")
    print(f"  path:     {result['path']}")
    print(f"  fulldict: {'yes (embedded CJK dictionary)' if result['fulldict'] else 'no'}")
    print("  config:   ladym_bin written to ladym.json")
    return 0
