package cli

// Backend-agnostic command-test helpers, shared by the remote-mode tests
// (remote_test.go, runs in both editions) and the local-engine command tests
// (commands_test.go, personal edition only).

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/spf13/cobra"
)

// captureStdout swaps os.Stdout for a pipe while fn runs — the commands print
// via fmt.Printf (os.Stdout), not cmd.OutOrStdout().
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	fnErr := fn()
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out), fnErr
}

// runCmd executes a command with args and returns combined stdout (both the
// cobra buffer and the real os.Stdout) plus the error.
func runCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	out, err := captureStdout(t, func() error { return cmd.Execute() })
	return out + buf.String(), err
}
