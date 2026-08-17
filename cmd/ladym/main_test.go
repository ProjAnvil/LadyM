package main

import (
	"os"
	"testing"
)

// TestMainHelp invokes main() with --help. cli.Execute() returns normally on
// the help path (no os.Exit), so main() can be exercised in-process. HOME is
// isolated so no real ~/.ladyM state is touched.
func TestMainHelp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	old := os.Args
	os.Args = []string{"ladym", "--help"}
	defer func() { os.Args = old }()

	main()
}
