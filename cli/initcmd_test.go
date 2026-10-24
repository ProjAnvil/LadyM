package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/storage"
)

// initSetup isolates HOME/env and chdirs into a fresh project dir.
func initSetup(t *testing.T) string {
	t.Helper()
	isolateEnv(t)
	t.Setenv("LADYM_DB", "")
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func runInitCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runCmd(t, initCmd(), args...)
}

func initFileExists(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Stat(p)
	return err == nil
}

// ---------------------------------------------------------------------------
// db creation
// ---------------------------------------------------------------------------

func TestInitCreatesDotDirDB(t *testing.T) {
	dir := initSetup(t)
	if _, err := runInitCmd(t); err != nil {
		t.Fatal(err)
	}
	if !initFileExists(t, filepath.Join(dir, ".ladyM", "ladym.db")) {
		t.Error(".ladyM/ladym.db missing after init")
	}
}

// ---------------------------------------------------------------------------
// legacy migration (CLI level)
// ---------------------------------------------------------------------------

func TestInitMigratesLegacyDB(t *testing.T) {
	dir := initSetup(t)
	legacy := filepath.Join(dir, "ladym.db")
	store, err := storage.NewStore(legacy, 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := runInitCmd(t); err != nil {
		t.Fatal(err)
	}
	if initFileExists(t, legacy) {
		t.Error("legacy ladym.db still present after migration")
	}
	if !initFileExists(t, filepath.Join(dir, ".ladyM", "ladym.db")) {
		t.Error("migrated db missing at .ladyM/ladym.db")
	}
}

func TestInitConflictBothDBs(t *testing.T) {
	dir := initSetup(t)
	if err := os.MkdirAll(filepath.Join(dir, ".ladyM"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ladym.db"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ladyM", "ladym.db"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runInitCmd(t)
	if err == nil {
		t.Fatal("expected conflict error when both legacy and new db exist")
	}
	if !strings.Contains(err.Error(), "exist") {
		t.Errorf("conflict error should mention both files exist, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// migrateLegacyDB unit tests (sidecars, no-op, conflict)
// ---------------------------------------------------------------------------

func TestMigrateLegacyDBMovesSidecars(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "ladym.db")
	target := filepath.Join(dir, ".ladyM", "ladym.db")
	for _, p := range []string{legacy, legacy + "-wal", legacy + "-shm", legacy + ".index.lock"} {
		if err := os.WriteFile(p, []byte(filepath.Base(p)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	migrated, err := migrateLegacyDB(legacy, target)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated {
		t.Fatal("migrated = false, want true")
	}
	for name, want := range map[string]string{
		target:                 "ladym.db",
		target + "-wal":        "ladym.db-wal",
		target + "-shm":        "ladym.db-shm",
		legacy + ".index.lock": "ladym.db.index.lock", // stale lock stays behind
	} {
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s content = %q, want %q", name, got, want)
		}
	}
	if initFileExists(t, legacy) || initFileExists(t, legacy+"-wal") || initFileExists(t, legacy+"-shm") {
		t.Error("legacy files should be gone after migration")
	}
}

func TestMigrateLegacyDBNoLegacy(t *testing.T) {
	dir := t.TempDir()
	migrated, err := migrateLegacyDB(filepath.Join(dir, "ladym.db"), filepath.Join(dir, ".ladyM", "ladym.db"))
	if err != nil {
		t.Fatal(err)
	}
	if migrated {
		t.Error("migrated = true, want false when no legacy db exists")
	}
}

func TestMigrateLegacyDBConflict(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "ladym.db")
	target := filepath.Join(dir, ".ladyM", "ladym.db")
	if err := os.WriteFile(legacy, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := migrateLegacyDB(legacy, target)
	if err == nil {
		t.Fatal("expected conflict error")
	}
}

// ---------------------------------------------------------------------------
// gitignore handling
// ---------------------------------------------------------------------------

func TestInitGitignoreAppend(t *testing.T) {
	dir := initSetup(t)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runInitCmd(t); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(got), "# ladyM\n.ladyM/\n") {
		t.Errorf(".gitignore = %q, want trailing ladyM block", got)
	}
	if !strings.Contains(string(got), "node_modules/\n") {
		t.Errorf(".gitignore = %q, original entries must survive", got)
	}
}

func TestInitGitignoreIdempotent(t *testing.T) {
	dir := initSetup(t)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n\n# ladyM\n.ladyM/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runInitCmd(t); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf(".gitignore changed on re-init:\n got %q\nwant %q", got, want)
	}
}

func TestInitGitignoreNoTrailingNewline(t *testing.T) {
	dir := initSetup(t)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runInitCmd(t); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	want := "node_modules/\n\n# ladyM\n.ladyM/\n"
	if string(got) != want {
		t.Errorf(".gitignore = %q, want %q", got, want)
	}
}

func TestInitGitignoreCreatedWhenGitPresent(t *testing.T) {
	dir := initSetup(t)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runInitCmd(t); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf(".gitignore should be created: %v", err)
	}
	if string(got) != "# ladyM\n.ladyM/\n" {
		t.Errorf(".gitignore = %q, want only the ladyM block", got)
	}
}

func TestInitGitignoreSkippedNoGit(t *testing.T) {
	dir := initSetup(t)
	out, err := runInitCmd(t)
	if err != nil {
		t.Fatal(err)
	}
	if initFileExists(t, filepath.Join(dir, ".gitignore")) {
		t.Error(".gitignore created without a git repo")
	}
	if !strings.Contains(strings.ToLower(out), "gitignore") {
		t.Errorf("output should carry a manual-gitignore hint, got: %q", out)
	}
}

// ---------------------------------------------------------------------------
// custom db path + idempotence
// ---------------------------------------------------------------------------

func TestInitCustomDBPath(t *testing.T) {
	dir := initSetup(t)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(dir, "custom", "mem.db")
	if _, err := runInitCmd(t, "--db", custom); err != nil {
		t.Fatal(err)
	}
	if !initFileExists(t, custom) {
		t.Errorf("custom db %s missing", custom)
	}
	if initFileExists(t, filepath.Join(dir, ".gitignore")) {
		t.Error("custom --db must not touch .gitignore")
	}
	if initFileExists(t, filepath.Join(dir, ".ladyM")) {
		t.Error("custom --db must not create .ladyM/")
	}
}

func TestInitIdempotentRunTwice(t *testing.T) {
	dir := initSetup(t)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if _, err := runInitCmd(t); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(got), ".ladyM/"); n != 1 {
		t.Errorf(".ladyM/ appears %d times in .gitignore, want 1: %q", n, got)
	}
}
