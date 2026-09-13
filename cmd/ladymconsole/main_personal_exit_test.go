//go:build !enterprise

package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestPersonalMainHelperProcess is not a real test: it is re-executed as a
// subprocess by TestMain_PersonalPlaceholder_PrintsMessageAndExitsNonZero so
// that main()'s os.Exit(1) terminates the child instead of the test runner.
func TestPersonalMainHelperProcess(t *testing.T) {
	if os.Getenv("LADYMCONSOLE_PERSONAL_MAIN_HELPER") != "1" {
		t.Skip("helper process for TestMain_PersonalPlaceholder tests only")
	}
	main()
	t.Fatal("main() returned without calling os.Exit")
}

func TestMain_PersonalPlaceholder_PrintsMessageAndExitsNonZero(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run", "^TestPersonalMainHelperProcess$", "-test.v")
	cmd.Env = append(os.Environ(), "LADYMCONSOLE_PERSONAL_MAIN_HELPER=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected exit-status error, got %v (stderr: %s)", err, stderr.String())
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %d (stderr: %s)", exitErr.ExitCode(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "enterprise-only") {
		t.Fatalf("expected enterprise-only message on stderr, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "ladym serve --http") {
		t.Fatalf("expected pointer to `ladym serve --http` on stderr, got %q", stderr.String())
	}
}
