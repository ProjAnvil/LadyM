# LadyM — dual-edition build targets.
# Personal edition: default `go build` (SQLite + Postgres backends).
# Enterprise edition: `go build -tags enterprise` (Postgres only; the binary
# carries no modernc.org/sqlite, enforced by verify-enterprise).

LADYM_TEST_PG_DSN ?= postgres://postgres:ladym@127.0.0.1:55432/ladym?sslmode=disable

.PHONY: build-personal build-enterprise verify-enterprise test test-pg

build-personal:
	go build -o bin/ladym ./cmd/ladym

build-enterprise:
	go build -tags enterprise -o bin/ladym-enterprise ./cmd/ladym

verify-enterprise: build-enterprise
	@! go version -m bin/ladym-enterprise | grep -q modernc.org/sqlite \
		|| { echo "FAIL: enterprise binary contains modernc.org/sqlite"; exit 1; }
	@go version -m bin/ladym-enterprise | grep -q pgx \
		|| { echo "FAIL: enterprise binary is missing pgx"; exit 1; }
	@echo "OK: enterprise binary has pgx and no modernc.org/sqlite"

test:
	go test ./...

test-pg:
	LADYM_TEST_PG_DSN='$(LADYM_TEST_PG_DSN)' go test ./storage/ ./operations/ ./engine/
