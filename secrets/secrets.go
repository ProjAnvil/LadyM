// Package secrets provides an encrypted secret store — AES-256-GCM over
// ~/.ladyM.
//
// Security boundary: this store prevents plaintext-at-rest — cat-ing
// secrets.enc will not reveal key values. It does NOT protect against full
// ~/.ladyM exfiltration (the master key and ciphertext live in the same
// directory). This is the explicit trade-off for cross-platform,
// non-interactive operation.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ProjAnvil/LadyM/config"
)

const (
	nonceLen  = 12 // AES-GCM standard nonce length
	aesKeyLen = 32
)

// Dir returns the default ~/.ladyM directory (resolved at call time).
func Dir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".ladyM")
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// DeriveAESKey derives a 32-byte AES key from a user-supplied string via
// HKDF-SHA256 (salt=None, info="ladym-master-key"), matching the Python
// cryptography backend. The user's raw passphrase is never persisted.
func DeriveAESKey(userKey string) []byte {
	ikm := []byte(userKey)
	salt := make([]byte, 32) // RFC 5869: salt=None → HashLen zero bytes
	prk := hmacSHA256(salt, ikm)
	info := []byte("ladym-master-key")
	// HKDF-Expand to 32 bytes: T(1) = HMAC(PRK, T(0)="" || info || 0x01)
	return hmacSHA256(prk, append(append([]byte{}, info...), 0x01))
}

// Store is the encrypted secret store.
type Store struct {
	dir     string
	master  string
	secrets string
	cache   map[string]string
}

// NewStore returns a Store rooted at dir (defaults to ~/.ladyM).
func NewStore(dir string) *Store {
	if dir == "" {
		dir = Dir()
	}
	return &Store{
		dir:     dir,
		master:  filepath.Join(dir, "master.key"),
		secrets: filepath.Join(dir, "secrets.enc"),
		cache:   map[string]string{},
	}
}

// MasterKeyPath returns the path to master.key (for display/diagnostics only).
func (s *Store) MasterKeyPath() string { return s.master }

// HasMasterKey reports whether master.key exists.
func (s *Store) HasMasterKey() bool { return fileExists(s.master) }

func (s *Store) requireMasterKey() error {
	if !s.HasMasterKey() {
		return configError("no master key set — run `ladym config set-master-key` first.")
	}
	return nil
}

func configError(msg string) error { return &config.ConfigError{Msg: msg} }

func (s *Store) readAESKey() ([]byte, error) {
	if err := s.requireMasterKey(); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(s.master)
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
}

// SetMasterKey writes the master key (deriving from key, or a random key when
// key is empty). Refuses when secrets.enc already has entries.
func (s *Store) SetMasterKey(key string) ([]byte, error) {
	if kv, _ := s.readAll(); len(kv) > 0 {
		return nil, configError("secrets.enc already has entries — setting a fresh master key would make them unrecoverable. Use `ladym config reset-master-key` to re-encrypt under a new key.")
	}
	var aesKey []byte
	if key != "" {
		aesKey = DeriveAESKey(key)
	} else {
		aesKey = make([]byte, 32)
		if _, err := rand.Read(aesKey); err != nil {
			return nil, err
		}
	}
	if err := s.writeMaster(aesKey); err != nil {
		return nil, err
	}
	return aesKey, nil
}

// ResetMasterKey re-encrypts every secret under a new master key.
func (s *Store) ResetMasterKey(newKey string) error {
	oldAES, err := s.readAESKey()
	if err != nil {
		return err
	}
	oldKV, err := s.readAll()
	if err != nil {
		return err
	}
	plain := map[string]string{}
	for name, enc := range oldKV {
		nonce, ct, err := split(enc)
		if err != nil {
			return err
		}
		gcm, err := cipher.NewGCM(mustAES(oldAES))
		if err != nil {
			return err
		}
		pt, err := gcm.Open(nil, nonce, ct, nil)
		if err != nil {
			return err
		}
		plain[name] = string(pt)
	}
	var newAES []byte
	if newKey != "" {
		newAES = DeriveAESKey(newKey)
	} else {
		newAES = make([]byte, 32)
		if _, err := rand.Read(newAES); err != nil {
			return err
		}
	}
	newKV := map[string]string{}
	for name, value := range plain {
		enc, err := encryptValue(newAES, value)
		if err != nil {
			return err
		}
		newKV[name] = enc
	}
	masterBytes := base64.StdEncoding.EncodeToString(newAES)
	secretsBytes := renderKV(newKV)
	if err := atomicWritePair(
		[2]string{s.master, masterBytes},
		[2]string{s.secrets, secretsBytes},
	); err != nil {
		return err
	}
	s.cache = map[string]string{}
	return nil
}

