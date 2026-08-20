package cli

// Tests for the --server remote mode: every data command can talk to a
// `ladym serve --http` data-plane (api package) over HTTP instead of opening
// a local engine. The fake servers here record the exact request (method,
// path, auth header, body) and respond with canned JSON so the tests pin down
// both the wire protocol and the stdout parity with the local path.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// remoteReq is one request the fake server saw.
type remoteReq struct {
	method string
	path   string
	auth   string
	body   string
}

// fakeRemoteServer records every request and answers with respond.
func fakeRemoteServer(t *testing.T, respond func(w http.ResponseWriter, body string)) (*httptest.Server, func() []remoteReq) {
	t.Helper()
	var mu sync.Mutex
	var reqs []remoteReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		reqs = append(reqs, remoteReq{r.Method, r.URL.Path, r.Header.Get("Authorization"), string(b)})
		mu.Unlock()
		respond(w, string(b))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []remoteReq {
		mu.Lock()
		defer mu.Unlock()
		return append([]remoteReq(nil), reqs...)
	}
}

// singleReq asserts the fake server saw exactly one request and returns it.
func singleReq(t *testing.T, reqs func() []remoteReq) remoteReq {
	t.Helper()
	got := reqs()
	if len(got) != 1 {
		t.Fatalf("fake server saw %d requests, want 1", len(got))
	}
	return got[0]
}

func jsonOK(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, body)
}

// ---- remember ----

func TestRemoteRemember(t *testing.T) {
	srv, reqs := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, `{"id":"mem-1","hash":"0123456789abcdef"}`)
	})
	out, err := runCmd(t, rememberCmd(), "--server", srv.URL, "--user", "alice", "--password", "pw-1",
		"-w", "ws1", "--tags", "a, b", "--source", "test", "the sky is blue on clear days")
	if err != nil {
		t.Fatalf("remote remember: %v", err)
	}
	// stdout must match the local path byte for byte.
	if want := "remembered id=mem-1 hash=01234567\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	rq := singleReq(t, reqs)
	if rq.method != http.MethodPost || rq.path != "/api/remember" {
		t.Errorf("request = %s %s, want POST /api/remember", rq.method, rq.path)
	}
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:pw-1")); rq.auth != want {
		t.Errorf("Authorization = %q, want %q", rq.auth, want)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(rq.body), &body); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	if body["content"] != "the sky is blue on clear days" {
		t.Errorf("body content = %v", body["content"])
	}
	if body["source"] != "test" {
		t.Errorf("body source = %v", body["source"])
	}
	if body["workspace"] != "ws1" {
		t.Errorf("body workspace = %v (want the -w value sent to the server)", body["workspace"])
	}
	tags, _ := body["tags"].([]any)
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("body tags = %v, want [a b]", body["tags"])
	}
}

func TestRemoteRememberGated(t *testing.T) {
	srv, _ := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, `{"id":null,"hash":null,"gated":"dropped","reason":"noise"}`)
	})
	out, err := runCmd(t, rememberCmd(), "--server", srv.URL, "lol ok")
	if err != nil {
		t.Fatalf("remote remember gated: %v", err)
	}
	if want := "dropped reason=noise (gated; not persisted)\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

// ---- record ----

func TestRemoteRecord(t *testing.T) {
	srv, reqs := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, `{"id":"ev-1","layer":"L1_episodic","type":"event"}`)
	})
	out, err := runCmd(t, recordCmd(), "--server", srv.URL,
		"--agent", "tester", "--action", "ran tests",
		"--observation", "all green", "--outcome", "pass", "--tags", "ci")
	if err != nil {
		t.Fatalf("remote record: %v", err)
	}
	if want := "recorded id=ev-1 layer=L1_episodic type=event\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	rq := singleReq(t, reqs)
	if rq.path != "/api/record_event" {
		t.Errorf("path = %s, want /api/record_event", rq.path)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(rq.body), &body); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	for k, want := range map[string]string{
		"agent": "tester", "action": "ran tests",
		"observation": "all green", "outcome": "pass",
	} {
		if body[k] != want {
			t.Errorf("body %s = %v, want %q", k, body[k], want)
		}
	}
	tags, _ := body["tags"].([]any)
	if len(tags) != 1 || tags[0] != "ci" {
		t.Errorf("body tags = %v, want [ci]", body["tags"])
	}
}

// ---- recall ----

