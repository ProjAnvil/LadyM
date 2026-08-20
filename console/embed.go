// Package console embeds the built LadyM management console (Vue 3 + Vite,
// sources in this directory, build via `make console-build`) so `ladym serve
// --http` can serve it at "/" without node on the machine.
//
// console/dist is committed to the repo: a plain `go build` always works.
// Rebuild dist only when the frontend sources change.
package console

import (
	"embed"
	"io/fs"
)

//go:embed dist
var dist embed.FS

// Dist returns the built console assets rooted at dist/ (index.html at the
// root, hashed bundles under assets/).
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // embed guarantees dist/ exists
	}
	return sub
}
