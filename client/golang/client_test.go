//go:build !enterprise

package client

// End-to-end tests for the Go SDK: a real api.NewHandler (engine + config.
// ForTesting + hashing embeddings) behind httptest, exercised through every
// Client method, plus the error paths (401/403/404/unreachable/non-JSON
// bodies) and the *Error shape.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/api"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
	"golang.org/x/crypto/bcrypt"
)

// newTestServer brings up a real data-plane. With authEnabled, the given
// users are seeded into the users table.
func newTestServer(t *testing.T, authEnabled bool, users ...*schema.User) *httptest.Server {
	t.Helper()
	cfg := config.ForTesting(t.TempDir())
	cfg.AuthEnabled = authEnabled
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	for _, u := range users {
		if err := eng.Store.PutUser(u); err != nil {
			t.Fatalf("PutUser %s: %v", u.Username, err)
		}
	}
	srv := httptest.NewServer(api.NewHandler(eng, cfg))
	t.Cleanup(srv.Close)
	return srv
}

func adminUser(t *testing.T, username, password string) *schema.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return &schema.User{Username: username, PasswordHash: string(hash), Admin: true, CreatedAt: schema.Now()}
}

// asError unwraps err into *Error or fails the test.
func asError(t *testing.T, err error) *Error {
	t.Helper()
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("error is %T (%v), want *client.Error", err, err)
	}
	return cerr
}

// ---- ping / login ----

func TestPing(t *testing.T) {
	srv := newTestServer(t, false)
	if err := New(srv.URL).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestLogin(t *testing.T) {
	srv := newTestServer(t, true, adminUser(t, "alice", "s3cret"))

	u, err := New(srv.URL, WithAuth("alice", "s3cret")).Login(context.Background())
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if u.Username != "alice" || !u.Admin {
		t.Errorf("Login user = %+v, want alice admin", u)
	}

	_, err = New(srv.URL, WithAuth("alice", "wrong")).Login(context.Background())
	cerr := asError(t, err)
	if cerr.StatusCode != http.StatusUnauthorized || cerr.Message != "unauthorized" {
		t.Errorf("Login wrong password: Error = %+v, want 401 unauthorized", cerr)
	}
}

// ---- data plane roundtrip ----

func TestRememberRecallStatsForgetRoundtrip(t *testing.T) {
	srv := newTestServer(t, false)
	c := New(srv.URL)
	ctx := context.Background()

	rem, err := c.Remember(ctx, "the client sdk zephyr quixotic fact", []string{"sdk", "test"}, "")
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if rem.ID == "" || rem.Hash == "" || rem.Dropped() {
		t.Fatalf("Remember result = %+v, want persisted id+hash", rem)
	}

	rec, err := c.Recall(ctx, "client sdk zephyr quixotic", RecallOptions{})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	found := false
	for _, r := range rec.Results {
		if r.Memory.ID == rem.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("Recall did not return remembered id %s", rem.ID)
	}

	st, err := c.Stats(ctx, "")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.TotalMemories != 1 {
		t.Errorf("Stats total = %d, want 1", st.TotalMemories)
	}

	if err := c.Forget(ctx, rem.ID); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	st, err = c.Stats(ctx, "")
	if err != nil {
		t.Fatalf("Stats after forget: %v", err)
	}
	if st.TotalMemories != 0 {
		t.Errorf("Stats after forget total = %d, want 0", st.TotalMemories)
	}
}

func TestRecordEvent(t *testing.T) {
	srv := newTestServer(t, false)
	res, err := New(srv.URL).RecordEvent(context.Background(),
		"tester", "ran sdk tests", "all green", "pass", []string{"ci"}, "")
	if err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}
	if res.ID == "" || res.Layer == "" || res.Type == "" {
		t.Errorf("RecordEvent result = %+v, want id/layer/type", res)
	}
}

