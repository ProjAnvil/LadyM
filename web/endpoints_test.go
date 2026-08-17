package web

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/config"
)

// newTestServerWithConfig builds a mux around a config file with the given
// extra TOML content appended (secretsDir = t.TempDir()).
func newTestServerWithConfig(t *testing.T, extraToml string) *httptest.Server {
	t.Helper()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "ladym.toml")
	content := extraToml + "[embedding]\nprovider = \"hashing\"\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newMux(cfgPath, tmp))
	t.Cleanup(srv.Close)
	return srv
}

// newBadConfigServer builds a mux around an unparseable config file so every
// handler that calls config.Load takes its 500 branch.
func newBadConfigServer(t *testing.T) *httptest.Server {
	t.Helper()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "ladym.toml")
	if err := os.WriteFile(cfgPath, []byte("not = = valid toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newMux(cfgPath, tmp))
	t.Cleanup(srv.Close)
	return srv
}

func statusOf(t *testing.T, resp *http.Response) int {
	t.Helper()
	resp.Body.Close()
	return resp.StatusCode
}

func TestCheckMark(t *testing.T) {
	if got := checkMark(true); got != "✓" {
		t.Fatalf("checkMark(true) = %q", got)
	}
	if got := checkMark(false); got != "✗" {
		t.Fatalf("checkMark(false) = %q", got)
	}
}

func TestTomlScalar(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{true, "true"},
		{false, "false"},
		{42, "42"},
		{1.5, "1.5"},
		{"plain", `"plain"`},
		{`a"b\c`, `"a\"b\\c"`},
		{nil, `"<nil>"`}, // default branch: fmt.Sprint + quoting
	}
	for _, c := range cases {
		if got := tomlScalar(c.in); got != c.want {
			t.Errorf("tomlScalar(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWriteToml(t *testing.T) {
	cfg := config.Default()
	cfg.DBPath = `db "quoted" \path`
	path := filepath.Join(t.TempDir(), "out.toml")
	if err := writeToml(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`db_path = "db \"quoted\" \\path"`,
		"[embedding]",
		"provider = \"hashing\"",
		"[llm]",
		"provider = \"none\"",
		"[activation]",
		"similarity = 1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("written toml missing %q:\n%s", want, text)
		}
	}

	// Error branch: parent directory does not exist.
	if err := writeToml(filepath.Join(t.TempDir(), "nope", "out.toml"), cfg); err == nil {
		t.Fatal("writeToml to missing dir should fail")
	}
}

func TestApplyForm(t *testing.T) {
	form := url.Values{
		"db_path":                    {"/tmp/x.db"},
		"workspace":                  {"ws"},
		"embedding_provider":         {"http"},
		"embedding_base_url":         {"http://localhost:9"},
		"embedding_model":            {"m"},
		"embedding_api_key_env":      {"MY_KEY"},
		"embedding_fallback":         {"hashing"},
		"embedding_query_cache_size": {"7"},
		"llm_provider":               {"http"},
		"llm_base_url":               {"http://localhost:8"},
		"llm_model":                  {"gpt-x"},
		"llm_api_key_env":            {"MY_LLM_KEY"},
		"llm_structured_method":      {"json_mode"},
		"activation_similarity":      {"0.9"},
		"activation_recency":         {"0.8"},
		"activation_frequency":       {"0.7"},
	}
	r := httptest.NewRequest(http.MethodPost, "/save", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	cfg := config.Default()
	applyForm(cfg, r)

	if cfg.DBPath != "/tmp/x.db" || cfg.Workspace != "ws" {
		t.Errorf("core fields: %+v", cfg)
	}
	if cfg.EmbeddingProvider != "http" || cfg.EmbeddingBaseURL != "http://localhost:9" ||
		cfg.EmbeddingModel != "m" || cfg.EmbeddingAPIKeyEnv != "MY_KEY" ||
		cfg.EmbeddingFallback != "hashing" || cfg.EmbeddingQueryCacheSize != 7 {
		t.Errorf("embedding fields: %+v", cfg)
	}
	if cfg.LLMProvider != "http" || cfg.LLMBaseURL != "http://localhost:8" ||
		cfg.LLMModel != "gpt-x" || cfg.LLMAPIKeyEnv != "MY_LLM_KEY" ||
		cfg.LLMStructuredMethod != "json_mode" {
		t.Errorf("llm fields: %+v", cfg)
	}
	if cfg.Activation.Similarity != 0.9 || cfg.Activation.Recency != 0.8 || cfg.Activation.Frequency != 0.7 {
		t.Errorf("activation fields: %+v", cfg.Activation)
	}

	// Empty form leaves defaults untouched.
	r2 := httptest.NewRequest(http.MethodPost, "/save", nil)
	cfg2 := config.Default()
	applyForm(cfg2, r2)
	if cfg2.DBPath != config.Default().DBPath || cfg2.LLMProvider != "none" {
		t.Errorf("empty form changed defaults: %+v", cfg2)
	}
}

func TestSaveEndpoint(t *testing.T) {
	workDir := t.TempDir()
	t.Chdir(workDir) // /save writes ./ladym.toml relative to cwd
	t.Setenv("HOME", t.TempDir())
	srv := newTestServer(t)

	resp := postForm(t, srv.URL+"/save", url.Values{
		"db_path":   {"/tmp/saved.db"},
		"workspace": {"saved-ws"},
	})
	body := make([]byte, 1024)
	n, _ := resp.Body.Read(body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("POST /save: status %d", resp.StatusCode)
	}
	if !strings.Contains(string(body[:n]), "Saved") {
		t.Fatalf("POST /save body = %q", body[:n])
	}
	data, err := os.ReadFile(filepath.Join(workDir, "ladym.toml"))
	if err != nil {
		t.Fatal("save should write ./ladym.toml:", err)
	}
	if !strings.Contains(string(data), `db_path = "/tmp/saved.db"`) {
		t.Fatalf("saved toml missing db_path:\n%s", data)
	}
}

func TestStatsEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dbPath := filepath.Join(t.TempDir(), "stats.db")
	srv := newTestServerWithConfig(t, "db_path = "+`"`+dbPath+`"`+"\n")

	resp, err := http.Get(srv.URL + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 1024)
	n, _ := resp.Body.Read(body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET /stats: status %d body %q", resp.StatusCode, body[:n])
	}
	if !strings.Contains(string(body[:n]), "total memories:") {
		t.Fatalf("GET /stats body = %q", body[:n])
	}
}

func TestHandlerConfigLoadErrors(t *testing.T) {
	srv := newBadConfigServer(t)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, resp); got != 500 {
		t.Fatalf("GET / with bad config: status %d, want 500", got)
	}

	resp = postForm(t, srv.URL+"/save", url.Values{"db_path": {"x"}})
	if got := statusOf(t, resp); got != 500 {
		t.Fatalf("POST /save with bad config: status %d, want 500", got)
	}

	resp, err = http.Get(srv.URL + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, resp); got != 500 {
		t.Fatalf("GET /stats with bad config: status %d, want 500", got)
	}
}

func TestStatsEndpointEngineError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// db_path pointing at a directory makes engine.New fail → 500.
	dir := t.TempDir()
	srv := newTestServerWithConfig(t, "db_path = "+`"`+dir+`"`+"\n")

	resp, err := http.Get(srv.URL + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, resp); got != 500 {
		t.Fatalf("GET /stats with directory db_path: status %d, want 500", got)
	}
}

