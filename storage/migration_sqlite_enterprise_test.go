//go:build enterprise

package storage

import "errors"

func migrationSQLiteExec(Store, string, ...any) error {
	return errors.New("SQLite is not available in enterprise")
}
