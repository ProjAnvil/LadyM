package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// PGDatabase isolates each case from the shared database and other tests.
func PGDatabase(t *testing.T, dsn string) string {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test configuration")
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	name := "ladym_test_" + hex.EncodeToString(suffix[:])
	adminCfg := cfg.Copy()
	adminCfg.Database = "postgres"
	admin, err := pgx.ConnectConfig(t.Context(), adminCfg)
	if err != nil {
		t.Fatal("cannot connect to PostgreSQL test server")
	}
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+name); err != nil {
		admin.Close(t.Context())
		t.Fatalf("create isolated database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
		if err := admin.Close(ctx); err != nil {
			t.Errorf("close test admin: %v", err)
		}
	})
	u := &url.URL{Scheme: "postgres", User: url.UserPassword(cfg.User, cfg.Password),
		Host: net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), Path: "/" + name}
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}
