//go:build enterprise

package storage

// Edition marks the build flavour of this binary ("enterprise" here —
// compiled with -tags enterprise, without the SQLite backend). The CLI
// surfaces it via --version; the personal variant lives in edition.go.
const Edition = "enterprise"
