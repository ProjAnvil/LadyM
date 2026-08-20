//go:build !enterprise

package cli

// End-to-end remote-mode tests against the real api package (no mock): they
// spin a local engine on the personal-edition default (sqlite) backend, so
// they are excluded from enterprise builds. The fake-server wire-protocol
// tests stay in remote_test.go and run in both editions.

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/api"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
	"golang.org/x/crypto/bcrypt"
)

func TestRemoteEndToEndRealAPI(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	srv := httptest.NewServer(api.NewHandler(eng, cfg))
	t.Cleanup(srv.Close)

	// No -w here: engine.Stats counts are scoped to the engine's configured
	// workspace, so the memory must land in the default workspace for the
	// stats assertion below. (-w over the wire is covered by the fake-server
	// tests above.)
	out, err := runCmd(t, rememberCmd(), "--server", srv.URL,
		"remote e2e quixotic zephyr fact")
	if err != nil {
		t.Fatalf("remote remember: %v", err)
	}
	m := idRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("remember output missing id: %q", out)
	}
	id := m[1]

	out, err = runCmd(t, recallCmd(), "--server", srv.URL,
		"remote e2e quixotic zephyr")
	if err != nil {
		t.Fatalf("remote recall: %v", err)
	}
	if !strings.Contains(out, id) {
		t.Errorf("recall did not return remembered id %s:\n%s", id, out)
	}

	out, err = runCmd(t, statsCmd(), "--server", srv.URL)
	if err != nil {
		t.Fatalf("remote stats: %v", err)
	}
	if !strings.Contains(out, "total memories: 1") {
		t.Errorf("stats output missing the remembered memory:\n%s", out)
	}

	out, err = runCmd(t, forgetCmd(), "--server", srv.URL, id)
	if err != nil {
		t.Fatalf("remote forget: %v", err)
	}
	if !strings.Contains(out, "forgot "+id) {
		t.Errorf("forget output:\n%s", out)
	}
}

// TestRemoteEndToEndRealAPIAuth runs the same wire against an auth-enabled
// real server: no credentials -> single-line 401 error; a valid users-table
// account -> works. A passwordless account works with --user alone.
func TestRemoteEndToEndRealAPIAuth(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.AuthEnabled = true
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*schema.User{
		{Username: "cli-admin", PasswordHash: string(hash), Admin: true, CreatedAt: schema.Now()},
		{Username: "nopw", Workspace: "nopw-ws", CreatedAt: schema.Now()},
	} {
		if err := eng.Store.PutUser(u); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(api.NewHandler(eng, cfg))
	t.Cleanup(srv.Close)

	if _, err := runCmd(t, statsCmd(), "--server", srv.URL); err == nil {
		t.Fatal("expected 401 error without credentials")
	} else if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("error = %q, want 'unauthorized'", err.Error())
	}

	out, err := runCmd(t, statsCmd(), "--server", srv.URL, "--user", "cli-admin", "--password", "s3cret")
	if err != nil {
		t.Fatalf("remote stats with credentials: %v", err)
	}
	if !strings.Contains(out, "LadyM stats") {
		t.Errorf("output missing stats header:\n%s", out)
	}

	// Passwordless account: --user alone authenticates.
	if _, err := runCmd(t, statsCmd(), "--server", srv.URL, "--user", "nopw"); err != nil {
		t.Fatalf("remote stats as passwordless user: %v", err)
	}
}
