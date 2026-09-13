//go:build !enterprise

// Workspace forcing fallback: a non-admin user with an empty workspace
// column is scoped to the server default workspace.

package api_test

import (
	"net/http"
	"testing"

	"github.com/ProjAnvil/LadyM/api"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/observability"
)

// TestAuthWorkspaceFallback: a non-admin user with an empty workspace column
// is forced into the server default workspace — the X-Ladym-Workspace echo
// shows the effective forcing.
func TestAuthWorkspaceFallback(t *testing.T) {
	eng, cfg := newTestEngine(t, func(cfg *config.Config) { cfg.AuthEnabled = true })
	addUser(t, eng, "root", "s3cret-admin", "", true)
	addUser(t, eng, "bob", "pw-bob", "", false) // no workspace -> server default
	h := api.NewHandlerWithRegistry(eng, cfg, observability.New())

	rec := do(t, h, "/api/stats", "bob", "pw-bob", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats as workspace-less user = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Ladym-Workspace"); got != cfg.Workspace {
		t.Errorf("X-Ladym-Workspace = %q, want the server default %q", got, cfg.Workspace)
	}
	// The workspaces roster is narrowed to the forced one for non-admins.
	m := decodeBody(t, rec)
	workspaces, _ := m["workspaces"].([]any)
	if len(workspaces) != 1 || workspaces[0] != cfg.Workspace {
		t.Errorf("workspaces = %v, want exactly [%q]", m["workspaces"], cfg.Workspace)
	}
}
