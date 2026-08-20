//go:build !enterprise

package cli

// Personal edition: the ladym root has no console command — the console stays
// embedded in `ladym serve --http`, and the standalone console role is the
// enterprise-only ladymconsole binary (cmd/ladymconsole).

import (
	"strings"
	"testing"
)

func TestNoConsoleCommandPersonal(t *testing.T) {
	root := newRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "console" {
			t.Error("personal build must not register the `console` command")
		}
	}
	out, err := runCmd(t, root, "--help")
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	if strings.Contains(out, "console") {
		t.Errorf("personal help must not mention console:\n%s", out)
	}
}
