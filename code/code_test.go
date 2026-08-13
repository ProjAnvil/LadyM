package code

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestDetectLanguage(t *testing.T) {
	cases := map[string]string{
		"a.py": "python", "b.js": "javascript", "c.go": "go", "README.md": "",
	}
	for path, want := range cases {
		if got := DetectLanguage(path); got != want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestExtractPythonSymbols(t *testing.T) {
	src := `def hash_password(password: str) -> str:
    """Hash a plaintext password."""
    return "hashed:" + password


def verify_password(password: str, hashed: str) -> bool:
    """Check a plaintext password against a stored hash."""
    return hash_password(password) == hashed


class AuthService:
    """High-level service."""

    def __init__(self, secret: str):
        self.secret = secret

    def login(self, username: str, password: str) -> str:
        """Issue a JWT."""
        if verify_password(password, self.secret):
            return self._issue_token(username)

    def _issue_token(self, username: str) -> str:
        return "jwt." + username
`
	syms := ExtractSymbols([]byte(src), "python", "auth.service", "auth/service.py", 40)
	names := map[string]bool{}
	for _, s := range syms {
		names[s.QualifiedName] = true
	}
	for _, want := range []string{
		"auth.service.hash_password",
		"auth.service.verify_password",
		"auth.service.AuthService",
		"auth.service.AuthService.__init__",
		"auth.service.AuthService.login",
		"auth.service.AuthService._issue_token",
	} {
		if !names[want] {
			t.Errorf("missing symbol %q; have %v", want, names)
		}
	}

	// verify_password has a signature and docstring
	for _, s := range syms {
		if strings.HasSuffix(s.QualifiedName, "verify_password") {
			if s.Signature == "" {
				t.Error("verify_password signature empty")
			}
			if !strings.Contains(s.Docstring, "Check") && !strings.Contains(strings.ToLower(s.Docstring), "plaintext") {
				t.Errorf("verify_password docstring = %q", s.Docstring)
			}
		}
	}

	refs := BuildRefs(syms, "auth/service.py")
	if len(refs) < 1 {
		t.Fatal("expected at least one intra-file ref")
	}
}

func TestIndexCodebaseEndToEnd(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "auth"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth", "service.py"), []byte(`def hash_password(password: str) -> str:
    """Hash a plaintext password."""
    return "hashed:" + password


class AuthService:
    def login(self, username: str, password: str) -> str:
        return self._issue_token(username)

    def _issue_token(self, username: str) -> str:
        return "jwt." + username
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := storage.NewStore(filepath.Join(t.TempDir(), "db.sqlite"), 256, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	emb := storage.NewHashingEmbedding(256)
	cfg := config.ForTesting(t.TempDir())

	report, err := IndexCodebase(dir, store, emb, cfg, "test", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesIndexed != 1 {
		t.Fatalf("files_indexed = %d, want 1", report.FilesIndexed)
	}
	if report.SymbolsWritten < 4 {
		t.Errorf("symbols_written = %d, want >= 4", report.SymbolsWritten)
	}

	syms, err := store.SymbolsForFile("auth/service.py")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range syms {
		names[s.QualifiedName] = true
	}
	if !names["auth.service.AuthService.login"] {
		t.Errorf("missing method; have %v", names)
	}

	// incremental: second run skips unchanged
	report2, err := IndexCodebase(dir, store, emb, cfg, "test", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report2.FilesSkippedUnchanged != 1 {
		t.Errorf("skipped_unchanged = %d, want 1", report2.FilesSkippedUnchanged)
	}
	if report2.FilesIndexed != 0 {
		t.Errorf("files_indexed = %d, want 0", report2.FilesIndexed)
	}
}
