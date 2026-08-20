//go:build enterprise

// Enterprise edition: the api node does NOT embed the management console — it
// is served by the standalone ladymconsole binary (cmd/ladymconsole)
// against the same Postgres deployment. This stub keeps the api package free
// of the console package in enterprise builds.
package api

import (
	"net/http"
	"strings"
)

// mountConsole is the enterprise-edition stub: "/" and every other non-/api
// path answer a JSON 404 (same shape as unknown /api paths) pointing at the
// console binary. Unknown /api paths keep the exact personal-edition message
// so /api/* behavior does not drift between editions.
func mountConsole(mux *http.ServeMux) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "not found: "+r.URL.Path)
			return
		}
		writeError(w, http.StatusNotFound,
			"console not embedded in enterprise build; run the `ladymconsole` binary")
	})
}
