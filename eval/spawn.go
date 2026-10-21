//go:build eval

package eval

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// SpawnResult records one invocation of the real ladym binary.
type SpawnResult struct {
	DurationMs float64
	ExitCode   int
	Stdout     string
	Stderr     string
}

var (
	buildOnce sync.Once
	builtPath string
	buildErr  error
)

// ResolveBinary returns a usable ladym binary. It prefers LADYM_BIN so callers
// can pin a specific build, and otherwise compiles ./cmd/ladym fresh, once per
// test binary run. It deliberately ignores whatever `ladym` happens to be on
// PATH: that copy is usually an older release and would silently measure the
// wrong code.
func ResolveBinary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("LADYM_BIN"); p != "" {
		return p
	}
	buildOnce.Do(func() {
		root, err := repoRoot()
		if err != nil {
			buildErr = err
			return
		}
		dir, err := os.MkdirTemp("", "ladym-eval-bin")
		if err != nil {
			buildErr = err
			return
		}
		out := filepath.Join(dir, "ladym")
		cmd := exec.Command("go", "build", "-o", out, "./cmd/ladym")
		cmd.Dir = root
		if out_, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build ./cmd/ladym: %v: %s", err, out_)
			return
		}
		builtPath = out
	})
	if buildErr != nil {
		t.Fatalf("resolve ladym binary: %v", buildErr)
	}
	return builtPath
}

// repoRoot walks up from the package directory to the module root, so the build
// command runs against the right module regardless of how tests are invoked.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod found above the working directory")
		}
		dir = parent
	}
}

// SpawnHook runs the hook subcommand once, feeding payload on stdin, and times
// the run until process exit. A non-zero child exit lands in the result rather
// than as an error: only failing to run the binary at all is an error, because
// assertions decide what each exit code means.
func SpawnHook(binPath, dbPath, harness, payload string) (SpawnResult, error) {
	cmd := exec.Command(binPath, "hook", "--db", dbPath, "--harness", harness)
	cmd.Stdin = strings.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Pin the offline providers. Without this the child inherits whatever
	// ~/.ladyM/config.toml says, so a developer with a remote embedding provider
	// configured would make the harness network- and API-key-dependent, and the
	// timings would measure network latency instead of the write path.
	cmd.Env = append(os.Environ(), "LADYM_EMBEDDING=hashing", "LADYM_LLM=none")

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	res := SpawnResult{
		DurationMs: float64(elapsed.Nanoseconds()) / 1e6,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	return res, runErr
}
