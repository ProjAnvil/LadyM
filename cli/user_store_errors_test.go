//go:build !enterprise

// Tests for the `ladym user` commands' store failure paths (writes against a
// read-only db, reads against a users table with a wrong schema) and for
// userPasswdCmd's pre-store validation failures.

package cli

import (
	"database/sql"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// seedUser adds one user through the CLI so failure tests have a row to work
// (and fail) against.
func seedUser(t *testing.T, db, username string) {
	t.Helper()
	t.Setenv("LADYM_TEST_SEED_PW", "pw")
	if _, err := runCmd(t, userAddCmd(), "--db", db, "--password-env", "LADYM_TEST_SEED_PW", username); err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
}

// breakUsersTable replaces the users table with a same-named table lacking
// the real columns, so reads fail with "no such column: username" while
// engine construction (CREATE TABLE IF NOT EXISTS is a no-op) still succeeds.
func breakUsersTable(t *testing.T, db string) {
	t.Helper()
	d, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("DROP TABLE users; CREATE TABLE users(x TEXT)"); err != nil {
		t.Fatalf("break users table: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

// user add on a read-only db fails at PutUser.
func TestUserAdd_ReadOnlyDB_Fails(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	seedUser(t, db, "carol")
	makeReadOnly(t, db)
	t.Setenv("LADYM_TEST_PW", "pw")

	if _, err := runCmd(t, userAddCmd(), "--db", db, "--password-env", "LADYM_TEST_PW", "dave"); err == nil ||
		!strings.Contains(err.Error(), "readonly") {
		t.Errorf("user add on read-only db: err = %v, want readonly error", err)
	}
}

// user delete on a read-only db fails at the DELETE (the read of the user
// still works).
func TestUserDelete_ReadOnlyDB_Fails(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	seedUser(t, db, "carol")
	makeReadOnly(t, db)

	if _, err := runCmd(t, userDeleteCmd(), "--db", db, "carol"); err == nil ||
		!strings.Contains(err.Error(), "readonly") {
		t.Errorf("user delete on read-only db: err = %v, want readonly error", err)
	}
}

// user passwd on a read-only db fails at the PutUser write-back.
func TestUserPasswd_ReadOnlyDB_Fails(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	seedUser(t, db, "carol")
	makeReadOnly(t, db)
	t.Setenv("LADYM_TEST_PW", "new-pw")

	if _, err := runCmd(t, userPasswdCmd(), "--db", db, "--password-env", "LADYM_TEST_PW", "carol"); err == nil ||
		!strings.Contains(err.Error(), "readonly") {
		t.Errorf("user passwd on read-only db: err = %v, want readonly error", err)
	}
}

// With the users table replaced by a wrong-schema table, the read paths of
// list/delete/passwd fail with the sqlite column error.
func TestUserCmds_BrokenUsersTable_ReadsFail(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	seedUser(t, db, "carol")
	breakUsersTable(t, db)
	t.Setenv("LADYM_TEST_PW", "pw")

	if _, err := runCmd(t, userListCmd(), "--db", db); err == nil ||
		!strings.Contains(err.Error(), "username") {
		t.Errorf("user list with broken users table: err = %v, want 'no such column: username'", err)
	}
	if _, err := runCmd(t, userDeleteCmd(), "--db", db, "carol"); err == nil ||
		!strings.Contains(err.Error(), "username") {
		t.Errorf("user delete with broken users table: err = %v, want 'no such column: username'", err)
	}
	if _, err := runCmd(t, userPasswdCmd(), "--db", db, "--password-env", "LADYM_TEST_PW", "carol"); err == nil ||
		!strings.Contains(err.Error(), "username") {
		t.Errorf("user passwd with broken users table: err = %v, want 'no such column: username'", err)
	}
}

// user passwd with an unset --password-env variable fails before touching the
// store.
func TestUserPasswd_PasswordEnvUnset_Fails(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")

	if _, err := runCmd(t, userPasswdCmd(), "--db", db, "--password-env", "LADYM_TEST_PW_UNSET", "carol"); err == nil ||
		!strings.Contains(err.Error(), "LADYM_TEST_PW_UNSET is not set") {
		t.Errorf("passwd with unset password-env: err = %v, want ConfigError naming the var", err)
	}
}

// user passwd with a password beyond bcrypt's 72-byte limit fails at hashing;
// the existing row keeps its old hash.
func TestUserPasswd_OverlongPassword_Fails(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	seedUser(t, db, "carol")
	t.Setenv("LADYM_TEST_PW_LONG", strings.Repeat("a", 100))

	if _, err := runCmd(t, userPasswdCmd(), "--db", db, "--password-env", "LADYM_TEST_PW_LONG", "carol"); err == nil {
		t.Error("passwd with >72-byte password should fail (bcrypt limit)")
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
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("pw")) != nil {
		t.Error("failed passwd must leave the old password hash in place")
	}
}
