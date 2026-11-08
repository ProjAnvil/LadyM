package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ContextEmbeddingProvider interface {
	EmbedContext(context.Context, string) ([]float32, error)
	EmbedBatchContext(context.Context, []string) ([][]float32, error)
}

var ErrEmbeddingContextUnsupported = errors.New("embedding provider does not support context cancellation")
var ErrMaintenanceLease = fmt.Errorf("%w: maintenance lease is no longer owned or live", ErrMemoryConflict)

func EmbedContext(ctx context.Context, provider EmbeddingProvider, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p, ok := provider.(ContextEmbeddingProvider); ok {
		return p.EmbedContext(ctx, text)
	}
	return nil, ErrEmbeddingContextUnsupported
}

func (s *PostgresStore) WithContext(ctx context.Context) Store {
	if ctx == nil {
		ctx = context.Background()
	}
	view := *s
	view.pool = &contextPool{Pool: s.pool.Pool, ctx: ctx}
	binding := *s.embeddingMigrations
	binding.begin = func() (migrationTransaction, error) { return view.beginMemoryTransaction(MemoryBatch{}) }
	view.embeddingMigrations = &binding
	return &view
}

type contextPool struct {
	*pgxpool.Pool
	ctx context.Context
}

func (p *contextPool) Close() {
	if p.ctx == nil {
		p.Pool.Close()
	}
}

func (p *contextPool) context(ctx context.Context) context.Context {
	if p.ctx != nil {
		return p.ctx
	}
	return ctx
}
func (p *contextPool) Exec(ctx context.Context, q string, args ...any) (pgconn.CommandTag, error) {
	return p.Pool.Exec(p.context(ctx), q, args...)
}
func (p *contextPool) Query(ctx context.Context, q string, args ...any) (pgx.Rows, error) {
	return p.Pool.Query(p.context(ctx), q, args...)
}
func (p *contextPool) QueryRow(ctx context.Context, q string, args ...any) pgx.Row {
	return p.Pool.QueryRow(p.context(ctx), q, args...)
}
func (p *contextPool) Ping(ctx context.Context) error { return p.Pool.Ping(p.context(ctx)) }
func (p *contextPool) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	return p.Pool.Acquire(p.context(ctx))
}
func (p *contextPool) Begin(ctx context.Context) (pgx.Tx, error) {
	ctx = p.context(ctx)
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &contextPGTx{Tx: tx, ctx: ctx}, nil
}

type contextPGTx struct {
	pgx.Tx
	ctx context.Context
}

func (t *contextPGTx) Exec(_ context.Context, q string, args ...any) (pgconn.CommandTag, error) {
	return t.Tx.Exec(t.ctx, q, args...)
}
func (t *contextPGTx) Query(_ context.Context, q string, args ...any) (pgx.Rows, error) {
	return t.Tx.Query(t.ctx, q, args...)
}
func (t *contextPGTx) QueryRow(_ context.Context, q string, args ...any) pgx.Row {
	return t.Tx.QueryRow(t.ctx, q, args...)
}
func (t *contextPGTx) Commit(_ context.Context) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}

	return t.Tx.Commit(t.ctx)
}

func (t *contextPGTx) Rollback(_ context.Context) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return t.Tx.Rollback(ctx)
}

func maintenanceFence(tx memoryTransaction, fence *MaintenanceFence) error {
	if fence == nil {
		return nil
	}
	if err := fence.Context.Err(); err != nil {
		return err
	}
	jobMemory, err := tx.get(fence.JobID)
	if err != nil {
		return err
	}
	job, err := schema.MaintenanceJobFromMemory(jobMemory)
	now, clockErr := tx.now()
	if clockErr != nil {
		return clockErr
	}
	if err != nil || job.State != "running" || job.Owner != fence.Owner || job.Revision != fence.Revision || job.LeaseUntil != fence.LeaseUntil || job.LeaseUntil <= now {
		return ErrMaintenanceLease
	}
	for _, id := range job.InputIDs {
		input, err := tx.get(id)
		if err != nil {
			return err
		}
		if input == nil || input.Workspace != job.Workspace {
			return ErrMemoryConflict
		}
		fp, err := schema.MaintenanceInputFingerprint(input)
		if err != nil {
			return err
		}
		if fp != job.Fingerprints[id] || schema.ManagedRevision(input) != job.InputRevisions[id] {
			return ErrMemoryConflict
		}
		if id != job.TargetID || job.Operation == "new_episode" {
			fresh, err := freshMemory(input, now, tx.get, func(id string) ([]string, error) {
				edges, err := tx.edges("")
				return outgoingSources(id, edges), err
			}, map[string]bool{})
			if err != nil {
				return err
			}
			if !fresh {
				return ErrMemoryConflict
			}
		}
	}
	state, err := migrationRead(tx.(migrationTransaction))
	if err != nil {
		return err
	}
	if state.Generation != fence.Generation || state.ActiveFingerprint != fence.Fingerprint {
		return ErrEmbeddingMigration
	}
	return nil
}
