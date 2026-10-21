// Package eval measures the LadyM hook write path. It answers two questions:
// do harness events actually persist with the right fields (fidelity), and what
// does each write cost in tokens and duration (performance).
//
// The harness is read-only with respect to product code: it spawns the real
// `ladym hook` binary and reads the resulting SQLite file directly, never
// modifying either. Everything here is behind the `eval` build tag so the
// heavy fixtures and multi-scale runs stay out of the default `make test`.
package eval
