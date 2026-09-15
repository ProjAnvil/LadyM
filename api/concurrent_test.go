//go:build !enterprise

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// roundTrip issues one POST against the handler from a goroutine (unlike do,
// it never touches *testing.T) and reports the status code and decoded body.
func roundTrip(h http.Handler, path, body string) (int, map[string]any, error) {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return rec.Code, nil, fmt.Errorf("%s -> %d: %s", path, rec.Code, rec.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		return rec.Code, nil, fmt.Errorf("%s: non-JSON response: %w", path, err)
	}
	return rec.Code, m, nil
}

// TestConcurrentWorkspacesIsolated hammers the handler with 32 goroutines
// across 4 workspaces (remember + record_event + recall + stats per round)
// and then verifies no workspace's unique marker leaks into another
// workspace's recall results.
func TestConcurrentWorkspacesIsolated(t *testing.T) {
	h := newTestHandler(t, nil)

	const nWS = 4
	const nGo = 32
	const rounds = 4
	workspaces := make([]string, nWS)
	for i := range workspaces {
		workspaces[i] = fmt.Sprintf("conc-ws-%d", i)
	}

	errs := make(chan error, nGo*rounds*4)
	var wg sync.WaitGroup
	for g := range nGo {
		wg.Go(func() {
			ws := workspaces[g%nWS]
			for r := range rounds {
				marker := fmt.Sprintf("quixplotron-%s-%d-%d", ws, g, r)
				body := fmt.Sprintf(`{"content":"%s: the deploy pin is %d","workspace":%q}`, marker, g*100+r, ws)
				if _, _, err := roundTrip(h, "/api/remember", body); err != nil {
					errs <- err
					continue
				}
				body = fmt.Sprintf(`{"agent":"loadbot","action":"ping-%s","observation":%q,"workspace":%q}`, marker, marker, ws)
				if _, _, err := roundTrip(h, "/api/record_event", body); err != nil {
					errs <- err
					continue
				}
				body = fmt.Sprintf(`{"query":"quixplotron","workspace":%q}`, ws)
				if _, _, err := roundTrip(h, "/api/recall", body); err != nil {
					errs <- err
					continue
				}
				body = fmt.Sprintf(`{"workspace":%q}`, ws)
				if _, _, err := roundTrip(h, "/api/stats", body); err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// Cross-workspace isolation: each workspace's marker prefix is found when
	// recalling its own workspace (some writes may be gate-dropped as
	// near-duplicates, so assert on the prefix, not one exact marker) and
	// absent from every other workspace.
	for _, ws := range workspaces {
		prefix := "quixplotron-" + ws
		_, m, err := roundTrip(h, "/api/recall", fmt.Sprintf(`{"query":%q,"workspace":%q,"top_k":10}`, prefix+" deploy pin", ws))
		if err != nil {
			t.Fatal(err)
		}
		results, _ := m["results"].([]any)
		found := false
		for _, res := range results {
			mem, _ := res.(map[string]any)["memory"].(map[string]any)
			if s, _ := mem["content"].(string); strings.Contains(s, prefix) {
				found = true
			}
		}
		if !found {
			t.Errorf("recall in %s did not return any of its own %q facts", ws, prefix)
		}
		for _, other := range workspaces {
			if other == ws {
				continue
			}
			_, m, err := roundTrip(h, "/api/recall", fmt.Sprintf(`{"query":%q,"workspace":%q,"top_k":10}`, prefix+" deploy pin", other))
			if err != nil {
				t.Fatal(err)
			}
			results, _ := m["results"].([]any)
			for _, res := range results {
				mem, _ := res.(map[string]any)["memory"].(map[string]any)
				if s, _ := mem["content"].(string); strings.Contains(s, prefix) {
					t.Errorf("marker %q of %s leaked into recall of %s", prefix, ws, other)
				}
			}
		}
	}
}
