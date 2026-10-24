//go:build eval

package eval

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// mustPayload is the assertion-flavoured wrapper around PayloadJSON, so the
// non-test runner can use the same method without dragging testing.T along.
func (f Fixture) mustPayload(t *testing.T) string {
	t.Helper()
	s, err := f.PayloadJSON()
	if err != nil {
		t.Fatalf("marshal payload for %s: %v", f.Name, err)
	}
	return s
}

// metaOf decodes a row's metadata JSON into a flat string map, dropping any
// non-string values: every field this matrix asserts is a string.
func metaOf(t *testing.T, r Row) map[string]string {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(r.Metadata), &raw); err != nil {
		t.Fatalf("decode metadata %q: %v", r.Metadata, err)
	}
	out := map[string]string{}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func TestFixtureMergeOverrideWithoutMutatingInputs(t *testing.T) {
	base := map[string]any{"event": "base", "keep": "original"}
	extras := map[string]any{"event": "override", "new": true}
	merged := merge(base, extras)
	payload, err := (Fixture{Payload: merged}).PayloadJSON()
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"event":"override","keep":"original","new":true}`; payload != want {
		t.Errorf("merged payload = %s, want %s", payload, want)
	}
	merged["keep"] = "changed"
	merged["new"] = false
	if base["event"] != "base" || base["keep"] != "original" || len(base) != 2 {
		t.Errorf("base mutated by merge: %v", base)
	}
	if extras["event"] != "override" || extras["new"] != true || len(extras) != 2 {
		t.Errorf("extras mutated by merge: %v", extras)
	}
}

func TestFixtureMergeNilInputs(t *testing.T) {
	cases := []struct {
		name   string
		base   map[string]any
		extras map[string]any
		want   string
	}{
		{"both-nil", nil, nil, "{}"},
		{"nil-base", nil, map[string]any{"new": true}, `{"new":true}`},
		{"nil-extras", map[string]any{"keep": "original"}, nil, `{"keep":"original"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged := merge(tc.base, tc.extras)
			if merged == nil {
				t.Fatal("merge returned nil, want a writable map")
			}
			payload, err := (Fixture{Payload: merged}).PayloadJSON()
			if err != nil {
				t.Fatal(err)
			}
			if payload != tc.want {
				t.Errorf("payload = %s, want %s", payload, tc.want)
			}
		})
	}
}

// TestFidelityMatrix drives every fixture through the REAL `ladym hook` binary
// and asserts what landed in SQLite. Each fixture gets its own database so a
// failing case can never contaminate another.
func TestFidelityMatrix(t *testing.T) {
	bin := ResolveBinary(t)

	for _, fx := range Fixtures() {
		t.Run(fx.Name, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "probe.ladym.db")

			res, err := SpawnHook(bin, db, fx.Harness, fx.mustPayload(t))
			if err != nil {
				t.Fatalf("spawn hook: %v", err)
			}
			if res.ExitCode != fx.Want.ExitCode {
				t.Errorf("exit = %d, want %d (stderr: %s)", res.ExitCode, fx.Want.ExitCode, res.Stderr)
			}
			if res.Stdout != "" {
				t.Errorf("stdout = %q, want empty: the hook is a pure observer", res.Stdout)
			}

			eps, err := ReadEpisodes(db, fx.Workspace)
			if err != nil {
				t.Fatalf("read episodes: %v", err)
			}
			if len(eps) != fx.Want.Episodes {
				t.Fatalf("episodes = %d, want %d (content: %q)", len(eps), fx.Want.Episodes, eps)
			}

			edges, err := ReadEdges(db)
			if err != nil {
				t.Fatalf("read edges: %v", err)
			}
			if len(edges) != fx.Want.Edges {
				t.Errorf("edges = %d, want %d", len(edges), fx.Want.Edges)
			}

			if len(eps) == 0 {
				return
			}
			row := eps[len(eps)-1]
			meta := metaOf(t, row)

			if got := meta["action_class"]; got != fx.Want.ActionClass {
				t.Errorf("action_class = %q, want %q", got, fx.Want.ActionClass)
			}
			if got := meta["action_raw"]; got != fx.Want.ActionRaw {
				t.Errorf("action_raw = %q, want %q", got, fx.Want.ActionRaw)
			}
			// Outcome is the only field separating PostToolUse from
			// PostToolUseFailure, so it must be asserted explicitly.
			if got := meta["outcome"]; got != fx.Want.Outcome {
				t.Errorf("outcome = %q, want %q", got, fx.Want.Outcome)
			}
			if got := meta["hook_event_name"]; got != fx.EventName {
				t.Errorf("hook_event_name = %q, want %q", got, fx.EventName)
			}
			if got := meta["session_id"]; got != evalSessionID {
				t.Errorf("session_id = %q, want %q", got, evalSessionID)
			}
			if got := meta["cwd"]; got != evalCwd {
				t.Errorf("cwd = %q, want %q", got, evalCwd)
			}
			if fx.Want.ObsRunes != 0 {
				if got := len([]rune(meta["observation"])); got != fx.Want.ObsRunes {
					t.Errorf("observation runes = %d, want %d", got, fx.Want.ObsRunes)
				}
			}
			// Resource names must be checked, not just edge counts: a wrong count
			// can hide a wrong resource entirely.
			if len(fx.Want.Resources) > 0 {
				linked := linkedResourceNames(t, db, row.ID)
				for _, want := range fx.Want.Resources {
					if !linked[want] {
						t.Errorf("resource %q is not linked to the episode (linked: %v)", want, linked)
					}
				}
			}
		})
	}
}

