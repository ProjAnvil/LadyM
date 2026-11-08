package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresContextReadCancellation(t *testing.T) {
	for _, operation := range []string{"search", "evidence"} {
		for _, blockedBy := range []string{"migration_lock", "pool_acquire"} {
			t.Run(operation+"/"+blockedBy, func(t *testing.T) {
				s := newPGStoreOrSkip(t, 8)
				var release func()
				if blockedBy == "migration_lock" {
					lock, err := s.pool.Begin(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					if _, err := lock.Exec(t.Context(), "LOCK TABLE memories IN ACCESS EXCLUSIVE MODE"); err != nil {
						t.Fatal(err)
					}
					release = func() {
						if err := lock.Rollback(t.Context()); err != nil {
							t.Error(err)
						}
					}
				} else {
					var connections []*pgxpool.Conn
					for range s.pool.Config().MaxConns {
						conn, err := s.pool.Acquire(t.Context())
						if err != nil {
							t.Fatal(err)
						}
						connections = append(connections, conn)
					}
					release = func() {
						for _, conn := range connections {
							conn.Release()
						}
					}
				}
				released := false
				t.Cleanup(func() {
					if !released {
						release()
					}
				})
				ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
				defer cancel()
				view := s.WithContext(ctx)
				result := make(chan error, 1)
				start := time.Now()
				go func() {
					if operation == "search" {
						_, err := view.SearchMemories([]float32{1, 0, 0, 0, 0, 0, 0, 0}, 1, MemoryFilter{})
						result <- err
					} else {
						_, err := view.EvidenceReferences("")
						result <- err
					}
				}()
				select {
				case err := <-result:
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Errorf("read did not inherit deadline: %v", err)
					}
				case <-time.After(500 * time.Millisecond):
					t.Error("read exceeded cancellation bound while still blocked")
					release()
					released = true
					select {
					case <-result:
					case <-time.After(2 * time.Second):
						t.Fatal("read did not finish after releasing blocker")
					}
				}
				elapsed := time.Since(start)
				if !released {
					release()
					released = true
				}
				if elapsed > 500*time.Millisecond {
					t.Errorf("40ms read deadline returned after %s", elapsed)
				}
				if err := s.Ping(); err != nil {
					t.Fatalf("context view poisoned root pool: %v", err)
				}
				if _, err := s.SearchMemories([]float32{1, 0, 0, 0, 0, 0, 0, 0}, 1, MemoryFilter{}); err != nil {
					t.Fatalf("root search failed: %v", err)
				}
				if _, err := s.EvidenceReferences(""); err != nil {
					t.Fatalf("root evidence read failed: %v", err)
				}
				if err := s.PutMemory(pgMem("after-cancellation"), nil); err != nil {
					t.Fatalf("root write failed: %v", err)
				}
			})
		}
	}
}

func TestPostgresContextPoolVariants(t *testing.T) {
	s := newPGStoreOrSkip(t, 8)
	if _, err := s.pool.Exec(t.Context(), "CREATE TABLE context_probe (id integer)"); err != nil {
		t.Fatal(err)
	}
	operations := map[string]func(*contextPool) error{
		"exec": func(p *contextPool) error {
			_, err := p.Exec(context.Background(), "SELECT 1")
			return err
		},
		"query": func(p *contextPool) error {
			rows, err := p.Query(context.Background(), "SELECT 1")
			if rows != nil {
				rows.Close()
			}
			return err
		},
		"query_row": func(p *contextPool) error {
			var n int
			return p.QueryRow(context.Background(), "SELECT 1").Scan(&n)
		},
		"ping": func(p *contextPool) error { return p.Ping(context.Background()) },
		"acquire": func(p *contextPool) error {
			conn, err := p.Acquire(context.Background())
			if conn != nil {
				conn.Release()
			}
			return err
		},
		"acquire_func": func(p *contextPool) error {
			return p.AcquireFunc(context.Background(), func(*pgxpool.Conn) error { return nil })
		},
		"begin": func(p *contextPool) error {
			tx, err := p.Begin(context.Background())
			if tx != nil {
				defer tx.Rollback(context.Background())
			}
			return err
		},
		"begin_tx": func(p *contextPool) error {
			tx, err := p.BeginTx(context.Background(), pgx.TxOptions{AccessMode: pgx.ReadOnly})
			if tx != nil {
				defer tx.Rollback(context.Background())
			}
			return err
		},
		"copy_from": func(p *contextPool) error {
			_, err := p.CopyFrom(context.Background(), pgx.Identifier{"context_probe"}, []string{"id"}, pgx.CopyFromRows([][]any{{1}}))
			return err
		},
		"send_batch": func(p *contextPool) error {
			batch := &pgx.Batch{}
			batch.Queue("SELECT 1")
			return p.SendBatch(context.Background(), batch).Close()
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			view := s.WithContext(ctx).(*PostgresStore)
			cancel()
			if err := operation(view.pool); !errors.Is(err, context.Canceled) {
				t.Errorf("pool operation lost caller context: %v", err)
			}
		})
	}
}

