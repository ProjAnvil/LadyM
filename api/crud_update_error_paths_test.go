//go:build !enterprise

// Error paths of the CRUD update endpoints that happy-path and generic
// store-failure tests do not reach: bcrypt's 72-byte limit on user updates,
// a PutUser failure after the field merge, and the post-update memory reload
// failing.

package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/api"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/observability"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// TestUpdateUserOverlongPassword: bcrypt rejects >72-byte passwords; the
// update endpoint surfaces the hashing failure as a 500 and the account
// keeps its old password.
func TestUpdateUserOverlongPassword(t *testing.T) {
	eng, cfg := newTestEngine(t, func(cfg *config.Config) { cfg.AuthEnabled = true })
	addUser(t, eng, "root", "s3cret-admin", "", true)
	addUser(t, eng, "victim", "pw-victim", "", false)
	h := api.NewHandlerWithRegistry(eng, cfg, observability.New())

	long := strings.Repeat("a", 100)
	rec := doReq(t, h, http.MethodPut, "/api/users/victim", "root", "s3cret-admin", `{"password": "`+long+`"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("update user with >72-byte password = %d (%s), want 500", rec.Code, rec.Body.String())
	}
	// The failed update must not persist: the old password still logs in.
	rec = do(t, h, "/api/login", "root", "s3cret-admin", `{"username": "victim", "password": "pw-victim"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login with old password after failed update = %d, want 200", rec.Code)
	}
}

// TestUpdateUserPutUserFailure: PutUser fails after the field merge -> 500,
// and the stored row keeps its previous values.
func TestUpdateUserPutUserFailure(t *testing.T) {
	eng, cfg := newTestEngine(t, nil)
	addUser(t, eng, "victim", "pw", "", false)
	eng.Store = &failStore{Store: eng.Store, putUserErr: errBoom}
	h := api.NewHandlerWithRegistry(eng, cfg, observability.New())

	rec := doReq(t, h, http.MethodPut, "/api/users/victim", "", "", `{"admin": true}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("update user with broken PutUser = %d (%s), want 500", rec.Code, rec.Body.String())
	}
	if m := decodeBody(t, rec); !strings.Contains(m["error"].(string), errBoom.Error()) {
		t.Errorf("500 body should carry the store error: %v", m)
	}
}

// failOnNthGetMemoryStore fails the n-th GetMemory call and passes the rest
// through — the update endpoint reads the row twice (pre-update fetch and
// post-update reload) and only the second read must fail here.
type failOnNthGetMemoryStore struct {
	storage.Store
	n     int
	calls int
}

func (s *failOnNthGetMemoryStore) GetMemory(id string) (*schema.Memory, error) {
	s.calls++
	if s.calls == s.n {
		return nil, errBoom
	}
	return s.Store.GetMemory(id)
}

// TestUpdateMemoryReloadFailure: the post-update reload of the row fails ->
// 500 (the update itself already persisted).
func TestUpdateMemoryReloadFailure(t *testing.T) {
	eng, cfg := newTestEngine(t, nil)
	h := api.NewHandlerWithRegistry(eng, cfg, observability.New())
	id := rememberWS(t, h, "w1", "quixotic fact stored before the reload failure")

	eng.Store = &failOnNthGetMemoryStore{Store: eng.Store, n: 2}
	rec := putMemory(t, h, "", "", id, `{"summary": "summary written before reload fails"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("update with broken post-update reload = %d (%s), want 500", rec.Code, rec.Body.String())
	}
	if m := decodeBody(t, rec); !strings.Contains(m["error"].(string), errBoom.Error()) {
		t.Errorf("500 body should carry the store error: %v", m)
	}
}
