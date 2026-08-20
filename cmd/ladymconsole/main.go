//go:build enterprise

// ladymconsole is the enterprise console-role binary: the management console
// (embedded Vue SPA) plus the full /api data-plane, against the same Postgres
// deployment as the api nodes. It is a separate main so the ladym binary
// (api/worker roles) does not embed the console assets at all.
package main

import (
	"github.com/ProjAnvil/LadyM/cli"
	"github.com/ProjAnvil/LadyM/console"
)

func main() {
	cli.ExecuteConsole(console.Mount)
}
