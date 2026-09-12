//go:build enterprise

package main

import (
	"io"
	"os"
	"testing"
)

// TestMain_EnterpriseHelp_ReturnsWithoutError exercises the real main() entry
// point with --help: cobra prints usage and ExecuteConsole returns without
// reaching fatalOnError's os.Exit, so main() can run inside the test process.
// The server path (no args) is not hermetic and is covered in cli tests.
func TestMain_EnterpriseHelp_ReturnsWithoutError(t *testing.T) {
	origArgs := os.Args
	origStdout := os.Stdout
	t.Cleanup(func() {
		os.Args = origArgs
		os.Stdout = origStdout
	})

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	os.Args = []string{"ladymconsole", "--help"}

	main()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read help output: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected cobra help text on stdout, got nothing")
	}
}
