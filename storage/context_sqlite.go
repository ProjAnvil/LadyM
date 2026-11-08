//go:build !enterprise

package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type contextDB struct {
	*sql.DB
	ctx context.Context
}

func sqliteContextDSN(path string) string {
	if path == ":memory:" {
		path = "file::memory:"
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "_pragma=foreign_keys(1)&_pragma=busy_timeout(" + fmt.Sprint(busyTimeoutMS) + ")"
}

func (d *contextDB) context() context.Context {
	if d.ctx != nil {
		return d.ctx
	}
	return context.Background()
}
func (d *contextDB) Close() error {
	if d.ctx != nil {
		return nil
	}
	return d.DB.Close()
}

func (d *contextDB) Exec(q string, args ...any) (sql.Result, error) {
	if d.ctx == nil {
		return d.DB.Exec(q, args...)
	}
	conn, cleanup, err := d.connection()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	for {
		result, err := conn.ExecContext(d.ctx, q, args...)
		if !sqliteBusy(err) {
			return result, err
		}
		if err := waitSQLite(d.ctx); err != nil {
			return nil, err
		}
	}
}
func (d *contextDB) Query(q string, args ...any) (*contextRows, error) {
	if d.ctx == nil {
		rows, err := d.DB.Query(q, args...)
		return &contextRows{Rows: rows, cleanup: func() {}}, err
	}
	conn, cleanup, err := d.connection()
	if err != nil {
		return nil, err
	}
	for {
		rows, err := conn.QueryContext(d.ctx, q, args...)
		if err == nil {
			return &contextRows{Rows: rows, cleanup: cleanup}, nil
		}
		if !sqliteBusy(err) {
			cleanup()
			return nil, err
		}
		if err := waitSQLite(d.ctx); err != nil {
			cleanup()
			return nil, err
		}
	}
}
func (d *contextDB) QueryRow(q string, args ...any) *contextRow {
	return &contextRow{query: func() (*contextRows, error) { return d.Query(q, args...) }}
}
func (d *contextDB) Begin() (*contextSQLTx, error) { return d.BeginTx(d.context(), nil) }
func (d *contextDB) BeginTx(ctx context.Context, options *sql.TxOptions) (*contextSQLTx, error) {
	if d.ctx != nil {
		ctx = d.ctx
		conn, cleanup, err := d.connection()
		if err != nil {
			return nil, err
		}
		tx, err := conn.BeginTx(ctx, options)
		if err != nil {
			cleanup()
			return nil, err
		}
		return &contextSQLTx{Tx: tx, ctx: ctx, cleanup: cleanup, retryBusy: true}, nil
	}
	tx, err := d.DB.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &contextSQLTx{Tx: tx, ctx: ctx, cleanup: func() {}}, nil
}

type contextSQLTx struct {
	*sql.Tx
	ctx       context.Context
	cleanup   func()
	retryBusy bool
}

func (t *contextSQLTx) Exec(q string, args ...any) (sql.Result, error) {
	for {
		result, err := t.Tx.ExecContext(t.ctx, q, args...)
		if !t.retryBusy || !sqliteBusy(err) {
			return result, err
		}
		if err := waitSQLite(t.ctx); err != nil {
			return nil, err
		}
	}
}
func (t *contextSQLTx) Query(q string, args ...any) (*sql.Rows, error) {
	return t.Tx.QueryContext(t.ctx, q, args...)
}
func (t *contextSQLTx) QueryRow(q string, args ...any) *sql.Row {
	return t.Tx.QueryRowContext(t.ctx, q, args...)
}
func (t *contextSQLTx) Commit() error {
	defer t.cleanup()
	if err := t.ctx.Err(); err != nil {
		_ = t.Tx.Rollback()
		return err
	}
	return t.Tx.Commit()
}

func (t *contextSQLTx) Rollback() error {
	defer t.cleanup()
	return t.Tx.Rollback()
}

type contextRows struct {
	*sql.Rows
	cleanup func()
}

func (r *contextRows) Close() error { defer r.cleanup(); return r.Rows.Close() }

type contextRow struct{ query func() (*contextRows, error) }

func (r *contextRow) Scan(dest ...any) error {
	rows, err := r.query()
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	return rows.Scan(dest...)
}

func sqliteBusy(err error) bool {
	code, ok := errors.AsType[interface {
		error
		Code() int
	}](err)
	return ok && (code.Code()&255 == 5 || code.Code()&255 == 6)
}
func waitSQLite(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Millisecond):
		return nil
	}
}
func (d *contextDB) connection() (*sql.Conn, func(), error) {
	conn, err := d.DB.Conn(d.ctx)
	if err != nil {
		return nil, nil, err
	}
	var oldTimeout int
	if err := conn.QueryRowContext(d.ctx, "PRAGMA busy_timeout").Scan(&oldTimeout); err != nil {
		conn.Close()
		return nil, nil, err
	}
	if _, err := conn.ExecContext(d.ctx, "PRAGMA busy_timeout=5"); err != nil {
		conn.Close()
		return nil, nil, err
	}
	cleanup := sync.OnceFunc(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = conn.ExecContext(ctx, "PRAGMA busy_timeout="+fmt.Sprint(oldTimeout))
		_ = conn.Close()
	})
	return conn, cleanup, nil
}

func (s *SQLiteStore) WithContext(ctx context.Context) Store {
	if ctx == nil {
		ctx = context.Background()
	}
	view := *s
	view.db = &contextDB{DB: s.db.DB, ctx: ctx}
	binding := *s.embeddingMigrations
	binding.begin = func() (migrationTransaction, error) { return view.beginMemoryTransaction() }
	view.embeddingMigrations = &binding
	return &view
}
