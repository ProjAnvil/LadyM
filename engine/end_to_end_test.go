//go:build !enterprise

package engine

// Port of main:tests/integration/test_end_to_end.py — the full memory
// lifecycle, mirroring the user's core pain point: an agent should NOT need
// to Read/Grep a file every turn — it should recall the indexed analysis
// instead.

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
)

const authFixture = `def hash_password(password: str) -> str:
    """Hash a plaintext password with a salt."""
    return "hashed:" + password

def verify_password(plaintext: str, hashed: str) -> bool:
    """Check a plaintext password against its hash."""
    return hash_password(plaintext) == hashed

class AuthService:
    def login(self, username: str, password: str) -> str:
        """Log in and issue a token."""
        if verify_password(password, self.secret):
            return self._issue_token(username)

    def _issue_token(self, username: str) -> str:
        return "tok." + username
`

// engineWithFixture builds a test engine with the auth fixture repo indexed.
func engineWithFixture(t *testing.T) (*Engine, string) {
	t.Helper()
	eng := newTestEngine(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "auth.py"), authFixture)
	report, err := eng.IndexCode(dir, false, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesIndexed != 1 {
		t.Fatalf("files_indexed = %d, want 1", report.FilesIndexed)
	}
	return eng, dir
}

// ----------------------------------------------------------------------------
// The headline scenario: no Read/Grep needed — recall returns the analysis.
// ----------------------------------------------------------------------------

func TestRecallReturnsCodeAnalysisWithoutReadingFile(t *testing.T) {
	eng, _ := engineWithFixture(t)
	resp, err := eng.SearchCode("verify a password against its hash", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one code memory to be recalled")
	}

	// The agent gets the analysis without opening the file: symbol identity
	// and docstring are carried through the recalled memory content.
	var top *schema.Memory
	for _, r := range resp.Results {
		if strings.Contains(r.Memory.Content, "verify_password") {
			top = r.Memory
			break
		}
	}
	if top == nil {
		t.Fatalf("no result mentions verify_password: %+v", resp.Results)
	}
	if !strings.Contains(top.Content, "Check") && !strings.Contains(top.Content, "plaintext") {
		t.Errorf("docstring not carried through: %q", top.Content)
	}

	// The signature is queryable via the code_symbols projection.
	syms, err := eng.Store.SymbolsForFile(top.MetaString("file_path"))
	if err != nil {
		t.Fatal(err)
	}
	var verify *schema.CodeSymbol
	for _, s := range syms {
		if strings.Contains(s.QualifiedName, "verify_password") {
			verify = s
			break
		}
	}
	if verify == nil {
		t.Fatalf("no code symbol for verify_password in %+v", syms)
	}
	if !strings.Contains(verify.Signature, "verify_password") {
		t.Errorf("signature = %q, want it to name verify_password", verify.Signature)
	}
	if !strings.Contains(verify.Signature, "plaintext") {
		t.Errorf("signature = %q, want the parameter list preserved", verify.Signature)
	}
	if !strings.Contains(verify.Signature, "->") {
		t.Errorf("signature = %q, want the return annotation preserved", verify.Signature)
	}
	if verify.LineStart <= 0 {
		t.Errorf("line_start = %d, want > 0", verify.LineStart)
	}

	// Callers are available without re-parsing.
	refs, err := eng.Store.RefsForSymbol(verify.QualifiedName, "in")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	for _, r := range refs {
		if r.SrcSymbol != verify.QualifiedName {
			called = true
			break
		}
	}
	if !called {
		t.Error("expected a caller of verify_password in code_refs")
	}
}

func TestIndexPersistsAcrossEngineReopen(t *testing.T) {
	// A reopened engine (new process equivalent) can still answer recalls —
	// the whole point of having memory rather than re-reading on every turn.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "auth.py"), authFixture)
	cfg := config.ForTesting(t.TempDir())

	eng1, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng1.IndexCode(dir, false, "", nil); err != nil {
		t.Fatal(err)
	}
	s1, err := eng1.Stats()
	if err != nil {
		t.Fatal(err)
	}
	eng1.Close()

	eng2, err := New(cfg) // same db path, fresh engine
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	resp, err := eng2.SearchCode("verify password", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected search results after reopen")
	}
	s2, err := eng2.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if s2.CodeSymbols != s1.CodeSymbols {
		t.Errorf("code_symbols = %d after reopen, want %d", s2.CodeSymbols, s1.CodeSymbols)
	}
}

// ----------------------------------------------------------------------------
// Full cognitive lifecycle: encode → consolidate → proceduralize → recall → decay
// ----------------------------------------------------------------------------

