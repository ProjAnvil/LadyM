//go:build !enterprise

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func installFixture(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "settings.json")
}

func mustWriteFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func parseJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("bad JSON %q: %v", string(b), err)
	}
	return doc
}

func TestInstallHookFileCreates(t *testing.T) {
	p := installFixture(t)
	if err := installHookFile(p, "claude-code", claudeHookEvents, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	hooks := parseJSON(t, b)["hooks"].(map[string]any)
	for _, ev := range claudeHookEvents {
		if _, ok := hooks[ev]; !ok {
			t.Errorf("event %s missing", ev)
		}
	}
	if _, ok := hooks["PostToolUseFailure"]; !ok {
		t.Error("PostToolUseFailure event missing")
	}
}

func TestInstallHookFilePreservesUserHooks(t *testing.T) {
	p := installFixture(t)
	user := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"notify-send done"}]}]},"model":"sonnet"}`
	mustWriteFile(t, p, user)
	if err := installHookFile(p, "claude-code", claudeHookEvents, false); err != nil {
		t.Fatal(err)
	}
	doc := parseJSON(t, mustReadFile(t, p))
	if doc["model"] != "sonnet" {
		t.Error("non-hook keys must survive")
	}
	hooks := doc["hooks"].(map[string]any)
	stop := hooks["Stop"].([]any)
	if len(stop) != 2 {
		t.Fatalf("Stop groups = %d, want 2 (user + ours)", len(stop))
	}
}

func TestInstallHookFileIdempotent(t *testing.T) {
	p := installFixture(t)
	if err := installHookFile(p, "claude-code", claudeHookEvents, false); err != nil {
		t.Fatal(err)
	}
	b1 := mustReadFile(t, p)
	if err := installHookFile(p, "claude-code", claudeHookEvents, false); err != nil {
		t.Fatal(err)
	}
	b2 := mustReadFile(t, p)
	if string(b1) != string(b2) {
		t.Error("second install must be a no-op")
	}
}

func TestInstallHookFileRejectsCorruptFile(t *testing.T) {
	p := installFixture(t)
	mustWriteFile(t, p, "{broken")
	if err := installHookFile(p, "claude-code", claudeHookEvents, false); err == nil {
		t.Fatal("corrupt file must be refused")
	}
	// The corrupt file must not be overwritten either.
	if b := mustReadFile(t, p); string(b) != "{broken" {
		t.Errorf("corrupt file was modified: %q", string(b))
	}
	if _, err := os.Stat(p + ".ladym-bak"); !os.IsNotExist(err) {
		t.Error("no backup may be taken for an unreadable file")
	}
}

func TestInstallHookFileBackupOnce(t *testing.T) {
	p := installFixture(t)
	mustWriteFile(t, p, `{"hooks":{}}`)
	if err := installHookFile(p, "claude-code", claudeHookEvents, false); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, p, `{"hooks":{},"x":1}`) // user edits between installs
	if err := installHookFile(p, "claude-code", claudeHookEvents, false); err != nil {
		t.Fatal(err)
	}
	bak, err := os.ReadFile(p + ".ladym-bak")
	if err != nil {
		t.Fatal("backup must exist")
	}
	// Semantic assert: the backup holds the FIRST original (a verbatim copy,
	// never rolled over by later installs) and carries no ladym entries.
	got := parseJSON(t, bak)
	want := parseJSON(t, []byte(`{"hooks":{}}`))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("backup = %q, want the first original {\"hooks\":{}}", string(bak))
	}
	if strings.Contains(string(bak), "ladym hook") {
		t.Error("backup must not contain our hook entries")
	}
}

func TestUninstallHookFileRemovesOnlyOurs(t *testing.T) {
	p := installFixture(t)
	user := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"notify-send done"}]}]},"model":"sonnet"}`
	mustWriteFile(t, p, user)
	if err := installHookFile(p, "claude-code", claudeHookEvents, false); err != nil {
		t.Fatal(err)
	}
	if err := uninstallHookFile(p); err != nil {
		t.Fatal(err)
	}
	b := mustReadFile(t, p)
	if strings.Contains(string(b), "ladym hook") {
		t.Errorf("our command leaked after uninstall: %s", string(b))
	}
	doc := parseJSON(t, b)
	if doc["model"] != "sonnet" {
		t.Error("non-hook keys must survive uninstall")
	}
	hooks := doc["hooks"].(map[string]any)
	stop, ok := hooks["Stop"].([]any)
	if !ok || len(stop) != 1 {
		t.Fatalf("Stop groups = %d, want 1 (user only)", len(stop))
	}
}

