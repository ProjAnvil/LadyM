//go:build !enterprise

package storage

// Edition marks the build flavour of this binary ("personal" here). The CLI
// surfaces it via --version; the enterprise variant lives in
// edition_enterprise.go behind the enterprise build tag.
const Edition = "personal"
