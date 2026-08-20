// Mounting the embedded console on an HTTP mux. The console package is a pure
// asset package (no LadyM dependencies); both the personal-edition api node
// (api/console_mount_personal.go) and the enterprise ladymconsole binary
// (cmd/ladymconsole, via cli.NewConsoleCmd) mount it through here, so the serving
// semantics — static assets from the embed FS, SPA fallback to index.html,
// JSON 404 for unknown /api paths — live in exactly one place.
package console

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
)

// Mount registers the embedded console at mux's "/". It is registered without
// a method constraint so unknown paths (any method) reach the handler — it
// 404s the /api/ ones and serves the SPA for the rest. The mount is
// deliberately NOT behind any auth middleware: the login page itself has to
// load before anyone can authenticate; only /api/* is gated (by the api
// package's middleware).
func Mount(mux *http.ServeMux) {
	mux.Handle("/", &spaHandler{fsys: Dist()})
}

// spaHandler serves the embedded console build with SPA fallback.
type spaHandler struct {
	fsys fs.FS
}

func (c *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	// Unknown /api paths must not fall back to the SPA.
	if strings.HasPrefix(p, "api/") {
		writeNotFound(w, r.URL.Path)
		return
	}
	if p != "" {
		if f, err := c.fsys.Open(p); err == nil {
			_ = f.Close()
			http.FileServer(http.FS(c.fsys)).ServeHTTP(w, r)
			return
		}
	}
	// "/" and unknown client-side routes get index.html.
	b, err := fs.ReadFile(c.fsys, "index.html")
	if err != nil {
		http.Error(w, "console assets not embedded", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

// writeNotFound mirrors the api package's JSON error shape ({"error": ...})
// without importing it — the dependency direction is api → console, never the
// reverse.
func writeNotFound(w http.ResponseWriter, path string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	b, err := json.Marshal(map[string]string{"error": "not found: " + path})
	if err != nil {
		return // a path string cannot fail to marshal
	}
	_, _ = w.Write(b)
}
