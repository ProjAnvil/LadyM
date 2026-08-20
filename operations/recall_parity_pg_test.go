//go:build !enterprise

package operations

// Cross-backend recall parity: the same deterministic corpus (hashing
// embeddings, fixed ids/timestamps) must yield the identical recall result
// sequence from a SQLite store and a Postgres store. Gated on
// LADYM_TEST_PG_DSN; skips cleanly without it.

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
	"github.com/jackc/pgx/v5"
)

// recallParityPGDSN creates a random per-test database on the server named by
// dsn and returns a DSN pointing at it (dropped on cleanup). Duplicated from
// the storage suite helper — Go test helpers do not cross packages.
func recallParityPGDSN(t *testing.T, dsn string) string {
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
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))),
		Path:   "/" + dbName,
	}
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}

func TestRecallParityAcrossBackends(t *testing.T) {
	dsn := os.Getenv("LADYM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LADYM_TEST_PG_DSN not set")
	}

	emb := storage.NewHashingEmbedding(256)
	lite, err := storage.NewStore(filepath.Join(t.TempDir(), "parity.db"), emb.Dim(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lite.Close() })
	pg, err := storage.NewPostgresStore(recallParityPGDSN(t, dsn), emb.Dim())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.Close() })

	// Fixed corpus: one memory value shared by both backends (same id,
	// timestamps and vector → identical activation scores).
	contents := []string{
		"auth uses jwt tokens with 24h expiry",
		"jwt refresh tokens rotate on every use",
		"deploy pipeline builds docker images",
		"database migrations run via golang-migrate",
		"config loads from toml files",
		"logging uses structured json output",
		"metrics export via prometheus endpoint",
		"grpc services defined in proto files",
		"frontend built with react and vite",
		"cache layer backed by redis cluster",
		"search powered by vector embeddings",
		"payments handled by stripe webhooks",
	}
	stores := []storage.Store{lite, pg}
	for i, c := range contents {
		vec, err := emb.Embed(c)
		if err != nil {
			t.Fatal(err)
		}
		m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m.ID = fmt.Sprintf("pm-%02d", i)
		m.Content = c
		m.Workspace = "test"
		for _, s := range stores {
			if err := s.PutMemory(m, vec); err != nil {
				t.Fatal(err)
			}
		}
	}

	cfg := config.ForTesting(t.TempDir())
	names := []string{"sqlite", "postgres"}
	var seqs [2][]string
	var tiers [2]int
	for i, s := range stores {
		resp, err := Recall(s, emb, "jwt token expiry", cfg, "test", 5, nil, nil, 0)
		if err != nil {
			t.Fatalf("%s recall: %v", names[i], err)
		}
		if len(resp.Results) == 0 {
			t.Fatalf("%s recall returned no results", names[i])
		}
		tiers[i] = resp.TierReached
		for _, r := range resp.Results {
			seqs[i] = append(seqs[i], r.Memory.ID)
		}
	}
	if tiers[0] != tiers[1] {
		t.Errorf("TierReached differs: sqlite=%d postgres=%d", tiers[0], tiers[1])
	}
	if len(seqs[0]) != len(seqs[1]) {
		t.Fatalf("result counts differ: sqlite=%v postgres=%v", seqs[0], seqs[1])
	}
	for i := range seqs[0] {
		if seqs[0][i] != seqs[1][i] {
			t.Fatalf("recall sequences differ:\nsqlite:   %v\npostgres: %v", seqs[0], seqs[1])
		}
	}
	t.Logf("recall parity ok, top-k = %v (tier %d)", seqs[0], tiers[0])
}