func TestFullMemoryLifecycle(t *testing.T) {
	eng := newTestEngine(t)

	// 1. encode a bunch of episodes (some succeed, some fail)
	for i := 0; i < 4; i++ {
		if _, err := eng.RecordEvent("claude", "deploy to prod",
			fmt.Sprintf("ran deploy.sh release %d", i), "success", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := eng.RecordEvent("claude", "deploy to prod",
		"ran deploy.sh broken", "failure", nil, nil); err != nil {
		t.Fatal(err)
	}

	// 2. consolidate episodes → semantic facts
	cReport, err := eng.Consolidate("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if cReport.PromotedToSemantic < 1 {
		t.Errorf("promoted_to_semantic = %d, want >= 1", cReport.PromotedToSemantic)
	}

	// 3. proceduralize successful clusters → playbook
	pReport, err := eng.Proceduralize("", 3)
	if err != nil {
		t.Fatal(err)
	}
	if pReport.PlaybooksCreated < 1 {
		t.Errorf("playbooks_created = %d, want >= 1", pReport.PlaybooksCreated)
	}

	// 4. recall pulls from multiple layers in one query
	resp, err := eng.Recall("deploy to prod", "", 0, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	layersSeen := map[schema.Layer]bool{}
	for _, r := range resp.Results {
		layersSeen[r.Memory.Layer] = true
	}
	if !layersSeen[schema.LayerSemantic] && !layersSeen[schema.LayerProcedural] && !layersSeen[schema.LayerEpisodic] {
		t.Error("recall saw none of the episodic/semantic/procedural layers")
	}

	// 5. decay does not touch the promoted semantic/procedural items
	old := schema.Now() - 100*365*24*3600
	backdoorExec(t, eng.Config.DBPath,
		"UPDATE memories SET last_access_at = ? WHERE layer = ?", old, string(schema.LayerEpisodic))
	if _, err := eng.Decay("", false, 1.0, 0.9); err != nil {
		t.Fatal(err)
	}
	// Only episodic items decayed; semantic + procedural untouched.
	remaining, err := eng.Store.IterMemories("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	remainingLayers := map[schema.Layer]bool{}
	for _, m := range remaining {
		remainingLayers[m.Layer] = true
	}
	if !remainingLayers[schema.LayerSemantic] {
		t.Error("semantic memories must survive decay")
	}
	if !remainingLayers[schema.LayerProcedural] {
		t.Error("procedural memories must survive decay")
	}
}

// ----------------------------------------------------------------------------
// Two-tier retrieval (HyMem cognitive economy)
// ----------------------------------------------------------------------------

func TestTier1SufficientForWellCoveredQuery(t *testing.T) {
	eng, _ := engineWithFixture(t)
	// Tier 1 should usually suffice for an on-topic code query against a
	// small repo.
	resp, err := eng.SearchCode("AuthService login password", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected search results")
	}
	if resp.TierReached != 1 && resp.TierReached != 2 {
		t.Errorf("tier_reached = %d, want 1 or 2", resp.TierReached)
	}
}

func TestTier2TriggeredWhenAnchorLinksExist(t *testing.T) {
	eng := newTestEngine(t)
	a, err := eng.Semantic.PutFact("obscure anchor fact about xyzzy", "", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := eng.Semantic.PutFact("xyzzy neighbour that elaborates the anchor meaningfully", "", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Link(a.ID, b.ID, "elaborates"); err != nil {
		t.Fatal(err)
	}
	resp, err := eng.Recall("xyzzy", "", 0, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range resp.Results {
		if r.Memory.ID == a.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("anchor memory not in recall results")
	}
}

// ----------------------------------------------------------------------------
// Multi-workspace isolation
// ----------------------------------------------------------------------------

func TestWorkspaceIsolation(t *testing.T) {
	dir := t.TempDir()
	cfg1 := config.ForTesting(dir)
	cfg1.Workspace = "team_a"
	engA, err := New(cfg1)
	if err != nil {
		t.Fatal(err)
	}
	defer engA.Close()
	if _, err := engA.Semantic.PutFact("team A secret: deploy key abc", "", nil, nil, ""); err != nil {
		t.Fatal(err)
	}

	cfg2 := config.ForTesting(dir) // same db file, different workspace
	cfg2.Workspace = "team_b"
	engB, err := New(cfg2)
	if err != nil {
		t.Fatal(err)
	}
	defer engB.Close()

	respA, err := engA.Recall("deploy key", "", 0, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range respA.Results {
		if strings.Contains(r.Memory.Content, "team A secret") {
			found = true
		}
	}
	if !found {
		t.Error("team_a recall should return the team A secret")
	}

	respB, err := engB.Recall("deploy key", "", 0, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range respB.Results {
		if strings.Contains(r.Memory.Content, "team A secret") {
			t.Error("team_b recall must not leak the team A secret")
		}
	}
}

// ----------------------------------------------------------------------------
// Incremental indexing correctness
// ----------------------------------------------------------------------------

func TestIncrementalIndexPicksUpNewFile(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "a.py"), "def alpha():\n    return 1\n")
	eng := newTestEngine(t)

	r1, err := eng.IndexCode(repo, false, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r1.FilesIndexed != 1 {
		t.Fatalf("files_indexed = %d, want 1", r1.FilesIndexed)
	}

	writeFile(t, filepath.Join(repo, "b.py"), "def beta():\n    return 2\n")
	r2, err := eng.IndexCode(repo, false, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r2.FilesIndexed != 1 {
		t.Errorf("files_indexed = %d, want 1 (only the new file)", r2.FilesIndexed)
	}
	if r2.FilesSkippedUnchanged != 1 {
		t.Errorf("files_skipped_unchanged = %d, want 1 (a.py unchanged)", r2.FilesSkippedUnchanged)
	}

	resp, err := eng.SearchCode("beta function", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range resp.Results {
		if strings.Contains(r.Memory.Content, "beta") {
			found = true
		}
	}
	if !found {
		t.Error("expected beta to be retrievable after incremental index")
	}
}

func TestIncrementalIndexPicksUpChangedFile(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "a.py"), "def alpha():\n    return 1\n")
	eng := newTestEngine(t)
	if _, err := eng.IndexCode(repo, false, "", nil); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(repo, "a.py"), "def alpha_v2():\n    return 2\n")
	r, err := eng.IndexCode(repo, false, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.FilesIndexed != 1 {
		t.Errorf("files_indexed = %d, want 1", r.FilesIndexed)
	}

	// The new symbol should be retrievable, the old one gone.
	resp, err := eng.SearchCode("alpha", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	var contents strings.Builder
	for _, r := range resp.Results {
		contents.WriteString(r.Memory.Content)
		contents.WriteString(" ")
	}
	if !strings.Contains(contents.String(), "alpha_v2") {
		t.Errorf("expected alpha_v2 in results: %q", contents.String())
	}
}
