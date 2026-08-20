package storage

// Postgres test gate shared by both editions: freshPGDatabase hands each test
// a random per-test database on the server named by LADYM_TEST_PG_DSN. It is
// backend-agnostic (pgx only), so it stays compilable in enterprise builds
// where the SQLite-side suite files are excluded by build tag.

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"net"
	"net/url"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"
)

// suiteDim is the vector dim used across the suite (small on purpose).
const suiteDim = 8

// freshPGDatabase creates a random per-test database on the server named by
// dsn and returns a DSN pointing at it. The database is dropped on test
// cleanup, so PG subtests never pollute each other or the shared ladym db.
func freshPGDatabase(t *testing.T, dsn string) string {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse LADYM_TEST_PG_DSN: %v", err)
	}
	var suffix [8]byte
	if _, err := crand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	dbName := "ladym_test_" + hex.EncodeToString(suffix[:])
	adminCfg := cfg.Copy()
	adminCfg.Database = "postgres"
	ctx := context.Background()
	admin, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		t.Fatalf("connect to postgres admin database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		admin.Close(ctx)
		t.Fatalf("create test database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP DATABASE "+dbName+" WITH (FORCE)"); err != nil {
			t.Logf("drop test database %s: %v", dbName, err)
		}
		admin.Close(ctx)
	})
	testCfg := cfg.Copy()
	testCfg.Database = dbName
	// NOTE: ConnConfig.ConnString() returns the *original* DSN string and
	// drops the Database override, so rebuild a URL DSN from the fields.
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(testCfg.User, testCfg.Password),
		Host:   net.JoinHostPort(testCfg.Host, strconv.Itoa(int(testCfg.Port))),
		Path:   "/" + testCfg.Database,
	}
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}
