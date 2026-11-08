package hook

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func identityFixture(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	// Stop Git discovery at the fixture even when TMPDIR is inside a worktree.
	if err := os.WriteFile(filepath.Join(base, ".git"), []byte("gitdir: ./not-a-repository\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return base
}

func TestCanonicalIdentityPaths(t *testing.T) {
	base := identityFixture(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	root := filepath.Join(base, "空 格", "project")
	other := filepath.Join(base, "other", "project")
	nested := filepath.Join(root, "nested")
	for _, dir := range []string{nested, other} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	gotRoot, ws, err := ProjectIdentity(nested + string(filepath.Separator))
	wantWS := fmt.Sprintf("project:%x", sha256.Sum256([]byte(root)))
	if err != nil || gotRoot != root || ws != wantWS {
		t.Fatalf("nested git identity: %q %q %v, want %q %q", gotRoot, ws, err, root, wantWS)
	}
	_, sameWS, err := ProjectIdentity(root)
	if err != nil || ws != sameWS {
		t.Fatalf("nested/root mismatch: %q %v", sameWS, err)
	}
	otherRoot, otherWS, err := ProjectIdentity(other)
	if err != nil || otherRoot != other || otherWS == ws {
		t.Fatalf("same dirname must not collide: %q %q %v", otherRoot, otherWS, err)
	}
	outside := filepath.Join(base, "outside.go")
	p := &Payload{HookEventName: "PostToolUse", ToolName: "Edit", Cwd: nested,
		ToolInput: map[string]any{"file_path": "a/../main.go", "notebook_path": outside}}
	ev, err := p.ToCanonicalOperation("codex", root)
	if err != nil || !reflect.DeepEqual(ev.Resources, []string{"nested/main.go", outside}) {
		t.Fatalf("canonical resources: %+v %v", ev, err)
	}
	if ev.Metadata["resource_identity"] != "path" {
		t.Fatal("path identity metadata missing")
	}
	p.ToolInput = map[string]any{"file_path": filepath.Join(root, "other", "main.go")}
	second, err := p.ToCanonicalOperation("codex", root)
	if err != nil || second.Resources[0] == ev.Resources[0] {
		t.Fatalf("main.go paths collapsed: %+v %v", second, err)
	}
	if legacy := p.ToOperation("codex"); legacy.Resources[0] != "main.go" {
		t.Fatal("public legacy basename behavior changed")
	}
	p.HookEventName = "SessionStart"
	if ev, err := p.ToCanonicalOperation("codex", root); err != nil || len(ev.Resources) != 0 {
		t.Fatalf("non-tool event acquired resources: %+v %v", ev, err)
	}
	p.HookEventName = "Unknown"
	if ev, err := p.ToCanonicalOperation("codex", root); ev != nil || err != nil {
		t.Fatalf("unknown canonical event: %+v %v", ev, err)
	}
}

func TestCanonicalIdentitySymlinkAndErrors(t *testing.T) {
	base := identityFixture(t)
	root := filepath.Join(base, "repo")
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	got, _, err := ProjectIdentity(filepath.Join(link, "nested"))
	if err != nil || got != link {
		t.Fatalf("symlink spelling resolved away: %q %v", got, err)
	}
	if _, _, err := ProjectIdentity(filepath.Join(base, "missing")); err == nil {
		t.Fatal("invalid cwd must report an error")
	}
	t.Setenv("PATH", base)
	if _, _, err := ProjectIdentity(root); err == nil {
		t.Fatal("missing git must report an error, not silently change identity")
	}
	p := &Payload{HookEventName: "PostToolUse", ToolInput: map[string]any{"file_path": "bad\x00name"}}
	if _, err := p.ToCanonicalOperation("codex", root); err == nil {
		t.Fatal("invalid path must fail")
	}
	if _, err := p.ToCanonicalOperation("codex", ""); err == nil {
		t.Fatal("missing root must fail")
	}
}

func TestCanonicalIdentityPollutedGitEnv(t *testing.T) {
	base := identityFixture(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	root := filepath.Join(base, "repo")
	nested := filepath.Join(root, "nested", "deeper")
	other := filepath.Join(base, "other")
	nonGit := filepath.Join(base, "non-git")
	for _, dir := range []string{nested, other, nonGit} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{root, other} {
		if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
	}
	config := filepath.Join(base, "gitconfig")
	if err := os.WriteFile(config, []byte("[core]\n\tworktree = "+other+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"work-tree", map[string]string{"GIT_DIR": filepath.Join(root, ".git"), "GIT_WORK_TREE": root}},
		{"foreign-repository", map[string]string{"GIT_DIR": filepath.Join(other, ".git"), "GIT_WORK_TREE": other}},
		{"common-dir", map[string]string{"GIT_COMMON_DIR": filepath.Join(other, ".git")}},
		{"index", map[string]string{"GIT_INDEX_FILE": filepath.Join(base, "missing-index")}},
		{"objects", map[string]string{"GIT_OBJECT_DIRECTORY": filepath.Join(base, "missing-objects")}},
		{"alternates", map[string]string{"GIT_ALTERNATE_OBJECT_DIRECTORIES": filepath.Join(other, ".git", "objects")}},
		{"config-count", map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.worktree", "GIT_CONFIG_VALUE_0": other}},
		{"config-parameters", map[string]string{"GIT_CONFIG_PARAMETERS": "'core.worktree'='" + other + "'"}},
		{"config", map[string]string{"GIT_CONFIG": config}},
		{"global-config", map[string]string{"GIT_CONFIG_GLOBAL": config}},
		{"system-config", map[string]string{"GIT_CONFIG_SYSTEM": config}},
		{"ceiling-root", map[string]string{"GIT_CEILING_DIRECTORIES": root}},
		{"discovery-disabled", map[string]string{"GIT_DISCOVERY_ACROSS_FILESYSTEM": "0"}},
		{"discovery-enabled", map[string]string{"GIT_DISCOVERY_ACROSS_FILESYSTEM": "1"}},
		{"discovery-invalid", map[string]string{"GIT_DISCOVERY_ACROSS_FILESYSTEM": "invalid"}},
		{"ceiling-with-discovery-enabled", map[string]string{"GIT_CEILING_DIRECTORIES": root, "GIT_DISCOVERY_ACROSS_FILESYSTEM": "1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			for _, cwd := range []string{root, nested, nonGit} {
				wantRoot := root
				if cwd == nonGit {
					wantRoot = nonGit
				}
				gotRoot, ws, err := ProjectIdentity(cwd)
				wantWS := fmt.Sprintf("project:%x", sha256.Sum256([]byte(wantRoot)))
				if err != nil || gotRoot != wantRoot || ws != wantWS {
					t.Errorf("identity for %q: %q %q %v, want %q %q", cwd, gotRoot, ws, err, wantRoot, wantWS)
				}
			}
		})
	}
}

func TestHookObservedToolOutcome(t *testing.T) {
	for _, event := range []string{"SessionStart", "SessionEnd", "UserPromptSubmit", "Stop", "PostToolUse", "PostToolUseFailure"} {
		t.Run(event, func(t *testing.T) {
			p := &Payload{HookEventName: event, ToolName: "Bash",
				ToolResponse: map[string]any{"knowledge_status": "verified", "task_outcome": "success", "verified_at": 123}}
			ev := p.ToOperation("codex")
			if ev.Metadata["knowledge_status"] != "observed" {
				t.Errorf("status = %v, want observed", ev.Metadata["knowledge_status"])
			}
			for _, key := range []string{"task_outcome", "verified_at"} {
				if _, exists := ev.Metadata[key]; exists {
					t.Errorf("tool output fabricated %s", key)
				}
			}
			want := ""
			if event == "PostToolUse" {
				want = "success"
			} else if event == "PostToolUseFailure" {
				want = "fail"
			}
			if got, _ := ev.Metadata["tool_outcome"].(string); got != want {
				t.Errorf("tool_outcome = %q, want %q", got, want)
			}
		})
	}
}
