//go:build !enterprise

// Embedding-provider and store failures on the engine-backed endpoints:
// recall/search_code embed the query at call time, record_event writes
// through the per-request Scope layers, consolidate scans episodic memories,
// and the memory-update endpoint re-embeds changed content. Every failure
// surfaces as a 500 carrying the engine error via engineError.

package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/api"
	"github.com/ProjAnvil/LadyM/observability"
)

// failEmbedProvider fails every embed call with errBoom.
type failEmbedProvider struct{}

func (failEmbedProvider) Embed(string) ([]float32, error)          { return nil, errBoom }
func (failEmbedProvider) EmbedBatch([]string) ([][]float32, error) { return nil, errBoom }
func (failEmbedProvider) Dim() int                                 { return 0 }
func (failEmbedProvider) HealthCheck() (bool, string)              { return false, errBoom.Error() }

// TestQueryEndpointsEmbedFailure: the query embedding fails -> 500 carrying
// the provider error (recall and search_code both embed the query).
func TestQueryEndpointsEmbedFailure(t *testing.T) {
	eng, cfg := newTestEngine(t, nil)
	eng.Provider = failEmbedProvider{}
	h := api.NewHandlerWithRegistry(eng, cfg, observability.New())

	for _, path := range []string{"/api/recall", "/api/search_code"} {
		rec := do(t, h, path, "", "", `{"query": "quixotic zeppelins"}`)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("POST %s with broken provider = %d, want 500", path, rec.Code)
		}
		if m := decodeBody(t, rec); !strings.Contains(m["error"].(string), errBoom.Error()) {
			t.Errorf("POST %s 500 body should carry the provider error: %v", path, m)
		}
	}
}

// TestRecordEventStoreFailure: the episodic Record write (PutMemory) fails
// -> 500.
func TestRecordEventStoreFailure(t *testing.T) {
	h := newFailHandler(t, nil, &failStore{putMemoryErr: errBoom})
	rec := do(t, h, "/api/record_event", "", "", `{"agent": "tester", "action": "explode"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("record_event with broken PutMemory = %d (%s), want 500", rec.Code, rec.Body.String())
	}
	if m := decodeBody(t, rec); !strings.Contains(m["error"].(string), errBoom.Error()) {
		t.Errorf("500 body should carry the store error: %v", m)
	}
}

// TestConsolidateStoreFailure: the episodic scan (IterMemories) fails -> 500.
func TestConsolidateStoreFailure(t *testing.T) {
	h := newFailHandler(t, nil, &failStore{iterErr: errBoom})
	rec := do(t, h, "/api/consolidate", "", "", `{}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("consolidate with broken IterMemories = %d (%s), want 500", rec.Code, rec.Body.String())
	}
	if m := decodeBody(t, rec); !strings.Contains(m["error"].(string), errBoom.Error()) {
		t.Errorf("500 body should carry the store error: %v", m)
	}
}

// TestUpdateMemoryEmbedFailure: changed content must be re-embedded; a
// provider failure is a 500 and leaves the stored row untouched.
func TestUpdateMemoryEmbedFailure(t *testing.T) {
	eng, cfg := newTestEngine(t, nil)
	h := api.NewHandlerWithRegistry(eng, cfg, observability.New())
	id := rememberWS(t, h, "w1", "quixotic fact stored before the provider failure")

	eng.Provider = failEmbedProvider{}
	rec := putMemory(t, h, "", "", id, `{"content": "rewritten quixotic content after failure"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("update with broken provider = %d (%s), want 500", rec.Code, rec.Body.String())
	}
	if m := decodeBody(t, rec); !strings.Contains(m["error"].(string), errBoom.Error()) {
		t.Errorf("500 body should carry the provider error: %v", m)
	}
}