func TestUninstallHookFilePrunesEmptyEventKeys(t *testing.T) {
	p := installFixture(t)
	user := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"notify-send done"}]}]}}`
	mustWriteFile(t, p, user)
	if err := installHookFile(p, "claude-code", claudeHookEvents, false); err != nil {
		t.Fatal(err)
	}
	if err := uninstallHookFile(p); err != nil {
		t.Fatal(err)
	}
	doc := parseJSON(t, mustReadFile(t, p))
	hooks := doc["hooks"].(map[string]any)
	if len(hooks) != 1 {
		t.Errorf("events pruned to %d keys, want 1 (only the user's Stop): %v", len(hooks), hooks)
	}
	if _, ok := hooks["SessionStart"]; ok {
		t.Error("SessionStart (ours only) must be pruned")
	}
}

func TestUninstallHookFileRemovesInstalledFile(t *testing.T) {
	p := installFixture(t)
	if err := installHookFile(p, "claude-code", claudeHookEvents, false); err != nil {
		t.Fatal(err)
	}
	if err := uninstallHookFile(p); err != nil {
		t.Fatal(err)
	}
	// Our install created a hooks-only file; uninstall prunes it entirely.
	if b, err := os.ReadFile(p); err == nil {
		t.Errorf("hooks-only file should be removed, content=%q", string(b))
	}
}

func TestUninstallHookFileMissingNoop(t *testing.T) {
	p := installFixture(t) // never created
	if err := uninstallHookFile(p); err != nil {
		t.Fatalf("missing file must be a no-op: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("uninstall must not create %s", p)
	}
}

// claudeSettingsPath returns $HOME/.claude/settings.json under the isolated
// HOME that isolateEnv installed.
func claudeSettingsPath(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".claude", "settings.json")
}

func TestHookInstallCmdClaude(t *testing.T) {
	isolateEnv(t)
	cmd := hookCmd()
	cmd.SetIn(bytes.NewReader(nil))
	out, err := runCmd(t, cmd, "install", "--claude")
	if err != nil {
		t.Fatalf("install --claude: %v, out=%q", err, out)
	}
	if !strings.Contains(out, "restart your session") {
		t.Errorf("install guidance missing: %q", out)
	}
	hooks := parseJSON(t, mustReadFile(t, claudeSettingsPath(t)))["hooks"].(map[string]any)
	for _, ev := range claudeHookEvents {
		if _, ok := hooks[ev]; !ok {
			t.Errorf("event %s missing after install", ev)
		}
	}
}

func TestHookUninstallCmdClaude(t *testing.T) {
	isolateEnv(t)
	cmd := hookCmd()
	cmd.SetIn(bytes.NewReader(nil))
	if out, err := runCmd(t, cmd, "install", "--claude"); err != nil {
		t.Fatalf("install --claude: %v, out=%q", err, out)
	}
	out, err := runCmd(t, cmd, "uninstall", "--claude")
	if err != nil {
		t.Fatalf("uninstall --claude: %v, out=%q", err, out)
	}
	// Our install created a hooks-only file; uninstall prunes it entirely, so
	// either the file is gone or it no longer carries our command.
	p := claudeSettingsPath(t)
	if b, err := os.ReadFile(p); err == nil {
		if strings.Contains(string(b), "ladym hook") {
			t.Errorf("our command leaked after uninstall: %s", string(b))
		}
	}
	// Uninstalling again (file absent) stays a no-op success.
	if out, err := runCmd(t, cmd, "uninstall", "--claude"); err != nil {
		t.Fatalf("second uninstall must be a no-op: %v, out=%q", err, out)
	}
}

func mustReadFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