func TestConsolidate(t *testing.T) {
	srv := newTestServer(t, false)
	res, err := New(srv.URL).Consolidate(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if res == nil {
		t.Fatal("Consolidate result is nil")
	}
}

func TestLink(t *testing.T) {
	srv := newTestServer(t, false)
	c := New(srv.URL)
	ctx := context.Background()
	a, err := c.Remember(ctx, "link source fact about alpha", nil, "")
	if err != nil {
		t.Fatalf("Remember a: %v", err)
	}
	b, err := c.Remember(ctx, "link destination fact about beta", nil, "")
	if err != nil {
		t.Fatalf("Remember b: %v", err)
	}
	edgeID, err := c.Link(ctx, a.ID, b.ID, "causes")
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if edgeID == "" {
		t.Error("Link returned empty edge id")
	}
}

// ---- console CRUD: memories ----

func TestMemoriesCRUD(t *testing.T) {
	srv := newTestServer(t, false)
	c := New(srv.URL)
	ctx := context.Background()

	m1, err := c.Remember(ctx, "crud alpha memory", []string{"a"}, "")
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if _, err := c.Remember(ctx, "crud beta memory", []string{"b"}, ""); err != nil {
		t.Fatalf("Remember: %v", err)
	}

	list, err := c.ListMemories(ctx, MemoryFilter{})
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if list.Total != 2 || len(list.Memories) != 2 {
		t.Fatalf("ListMemories = %d/%d, want 2/2", len(list.Memories), list.Total)
	}

	page, err := c.ListMemories(ctx, MemoryFilter{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("ListMemories paged: %v", err)
	}
	if page.Total != 2 || len(page.Memories) != 1 {
		t.Errorf("paged ListMemories = %d/%d, want 1/2", len(page.Memories), page.Total)
	}

	summary, tag := "updated summary", "updated"
	if err := c.UpdateMemory(ctx, m1.ID, MemoryPatch{Summary: &summary, Tags: []string{tag}}); err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}
	list, err = c.ListMemories(ctx, MemoryFilter{})
	if err != nil {
		t.Fatalf("ListMemories after update: %v", err)
	}
	var updated *schema.Memory
	for _, m := range list.Memories {
		if m.ID == m1.ID {
			updated = m
		}
	}
	if updated == nil {
		t.Fatalf("updated memory %s not in list", m1.ID)
	}
	if updated.Summary != summary || len(updated.Tags) != 1 || updated.Tags[0] != tag {
		t.Errorf("updated memory = summary %q tags %v, want %q [%s]", updated.Summary, updated.Tags, summary, tag)
	}

	if err := c.DeleteMemory(ctx, m1.ID); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}
	err = c.DeleteMemory(ctx, m1.ID)
	cerr := asError(t, err)
	if cerr.StatusCode != http.StatusNotFound || !strings.Contains(cerr.Message, "memory not found") {
		t.Errorf("second DeleteMemory: Error = %+v, want 404 'memory not found'", cerr)
	}
}

// ---- console CRUD: users ----

func TestUsersCRUD(t *testing.T) {
	srv := newTestServer(t, true, adminUser(t, "root", "pw"))
	c := New(srv.URL, WithAuth("root", "pw"))
	ctx := context.Background()

	u, err := c.CreateUser(ctx, "bob", "bobpw", "bob-ws", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.Username != "bob" || u.Workspace != "bob-ws" || u.Admin {
		t.Errorf("CreateUser user = %+v", u)
	}
	if u.PasswordHash != "" {
		t.Errorf("PasswordHash leaked over the wire: %q", u.PasswordHash)
	}

	users, err := c.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("ListUsers = %d users, want 2 (root, bob)", len(users))
	}

	newPW, newWS := "newpw", "new-ws"
	if _, err := c.UpdateUser(ctx, "bob", UserPatch{Password: &newPW, Workspace: &newWS}); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	// The new password authenticates; the old one does not.
	bob := New(srv.URL, WithAuth("bob", "newpw"))
	if _, err := bob.Login(ctx); err != nil {
		t.Fatalf("Login with updated password: %v", err)
	}
	oldBob := New(srv.URL, WithAuth("bob", "bobpw"))
	if _, err := oldBob.Login(ctx); err == nil {
		t.Error("Login with old password succeeded after UpdateUser")
	}

	// Non-admin is forbidden from the users API.
	if _, err := bob.ListUsers(ctx); err == nil {
		t.Fatal("ListUsers as non-admin: expected 403")
	} else if cerr := asError(t, err); cerr.StatusCode != http.StatusForbidden {
		t.Errorf("ListUsers as non-admin: status = %d, want 403", cerr.StatusCode)
	}

	if err := c.DeleteUser(ctx, "bob"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	err = c.DeleteUser(ctx, "bob")
	if cerr := asError(t, err); cerr.StatusCode != http.StatusNotFound {
		t.Errorf("second DeleteUser: status = %d, want 404", cerr.StatusCode)
	}
}

// ---- error paths ----

func TestUnauthorizedWithoutCredentials(t *testing.T) {
	srv := newTestServer(t, true, adminUser(t, "alice", "s3cret"))
	_, err := New(srv.URL).Stats(context.Background(), "")
	cerr := asError(t, err)
	if cerr.StatusCode != http.StatusUnauthorized || cerr.Message != "unauthorized" {
		t.Errorf("Error = %+v, want 401 unauthorized", cerr)
	}
}

func TestErrorShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"store locked"}`)
	}))
	t.Cleanup(srv.Close)

	err := New(srv.URL).Forget(context.Background(), "mem-1")
	cerr := asError(t, err)
	if cerr.StatusCode != 500 || cerr.Message != "store locked" {
		t.Errorf("Error = %+v, want {500 store locked}", cerr)
	}
	if strings.Contains(cerr.Error(), "\n") {
		t.Errorf("Error() must be single-line: %q", cerr.Error())
	}
}

// TestErrorNonJSONBody: a non-JSON error body (proxy pages etc.) falls back
// to the trimmed body text as the message.
func TestErrorNonJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintln(w, "bad gateway")
	}))
	t.Cleanup(srv.Close)

	err := New(srv.URL).Forget(context.Background(), "mem-1")
	cerr := asError(t, err)
	if cerr.StatusCode != http.StatusBadGateway || cerr.Message != "bad gateway" {
		t.Errorf("Error = %+v, want {502 'bad gateway'}", cerr)
	}
	if strings.Contains(cerr.Message, "\n") {
		t.Errorf("Message must be single-line: %q", cerr.Message)
	}
}

// TestRememberGated decodes the attention-gate drop shape (id/hash null).
func TestRememberGated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":null,"hash":null,"gated":"dropped","reason":"noise"}`)
	}))
	t.Cleanup(srv.Close)

	res, err := New(srv.URL).Remember(context.Background(), "lol ok", nil, "")
	if err != nil {
		t.Fatalf("Remember gated: %v", err)
	}
	if !res.Dropped() || res.Reason != "noise" || res.ID != "" || res.Hash != "" {
		t.Errorf("gated result = %+v, want dropped reason=noise empty id/hash", res)
	}
}

