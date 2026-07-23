"""SecretStore — AES-256-GCM over ~/.ladyM."""

import os
import stat

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