const remoteRecallBody = `{
  "query": "capital of France",
  "tier_reached": 1,
  "reflected_sufficient": true,
  "elapsed_ms": 1.5,
  "results": [
    {"score": 0.9, "tier": 1, "via": ["semantic"],
     "memory": {"id": "mem-9", "layer": "L2_semantic", "type": "fact",
                "summary": "paris", "content": "the capital of France is Paris",
                "source": "cli", "tags": []}}
  ]
}`

func TestRemoteRecallTable(t *testing.T) {
	srv, reqs := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, remoteRecallBody)
	})
	out, err := runCmd(t, recallCmd(), "--server", srv.URL, "-w", "ws1",
		"--top-k", "3", "capital of France")
	if err != nil {
		t.Fatalf("remote recall: %v", err)
	}
	if !strings.Contains(out, "recall: capital of France  (tier 1, 1.5ms)\n") {
		t.Errorf("output missing recall header:\n%s", out)
	}
	if !strings.Contains(out, "mem-9") || !strings.Contains(out, "paris") {
		t.Errorf("output missing result row:\n%s", out)
	}
	rq := singleReq(t, reqs)
	if rq.path != "/api/recall" {
		t.Errorf("path = %s, want /api/recall", rq.path)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(rq.body), &body); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	if body["query"] != "capital of France" {
		t.Errorf("body query = %v", body["query"])
	}
	if body["top_k"] != float64(3) {
		t.Errorf("body top_k = %v, want 3", body["top_k"])
	}
	if body["workspace"] != "ws1" {
		t.Errorf("body workspace = %v, want ws1", body["workspace"])
	}
}

func TestRemoteRecallJSON(t *testing.T) {
	srv, _ := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, remoteRecallBody)
	})
	out, err := runCmd(t, recallCmd(), "--server", srv.URL, "--json", "capital of France")
	if err != nil {
		t.Fatalf("remote recall --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if payload["query"] != "capital of France" {
		t.Errorf("JSON payload query = %v", payload["query"])
	}
	results, _ := payload["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("JSON payload results = %v", payload["results"])
	}
	r0, _ := results[0].(map[string]any)
	if r0["id"] != "mem-9" {
		t.Errorf("JSON result id = %v (flat cli shape expected, not the wire shape)", r0["id"])
	}
}

func TestRemoteRecallNoMatch(t *testing.T) {
	srv, _ := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, `{"query":"nothing","tier_reached":1,"reflected_sufficient":false,"elapsed_ms":0.5,"results":[]}`)
	})
	out, err := runCmd(t, recallCmd(), "--server", srv.URL, "nothing")
	if err != nil {
		t.Fatalf("remote recall: %v", err)
	}
	if want := "no memories matched\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestRemoteRecallCodeOnly(t *testing.T) {
	srv, reqs := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, remoteRecallBody)
	})
	if _, err := runCmd(t, recallCmd(), "--server", srv.URL, "--code", "func main"); err != nil {
		t.Fatalf("remote recall --code: %v", err)
	}
	rq := singleReq(t, reqs)
	if !strings.Contains(rq.body, `"code_only":true`) {
		t.Errorf("body missing code_only=true: %s", rq.body)
	}
}

// ---- stats / forget / link / consolidate ----

func TestRemoteStats(t *testing.T) {
	srv, reqs := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, `{"total_memories":3,"by_layer":{"L2_semantic":3},"by_type":{"fact":3},`+
			`"edges":1,"code_symbols":2,"workspaces":["ws1"],"db_path":"/srv/ladym.db",`+
			`"avg_tokens_per_memory":10.5}`)
	})
	out, err := runCmd(t, statsCmd(), "--server", srv.URL)
	if err != nil {
		t.Fatalf("remote stats: %v", err)
	}
	for _, want := range []string{"LadyM stats  db=/srv/ladym.db", "total memories: 3", "workspaces: ws1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	rq := singleReq(t, reqs)
	if rq.method != http.MethodPost || rq.path != "/api/stats" {
		t.Errorf("request = %s %s, want POST /api/stats", rq.method, rq.path)
	}
}

func TestRemoteStatsScopedWorkspace(t *testing.T) {
	srv, reqs := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, `{"total_memories":1,"workspaces":["a","b"],"db_path":"/srv/ladym.db"}`)
	})
	out, err := runCmd(t, statsCmd(), "--server", srv.URL, "-w", "a")
	if err != nil {
		t.Fatalf("remote stats -w: %v", err)
	}
	if !strings.Contains(out, "workspaces: a") || strings.Contains(out, "b") && strings.Contains(out, "workspaces: a, b") {
		t.Errorf("scoped workspace output wrong:\n%s", out)
	}
	// The -w scope must reach the server so its workspace enforcement can apply:
	// the request body carries the workspace.
	rq := singleReq(t, reqs)
	var body map[string]any
	if err := json.Unmarshal([]byte(rq.body), &body); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	if body["workspace"] != "a" {
		t.Errorf("stats body workspace = %v, want a", body["workspace"])
	}
}