// Get returns the decrypted value for name, or "" when absent.
func (s *Store) Get(name string) (string, error) {
	if v, ok := s.cache[name]; ok {
		return v, nil
	}
	kv, err := s.readAll()
	if err != nil {
		return "", err
	}
	enc, ok := kv[name]
	if !ok {
		return "", nil
	}
	nonce, ct, err := split(enc)
	if err != nil {
		return "", err
	}
	aesKey, err := s.readAESKey()
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(mustAES(aesKey))
	if err != nil {
		return "", err
	}
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	s.cache[name] = string(pt)
	return string(pt), nil
}

// Set stores name=value (encrypted at rest).
func (s *Store) Set(name, value string) error {
	if err := s.requireMasterKey(); err != nil {
		return err
	}
	kv, err := s.readAll()
	if err != nil {
		return err
	}
	aesKey, err := s.readAESKey()
	if err != nil {
		return err
	}
	enc, err := encryptValue(aesKey, value)
	if err != nil {
		return err
	}
	kv[name] = enc
	if err := s.writeAll(kv); err != nil {
		return err
	}
	s.cache[name] = value
	return nil
}

// ListNames returns the sorted stored key names (values never echoed).
func (s *Store) ListNames() ([]string, error) {
	kv, err := s.readAll()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(kv))
	for n := range kv {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// Remove deletes name; returns false when absent.
func (s *Store) Remove(name string) (bool, error) {
	kv, err := s.readAll()
	if err != nil {
		return false, err
	}
	if _, ok := kv[name]; !ok {
		return false, nil
	}
	delete(kv, name)
	if err := s.writeAll(kv); err != nil {
		return false, err
	}
	delete(s.cache, name)
	return true, nil
}

// ---------------------------------------------------------------------------
// crypto helpers
// ---------------------------------------------------------------------------

func mustAES(key []byte) cipher.Block {
	block, err := aes.NewCipher(key)
	if err != nil {
		// aes.NewCipher only fails on invalid key sizes; we always pass 32 bytes.
		panic(err)
	}
	return block
}

func encryptValue(aesKey []byte, value string) (string, error) {
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(mustAES(aesKey))
	if err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(value), nil)
	return base64.StdEncoding.EncodeToString(append(nonce, ct...)), nil
}

func split(enc string) ([]byte, []byte, error) {
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) < nonceLen {
		return nil, nil, fmt.Errorf("corrupt secret: decoded ciphertext is %d bytes, shorter than the %d-byte nonce", len(raw), nonceLen)
	}
	return raw[:nonceLen], raw[nonceLen:], nil
}

// ---------------------------------------------------------------------------
// low-level IO (atomic + permissions)
// ---------------------------------------------------------------------------

func (s *Store) readAll() (map[string]string, error) {
	if !fileExists(s.secrets) {
		return map[string]string{}, nil
	}
	b, err := os.ReadFile(s.secrets)
	if err != nil {
		return nil, err
	}
	kv := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		name, enc, _ := strings.Cut(line, "=")
		kv[strings.TrimSpace(name)] = strings.TrimSpace(enc)
	}
	return kv, nil
}

func renderKV(kv map[string]string) string {
	names := make([]string, 0, len(kv))
	for n := range kv {
		names = append(names, n)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, n := range names {
		sb.WriteString(n)
		sb.WriteString(" = ")
		sb.WriteString(kv[n])
		sb.WriteString("\n")
	}
	return sb.String()
}

func (s *Store) writeAll(kv map[string]string) error {
	if err := s.ensureDir(); err != nil {
		return err
	}
	return atomicWrite(s.secrets, []byte(renderKV(kv)))
}

func (s *Store) writeMaster(aesKey []byte) error {
	if err := s.ensureDir(); err != nil {
		return err
	}
	return atomicWrite(s.master, []byte(base64.StdEncoding.EncodeToString(aesKey)))
}

func (s *Store) ensureDir() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(s.dir, 0o700) // best-effort on non-POSIX
	return nil
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0o600)
	return os.Rename(tmp, path)
}

func atomicWritePair(pairA, pairB [2]string) error {
	tmpA, tmpB := pairA[0]+".tmp", pairB[0]+".tmp"
	if err := os.WriteFile(tmpA, []byte(pairA[1]), 0o600); err != nil {
		return err
	}
	_ = os.Chmod(tmpA, 0o600)
	if err := os.WriteFile(tmpB, []byte(pairB[1]), 0o600); err != nil {
		_ = os.Remove(tmpA)
		return err
	}
	_ = os.Chmod(tmpB, 0o600)
	if err := os.Rename(tmpA, pairA[0]); err != nil {
		_ = os.Remove(tmpA)
		_ = os.Remove(tmpB)
		return err
	}
	if err := os.Rename(tmpB, pairB[0]); err != nil {
		_ = os.Remove(tmpB)
		return err
	}
	return nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
