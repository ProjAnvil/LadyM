//go:build enterprise

package storage

// Enterprise-edition OpenStore semantics: the binary carries no SQLite
// backend, so the sqlite paths (including the empty default) must fail fast
// with an actionable config error instead of failing deep inside a missing
// driver. Mirrors storage/backend_test.go for the personal edition.

import (
	"os"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
)

func TestOpenStoreEnterpriseDefaultFailsFast(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	_, err := OpenStore(cfg, 8)
	if err == nil {
		t.Fatal("expected error for default (sqlite) backend in enterprise build")
	}
	msg := err.Error()
	if !strings.Contains(msg, "postgres") || !strings.Contains(msg, "store.dsn") {
		t.Errorf("error not actionable: %q", msg)
	}
}

func TestOpenStoreEnterpriseExplicitSQLiteFailsFast(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.StoreBackend = "sqlite"
	if _, err := OpenStore(cfg, 8); err == nil {
		t.Fatal("expected error for explicit sqlite backend in enterprise build")
	}
}

func TestOpenStoreEnterprisePostgresWithoutDSN(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.StoreBackend = "postgres"
	cfg.StoreDSN = ""
	_, err := OpenStore(cfg, 8)
	if err == nil {
		t.Fatal("expected error for postgres backend without DSN")
	}
	msg := err.Error()
	if !strings.Contains(msg, "store.dsn") || !strings.Contains(msg, "LADYM_STORE_DSN") {
		t.Errorf("error not actionable: %q", msg)
	}
}

func TestOpenStoreEnterpriseUnknownBackend(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.StoreBackend = "couchdb"
	_, err := OpenStore(cfg, 8)
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
	msg := err.Error()
	if !strings.Contains(msg, "couchdb") || !strings.Contains(msg, "postgres") {
		t.Errorf("error should name the bad value and the only valid backend: %q", msg)
	}
}

func TestOpenStoreEnterprisePostgres(t *testing.T) {
	dsn := os.Getenv("LADYM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LADYM_TEST_PG_DSN not set")
	}
	cfg := config.ForTesting(t.TempDir())
	cfg.StoreBackend = "postgres"
	cfg.StoreDSN = freshPGDatabase(t, dsn)
	st, err := OpenStore(cfg, suiteDim)
	if err != nil {
		t.Fatalf("OpenStore(postgres): %v", err)
	}
	defer st.Close()
	if _, ok := st.(*PostgresStore); !ok {
		t.Errorf("OpenStore(postgres) = %T, want *PostgresStore", st)
	}
	if err := st.SetMeta("k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, err := st.GetMeta("k"); err != nil || v != "v" {
		t.Errorf("GetMeta = %q, %v", v, err)
	}
}
