//go:build !enterprise

// Personal-edition placeholder so `go build ./...` / `go vet ./...` see a
// buildable package in this directory without the enterprise tag. The console
// role does not exist in personal builds — the console is embedded in
// `ladym serve --http` — and the real entry point (main.go) is enterprise-only.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "ladymconsole is an enterprise-only role (build with -tags enterprise); the personal edition embeds the console in `ladym serve --http`")
	os.Exit(1)
}