// linkedResourceNames resolves the resource entity names an episode is linked
// to, so assertions can name resources instead of just counting edges.
func linkedResourceNames(t *testing.T, db, episodeID string) map[string]bool {
	t.Helper()
	edges, err := ReadEdges(db)
	if err != nil {
		t.Fatalf("read edges: %v", err)
	}
	dst := map[string]bool{}
	for _, e := range edges {
		if e.SrcID == episodeID && e.Relation == "operates_on" {
			dst[e.DstID] = true
		}
	}
	rows, err := ReadAll(db, evalWorkspace)
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	out := map[string]bool{}
	for _, r := range rows {
		if dst[r.ID] {
			out[resourceName(r)] = true
		}
	}
	return out
}

// resourceName pulls the human-readable name out of a resource entity row.
func resourceName(r Row) string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(r.Metadata), &raw); err != nil {
		return r.Summary
	}
	for _, key := range []string{"name", "alias", "resource", "file_path"} {
		if s, ok := raw[key].(string); ok && s != "" {
			return s
		}
	}
	return r.Summary
}

// TestObservationIsPersistedTwice pins a write-amplification finding:
// layers.EpisodicMemory.Record copies observation into both Content and
// Metadata, so every captured byte of tool output is stored twice. This test
// exists so the amplification accounted for in the token report stays honest if
// the product code ever stops duplicating.
func TestObservationIsPersistedTwice(t *testing.T) {
	bin := ResolveBinary(t)
	const obs = "at line 42: nil pointer dereference"
	fx := Fixture{
		Name: "dup", Harness: "claude-code", EventName: "PostToolUse", Workspace: evalWorkspace,
		Payload: toolPayload("PostToolUse", "Read", map[string]any{}, obs),
		Want:    Want{Episodes: 1, ObsRunes: len(obs)},
	}
	db := filepath.Join(t.TempDir(), "probe.ladym.db")
	if _, err := SpawnHook(bin, db, fx.Harness, fx.mustPayload(t)); err != nil {
		t.Fatalf("spawn hook: %v", err)
	}
	eps, err := ReadEpisodes(db, fx.Workspace)
	if err != nil {
		t.Fatalf("read episodes: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("episodes = %d, want 1", len(eps))
	}
	row := eps[0]

	if !strings.Contains(row.Content, "observation="+obs) {
		t.Errorf("content is missing the observation copy: %q", row.Content)
	}
	if got := metaOf(t, row)["observation"]; got != obs {
		t.Errorf("metadata observation = %q, want %q", got, obs)
	}
}

// TestHookRejectsBadJSON checks the harness never blocks on malformed input.
func TestHookRejectsBadJSON(t *testing.T) {
	bin := ResolveBinary(t)
	db := filepath.Join(t.TempDir(), "probe.ladym.db")

	res, err := SpawnHook(bin, db, "claude-code", "not json at all")
	if err != nil {
		t.Fatalf("spawn hook: %v", err)
	}
	if res.ExitCode != 1 {
		t.Errorf("exit = %d, want 1 for malformed stdin", res.ExitCode)
	}
	if res.Stdout != "" {
		t.Errorf("stdout = %q, want empty", res.Stdout)
	}
}

// TestHookNeverBlocks asserts the exit code is never 2, the value harnesses
// treat as a blocking decision.
func TestHookNeverBlocks(t *testing.T) {
	bin := ResolveBinary(t)
	for _, fx := range Fixtures() {
		db := filepath.Join(t.TempDir(), "probe.ladym.db")
		res, err := SpawnHook(bin, db, fx.Harness, fx.mustPayload(t))
		if err != nil {
			t.Fatalf("%s: spawn hook: %v", fx.Name, err)
		}
		if res.ExitCode == 2 {
			t.Errorf("%s: exit = 2, the hook must never block the harness", fx.Name)
		}
	}
}
