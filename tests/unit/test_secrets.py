"""SecretStore — AES-256-GCM over ~/.ladyM."""

import os
import stat
from pathlib import Path

import pytest

from ladym.errors import ConfigError
from ladym.secrets import SecretStore


@pytest.fixture
def store(tmp_path):
    return SecretStore(dir=tmp_path)


def test_no_master_key_means_empty(store):
    assert not store.has_master_key()
    with pytest.raises(ConfigError, match="set-master-key"):
        store.require_master_key()
    with pytest.raises(ConfigError):
        store.set("K", "v")  # cannot set without master key


def test_set_master_key_random_then_roundtrip(store):
    aes = store.set_master_key(None)  # random
    assert len(aes) == 32
    store.set("DEEPSEEK_API_KEY", "sk-abc")
    assert store.get("DEEPSEEK_API_KEY") == "sk-abc"
    assert store.list_names() == ["DEEPSEEK_API_KEY"]


def test_set_master_key_user_string_derived(store):
    store.set_master_key("my-passphrase")
    store.set("K", "secret")
    assert store.get("K") == "secret"
    # user string is NOT stored verbatim
    assert b"my-passphrase" not in (store._master).read_bytes()


def test_master_key_file_permissions(store):
    store.set_master_key(None)
    mode = stat.S_IMODE(os.stat(store._master).st_mode)
    assert mode == 0o600
    dmode = stat.S_IMODE(os.stat(store._dir).st_mode)
    assert dmode == 0o700


def test_overwrite_and_remove(store):
    store.set_master_key(None)
    store.set("K", "v1")
    store.set("K", "v2")  # overwrite
    assert store.get("K") == "v2"
    assert store.remove("K") is True
    assert store.get("K") is None
    assert store.remove("K") is False


def test_set_master_key_refuses_when_secrets_exist(store):
    store.set_master_key(None)
    store.set("K", "v")
    with pytest.raises(ConfigError, match="reset-master-key"):
        store.set_master_key("new")


def test_reset_master_key_reencrypts(store):
    store.set_master_key("old")
    store.set("A", "1")
    store.set("B", "2")
    store.reset_master_key("new")
    assert store.get("A") == "1"
    assert store.get("B") == "2"
    assert store.list_names() == ["A", "B"]


def test_reset_master_key_requires_existing(store):
    with pytest.raises(ConfigError, match="set-master-key"):
        store.reset_master_key("new")


def test_ciphertext_is_not_plaintext(store):
    store.set_master_key(None)
    store.set("K", "super-secret-value")
    raw = store._secrets.read_text()
    assert "super-secret-value" not in raw


def test_reset_master_key_is_cross_file_atomic_on_failure(monkeypatch, store):
    """Spec §1: 任一步失败则不变 — if any staging/replace step fails BEFORE
    the first on-disk target is touched, BOTH files must still hold the OLD
    contents (old master key still decrypts the old secrets).

    The store stages both temp files fully, THEN runs the two ``os.replace``
    calls back-to-back. We force the FIRST ``os.replace`` to raise, exercising
    the "failure before any target is swapped" branch: both targets must be
    byte-identical to their pre-reset state and no temp files leak.

    (A failure on the *second* replace is the irreducible POSIX window that
    spec §1 acknowledges needs a directory-rename trick to close; that window
    is out of scope per the task brief — "ordering + minimizing the window is
    the spec's bar".)
    """
    store.set_master_key("old")
    store.set("A", "1")
    store.set("B", "2")

    # Snapshot pre-reset on-disk state.
    old_master_bytes = store._master.read_bytes()
    old_secrets_bytes = store._secrets.read_bytes()

    # Force the FIRST os.replace call to raise. Both temp files are already
    # staged on disk at this point, so the contract under test is: neither
    # target file is swapped, and the staged temps get cleaned up.
    def flaky_replace(src, dst):
        raise OSError("simulated failure on first os.replace")

    monkeypatch.setattr("ladym.secrets.os.replace", flaky_replace)

    with pytest.raises(OSError, match="simulated failure on first os.replace"):
        store.reset_master_key("new")

    # BOTH files must be byte-identical to their pre-reset state.
    assert store._master.read_bytes() == old_master_bytes
    assert store._secrets.read_bytes() == old_secrets_bytes
    # No temp files leaked (cleanup ran on the failure path).
    assert not list(store._dir.glob("*.tmp"))
    # The OLD master key still decrypts the OLD secrets — roundtrip survives.
    assert store.get("A") == "1"
    assert store.get("B") == "2"
    assert store.list_names() == ["A", "B"]


def test_reset_master_key_failure_during_temp_staging_is_atomic(monkeypatch, store):
    """Spec §1 also covers the staging phase: if writing either temp file
    raises, neither target file is touched. We simulate the failure by making
    the second temp file's write raise (Path.write_bytes → OSError)."""
    store.set_master_key("old")
    store.set("A", "1")

    old_master_bytes = store._master.read_bytes()
    old_secrets_bytes = store._secrets.read_bytes()

    # The first temp (master.key.tmp) writes OK; the second (secrets.enc.tmp)
    # write raises. Patch write_bytes to fail on the secrets.enc temp path.
    real_write_bytes = Path.write_bytes

    def flaky_write_bytes(self, data):
        if self.name == "secrets.enc.tmp":
            raise OSError("simulated failure staging secrets.enc.tmp")
        return real_write_bytes(self, data)

    monkeypatch.setattr(Path, "write_bytes", flaky_write_bytes)

    with pytest.raises(OSError, match="simulated failure staging secrets.enc.tmp"):
        store.reset_master_key("new")

    # Neither target touched; the master.key.tmp that did get staged is cleaned up.
    assert store._master.read_bytes() == old_master_bytes
    assert store._secrets.read_bytes() == old_secrets_bytes
    assert not list(store._dir.glob("*.tmp"))
    assert store.get("A") == "1"