func TestUnreachable(t *testing.T) {
	err := New("http://127.0.0.1:1").Ping(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "cannot reach ladym server at http://127.0.0.1:1") {
		t.Errorf("error not actionable: %q", err.Error())
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("error must be single-line: %q", err.Error())
	}
	var cerr *Error
	if errors.As(err, &cerr) {
		t.Errorf("network error must not be a *Error (no HTTP status), got %+v", cerr)
	}
}

// TestAuthHeaderSent pins the Basic-auth wire behaviour: credentials set via
// WithAuth produce the Basic header; without WithAuth no header is sent.
func TestAuthHeaderSent(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"total_memories":0,"workspaces":[],"db_path":""}`)
	}))
	t.Cleanup(srv.Close)

	if _, err := New(srv.URL, WithAuth("alice", "s3cret")).Stats(context.Background(), ""); err != nil {
		t.Fatalf("Stats: %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	if gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}

	gotAuth = ""
	if _, err := New(srv.URL).Stats(context.Background(), ""); err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q without credentials, want empty", gotAuth)
	}
}

// countingRoundTripper records how many requests pass through it.
type countingRoundTripper struct {
	n int
}

func (rt *countingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.n++
	return http.DefaultTransport.RoundTrip(req)
}

// WithHTTPClient swaps the transport (every request goes through the
// injected client); WithTimeout aborts a too-slow call with a timeout error.
func TestClientOptions(t *testing.T) {
	srv := newTestServer(t, false)
	rt := &countingRoundTripper{}
	c := New(srv.URL, WithHTTPClient(&http.Client{Transport: rt}))
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping with custom client: %v", err)
	}
	if rt.n != 1 {
		t.Errorf("custom http.Client saw %d requests, want 1", rt.n)
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	t.Cleanup(slow.Close)
	err := New(slow.URL, WithTimeout(50*time.Millisecond)).Ping(context.Background())
	if err == nil {
		t.Fatal("Ping with 50ms timeout against a 300ms server should fail")
	}
	if !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "timeout") {
		t.Errorf("timeout error = %v, want a deadline/timeout mention", err)
	}
}

// A 500 from the server becomes *Error{500, "boom"} on every method,
// including the ones whose error paths the roundtrip tests don't reach.
func TestServerErrorAcrossMethods(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"boom"}`)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL)
	ctx := context.Background()

	wantErr := func(name string, err error) {
		t.Helper()
		cerr := asError(t, err)
		if cerr.StatusCode != http.StatusInternalServerError || cerr.Message != "boom" {
			t.Errorf("%s error = %+v, want {500 'boom'}", name, cerr)
		}
	}

	_, err := c.RememberWithSource(ctx, "content", "test", nil, "")
	wantErr("RememberWithSource", err)
	_, err = c.Recall(ctx, "q", RecallOptions{Workspace: "w", TopK: 3, CodeOnly: true})
	wantErr("Recall", err)
	_, err = c.RecordEvent(ctx, "a", "act", "obs", "out", nil, "w")
	wantErr("RecordEvent", err)
	_, err = c.Consolidate(ctx, "w", 0)
	wantErr("Consolidate", err)
	_, err = c.Link(ctx, "s", "d", "related_to")
	wantErr("Link", err)
	// ListMemories with every filter set (covers the query-encoding branches).
	_, err = c.ListMemories(ctx, MemoryFilter{Workspace: "w", Layer: "L2_semantic", Type: "fact", Limit: 5, Offset: 10})
	wantErr("ListMemories", err)
	_, err = c.CreateUser(ctx, "u", "p", "w", true)
	wantErr("CreateUser", err)
	pw := "p2"
	_, err = c.UpdateUser(ctx, "u", UserPatch{Password: &pw})
	wantErr("UpdateUser", err)
	wantErr("DeleteUser", c.DeleteUser(ctx, "u"))
}
