package secrets

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveAESKeyGolden(t *testing.T) {
	// Golden value captured from Python's ladym.secrets._derive_aes_key.
	got := base64.StdEncoding.EncodeToString(DeriveAESKey("test-passphrase"))
	want := "NNx8A7zHiVuWzma/0pHypDMa6/Ba5yg3HC4WmAGkPwI="
	if got != want {
		t.Errorf("DeriveAESKey = %s, want %s", got, want)
	}
}

func TestSecretStoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ladyM")
	s := NewStore(dir)
	if s.HasMasterKey() {
		t.Fatal("fresh store should have no master key")
	}
	if err := s.Set("KEY", "value"); err == nil {
		t.Fatal("Set without master key should fail")
	}
	if _, err := s.SetMasterKey("passphrase"); err != nil {
		t.Fatal(err)
	}
	if !s.HasMasterKey() {
		t.Fatal("master key should be set")
	}
	if err := s.Set("DEEPSEEK_API_KEY", "sk-12345"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("DEEPSEEK_API_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-12345" {
		t.Errorf("Get = %q, want %q", got, "sk-12345")
	}
	names, err := s.ListNames()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "DEEPSEEK_API_KEY" {
		t.Errorf("ListNames = %v", names)
	}
	// ciphertext must not contain the plaintext
	raw := readFile(t, filepath.Join(dir, "secrets.enc"))
	if strings.Contains(raw, "sk-12345") {
		t.Error("secrets.enc must not contain plaintext at rest")
	}
}

func TestSecretStoreReset(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ladyM")
	s := NewStore(dir)
	if _, err := s.SetMasterKey("old"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("A", "one"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetMasterKey("new"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("A")
	if err != nil {
		t.Fatal(err)
	}
	if got != "one" {
		t.Errorf("after reset Get = %q, want %q", got, "one")
	}
}

func TestSetMasterKeyRefusesWhenSecretsExist(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ladyM")
	s := NewStore(dir)
	if _, err := s.SetMasterKey("key"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("A", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMasterKey("other"); err == nil {
		t.Error("SetMasterKey should refuse when secrets already exist")
	}
}

func TestSplitTruncatedCiphertext(t *testing.T) {
	// Decodes to fewer bytes than the 12-byte GCM nonce: must return an
	// error, not panic on raw[:12].
	short := base64.StdEncoding.EncodeToString([]byte("tiny"))
	if _, _, err := split(short); err == nil {
		t.Error("split should reject ciphertext shorter than the nonce")
	}
}

func TestGetCorruptedEntryReturnsError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ladyM")
	s := NewStore(dir)
	if _, err := s.SetMasterKey("key"); err != nil {
		t.Fatal(err)
	}
	// Simulate a corrupted secrets.enc: value decodes to < 12 bytes.
	truncated := base64.StdEncoding.EncodeToString([]byte("tiny"))
	if err := os.WriteFile(filepath.Join(dir, "secrets.enc"),
		[]byte("BAD = "+truncated+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("BAD"); err == nil {
		t.Error("Get on truncated ciphertext should return an error, not panic")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
