package cli

// Dependency-direction acceptance (T3, binary-level split): the console embed
// (github.com/ProjAnvil/LadyM/console) must reach exactly the binaries that
// serve the SPA —
//   - personal:  cmd/ladym (api.NewHandler mounts it in `serve --http`);
//   - enterprise: cmd/ladymconsole only; cmd/ladym (api/worker roles) must NOT
//     depend on the console package at all.
//
// Implemented as `go list` subprocesses; skipped when go is not on PATH.

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/storage"
)

const consolePkg = "github.com/ProjAnvil/LadyM/console"

func goListDeps(t *testing.T, tags []string, pkg string) string {
	t.Helper()
	args := []string{"list"}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	args = append(args, "-deps", pkg)
	out, err := exec.Command("go", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("go list %s: %v\n%s", pkg, err, out)
	}
	return string(out)
}

func depsContain(deps, pkg string) bool {
	for _, line := range strings.Split(deps, "\n") {
		if line == pkg {
			return true
		}
	}
	return false
}

func TestConsoleDependencyByEdition(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	if storage.Edition == "enterprise" {
		ladym := goListDeps(t, []string{"enterprise"}, "github.com/ProjAnvil/LadyM/cmd/ladym")
		if depsContain(ladym, consolePkg) {
			t.Error("enterprise ladym binary (api/worker roles) must not depend on " + consolePkg)
		}
		ladymconsole := goListDeps(t, []string{"enterprise"}, "github.com/ProjAnvil/LadyM/cmd/ladymconsole")
		if !depsContain(ladymconsole, consolePkg) {
			t.Error("enterprise ladymconsole binary must depend on " + consolePkg)
		}
		return
	}
	ladym := goListDeps(t, nil, "github.com/ProjAnvil/LadyM/cmd/ladym")
	if !depsContain(ladym, consolePkg) {
		t.Error("personal ladym binary must embed the console (serve --http mounts it)")
	}
}
