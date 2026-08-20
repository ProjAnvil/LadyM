//go:build enterprise

package api_test

// Enterprise edition: the api node does NOT embed the management console —
// "/" and other non-/api paths answer a JSON 404 pointing at the standalone
// `ladymconsole` binary; unknown /api paths keep the exact JSON 404 shape and
// message of the personal edition; the /api/* data plane is unchanged. The
// engine runs live against LADYM_TEST_PG_DSN (skipped without it), mirroring
// api/console_test.go for the personal edition.

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/api"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/jackc/pgx/v5"
)

// enterprisePGHandler builds a NewHandler-backed handler against a fresh
// per-test PG database (the enterprise build has no sqlite). freshPGDSN is
// duplicated from the storage/operations PG helpers — Go test helpers do not
// cross packages.
func enterprisePGHandler(t *testing.T) http.Handler {
	t.Helper()
	dsn := os.Getenv("LADYM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LADYM_TEST_PG_DSN not set")
	}
	cfg := config.ForTesting(t.TempDir())
	cfg.StoreBackend = "postgres"
	cfg.StoreDSN = freshPGDSN(t, dsn)
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New(postgres): %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	return api.NewHandler(eng, cfg)
}

// freshPGDSN creates a random per-test database on the server named by dsn and
// returns a DSN pointing at it (dropped on cleanup).
func freshPGDSN(t *testing.T, dsn string) string {
	t.Helper()
	pcfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse LADYM_TEST_PG_DSN: %v", err)
	}
	var suffix [8]byte
	if _, err := crand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	dbName := "ladym_test_" + hex.EncodeToString(suffix[:])
	adminCfg := pcfg.Copy()
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
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(pcfg.User, pcfg.Password),
		Host:   net.JoinHostPort(pcfg.Host, strconv.Itoa(int(pcfg.Port))),
		Path:   "/" + dbName,
	}
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res.StatusCode, string(body)
}

func TestConsoleNotEmbeddedEnterprise(t *testing.T) {
	srv := httptest.NewServer(enterprisePGHandler(t))
	defer srv.Close()

	// "/" is a JSON 404 pointing at the standalone console binary.
	code, body := get(t, srv.URL+"/")
	if code != http.StatusNotFound {
		t.Errorf("GET / status = %d, want 404", code)
	}
	if !strings.Contains(body, `"error"`) || !strings.Contains(body, "ladymconsole") {
		t.Errorf("GET / body = %q, want JSON error pointing at `ladymconsole`", body)
	}

	// A client-side route gets the same treatment (no SPA fallback).
	code, body = get(t, srv.URL+"/users")
	if code != http.StatusNotFound || !strings.Contains(body, "ladymconsole") {
		t.Errorf("GET /users = %d %q, want 404 JSON console-binary hint", code, body)
	}

	// Unknown /api paths keep the personal-style JSON 404 (same shape, same
	// message) — the /api/* behavior must not drift between editions.
	code, body = get(t, srv.URL+"/api/nope")
	if code != http.StatusNotFound || !strings.Contains(body, "not found: /api/nope") {
		t.Errorf("GET /api/nope = %d %q, want 404 `not found: /api/nope`", code, body)
	}

	// The data plane itself is unchanged.
	res, err := http.Post(srv.URL+"/api/stats", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/stats: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("POST /api/stats status = %d, want 200", res.StatusCode)
	}
}
