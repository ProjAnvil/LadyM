//go:build !enterprise

package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
)

// OpenStore with the default config must keep the Task-1 behaviour: a SQLite
// store at cfg.DBPath.
func TestOpenStoreDefaultIsSQLite(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.DBPath = filepath.Join(t.TempDir(), "openstore.db")
	st, err := OpenStore(cfg, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, ok := st.(*SQLiteStore); !ok {
		t.Errorf("OpenStore(default) = %T, want *SQLiteStore", st)
	}
	if err := st.SetMeta("k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, err := st.GetMeta("k"); err != nil || v != "v" {
		t.Errorf("GetMeta = %q, %v", v, err)
	}
}

func TestOpenStoreExplicitSQLite(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.StoreBackend = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "explicit.db")
	st, err := OpenStore(cfg, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, ok := st.(*SQLiteStore); !ok {
		t.Errorf("OpenStore(sqlite) = %T, want *SQLiteStore", st)
	}
}

func TestOpenStorePostgresWithoutDSN(t *testing.T) {
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

// TestOpenStorePostgresWithDSN: with a reachable server the postgres
// backend returns a live PostgresStore (gated on LADYM_TEST_PG_DSN).
func TestOpenStorePostgresWithDSN(t *testing.T) {
	dsn := os.Getenv("LADYM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LADYM_TEST_PG_DSN not set")
	}
	cfg := config.ForTesting(t.TempDir())
	cfg.StoreBackend = "postgres"
	cfg.StoreDSN = freshPGDatabase(t, dsn)
	st, err := OpenStore(cfg, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, ok := st.(*PostgresStore); !ok {
		t.Errorf("OpenStore(postgres) = %T, want *PostgresStore", st)
	}
}

func TestOpenStoreUnknownBackend(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.StoreBackend = "couchdb"
	_, err := OpenStore(cfg, 8)
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
	msg := err.Error()
	if !strings.Contains(msg, "couchdb") || !strings.Contains(msg, "sqlite") || !strings.Contains(msg, "postgres") {
		t.Errorf("error should name the bad value and valid backends: %q", msg)
	}
}
