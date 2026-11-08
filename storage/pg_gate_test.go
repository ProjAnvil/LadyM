package storage

import (
	"testing"

	"github.com/ProjAnvil/LadyM/internal/testutil"
)

const suiteDim = 8

func freshPGDatabase(t *testing.T, dsn string) string {
	t.Helper()
	return testutil.PGDatabase(t, dsn)
}
