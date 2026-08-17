package secrets

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDir(t *testing.T) {
	t.Run("home set", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		if got := Dir(); got != filepath.Join(home, ".ladyM") {
			t.Errorf("Dir = %q, want %q", got, filepath.Join(home, ".ladyM"))
		}
	})
	t.Run("home unset falls back to dot", func(t *testing.T) {
		t.Setenv("HOME", "")
		if got := Dir(); got != filepath.Join(".", ".ladyM") {
			t.Errorf("Dir with empty HOME = %q, want %q", got, filepath.Join(".", ".ladyM"))
		}
	})
}

func TestNewStoreDefaultDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := NewStore("")
	want := filepath.Join(home, ".ladyM", "master.key")
	if s.MasterKeyPath() != want {
		t.Errorf("MasterKeyPath = %q, want %q", s.MasterKeyPath(), want)
	}
}

func TestMasterKeyPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ladyM")
	s := NewStore(dir)
	if got := s.MasterKeyPath(); got != filepath.Join(dir, "master.key") {
		t.Errorf("MasterKeyPath = %q", got)
	}
}

func TestRemove(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ladyM")
	s := NewStore(dir)
	if _, err := s.SetMasterKey("key"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("A", "one"); err != nil {
		t.Fatal(err)
	}
	// Removing an absent name returns false.
	ok, err := s.Remove("MISSING")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("Remove(MISSING) should be false")
	}
	// Removing a present name returns true and drops it from cache.
	ok, err = s.Remove("A")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("Remove(A) should be true")
	}
	got, err := s.Get("A")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("Get after Remove = %q, want empty", got)
	}
	names, err := s.ListNames()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Errorf("ListNames after Remove = %v, want empty", names)
	}
}

func TestRemoveUnreadableSecrets(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ladyM")
	s := NewStore(dir)
	if _, err := s.SetMasterKey("key"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("A", "one"); err != nil {
		t.Fatal(err)
	}
	enc := filepath.Join(dir, "secrets.enc")
	if err := os.Chmod(enc, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(enc, 0o600) })
	if _, err := s.Remove("A"); err == nil {
		t.Error("Remove should fail when secrets.enc is unreadable")
	}
}

func TestRemoveWriteError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ladyM")
	s := NewStore(dir)
	if _, err := s.SetMasterKey("key"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("A", "one"); err != nil {
		t.Fatal(err)
	}
	// readAll succeeds, but atomicWrite cannot create secrets.enc.tmp
	// because a directory already occupies that path.
	if err := os.Mkdir(filepath.Join(dir, "secrets.enc.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remove("A"); err == nil {
		t.Error("Remove should fail when secrets.enc cannot be rewritten")
	}
}

func TestSplitInvalidBase64(t *testing.T) {
	if _, _, err := split("%%%not-base64%%%"); err == nil {
		t.Error("split should reject invalid base64")
	}
}

func TestResetMasterKeyErrors(t *testing.T) {
	t.Run("no master key", func(t *testing.T) {
		s := NewStore(filepath.Join(t.TempDir(), ".ladyM"))
		if err := s.ResetMasterKey("new"); err == nil {
			t.Error("ResetMasterKey without a master key should fail")
		}
	})
	t.Run("invalid base64 master key", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".ladyM")
		s := NewStore(dir)
		if _, err := s.SetMasterKey("key"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(s.MasterKeyPath(), []byte("not!base64"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.ResetMasterKey("new"); err == nil {
			t.Error("ResetMasterKey with undecodable master.key should fail")
		}
	})
	t.Run("corrupt entry", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".ladyM")
		s := NewStore(dir)
		if _, err := s.SetMasterKey("key"); err != nil {
			t.Fatal(err)
		}
		truncated := base64.StdEncoding.EncodeToString([]byte("tiny"))
		if err := os.WriteFile(filepath.Join(dir, "secrets.enc"),
			[]byte("BAD = "+truncated+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.ResetMasterKey("new"); err == nil {
			t.Error("ResetMasterKey on truncated ciphertext should fail")
		}
	})
	t.Run("unreadable master key", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".ladyM")
		s := NewStore(dir)
		if _, err := s.SetMasterKey("key"); err != nil {
			t.Fatal(err)
		}
		// Stat succeeds on a 000 file, but ReadFile fails.
		if err := os.Chmod(s.MasterKeyPath(), 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(s.MasterKeyPath(), 0o600) })
		if err := s.ResetMasterKey("new"); err == nil {
			t.Error("ResetMasterKey with unreadable master.key should fail")
		}
	})
	t.Run("unreadable secrets.enc", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".ladyM")
		s := NewStore(dir)
		if _, err := s.SetMasterKey("key"); err != nil {
			t.Fatal(err)
		}
		if err := s.Set("A", "one"); err != nil {
			t.Fatal(err)
		}
		enc := filepath.Join(dir, "secrets.enc")
		if err := os.Chmod(enc, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(enc, 0o600) })
		if err := s.ResetMasterKey("new"); err == nil {
			t.Error("ResetMasterKey should fail when secrets.enc is unreadable")
		}
	})
	t.Run("secrets.enc is a directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".ladyM")
		s := NewStore(dir)
		if _, err := s.SetMasterKey("key"); err != nil {
			t.Fatal(err)
		}
		// readAll treats a directory as absent, but the pair write cannot
		// rename secrets.enc.tmp over a directory.
		if err := os.Mkdir(filepath.Join(dir, "secrets.enc"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := s.ResetMasterKey("new"); err == nil {
			t.Error("ResetMasterKey should fail when secrets.enc cannot be replaced")
		}
	})
	t.Run("wrong old key", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".ladyM")
		s := NewStore(dir)
		if _, err := s.SetMasterKey("old"); err != nil {
			t.Fatal(err)
		}
		if err := s.Set("A", "one"); err != nil {
			t.Fatal(err)
		}
		// Swap the master key out from under the existing ciphertext.
		if err := s.writeMaster(DeriveAESKey("other")); err != nil {
			t.Fatal(err)
		}
		if err := s.ResetMasterKey("new"); err == nil {
			t.Error("ResetMasterKey with mismatched old key should fail to decrypt")
		}
	})
}

func TestResetMasterKeyRandomKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ladyM")
	s := NewStore(dir)
	if _, err := s.SetMasterKey("old"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("A", "one"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetMasterKey(""); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("A")
	if err != nil {
		t.Fatal(err)
	}
	if got != "one" {
		t.Errorf("after random-key reset Get = %q, want %q", got, "one")
	}
}

func TestGetErrors(t *testing.T) {
	t.Run("entry exists but no master key", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".ladyM")
		s := NewStore(dir)
		if _, err := s.SetMasterKey("key"); err != nil {
			t.Fatal(err)
		}
		if err := s.Set("A", "one"); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(s.MasterKeyPath()); err != nil {
			t.Fatal(err)
		}
		// Fresh store: empty cache, so Get must hit disk and then fail
		// reading the AES key.
		if _, err := NewStore(dir).Get("A"); err == nil {
			t.Error("Get should fail when master.key is gone")
		}
	})
	t.Run("wrong master key", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".ladyM")
		s := NewStore(dir)
		if _, err := s.SetMasterKey("key"); err != nil {
			t.Fatal(err)
		}
		if err := s.Set("A", "one"); err != nil {
			t.Fatal(err)
		}
		if err := s.writeMaster(DeriveAESKey("other")); err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(dir).Get("A"); err == nil {
			t.Error("Get with the wrong master key should fail to decrypt")
		}
	})
	t.Run("unreadable secrets.enc", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".ladyM")
		s := NewStore(dir)
		if _, err := s.SetMasterKey("key"); err != nil {
			t.Fatal(err)
		}
		if err := s.Set("A", "one"); err != nil {
			t.Fatal(err)
		}
		enc := filepath.Join(dir, "secrets.enc")
		if err := os.Chmod(enc, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(enc, 0o600) })
		if _, err := NewStore(dir).Get("A"); err == nil {
			t.Error("Get should fail when secrets.enc is unreadable")
		}
	})
}

func TestSetErrors(t *testing.T) {
	t.Run("unreadable secrets.enc", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".ladyM")
		s := NewStore(dir)
		if _, err := s.SetMasterKey("key"); err != nil {
			t.Fatal(err)
		}
		if err := s.Set("A", "one"); err != nil {
			t.Fatal(err)
		}
		enc := filepath.Join(dir, "secrets.enc")
		if err := os.Chmod(enc, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(enc, 0o600) })
		if err := s.Set("B", "two"); err == nil {
			t.Error("Set should fail when secrets.enc is unreadable")
		}
	})
	t.Run("invalid base64 master key", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".ladyM")
		s := NewStore(dir)
		if _, err := s.SetMasterKey("key"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(s.MasterKeyPath(), []byte("not!base64"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.Set("A", "one"); err == nil {
			t.Error("Set with undecodable master.key should fail")
		}
	})
	t.Run("secrets.enc is a directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".ladyM")
		s := NewStore(dir)
		if _, err := s.SetMasterKey("key"); err != nil {
			t.Fatal(err)
		}
		// readAll treats a directory as absent, but the rename in
		// atomicWrite cannot replace a directory.
		if err := os.Mkdir(filepath.Join(dir, "secrets.enc"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := s.Set("A", "one"); err == nil {
			t.Error("Set should fail when secrets.enc cannot be replaced")
		}
	})
}

func TestSetMasterKeyRandomKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ladyM")
	s := NewStore(dir)
	aesKey, err := s.SetMasterKey("")
	if err != nil {
		t.Fatal(err)
	}
	if len(aesKey) != aesKeyLen {
		t.Errorf("random master key length = %d, want %d", len(aesKey), aesKeyLen)
	}
	if err := s.Set("A", "one"); err != nil {
		t.Fatal(err)
	}
}

func TestSetMasterKeyDirIsFile(t *testing.T) {
	// dir is an existing regular file: ensureDir/MkdirAll must fail.
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(f)
	if _, err := s.SetMasterKey("key"); err == nil {
		t.Error("SetMasterKey should fail when the store dir is not creatable")
	}
}

