//go:build enterprise

package cli

// Enterprise edition: the console role is the standalone ladymconsole binary
// (cmd/ladymconsole), built from NewConsoleCmd — it is NOT registered on the
// ladym root command. The construction test mirrors TestBuildServeHTTPHandler
// (cli_test.go, personal) and runs live against LADYM_TEST_PG_DSN (skipped
// without it). The console.Mount injection happens here (and in
// cmd/ladymconsole), never in the cli package itself.
//
// Note: this test file imports the console package, but test-only imports do
// not enter the ladym binary's dependency graph (go list -deps without -tests
// ignores them); the dependency-direction acceptance lives in
// consoledeps_test.go.

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

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/console"
	"github.com/jackc/pgx/v5"
)

func TestNewConsoleCmd(t *testing.T) {
	cmd := NewConsoleCmd(console.Mount)
	if cmd.Flags().Lookup("http") == nil {
		t.Error("console command missing --http flag")
	}
	if cmd.PersistentFlags().Lookup("config") == nil {
		t.Error("console command missing --config flag")
	}
	// The ladym root command must NOT carry the console role (separate binary).
	for _, c := range newRootCmd().Commands() {
		if c.Name() == "console" {
			t.Error("ladym root must not register a console command (console is the ladymconsole binary)")
		}
	}
}

// buildConsoleHTTPHandler wires api.NewMux + console.Mount: the SPA is served
// at "/" and the full /api data plane answers, against the same PG deployment.
func TestBuildConsoleHTTPHandler(t *testing.T) {
	dsn := os.Getenv("LADYM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LADYM_TEST_PG_DSN not set")
	}
	t.Setenv("HOME", t.TempDir())
	cfg := config.ForTesting(t.TempDir())
	cfg.StoreBackend = "postgres"
	cfg.StoreDSN = consoleTestPGDSN(t, dsn)

	h, _, closeFn, err := buildConsoleHTTPHandler(cfg, console.Mount)
	if err != nil {
		t.Fatalf("buildConsoleHTTPHandler: %v", err)
	}
	defer closeFn()
	srv := httptest.NewServer(h)
	defer srv.Close()

	// GET / serves the console index.html.
	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `<div id="app">`) {
		t.Errorf("GET / = %d, want 200 + console index.html", res.StatusCode)
	}

	// A client-side route falls back to the SPA.
	res, err = http.Get(srv.URL + "/users")
	if err != nil {
		t.Fatalf("GET /users: %v", err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `<div id="app">`) {
		t.Errorf("GET /users = %d, want 200 + index.html (SPA fallback)", res.StatusCode)
	}

	// The data plane answers.
	res, err = http.Post(srv.URL+"/api/stats", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/stats: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("POST /api/stats = %d, want 200", res.StatusCode)
	}

	// Unknown /api paths get a JSON 404, not the SPA fallback.
	res, err = http.Get(srv.URL + "/api/nope")
	if err != nil {
		t.Fatalf("GET /api/nope: %v", err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound || !strings.Contains(string(body), `"error"`) {
		t.Errorf("GET /api/nope = %d %q, want 404 JSON error", res.StatusCode, body)
	}
}

// consoleTestPGDSN creates a random per-test database on the server named by
// dsn and returns a DSN pointing at it (dropped on cleanup). Duplicated from
// the storage/operations PG helpers — Go test helpers do not cross packages.
func consoleTestPGDSN(t *testing.T, dsn string) string {
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
