package testutil

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPGDatabaseCleanupAfterTestCancellation(t *testing.T) {
	dsn := os.Getenv("LADYM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("cleanup proof requires explicit LADYM_TEST_PG_DSN")
	}
	var database string
	t.Run("postgres", func(t *testing.T) {
		cfg, err := pgx.ParseConfig(PGDatabase(t, dsn))
		if err != nil {
			t.Fatal("invalid isolated test configuration")
		}
		database = cfg.Database
		t.Cleanup(func() {
			if t.Context().Err() == nil {
				t.Error("test context must be cancelled before database cleanup")
			}
		})
	})
	conn, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal("cannot connect to PostgreSQL test server")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := conn.Close(ctx); err != nil {
			t.Error("close PostgreSQL cleanup probe")
		}
	})
	var exists bool
	if err := conn.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)", database).Scan(&exists); err != nil {
		t.Fatal("query isolated database cleanup")
	}
	if exists {
		t.Fatal("isolated PostgreSQL database survived cancelled test cleanup")
	}
}
