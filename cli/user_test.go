//go:build !enterprise

// Tests for the `ladym user` subcommand group (local users-table management):
// the two-prompt password read, --password-env resolution, and the
// add/list/delete/passwd commands against a temp db.

package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"golang.org/x/crypto/bcrypt"
)

// readPasswordTwice: matching entries return the password (prompts go to the
// prompt writer); mismatched entries are a ConfigError; a broken reader
// propagates the read error.
func TestReadPasswordTwice(t *testing.T) {
	var prompt bytes.Buffer
	pw, err := readPasswordTwice(strings.NewReader("s3cret\ns3cret\n"), &prompt)
	if err != nil || pw != "s3cret" {
		t.Fatalf("matching passwords: pw=%q err=%v", pw, err)
	}
	if got := prompt.String(); !strings.Contains(got, "Password: ") || !strings.Contains(got, "Confirm password: ") {
		t.Errorf("prompts = %q, want both labels", got)
	}

	if _, err := readPasswordTwice(strings.NewReader("a\nb\n"), nil); err == nil {
		t.Error("mismatched passwords should fail")
	} else {
		var cfgErr *config.ConfigError
		if !errors.As(err, &cfgErr) || !strings.Contains(cfgErr.Msg, "do not match") {
			t.Errorf("mismatch error = %v, want ConfigError 'do not match'", err)
		}
	}

	if _, err := readPasswordTwice(errReader{}, nil); err == nil {
		t.Error("reader failure should propagate")
	}
}

// errReader always fails (covers the non-EOF read-error path).
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read exploded") }

// resolveNewPassword: --password-env reads the named variable (set empty =
// passwordless); an unset variable and the no-TTY interactive path are
// ConfigErrors naming the fix.
func TestResolveNewPassword(t *testing.T) {
	t.Setenv("LADYM_TEST_PW", "from-env")
	pw, err := resolveNewPassword("LADYM_TEST_PW")
	if err != nil || pw != "from-env" {
		t.Fatalf("password-env set: pw=%q err=%v", pw, err)
	}

	t.Setenv("LADYM_TEST_PW_EMPTY", "")
	pw, err = resolveNewPassword("LADYM_TEST_PW_EMPTY")
	if err != nil || pw != "" {
		t.Fatalf("password-env set-but-empty (passwordless): pw=%q err=%v", pw, err)
	}

	if _, err := resolveNewPassword("LADYM_TEST_PW_UNSET"); err == nil ||
		!strings.Contains(err.Error(), "LADYM_TEST_PW_UNSET is not set") {
		t.Errorf("unset password-env: err=%v, want ConfigError naming the var", err)
	}

	old := stdinIsTTY
	stdinIsTTY = func() bool { return false }
	t.Cleanup(func() { stdinIsTTY = old })
	if _, err := resolveNewPassword(""); err == nil ||
		!strings.Contains(err.Error(), "--password-env") {
		t.Errorf("no TTY without password-env: err=%v, want ConfigError pointing at --password-env", err)
	}
}

// user add (via --password-env) persists a bcrypt hash that the password
// verifies against; user list prints the table without hashes.
func TestUserAddAndList(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	t.Setenv("LADYM_TEST_PW", "pw-carol")

	out, err := runCmd(t, userAddCmd(), "--db", db, "--password-env", "LADYM_TEST_PW",
		"--workspace", "acme", "--admin", "carol")
	if err != nil {
		t.Fatalf("user add: %v (%s)", err, out)
	}
	if !strings.Contains(out, "added user carol") || !strings.Contains(out, "workspace=acme") || !strings.Contains(out, "admin=true") {
		t.Errorf("add output = %q", out)
	}

	eng, err := newEngine(db, "")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	u, err := eng.Store.GetUser("carol")
	if err != nil || u == nil {
		t.Fatalf("GetUser(carol): %v %v", u, err)
	}
	if u.Workspace != "acme" || !u.Admin {
		t.Errorf("stored user = %+v, want workspace=acme admin=true", u)
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("pw-carol")) != nil {
		t.Error("stored hash does not verify against the given password")
	}

	// List: header + the row, password hash never printed.
	out, err = runCmd(t, userListCmd(), "--db", db)
	if err != nil {
		t.Fatalf("user list: %v", err)
	}
	if !strings.Contains(out, "username") || !strings.Contains(out, "carol") || !strings.Contains(out, "acme") {
		t.Errorf("list output = %q", out)
	}
	if strings.Contains(out, u.PasswordHash) {
		t.Errorf("list output leaks the password hash: %q", out)
	}
}

// user list on an empty table says so instead of printing a bare header.
func TestUserListEmpty(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	out, err := runCmd(t, userListCmd(), "--db", db)
	if err != nil {
		t.Fatalf("user list: %v", err)
	}
	if !strings.Contains(out, "no users") {
		t.Errorf("empty list output = %q, want 'no users'", out)
	}
}

