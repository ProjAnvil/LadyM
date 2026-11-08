//go:build !enterprise

package storage

import "errors"

func migrationSQLiteExec(s Store, query string, args ...any) error {
	db, ok := s.(*SQLiteStore)
	if !ok {
		return errors.New("not a SQLite store")
	}
	_, err := db.db.Exec(query, args...)
	return err
}
