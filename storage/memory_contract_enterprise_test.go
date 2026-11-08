//go:build enterprise

package storage

import (
	"os"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func runStoreBackends(t *testing.T, fn func(*testing.T, Store)) {
	t.Helper()
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("LADYM_TEST_PG_DSN")
		if dsn == "" {
			t.Skip("LADYM_TEST_PG_DSN not set")
		}
		s, err := NewPostgresStore(freshPGDatabase(t, dsn), suiteDim)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		fn(t, s)
	})
}

func suitePutMemory(t *testing.T, s Store, m *schema.Memory, vector []float32) {
	t.Helper()
	if err := s.PutMemory(m, vector); err != nil {
		t.Fatal(err)
	}
}

func putLegacyForeignReference(t *testing.T, s Store, m *schema.Memory, source string) {
	t.Helper()
	t.Fatal("SQLite is not available in enterprise builds")
}

func beginSQLiteContractTransaction(t *testing.T, s Store) memoryTransaction {
	t.Helper()
	t.Fatal("SQLite is not available in enterprise builds")
	return nil
}

func installSQLiteContractFault(t *testing.T, s Store) {
	t.Helper()
	t.Fatal("SQLite is not available in enterprise builds")
}