// user add without --password-env and without a TTY fails before touching
// the store.
func TestUserAddNoTTYFails(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	old := stdinIsTTY
	stdinIsTTY = func() bool { return false }
	t.Cleanup(func() { stdinIsTTY = old })

	if _, err := runCmd(t, userAddCmd(), "--db", db, "carol"); err == nil ||
		!strings.Contains(err.Error(), "--password-env") {
		t.Errorf("add without TTY: err=%v, want the --password-env ConfigError", err)
	}
}

// user delete removes the row; deleting a missing user is a ConfigError.
func TestUserDelete(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	t.Setenv("LADYM_TEST_PW", "pw")
	if _, err := runCmd(t, userAddCmd(), "--db", db, "--password-env", "LADYM_TEST_PW", "carol"); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	out, err := runCmd(t, userDeleteCmd(), "--db", db, "carol")
	if err != nil || !strings.Contains(out, "deleted user carol") {
		t.Fatalf("user delete: out=%q err=%v", out, err)
	}

	eng, err := newEngine(db, "")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if u, err := eng.Store.GetUser("carol"); err != nil || u != nil {
		t.Errorf("GetUser after delete = %v, %v; want nil, nil", u, err)
	}

	if _, err := runCmd(t, userDeleteCmd(), "--db", db, "carol"); err == nil ||
		!strings.Contains(err.Error(), "no such user carol") {
		t.Errorf("delete missing user: err=%v, want ConfigError 'no such user'", err)
	}
}

// user passwd replaces only the hash: workspace/admin survive, the new
// password verifies and the old one does not. A missing user is a
// ConfigError.
func TestUserPasswd(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	t.Setenv("LADYM_TEST_PW", "old-pw")
	t.Setenv("LADYM_TEST_PW2", "new-pw")
	if _, err := runCmd(t, userAddCmd(), "--db", db, "--password-env", "LADYM_TEST_PW",
		"--workspace", "acme", "--admin", "carol"); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	out, err := runCmd(t, userPasswdCmd(), "--db", db, "--password-env", "LADYM_TEST_PW2", "carol")
	if err != nil || !strings.Contains(out, "password updated for carol") {
		t.Fatalf("user passwd: out=%q err=%v", out, err)
	}

	eng, err := newEngine(db, "")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	u, err := eng.Store.GetUser("carol")
	if err != nil || u == nil {
		t.Fatalf("GetUser(carol): %v %v", u, err)
	}
	if u.Workspace != "acme" || !u.Admin {
		t.Errorf("passwd clobbered workspace/admin: %+v", u)
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("new-pw")) != nil {
		t.Error("new password does not verify after passwd")
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("old-pw")) == nil {
		t.Error("old password still verifies after passwd")
	}

	if _, err := runCmd(t, userPasswdCmd(), "--db", db, "--password-env", "LADYM_TEST_PW2", "nobody"); err == nil ||
		!strings.Contains(err.Error(), "no such user nobody") {
		t.Errorf("passwd missing user: err=%v, want ConfigError 'no such user'", err)
	}
}

// A password beyond bcrypt's 72-byte limit makes hashing fail; the command
// surfaces the error instead of writing a partial user row.
func TestUserAddOverlongPassword(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	t.Setenv("LADYM_TEST_PW_LONG", strings.Repeat("a", 100))

	if _, err := runCmd(t, userAddCmd(), "--db", db, "--password-env", "LADYM_TEST_PW_LONG", "carol"); err == nil {
		t.Fatal("user add with >72-byte password should fail (bcrypt limit)")
	}

	eng, err := newEngine(db, "")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if u, err := eng.Store.GetUser("carol"); err != nil || u != nil {
		t.Errorf("failed add must not persist a user: %v, %v", u, err)
	}
}

// A --db that cannot be opened (a directory) fails every user command at
// engine construction.
func TestUserCmdsEngineError(t *testing.T) {
	isolateEnv(t)
	setGlobalConfigPath(t, "")
	t.Setenv("LADYM_TEST_PW", "pw")
	dir := t.TempDir()

	if _, err := runCmd(t, userAddCmd(), "--db", dir, "--password-env", "LADYM_TEST_PW", "carol"); err == nil {
		t.Error("user add with db=directory should fail")
	}
	if _, err := runCmd(t, userListCmd(), "--db", dir); err == nil {
		t.Error("user list with db=directory should fail")
	}
	if _, err := runCmd(t, userDeleteCmd(), "--db", dir, "carol"); err == nil {
		t.Error("user delete with db=directory should fail")
	}
	if _, err := runCmd(t, userPasswdCmd(), "--db", dir, "--password-env", "LADYM_TEST_PW", "carol"); err == nil {
		t.Error("user passwd with db=directory should fail")
	}
}
