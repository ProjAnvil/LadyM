//go:build !enterprise

// Personal edition: the management console (console/dist, see the console
// package) is embedded in the api node — `ladym serve --http` serves it at
// "/". The enterprise variant of this file (console_mount_enterprise.go) is a
// 404 stub; the console's serving logic itself lives in console.Mount.
package api

import (
	"net/http"

	"github.com/ProjAnvil/LadyM/console"
)

// mountConsole mounts the embedded management console at "/", with SPA
// fallback. Deliberately outside the auth middleware (NewHandler mounts before
// wrapping): the login page must load before anyone can authenticate; only
// /api/* is gated.
func mountConsole(mux *http.ServeMux) {
	console.Mount(mux)
}
