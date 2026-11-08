//go:build !enterprise

package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/schema"
)

func TestMaintenanceSQLiteCrossHandleCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "context.db")
	a, err := NewStore(path, 8, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := NewStore(path, 8, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	release := maintenanceHoldWriter(t, a)
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = b.WithContext(ctx).PutMemory(schema.NewMemory(schema.LayerEpisodic, schema.TypeMaintenanceJob), nil)
	release()
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("cross-handle writer ignored deadline: %v (%s)", err, time.Since(start))
	}
	var foreignKeys, timeout int
	if err := b.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("replacement connection lost foreign-key protection: %v (%d)", err, foreignKeys)
	}
	if err := b.db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != busyTimeoutMS {
		t.Fatalf("context changed legacy wait timeout: %v (%d)", err, timeout)
	}
}
