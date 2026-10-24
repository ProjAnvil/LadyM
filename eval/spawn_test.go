//go:build eval

package eval

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpawnHookExitStatus(t *testing.T) {
	bin := ResolveBinary(t)
	t.Setenv("HOME", t.TempDir())
	cases := []struct {
		name    string
		payload string
		code    int
		stderr  string
	}{
		{"ignored-event", `{"hook_event_name":"UnknownEvent"}`, 0, `ignored event "UnknownEvent"`},
		{"invalid-json", `{`, 1, "ladym hook:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "hook.db")
			res, err := SpawnHook(bin, db, "claude-code", tc.payload)
			if err != nil {
				t.Fatalf("SpawnHook returned an error for a completed child: %v", err)
			}
			if res.ExitCode != tc.code {
				t.Errorf("exit code = %d, want %d; stderr: %s", res.ExitCode, tc.code, res.Stderr)
			}
			if res.Stdout != "" {
				t.Errorf("stdout = %q, want empty observer output", res.Stdout)
			}
			if !strings.Contains(res.Stderr, tc.stderr) {
				t.Errorf("stderr = %q, want to contain %q", res.Stderr, tc.stderr)
			}
		})
	}
}

func TestSpawnHookMissingBinary(t *testing.T) {
	dir := t.TempDir()
	_, err := SpawnHook(filepath.Join(dir, "missing-ladym"), filepath.Join(dir, "hook.db"), "claude-code", "{}")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SpawnHook missing binary error = %v, want os.ErrNotExist", err)
	}
}
