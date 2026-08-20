//go:build !enterprise

package cli

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// TestBuildServeHTTPHandler covers handler construction (split from listen):
// the returned handler must serve /api/stats against a temp db.
func TestBuildServeHTTPHandler(t *testing.T) {
	db := isolateEnv(t)
	cfg, err := loadConfig(db, "")
	if err != nil {
		t.Fatal(err)
	}
	h, _, closeFn, err := buildServeHTTPHandler(cfg)
	if err != nil {
		t.Fatalf("buildServeHTTPHandler: %v", err)
	}
	defer closeFn()

	req := httptest.NewRequest("POST", "/api/stats", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("/api/stats: %d %s", rec.Code, rec.Body.String())
	}
}

// TestServeHTTPBanner covers the startup lines printed by `serve --http`
// (listen address, db, workspace, auth mode): auth=off is the default with
// no warning; auth=on with an empty users table warns to bootstrap via CLI.
func TestServeHTTPBanner(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.Workspace = "ws1"
	store, err := storage.NewStore(filepath.Join(t.TempDir(), "banner.db"), 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	b := serveHTTPBanner(cfg, ":8080", store)
	for _, want := range []string{":8080", cfg.DBPath, "ws1", "auth=off"} {
		if !strings.Contains(b, want) {
			t.Errorf("http banner missing %q: %q", want, b)
		}
	}
	if strings.Contains(b, "WARNING") {
		t.Errorf("auth=off (personal default) must not warn: %q", b)
	}

	// auth on, users table empty -> bootstrap warning.
	cfg.AuthEnabled = true
	b = serveHTTPBanner(cfg, "8080", store)
	if !strings.Contains(b, "auth=on") {
		t.Errorf("banner missing auth=on: %q", b)
	}
	if !strings.Contains(b, "WARNING") || !strings.Contains(b, "ladym user add") {
		t.Errorf("auth on with no users must warn about `ladym user add`: %q", b)
	}

	// One user -> warning gone.
	if err := store.PutUser(&schema.User{Username: "root", Admin: true, CreatedAt: schema.Now()}); err != nil {
		t.Fatal(err)
	}
	b = serveHTTPBanner(cfg, "8080", store)
	if strings.Contains(b, "WARNING") {
		t.Errorf("auth on with users should not warn: %q", b)
	}
}

// TestServeHTTPBannerBackend: the banner names the store backend; under
// postgres the (empty) sqlite db path must not appear.
func TestServeHTTPBannerBackend(t *testing.T) {
	cfg := config.Default()
	cfg.DBPath = "/tmp/ladym.db"
	store, err := storage.NewStore(filepath.Join(t.TempDir(), "banner2.db"), 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cfg.StoreBackend = "sqlite"
	b := serveHTTPBanner(cfg, ":8080", store)
	if !strings.Contains(b, "backend=sqlite") || !strings.Contains(b, "db=/tmp/ladym.db") {
		t.Errorf("sqlite banner missing backend/db: %q", b)
	}

	cfg.StoreBackend = "postgres"
	b = serveHTTPBanner(cfg, ":8080", store)
	if !strings.Contains(b, "backend=postgres") {
		t.Errorf("postgres banner missing backend: %q", b)
	}
	if strings.Contains(b, "db=") {
		t.Errorf("postgres banner must not show the (empty) sqlite db path: %q", b)
	}
}

// TestServeHTTPListenError covers serveHTTP's listen tail without holding a
// port open: an address without a colon is prefixed with ":" (per the usage
// string), and an unresolvable port name makes ListenAndServe return the
// net/http error.
func TestServeHTTPListenError(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	cfg, err := loadConfig(db, "")
	if err != nil {
		t.Fatal(err)
	}
	err = serveHTTP(cfg, "ladym-not-a-real-port")
	if err == nil {
		t.Fatal("serveHTTP with a bogus port should fail")
	}
	if !strings.Contains(err.Error(), "ladym-not-a-real-port") {
		t.Errorf("listen error = %v, want it to name the address", err)
	}
}

// TestConfigCmdPrintsHelp: bare `ladym config` (no subcommand) prints the
// group help — the retired web editor's successor is the embedded console.
func TestConfigCmdPrintsHelp(t *testing.T) {
	out, err := runCmd(t, configCmd())
	if err != nil {
		t.Fatalf("bare config: %v", err)
	}
	for _, want := range []string{"secret store", "set", "set-master-key", "list", "rm"} {
		if !strings.Contains(out, want) {
			t.Errorf("config help missing %q: %q", want, out)
		}
	}
}

// TestBuildServeHTTPHandlerEngineError: an unopenable db (a directory) makes
// engine.New fail, and buildServeHTTPHandler propagates that error.
func TestBuildServeHTTPHandlerEngineError(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.DBPath = t.TempDir() // a directory: sqlite cannot open it
	if _, _, _, err := buildServeHTTPHandler(cfg); err == nil {
		t.Error("buildServeHTTPHandler with an unopenable db should fail")
	}
}
