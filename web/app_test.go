package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "ladym.toml")
	if err := os.WriteFile(cfgPath, []byte("[embedding]\nprovider = \"hashing\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := newMux(cfgPath, tmp)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func postForm(t *testing.T, url string, form url.Values) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func secretNames(t *testing.T, srv *httptest.Server) []string {
	t.Helper()
	out := getJSON(t, srv.URL+"/api/secrets")
	names := []string{}
	for _, n := range out["names"].([]any) {
		names = append(names, n.(string))
	}
	return names
}

// Regression: DELETE /api/secrets/{name} used to 404 because only the exact
// "/api/secrets" pattern was registered, which net/http does not match
// against subpaths.
func TestSecretsAPIFlow(t *testing.T) {
	srv := newTestServer(t)

	// No master key yet: Set must fail.
	resp := postForm(t, srv.URL+"/api/secrets", url.Values{"name": {"K"}, "value": {"v"}})
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("Set without master key: status %d, want 400", resp.StatusCode)
	}

	// Set master key.
	resp = postForm(t, srv.URL+"/api/master-key", nil)
	resp.Body.Close() // form POST without JSON body → decode yields zero payload → error 400
	resp, err := http.Post(srv.URL+"/api/master-key", "application/json", strings.NewReader(`{"key":"test-master-key"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("set-master-key: status %d", resp.StatusCode)
	}
	if out := getJSON(t, srv.URL+"/api/secrets"); out["master_key_set"] != true {
		t.Fatal("master_key_set should be true")
	}

	// Store a secret.
	resp = postForm(t, srv.URL+"/api/secrets", url.Values{"name": {"OPENAI_API_KEY"}, "value": {"sk-test"}})
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("Set: status %d", resp.StatusCode)
	}
	names := secretNames(t, srv)
	if len(names) != 1 || names[0] != "OPENAI_API_KEY" {
		t.Fatalf("names = %v", names)
	}

	// Delete it via the subtree route.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/secrets/OPENAI_API_KEY", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("DELETE /api/secrets/OPENAI_API_KEY: status %d, want 200", resp.StatusCode)
	}
	if names := secretNames(t, srv); len(names) != 0 {
		t.Fatalf("names after delete = %v", names)
	}
}

func TestSecretsDeleteMethodGuards(t *testing.T) {
	srv := newTestServer(t)

	// GET on the subtree is not allowed.
	resp, err := http.Get(srv.URL + "/api/secrets/OPENAI_API_KEY")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Fatalf("GET subtree: status %d, want 405", resp.StatusCode)
	}

	// POST with missing fields → 400.
	resp = postForm(t, srv.URL+"/api/secrets", url.Values{"name": {"ONLY_NAME"}})
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("POST missing value: status %d, want 400", resp.StatusCode)
	}
}

func TestIndexAndNotFound(t *testing.T) {
	srv := newTestServer(t)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET /: status %d", resp.StatusCode)
	}
	if !strings.Contains(string(body[:n]), "LadyM config") {
		t.Fatal("index page should contain 'LadyM config'")
	}

	resp, err = http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("GET /nope: status %d, want 404", resp.StatusCode)
	}
}
