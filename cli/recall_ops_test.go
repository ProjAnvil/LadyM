//go:build !enterprise

package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
)

// seedOps records three operations through a real engine on db (workspace
// "testws"): migrate ladym.db success, migrate ladym.db fail, deploy nginx.
func seedOps(t *testing.T, db string) (epOK, epFail *schema.Memory) {
	t.Helper()
	setGlobalConfigPath(t, "")
	eng, err := newEngine(db, "testws")
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	defer eng.Close()
	epOK, err = eng.RecordOperation(engine.OperationEvent{
		Agent: "a", Action: "migrate", Resources: []string{"ladym.db"},
		Observation: "schema v1 -> v2", Outcome: "success",
	})
	if err != nil {
		t.Fatal(err)
	}
	epFail, err = eng.RecordOperation(engine.OperationEvent{
		Agent: "a", Action: "migrate", Resources: []string{"ladym.db"},
		Observation: "table locked", Outcome: "fail",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.RecordOperation(engine.OperationEvent{
		Agent: "a", Action: "deploy", Resources: []string{"nginx"}, Outcome: "success",
	}); err != nil {
		t.Fatal(err)
	}
	return epOK, epFail
}

func TestRecallOpsCmdJSON(t *testing.T) {
	db := isolateEnv(t)
	epOK, epFail := seedOps(t, db)

	cases := []struct {
		name     string
		args     []string
		wantLen  int // -1: don't check length (fallback results are unordered)
		wantIDs  []string
		fallback bool
	}{
		{
			name: "action outcome limit filters",
			args: []string{"--db", db, "-w", "testws", "--json",
				"--resource", "ladym.db", "--action", "migrate",
				"--outcome", "fail", "--limit", "5"},
			wantLen:  1,
			wantIDs:  []string{epFail.ID},
			fallback: false,
		},
		{
			name: "all operations for resource",
			args: []string{"--db", db, "-w", "testws", "--json", "--resource", "ladym.db"},
			wantLen:  2,
			wantIDs:  []string{epOK.ID, epFail.ID},
			fallback: false,
		},
		{
			name: "resource miss falls back",
			args: []string{"--db", db, "-w", "testws", "--json", "--resource", "nonexistent-xyz"},
			wantLen:  -1,
			fallback: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runCmd(t, recallOpsCmd(), tc.args...)
			if err != nil {
				t.Fatalf("recall-ops: %v\n%s", err, out)
			}
			var payload struct {
				Resource any             `json:"resource"`
				Fallback bool            `json:"fallback"`
				Items    []map[string]any `json:"items"`
			}
			if err := json.Unmarshal([]byte(out), &payload); err != nil {
				t.Fatalf("output is not JSON: %v\n%s", err, out)
			}
			if payload.Fallback != tc.fallback {
				t.Errorf("fallback = %v, want %v\n%s", payload.Fallback, tc.fallback, out)
			}
			if tc.wantLen >= 0 && len(payload.Items) != tc.wantLen {
				t.Errorf("items = %d, want %d\n%s", len(payload.Items), tc.wantLen, out)
			}
			if !tc.fallback {
				res, _ := payload.Resource.(string)
				if res == "" {
					t.Errorf("resource missing from pivot hit:\n%s", out)
				}
			}
			if tc.wantIDs != nil {
				got := map[string]bool{}
				for _, it := range payload.Items {
					id, _ := it["id"].(string)
					got[id] = true
				}
				for _, id := range tc.wantIDs {
					if !got[id] {
						t.Errorf("items missing %s:\n%s", id, out)
					}
				}
			}
		})
	}
}

func TestRecallOpsCmdTable(t *testing.T) {
	db := isolateEnv(t)
	seedOps(t, db)
	out, err := runCmd(t, recallOpsCmd(), "--db", db, "-w", "testws", "--resource", "ladym.db")
	if err != nil {
		t.Fatalf("recall-ops table: %v\n%s", err, out)
	}
	if !strings.Contains(out, "recall-ops:") {
		t.Errorf("output missing header line:\n%s", out)
	}
	if !strings.Contains(out, "ladym.db") {
		t.Errorf("output missing resource name:\n%s", out)
	}
}

func TestRecallOpsCmdMissingResourceFlag(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	if _, err := runCmd(t, recallOpsCmd(), "--db", db); err == nil {
		t.Error("expected error when --resource is missing")
	}
}

func TestRecallOpsCmdEngineError(t *testing.T) {
	isolateEnv(t)
	setGlobalConfigPath(t, filepath.Join(t.TempDir(), "nope.toml"))
	if _, err := runCmd(t, recallOpsCmd(), "--resource", "x"); err == nil {
		t.Error("expected error for bad config")
	}
}
