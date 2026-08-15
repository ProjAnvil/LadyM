package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/code"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
)

// TestWriteIndexReportPrintsFirstFiveErrors mirrors Python's
// `report.errors[:5]` — the first five errors are printed, the rest only
// counted.
func TestWriteIndexReportPrintsFirstFiveErrors(t *testing.T) {
	report := &code.IndexReport{
		FilesSeen:    9,
		FilesIndexed: 7,
		Errors:       []string{"err0", "err1", "err2", "err3", "err4", "err5", "err6"},
	}
	var buf bytes.Buffer
	writeIndexReport(&buf, report)
	out := buf.String()
	if !strings.Contains(out, "errors: 7") {
		t.Errorf("output missing error count:\n%s", out)
	}
	for i := 0; i < 5; i++ {
		if !strings.Contains(out, "err"+string(rune('0'+i))) {
			t.Errorf("output missing error #%d:\n%s", i, out)
		}
	}
	for _, e := range []string{"err5", "err6"} {
		if strings.Contains(out, e) {
			t.Errorf("output should not contain %q (only first 5):\n%s", e, out)
		}
	}
}

// TestWriteStatsByLayerAndNoneWorkspaces mirrors Python cli.py stats():
// workspaces prints "(none)" when empty, and a by-layer breakdown follows.
func TestWriteStatsByLayerAndNoneWorkspaces(t *testing.T) {
	s := &schema.Stats{
		DBPath:        "/tmp/x.db",
		TotalMemories: 6,
		Edges:         1,
		CodeSymbols:   2,
		ByLayer:       map[string]int{"L2_semantic": 4, "L1_episodic": 2},
	}
	var buf bytes.Buffer
	writeStats(&buf, s, "")
	out := buf.String()
	if !strings.Contains(out, "workspaces: (none)") {
		t.Errorf("output missing '(none)' for empty workspaces:\n%s", out)
	}
	for _, want := range []string{"L1_episodic", "L2_semantic"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing by-layer row %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "L1_episodic") > strings.Index(out, "L2_semantic") {
		t.Errorf("by-layer rows should be sorted:\n%s", out)
	}

	// Non-empty workspaces still join with ", ".
	s.Workspaces = []string{"a", "b"}
	buf.Reset()
	writeStats(&buf, s, "")
	if !strings.Contains(buf.String(), "workspaces: a, b") {
		t.Errorf("output missing joined workspaces:\n%s", buf.String())
	}
}

// TestWriteRecallTableIncludesIDColumn: scenario playbooks assert on memory
// ids — the recall table must print the id as its first column.
func TestWriteRecallTableIncludesIDColumn(t *testing.T) {
	resp := &schema.RecallResponse{
		Query: "q",
		Results: []*schema.RecallResult{
			{
				Score: 0.9,
				Memory: &schema.Memory{
					ID:      "abc123def456",
					Layer:   "L2_semantic",
					Type:    "fact",
					Summary: "a fact",
					Source:  "cli",
				},
			},
		},
	}
	var buf bytes.Buffer
	writeRecallTable(&buf, resp)
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header + at least one row:\n%s", out)
	}
	if !strings.HasPrefix(lines[0], "id") {
		t.Errorf("header must start with the id column:\n%s", lines[0])
	}
	if !strings.HasPrefix(lines[1], "abc123def456") {
		t.Errorf("first data row must start with the memory id:\n%s", lines[1])
	}
}

// TestWriteStatsScopedWorkspace: with -w, the workspaces line must list only
// the scoped workspace, not every workspace in the db.
func TestWriteStatsScopedWorkspace(t *testing.T) {
	s := &schema.Stats{
		DBPath:     "/tmp/x.db",
		Workspaces: []string{"scn-s01", "other-ws"},
	}
	var buf bytes.Buffer
	writeStats(&buf, s, "scn-s01")
	out := buf.String()
	if !strings.Contains(out, "workspaces: scn-s01") {
		t.Errorf("output missing scoped workspace:\n%s", out)
	}
	if strings.Contains(out, "other-ws") {
		t.Errorf("output should not list other workspaces when -w is set:\n%s", out)
	}
}

// TestConfigRMMissingKeyPrintsOnce mirrors Python: "no such key KEY" is
// printed once and the process exits non-zero — the error returned must be a
// silent exitError so Execute does not print a second line.
func TestConfigRMMissingKeyPrintsOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate the secret store
	cmd := configRMCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"NOPE"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected non-nil error for missing key")
	}
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("error type = %T, want *exitError (silent; already printed)", err)
	}
	if ee.code == 0 {
		t.Errorf("exitError.code = 0, want non-zero")
	}
	if n := strings.Count(buf.String(), "no such key NOPE"); n != 1 {
		t.Errorf("'no such key NOPE' printed %d times, want exactly 1:\n%s", n, buf.String())
	}
}

// TestServeBanner mirrors the Python `ladym serve` stderr banner.
func TestServeBanner(t *testing.T) {
	cfg := config.Default()
	cfg.DBPath = "/tmp/ladym.db"
	cfg.Workspace = "ws1"
	b := serveBanner(cfg)
	if !strings.Contains(b, "/tmp/ladym.db") || !strings.Contains(b, "ws1") {
		t.Errorf("banner missing db/workspace: %q", b)
	}
}
