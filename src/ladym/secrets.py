"""Encrypted secret store — AES-256-GCM over ``~/.ladyM``.

Security boundary (READ BEFORE TRUSTING THIS):
    This store prevents **plaintext-at-rest**: ``cat``-ting ``secrets.enc``,
    shoulder-surfing, or accidentally pasting the file into chat/logs/commits
    will not reveal key values. It does **NOT** protect against full
    ``~/.ladyM/`` exfiltration — the master key and the ciphertext live in the
    same directory, so anyone who can read the directory can decrypt. This is
    the explicit trade-off for cross-platform, non-interactive operation
    (MCP servers and background workers must be able to decrypt without a
    passphrase prompt). For stronger isolation, keep ``~/.ladyM`` on encrypted
    storage and rely on OS account/file-permission boundaries.
"""

from __future__ import annotations

import base64
import os
import secrets as _py_secrets
from pathlib import Path

from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from cryptography.hazmat.primitives.kdf.hkdf import HKDF

from .errors import ConfigError

LADYM_DIR = Path.home() / ".ladyM"
_NONCE_LEN = 12  # AES-GCM standard nonce length
_AES_KEY_LEN = 32


def _derive_aes_key(user_key: str) -> bytes:
    """HKDF-SHA256 a user-supplied string into a 32-byte AES key.

    Uses ``cryptography``'s HKDF (works on all supported Python versions,
    including 3.11/3.12 — ``hashlib.hkdf_sha256`` is only available in
    Python 3.13+). The user's raw passphrase is never persisted: only the
    derived key is stored in ``master.key``.
    """
    return HKDF(
        algorithm=hashes.SHA256(),
        length=_AES_KEY_LEN,
        salt=None,
        info=b"ladym-master-key",
    ).derive(user_key.encode())


class SecretStore:
    def __init__(self, dir: Path = LADYM_DIR):
        self._dir = Path(dir)
        self._master = self._dir / "master.key"
        self._secrets = self._dir / "secrets.enc"
        self._cache: dict[str, str] = {}

    # ----- master key -----
    def has_master_key(self) -> bool:
        return self._master.exists()

    def require_master_key(self) -> None:
        if not self.has_master_key():
            raise ConfigError(
                "no master key set — run `ladym config set-master-key` first."
            )

    def _read_aes_key(self) -> bytes:
        self.require_master_key()
        return base64.b64decode(self._master.read_bytes())

    def set_master_key(self, key: str | None) -> bytes:
        if self._read_all():
            raise ConfigError(
                "secrets.enc already has entries — setting a fresh master key "
                "would make them unrecoverable. Use "
                "`ladym config reset-master-key` to re-encrypt under a new key."
            )
        aes_key = _derive_aes_key(key) if key is not None else _py_secrets.token_bytes(32)
        self._write_master(aes_key)
        return aes_key

    def reset_master_key(self, new_key: str | None) -> None:
        old_aes = self._read_aes_key()  # also require_master_key
        old_kv = self._read_all()
        # decrypt all under old key
        plain: dict[str, str] = {}
        for name, enc in old_kv.items():
            nonce, ct = self._split(enc)
            plain[name] = AESGCM(old_aes).decrypt(nonce, ct, None).decode()
        # re-encrypt under new key
        new_aes = _derive_aes_key(new_key) if new_key is not None else _py_secrets.token_bytes(32)
        new_kv: dict[str, str] = {}
        for name, value in plain.items():
            new_kv[name] = self._encrypt_value(new_aes, value)
        self._write_master(new_aes)
        self._write_all(new_kv)
        self._cache = {}

    # ----- kv -----
    def get(self, name: str) -> str | None:
        if name in self._cache:
            return self._cache[name]
        enc = self._read_all().get(name)
        if enc is None:
            return None
        nonce, ct = self._split(enc)
        value = AESGCM(self._read_aes_key()).decrypt(nonce, ct, None).decode()
        self._cache[name] = value
        return value

    def set(self, name: str, value: str) -> None:
        self.require_master_key()
        kv = self._read_all()
        kv[name] = self._encrypt_value(self._read_aes_key(), value)
        self._write_all(kv)
        self._cache[name] = value

    def list_names(self) -> list[str]:
        return sorted(self._read_all().keys())

    def remove(self, name: str) -> bool:
        kv = self._read_all()
        if name not in kv:
            return False
        del kv[name]
        self._write_all(kv)
        self._cache.pop(name, None)
        return True

    # ----- crypto helpers -----
    @staticmethod
    def _encrypt_value(aes_key: bytes, value: str) -> str:
        nonce = os.urandom(_NONCE_LEN)
        ct = AESGCM(aes_key).encrypt(nonce, value.encode(), None)
        return base64.b64encode(nonce + ct).decode()

    @staticmethod
    def _split(enc: str) -> tuple[bytes, bytes]:
        raw = base64.b64decode(enc)
        return raw[:_NONCE_LEN], raw[_NONCE_LEN:]

    # ----- low-level IO (atomic + permissions) -----
    def _read_all(self) -> dict[str, str]:
        if not self._secrets.exists():
            return {}
        kv: dict[str, str] = {}
        for line in self._secrets.read_text().splitlines():
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            name, _, enc = line.partition("=")
            kv[name.strip()] = enc.strip()
        return kv

    def _write_all(self, kv: dict[str, str]) -> None:
        self._ensure_dir()
        text = "".join(f"{k} = {v}\n" for k, v in sorted(kv.items()))
        self._atomic_write(self._secrets, text.encode(), 0o600)

    def _write_master(self, aes_key: bytes) -> None:
        self._ensure_dir()
        self._atomic_write(self._master, base64.b64encode(aes_key), 0o600)

    def _ensure_dir(self) -> None:
        self._dir.mkdir(parents=True, exist_ok=True)
        try:
            os.chmod(self._dir, 0o700)
        except OSError:
            pass  # non-POSIX FS best-effort

    @staticmethod
    def _atomic_write(path: Path, data: bytes, mode: int) -> None:
        tmp = path.with_suffix(path.suffix + ".tmp")
        tmp.write_bytes(data)
        try:
            os.chmod(tmp, mode)
        except OSError:
            pass
        os.replace(tmp, path)


def get_store() -> SecretStore:
    """Module-level accessor (resolves ``Path.home()`` at call time)."""
    return SecretStore()