func TestPostgresContextTransactionVariants(t *testing.T) {
	s := newPGStoreOrSkip(t, 8)
	if _, err := s.pool.Exec(t.Context(), "CREATE TABLE context_probe (id integer)"); err != nil {
		t.Fatal(err)
	}
	operations := map[string]func(pgx.Tx) error{
		"exec": func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), "SELECT 1")
			return err
		},
		"query": func(tx pgx.Tx) error {
			rows, err := tx.Query(context.Background(), "SELECT 1")
			if rows != nil {
				rows.Close()
			}
			return err
		},
		"query_row": func(tx pgx.Tx) error {
			var n int
			return tx.QueryRow(context.Background(), "SELECT 1").Scan(&n)
		},
		"begin": func(tx pgx.Tx) error {
			nested, err := tx.Begin(context.Background())
			if nested != nil {
				defer nested.Rollback(context.Background())
			}
			return err
		},
		"copy_from": func(tx pgx.Tx) error {
			_, err := tx.CopyFrom(context.Background(), pgx.Identifier{"context_probe"}, []string{"id"}, pgx.CopyFromRows([][]any{{1}}))
			return err
		},
		"send_batch": func(tx pgx.Tx) error {
			batch := &pgx.Batch{}
			batch.Queue("SELECT 1")
			return tx.SendBatch(context.Background(), batch).Close()
		},
		"prepare": func(tx pgx.Tx) error {
			_, err := tx.Prepare(context.Background(), "context_probe_query", "SELECT 1")
			return err
		},
		"commit": func(tx pgx.Tx) error { return tx.Commit(context.Background()) },
	}
	for _, begin := range []string{"begin", "begin_tx", "nested"} {
		for name, operation := range operations {
			t.Run(begin+"/"+name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				view := s.WithContext(ctx).(*PostgresStore)
				var tx pgx.Tx
				var err error
				if begin == "begin" {
					tx, err = view.pool.Begin(context.Background())
				} else {
					tx, err = view.pool.BeginTx(context.Background(), pgx.TxOptions{})
				}
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(context.Background())
				if begin == "nested" {
					tx, err = tx.Begin(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(context.Background())
				}
				cancel()
				if err := operation(tx); !errors.Is(err, context.Canceled) {
					t.Errorf("transaction operation lost caller context: %v", err)
				}
			})
		}
	}
	t.Run("root_options", func(t *testing.T) {
		tx, err := s.pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(t.Context())
		var isolation, readonly string
		if err := tx.QueryRow(t.Context(), "SHOW transaction_isolation").Scan(&isolation); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(t.Context(), "SHOW transaction_read_only").Scan(&readonly); err != nil {
			t.Fatal(err)
		}
		if isolation != "repeatable read" || readonly != "on" {
			t.Fatalf("transaction options lost: %q, %q", isolation, readonly)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		m := schema.NewMemory(schema.LayerWorking, schema.TypeNote)
		if err := s.PutMemory(m, nil); err != nil {
			t.Fatal(err)
		}
	})
}
