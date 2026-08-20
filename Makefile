# LadyM — dual-edition build targets.
# Personal edition: default `go build` (SQLite + Postgres backends); the
# management console is embedded in `ladym serve --http`.
# Enterprise edition: `go build -tags enterprise` (Postgres only; the binary
# carries no modernc.org/sqlite, enforced by verify-enterprise). The console is
# a SEPARATE BINARY (bin/ladymconsole-enterprise): ladym (api/worker roles)
# does not embed the Vue console at all.
#
# The console assets (console/dist, committed to the repo — a plain `go build`
# needs no node) are embedded only where they are served: personal ladym, and
# enterprise ladymconsole. Only run `make console-build` when the frontend
# sources under console/ change.

LADYM_TEST_PG_DSN ?= postgres://postgres:ladym@127.0.0.1:55432/ladym?sslmode=disable

.PHONY: build-personal build-enterprise verify-enterprise test test-pg test-enterprise test-all console-build lint-comments

lint-comments:
	./scripts/check-comments.sh

build-personal:
	go build -o bin/ladym ./cmd/ladym

build-enterprise:
	go build -tags enterprise -o bin/ladym-enterprise ./cmd/ladym
	go build -tags enterprise -o bin/ladymconsole-enterprise ./cmd/ladymconsole

console-build:
	cd console && npm ci && npm run build

verify-enterprise: build-enterprise
	@! go version -m bin/ladym-enterprise | grep -q modernc.org/sqlite \
		|| { echo "FAIL: enterprise binary contains modernc.org/sqlite"; exit 1; }
	@go version -m bin/ladym-enterprise | grep -q pgx \
		|| { echo "FAIL: enterprise binary is missing pgx"; exit 1; }
	@! go list -tags enterprise -deps ./cmd/ladym | grep -q 'ProjAnvil/LadyM/console$$' \
		|| { echo "FAIL: enterprise ladym binary depends on the console package (Vue embed)"; exit 1; }
	@go list -tags enterprise -deps ./cmd/ladymconsole | grep -q 'ProjAnvil/LadyM/console$$' \
		|| { echo "FAIL: ladymconsole binary is missing the console package"; exit 1; }
	@echo "OK: enterprise ladym has pgx, no modernc.org/sqlite, no console embed; ladymconsole carries the console"

# test: personal-edition full suite (no PG required; PG-gated cases skip).
test:
	go test ./...

# test-pg: personal-edition full suite with the PG-gated cases live against
# LADYM_TEST_PG_DSN.
test-pg:
	LADYM_TEST_PG_DSN='$(LADYM_TEST_PG_DSN)' go test ./...

# test-enterprise: enterprise-edition suite (-tags enterprise) — sqlite-only
# test files are excluded by build tag; PG-gated cases run live.
test-enterprise:
	LADYM_TEST_PG_DSN='$(LADYM_TEST_PG_DSN)' go test -tags enterprise ./...

# test-all: dual-edition full regression — personal (with PG) + enterprise
# (with PG) + the comment linter.
test-all: test-pg test-enterprise lint-comments

# package-*: release tarballs under dist/ (binary + README + deployment doc).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)

package-personal: build-personal
	mkdir -p dist
	tar -czf dist/ladym-personal-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz \
		-C bin ladym -C .. README.md docs/deployment.md
	@echo "dist/ladym-personal-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz"

package-enterprise: verify-enterprise
	mkdir -p dist
	tar -czf dist/ladym-enterprise-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz \
		-C bin ladym-enterprise ladymconsole-enterprise -C .. README.md docs/deployment.md docker-compose.enterprise.yml deploy/nginx.conf
	@echo "dist/ladym-enterprise-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz"