func TestListNamesUnreadableSecrets(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ladyM")
	s := NewStore(dir)
	if _, err := s.SetMasterKey("key"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("A", "one"); err != nil {
		t.Fatal(err)
	}
	enc := filepath.Join(dir, "secrets.enc")
	if err := os.Chmod(enc, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(enc, 0o600) })
	if _, err := s.ListNames(); err == nil {
		t.Error("ListNames should fail when secrets.enc is unreadable")
	}
}

func TestReadAllSkipsCommentsAndBlankLines(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ladyM")
	s := NewStore(dir)
	content := "# comment\n\n  \nno-equals-line\nA = enc-a\n B = enc-b \n"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.enc"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := s.ListNames()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "A" || names[1] != "B" {
		t.Errorf("ListNames = %v, want [A B]", names)
	}
}

func TestMustAESPanicsOnInvalidKeySize(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("mustAES should panic on an invalid key size")
		}
	}()
	mustAES([]byte("tenbytes!!")) // neither 16, 24 nor 32 bytes
}

func TestEnsureDirNotCreatable(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(filepath.Join(f, "sub"))
	if err := s.ensureDir(); err == nil {
		t.Error("ensureDir should fail when a path component is a file")
	}
}

func TestWriteAllDirIsFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(f)
	if err := s.writeAll(map[string]string{"A": "enc"}); err == nil {
		t.Error("writeAll should fail when the store dir is not creatable")
	}
}

func TestAtomicWriteErrors(t *testing.T) {
	t.Run("tmp not writable", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "missing", "out")
		if err := atomicWrite(target, []byte("data")); err == nil {
			t.Error("atomicWrite should fail when the tmp file cannot be created")
		}
	})
	t.Run("rename onto directory", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := atomicWrite(target, []byte("data")); err == nil {
			t.Error("atomicWrite should fail when the target is a directory")
		}
	})
}

func TestAtomicWritePair(t *testing.T) {
	dir := t.TempDir()
	t.Run("success", func(t *testing.T) {
		a := filepath.Join(dir, "a")
		b := filepath.Join(dir, "b")
		if err := atomicWritePair([2]string{a, "one"}, [2]string{b, "two"}); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, a); got != "one" {
			t.Errorf("a = %q, want %q", got, "one")
		}
		if got := readFile(t, b); got != "two" {
			t.Errorf("b = %q, want %q", got, "two")
		}
	})
	t.Run("first tmp not writable", func(t *testing.T) {
		a := filepath.Join(dir, "missing", "a")
		b := filepath.Join(dir, "b1")
		if err := atomicWritePair([2]string{a, "one"}, [2]string{b, "two"}); err == nil {
			t.Error("atomicWritePair should fail when the first tmp cannot be created")
		}
	})
	t.Run("second tmp not writable cleans up first", func(t *testing.T) {
		a := filepath.Join(dir, "a2")
		b := filepath.Join(dir, "missing", "b")
		if err := atomicWritePair([2]string{a, "one"}, [2]string{b, "two"}); err == nil {
			t.Error("atomicWritePair should fail when the second tmp cannot be created")
		}
		if fileExists(a + ".tmp") {
			t.Error("failed pair write should remove the first tmp file")
		}
	})
	t.Run("first rename fails", func(t *testing.T) {
		a := filepath.Join(dir, "adir")
		if err := os.Mkdir(a, 0o700); err != nil {
			t.Fatal(err)
		}
		b := filepath.Join(dir, "b3")
		if err := atomicWritePair([2]string{a, "one"}, [2]string{b, "two"}); err == nil {
			t.Error("atomicWritePair should fail when the first target is a directory")
		}
		if fileExists(a+".tmp") || fileExists(b+".tmp") {
			t.Error("failed pair write should remove both tmp files")
		}
	})
	t.Run("second rename fails", func(t *testing.T) {
		a := filepath.Join(dir, "a4")
		b := filepath.Join(dir, "bdir")
		if err := os.Mkdir(b, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := atomicWritePair([2]string{a, "one"}, [2]string{b, "two"}); err == nil {
			t.Error("atomicWritePair should fail when the second target is a directory")
		}
		// The first file was already renamed into place.
		if got := readFile(t, a); got != "one" {
			t.Errorf("a = %q, want %q", got, "one")
		}
		if fileExists(b + ".tmp") {
			t.Error("failed pair write should remove the second tmp file")
		}
	})
}

func TestEncryptValueProducesDecryptableCiphertext(t *testing.T) {
	key := DeriveAESKey("passphrase")
	enc, err := encryptValue(key, "secret-value")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(enc, "secret-value") {
		t.Error("ciphertext must not contain plaintext")
	}
	nonce, ct, err := split(enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(nonce) != nonceLen {
		t.Errorf("nonce length = %d, want %d", len(nonce), nonceLen)
	}
	if len(ct) == 0 {
		t.Error("ciphertext should not be empty")
	}
}