// Without -w the stats request body still carries an (empty) workspace field,
// which the server resolves to its default workspace.
func TestRemoteStatsNoWorkspace(t *testing.T) {
	srv, reqs := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, `{"total_memories":0,"workspaces":[],"db_path":"/srv/ladym.db"}`)
	})
	if _, err := runCmd(t, statsCmd(), "--server", srv.URL); err != nil {
		t.Fatalf("remote stats: %v", err)
	}
	rq := singleReq(t, reqs)
	var body map[string]any
	if err := json.Unmarshal([]byte(rq.body), &body); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	if ws, ok := body["workspace"]; !ok || ws != "" {
		t.Errorf("stats body workspace = %v (present=%v), want empty string", ws, ok)
	}
}

func TestRemoteForget(t *testing.T) {
	srv, reqs := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, `{"forgotten":"mem-1"}`)
	})
	out, err := runCmd(t, forgetCmd(), "--server", srv.URL, "mem-1")
	if err != nil {
		t.Fatalf("remote forget: %v", err)
	}
	if want := "forgot mem-1\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	rq := singleReq(t, reqs)
	if rq.path != "/api/forget" || !strings.Contains(rq.body, `"memory_id":"mem-1"`) {
		t.Errorf("request = %s %s body=%s", rq.method, rq.path, rq.body)
	}
}

func TestRemoteLink(t *testing.T) {
	srv, reqs := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, `{"id":"e1","src":"a","dst":"b","relation":"causes"}`)
	})
	out, err := runCmd(t, linkCmd(), "--server", srv.URL, "-r", "causes", "a", "b")
	if err != nil {
		t.Fatalf("remote link: %v", err)
	}
	if want := "linked a -[causes]-> b (id=e1)\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	rq := singleReq(t, reqs)
	if rq.path != "/api/link" {
		t.Errorf("path = %s, want /api/link", rq.path)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(rq.body), &body); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	if body["src"] != "a" || body["dst"] != "b" || body["relation"] != "causes" {
		t.Errorf("body = %v", body)
	}
}

func TestRemoteConsolidate(t *testing.T) {
	srv, reqs := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, `{"kept_episodes":2,"promoted_to_semantic":1,"actions":{"ADD":1,"NOOP":1}}`)
	})
	out, err := runCmd(t, consolidateCmd(), "--server", srv.URL, "-w", "ws1")
	if err != nil {
		t.Fatalf("remote consolidate: %v", err)
	}
	if want := "consolidated 2 episodes: ADD=1 UPDATE=0 DELETE=0 NOOP=1\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	rq := singleReq(t, reqs)
	if rq.path != "/api/consolidate" || !strings.Contains(rq.body, `"workspace":"ws1"`) {
		t.Errorf("request = %s %s body=%s", rq.method, rq.path, rq.body)
	}
}

// ---- credential resolution ----

func TestResolveRemoteAuth(t *testing.T) {
	t.Setenv("LADYM_USER", "env-user")
	t.Setenv("LADYM_PASSWORD", "env-pass")
	a := resolveRemoteAuth("", "")
	if a.user != "env-user" || a.password != "env-pass" {
		t.Errorf("flags empty -> env: got %+v", a)
	}
	a = resolveRemoteAuth("flag-user", "flag-pass")
	if a.user != "flag-user" || a.password != "flag-pass" {
		t.Errorf("flags override env: got %+v", a)
	}
	t.Setenv("LADYM_USER", "")
	t.Setenv("LADYM_PASSWORD", "")
	a = resolveRemoteAuth("", "")
	if a.user != "" || a.password != "" {
		t.Errorf("nothing configured -> empty: got %+v", a)
	}
	// A username without a password is valid (passwordless server accounts).
	t.Setenv("LADYM_USER", "nopw")
	a = resolveRemoteAuth("", "")
	if a.user != "nopw" || a.password != "" {
		t.Errorf("user only: got %+v", a)
	}
}