func TestSaveEndpointWriteError(t *testing.T) {
	// /save writes ./ladym.toml; a read-only cwd makes writeToml fail → 500.
	ro := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Chdir(ro)
	t.Setenv("HOME", t.TempDir())
	srv := newTestServer(t)

	resp := postForm(t, srv.URL+"/save", url.Values{"db_path": {"x"}})
	if got := statusOf(t, resp); got != 500 {
		t.Fatalf("POST /save with read-only cwd: status %d, want 500", got)
	}
}

func TestResetEndpoint(t *testing.T) {
	srv := newTestServer(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(srv.URL + "/reset")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("GET /reset: status %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("GET /reset Location = %q, want /", loc)
	}
}

func TestTestEmbeddingEndpoint(t *testing.T) {
	srv := newTestServer(t)

	// hashing provider works offline → ✓.
	resp := postForm(t, srv.URL+"/test/embedding", url.Values{"embedding_provider": {"hashing"}})
	body := make([]byte, 1024)
	n, _ := resp.Body.Read(body)
	resp.Body.Close()
	if !strings.Contains(string(body[:n]), "✓") {
		t.Fatalf("hashing health check body = %q", body[:n])
	}

	// Unknown provider → MakeProvider error → ✗.
	resp = postForm(t, srv.URL+"/test/embedding", url.Values{"embedding_provider": {"bogus"}})
	n, _ = resp.Body.Read(body)
	resp.Body.Close()
	if !strings.Contains(string(body[:n]), "✗") {
		t.Fatalf("bogus provider body = %q", body[:n])
	}
}

func TestTestLLMEndpoint(t *testing.T) {
	srv := newTestServer(t)
	read := func(resp *http.Response) string {
		body := make([]byte, 4096)
		n, _ := resp.Body.Read(body)
		resp.Body.Close()
		return string(body[:n])
	}

	// llm_provider=none → heuristic mode short-circuit.
	if body := read(postForm(t, srv.URL+"/test/llm", url.Values{"llm_provider": {"none"}})); !strings.Contains(body, "heuristic") {
		t.Fatalf("none provider body = %q", body)
	}

	// Unknown provider → MakeLLMProvider error → ✗.
	if body := read(postForm(t, srv.URL+"/test/llm", url.Values{"llm_provider": {"bogus"}})); !strings.Contains(body, "✗") {
		t.Fatalf("bogus provider body = %q", body)
	}

	// "NONE" slips past the handler's exact-match "none" guard, then
	// MakeLLMProvider lowercases it and returns a nil provider → heuristic.
	if body := read(postForm(t, srv.URL+"/test/llm", url.Values{"llm_provider": {"NONE"}})); !strings.Contains(body, "heuristic") {
		t.Fatalf("nil provider body = %q", body)
	}

	// Fake OpenAI-compatible endpoint → ✓; long reply also covers the
	// 20-char truncation branch.
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"pong-pong-pong-pong-pong-pong"}}]}`))
	}))
	defer fake.Close()
	body := read(postForm(t, srv.URL+"/test/llm", url.Values{
		"llm_provider": {"http"},
		"llm_base_url": {fake.URL},
		"llm_model":    {"m"},
	}))
	if !strings.Contains(body, "✓") {
		t.Fatalf("http provider body = %q", body)
	}

	// Unreachable endpoint → Complete error → ✗.
	body = read(postForm(t, srv.URL+"/test/llm", url.Values{
		"llm_provider": {"http"},
		"llm_base_url": {"http://127.0.0.1:1"},
		"llm_model":    {"m"},
	}))
	if !strings.Contains(body, "✗") {
		t.Fatalf("unreachable provider body = %q", body)
	}
}

func TestMasterKeyResetFlow(t *testing.T) {
	srv := newTestServer(t)
	post := func(payload string) *http.Response {
		resp, err := http.Post(srv.URL+"/api/master-key", "application/json", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// Reset without an existing master key → readAESKey error → 400.
	if got := statusOf(t, post(`{"reset":true,"key":"new-key"}`)); got != 400 {
		t.Fatalf("reset without master key: status %d, want 400", got)
	}

	// Set, store a secret, then reset → re-encrypts → 200.
	if got := statusOf(t, post(`{"key":"old-key"}`)); got != 200 {
		t.Fatalf("set master key: status %d, want 200", got)
	}
	resp := postForm(t, srv.URL+"/api/secrets", url.Values{"name": {"K"}, "value": {"v"}})
	resp.Body.Close()
	if got := statusOf(t, post(`{"reset":true,"key":"new-key"}`)); got != 200 {
		t.Fatalf("reset master key: status %d, want 200", got)
	}

	// Setting a fresh master key while secrets exist → 400.
	if got := statusOf(t, post(`{"key":"another"}`)); got != 400 {
		t.Fatalf("set master key over existing secrets: status %d, want 400", got)
	}

	// GET is not allowed on /api/master-key.
	resp, err := http.Get(srv.URL + "/api/master-key")
	if err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, resp); got != 405 {
		t.Fatalf("GET /api/master-key: status %d, want 405", got)
	}
}

func TestSecretsAPIGuards(t *testing.T) {
	srv := newTestServer(t)

	// Unsupported method on the collection route → 405.
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/secrets", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, resp); got != 405 {
		t.Fatalf("PUT /api/secrets: status %d, want 405", got)
	}

	// Empty name and nested names on the subtree route → 404.
	for _, path := range []string{"/api/secrets/", "/api/secrets/a/b"} {
		req, _ := http.NewRequest(http.MethodDelete, srv.URL+path, nil)
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if got := statusOf(t, resp); got != 404 {
			t.Fatalf("DELETE %s: status %d, want 404", path, got)
		}
	}

	// Delete failure surfaces as 400: make the secrets dir unwritable after
	// storing a secret so Remove's write-back fails.
	resp, err = http.Post(srv.URL+"/api/master-key", "application/json", strings.NewReader(`{"key":"k"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp = postForm(t, srv.URL+"/api/secrets", url.Values{"name": {"LOCKED"}, "value": {"v"}})
	resp.Body.Close()

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "ladym.toml")
	if err := os.WriteFile(cfgPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	lockedSrv := httptest.NewServer(newMux(cfgPath, tmp))
	defer lockedSrv.Close()
	resp, err = http.Post(lockedSrv.URL+"/api/master-key", "application/json", strings.NewReader(`{"key":"k"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp = postForm(t, lockedSrv.URL+"/api/secrets", url.Values{"name": {"LOCKED"}, "value": {"v"}})
	resp.Body.Close()
	// Remove first re-reads the store; making secrets.enc unreadable turns
	// that read into an error which the handler surfaces as 400.
	encPath := filepath.Join(tmp, "secrets.enc")
	if err := os.Chmod(encPath, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(encPath, 0o600)
	req, _ = http.NewRequest(http.MethodDelete, lockedSrv.URL+"/api/secrets/LOCKED", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, resp); got != 400 {
		t.Fatalf("DELETE with unwritable store: status %d, want 400", got)
	}
}

func TestOpenBrowser(t *testing.T) {
	makeExec := func(dir, name string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// macOS-style: only `open` on PATH.
	dir := t.TempDir()
	makeExec(dir, "open")
	t.Setenv("PATH", dir)
	if err := openBrowser("http://127.0.0.1/"); err != nil {
		t.Fatalf("openBrowser with fake open: %v", err)
	}

	// Linux-style: only `xdg-open` on PATH.
	dir2 := t.TempDir()
	makeExec(dir2, "xdg-open")
	t.Setenv("PATH", dir2)
	if err := openBrowser("http://127.0.0.1/"); err != nil {
		t.Fatalf("openBrowser with fake xdg-open: %v", err)
	}

	// Neither available → nil (no-op).
	t.Setenv("PATH", t.TempDir())
	if err := openBrowser("http://127.0.0.1/"); err != nil {
		t.Fatalf("openBrowser with no opener: %v", err)
	}
}

func TestRunServesAndOpensBrowser(t *testing.T) {
	// Fake `open` so the browser goroutine is exercised without launching
	// anything real.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "open"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HOME", t.TempDir())

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "ladym.toml")
	if err := os.WriteFile(cfgPath, []byte("[embedding]\nprovider = \"hashing\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Grab a free port, release it, and hand it to Run.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	errCh := make(chan error, 1)
	go func() { errCh <- Run(cfgPath, port, false) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("Run server never came up")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Give the browser goroutine (1s sleep + fake `open`) time to run.
	time.Sleep(1200 * time.Millisecond)
}