func TestRemoteUserPasswordFromEnv(t *testing.T) {
	srv, reqs := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, `{"forgotten":"mem-1"}`)
	})
	t.Setenv("LADYM_USER", "env-user")
	t.Setenv("LADYM_PASSWORD", "env-pass")
	if _, err := runCmd(t, forgetCmd(), "--server", srv.URL, "mem-1"); err != nil {
		t.Fatalf("remote forget: %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("env-user:env-pass"))
	if got := singleReq(t, reqs).auth; got != want {
		t.Errorf("Authorization = %q, want %q from LADYM_USER/LADYM_PASSWORD", got, want)
	}
	// The flags beat the env.
	if _, err := runCmd(t, forgetCmd(), "--server", srv.URL, "--user", "flag-user", "--password", "flag-pass", "mem-1"); err != nil {
		t.Fatalf("remote forget: %v", err)
	}
	want = "Basic " + base64.StdEncoding.EncodeToString([]byte("flag-user:flag-pass"))
	if got := reqs()[1].auth; got != want {
		t.Errorf("Authorization = %q, want %q (flags override env)", got, want)
	}
}

func TestRemoteNoUserNoAuthHeader(t *testing.T) {
	srv, reqs := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, `{"forgotten":"mem-1"}`)
	})
	t.Setenv("LADYM_USER", "")
	t.Setenv("LADYM_PASSWORD", "")
	if _, err := runCmd(t, forgetCmd(), "--server", srv.URL, "mem-1"); err != nil {
		t.Fatalf("remote forget: %v", err)
	}
	if got := singleReq(t, reqs).auth; got != "" {
		t.Errorf("Authorization = %q, want empty (no-auth deployment)", got)
	}
}

// ---- error paths ----

func TestRemoteUnauthorized(t *testing.T) {
	srv, _ := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"unauthorized"}`)
	})
	_, err := runCmd(t, statsCmd(), "--server", srv.URL, "--user", "x", "--password", "wrong")
	if err == nil {
		t.Fatal("expected error for 401")
	}
	msg := err.Error()
	if !strings.Contains(msg, "unauthorized") {
		t.Errorf("error missing server message: %q", msg)
	}
	if strings.Contains(msg, "\n") {
		t.Errorf("error must be a single line: %q", msg)
	}
}

func TestRemoteServerError(t *testing.T) {
	srv, _ := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"store locked"}`)
	})
	_, err := runCmd(t, recallCmd(), "--server", srv.URL, "q")
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if !strings.Contains(err.Error(), "store locked") {
		t.Errorf("error missing server message: %q", err.Error())
	}
}

func TestRemoteUnreachable(t *testing.T) {
	// Port 1 is never listening — the client must surface an actionable
	// single-line error instead of a stack.
	_, err := runCmd(t, statsCmd(), "--server", "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cannot reach ladym server at http://127.0.0.1:1") {
		t.Errorf("error not actionable: %q", msg)
	}
	if strings.Contains(msg, "\n") {
		t.Errorf("error must be a single line: %q", msg)
	}
}

func TestRemoteDBServerMutuallyExclusive(t *testing.T) {
	srv, reqs := fakeRemoteServer(t, func(w http.ResponseWriter, _ string) {
		jsonOK(w, `{}`)
	})
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"remember", []string{"--db", "/tmp/x.db", "--server", srv.URL, "fact"}},
		{"recall", []string{"--db", "/tmp/x.db", "--server", srv.URL, "q"}},
		{"stats", []string{"--db", "/tmp/x.db", "--server", srv.URL}},
		{"forget", []string{"--db", "/tmp/x.db", "--server", srv.URL, "id"}},
		{"link", []string{"--db", "/tmp/x.db", "--server", srv.URL, "a", "b"}},
		{"consolidate", []string{"--db", "/tmp/x.db", "--server", srv.URL}},
		{"record", []string{"--db", "/tmp/x.db", "--server", srv.URL, "--agent", "a", "--action", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			switch tc.name {
			case "remember":
				_, err = runCmd(t, rememberCmd(), tc.args...)
			case "recall":
				_, err = runCmd(t, recallCmd(), tc.args...)
			case "stats":
				_, err = runCmd(t, statsCmd(), tc.args...)
			case "forget":
				_, err = runCmd(t, forgetCmd(), tc.args...)
			case "link":
				_, err = runCmd(t, linkCmd(), tc.args...)
			case "consolidate":
				_, err = runCmd(t, consolidateCmd(), tc.args...)
			case "record":
				_, err = runCmd(t, recordCmd(), tc.args...)
			}
			if err == nil {
				t.Fatalf("%s: expected mutual-exclusion error", tc.name)
			}
			if !strings.Contains(err.Error(), "mutually exclusive") {
				t.Errorf("%s: error = %q, want 'mutually exclusive'", tc.name, err.Error())
			}
		})
	}
	if n := len(reqs()); n != 0 {
		t.Errorf("fake server saw %d requests; mutually-exclusive calls must not hit the network", n)
	}
}
