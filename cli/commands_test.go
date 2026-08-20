//go:build !enterprise

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/code"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
)

// ---- helpers ----

// isolateEnv points HOME at a temp dir (so ~/.ladym and ~/.ladyM are never
// touched) and returns a temp db path for --db.
func isolateEnv(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return filepath.Join(t.TempDir(), "ladym.db")
}

// setGlobalConfigPath overrides the --config global for the duration of a test.
func setGlobalConfigPath(t *testing.T, p string) {
	t.Helper()
	old := globalConfigPath
	globalConfigPath = p
	t.Cleanup(func() { globalConfigPath = old })
}

var idRe = regexp.MustCompile(`id=(\S+)`)

// rememberFact remembers content through the CLI and returns its memory id.
func rememberFact(t *testing.T, db, content string) string {
	t.Helper()
	out, err := runCmd(t, rememberCmd(), "--db", db, content)
	if err != nil {
		t.Fatalf("remember %q: %v", content, err)
	}
	m := idRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("remember output missing id: %q", out)
	}
	return m[1]
}

// ---- small helpers ----

func TestSplitTags(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , , b ", []string{"a", "b"}},
		{",,,", nil},
	}
	for _, tc := range cases {
		got := splitTags(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitTags(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitTags(%q) = %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
	// Empty input returns a non-nil slice (callers range over it).
	if got := splitTags(""); got == nil {
		t.Error("splitTags(\"\") returned nil, want empty slice")
	}
}

func TestShortHash(t *testing.T) {
	if got := shortHash("0123456789abcdef"); got != "01234567" {
		t.Errorf("shortHash long = %q, want first 8 chars", got)
	}
	if got := shortHash("abc"); got != "abc" {
		t.Errorf("shortHash short = %q, want unchanged", got)
	}
	if got := shortHash(""); got != "" {
		t.Errorf("shortHash empty = %q, want empty", got)
	}
}

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("hello", 10); got != "hello" {
		t.Errorf("truncateStr no-op = %q", got)
	}
	if got := truncateStr("hello world", 5); got != "hello" {
		t.Errorf("truncateStr cut = %q", got)
	}
	// Multibyte runes must be truncated by rune count, not bytes.
	if got := truncateStr("héllo wörld", 5); got != "héllo" {
		t.Errorf("truncateStr multibyte = %q, want %q", got, "héllo")
	}
}

func TestWriteIndexReportSkippedUnchanged(t *testing.T) {
	report := &code.IndexReport{FilesSeen: 4, FilesIndexed: 1, FilesSkippedUnchanged: 3}
	var buf bytes.Buffer
	writeIndexReport(&buf, report)
	out := buf.String()
	if !strings.Contains(out, "indexed 1/4 files") {
		t.Errorf("output missing summary line:\n%s", out)
	}
	if !strings.Contains(out, "skipped unchanged: 3") {
		t.Errorf("output missing skipped-unchanged line:\n%s", out)
	}
	if strings.Contains(out, "errors:") {
		t.Errorf("output should not mention errors when there are none:\n%s", out)
	}
}

// ---- root / config / engine wiring ----

func TestNewRootCmdRegistersSubcommands(t *testing.T) {
	root := newRootCmd()
	want := []string{"remember", "record", "recall", "index", "consolidate",
		"stats", "forget", "link", "serve", "worker", "config"}
	for _, name := range want {
		if _, _, err := root.Find([]string{name}); err != nil {
			t.Errorf("root command missing subcommand %q: %v", name, err)
		}
	}
	if f := root.PersistentFlags().Lookup("config"); f == nil {
		t.Error("root missing persistent --config flag")
	}
	if f := root.PersistentFlags().Lookup("debug"); f == nil {
		t.Error("root missing persistent --debug flag")
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	isolateEnv(t)
	setGlobalConfigPath(t, "")
	cfg, err := loadConfig("/tmp/x.db", "ws1")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.DBPath != "/tmp/x.db" || cfg.Workspace != "ws1" {
		t.Errorf("overrides not applied: db=%s ws=%s", cfg.DBPath, cfg.Workspace)
	}
	// Empty overrides leave defaults in place.
	cfg, err = loadConfig("", "")
	if err != nil {
		t.Fatalf("loadConfig empty: %v", err)
	}
	if cfg.DBPath == "" || cfg.Workspace == "" {
		t.Errorf("defaults missing: db=%q ws=%q", cfg.DBPath, cfg.Workspace)
	}
}

func TestLoadConfigBadPath(t *testing.T) {
	isolateEnv(t)
	setGlobalConfigPath(t, filepath.Join(t.TempDir(), "nope.toml"))
	if _, err := loadConfig("", ""); err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestNewEngine(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	eng, err := newEngine(db, "ws1")
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	defer eng.Close()
	if eng.Config.DBPath != db || eng.Config.Workspace != "ws1" {
		t.Errorf("engine config = db:%s ws:%s", eng.Config.DBPath, eng.Config.Workspace)
	}

	// A db path that is a directory surfaces the engine.New error branch.
	if _, err := newEngine(t.TempDir(), ""); err == nil {
		t.Error("expected error for db path that is a directory")
	}
}

func TestExecuteHelpSucceeds(t *testing.T) {
	old := os.Args
	os.Args = []string{"ladym"}
	t.Cleanup(func() { os.Args = old })
	if _, err := captureStdout(t, func() error {
		Execute()
		return nil
	}); err != nil {
		t.Fatalf("capture: %v", err)
	}
}

// TestExecuteHelperProcess is re-run in a subprocess by the exit-path tests
// below: Execute() calls os.Exit on error, which cannot be observed in-process.
func TestExecuteHelperProcess(t *testing.T) {
	if os.Getenv("LADYM_CLI_EXEC_HELPER") != "1" {
		return
	}
	os.Args = append([]string{"ladym"}, strings.Fields(os.Getenv("LADYM_CLI_EXEC_ARGS"))...)
	Execute()
	os.Exit(0) // Execute returned without calling os.Exit
}

// runExecHelper runs TestExecuteHelperProcess in a subprocess and returns its
// combined output and exit code.
func runExecHelper(t *testing.T, args string, extraEnv ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestExecuteHelperProcess$")
	cmd.Env = append(os.Environ(),
		"LADYM_CLI_EXEC_HELPER=1",
		"LADYM_CLI_EXEC_ARGS="+args,
		"HOME="+t.TempDir(),
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err == nil {
		return out.String(), 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String(), ee.ExitCode()
	}
	t.Fatalf("helper process: %v", err)
	return "", -1
}

func TestExecuteUnknownCommandExitsNonZero(t *testing.T) {
	out, code := runExecHelper(t, "boguscmd")
	if code != 1 {
		t.Errorf("exit code = %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "ladym:") {
		t.Errorf("stderr missing 'ladym:' prefix:\n%s", out)
	}
}

func TestExecuteDebugPrintsFullError(t *testing.T) {
	out, code := runExecHelper(t, "--debug boguscmd")
	if code != 1 {
		t.Errorf("exit code = %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "unknown command") {
		t.Errorf("debug output missing error detail:\n%s", out)
	}
}

func TestExecuteExitErrorCodePropagates(t *testing.T) {
	// `config rm` on a missing key prints once and exits 1 via exitError.
	out, code := runExecHelper(t, "config rm NOPE")
	if code != 1 {
		t.Errorf("exit code = %d, want 1:\n%s", code, out)
	}
	if n := strings.Count(out, "no such key NOPE"); n != 1 {
		t.Errorf("'no such key NOPE' printed %d times, want 1:\n%s", n, out)
	}
}

func TestExecuteConfigErrorMessage(t *testing.T) {
	// openai embedding without any key is a *config.ConfigError — the CLI
	// prints the actionable message, not a traceback.
	out, code := runExecHelper(t, "remember hello",
		"LADYM_EMBEDDING=openai", "LADYM_EMBEDDING_API_KEY_ENV=LADYM_TEST_MISSING_KEY")
	if code != 1 {
		t.Errorf("exit code = %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "ladym:") {
		t.Errorf("output missing 'ladym:' prefix:\n%s", out)
	}
}

// ---- data commands ----

func TestRememberCmd(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	out, err := runCmd(t, rememberCmd(), "--db", db, "--tags", "a, b", "--source", "test",
		"the sky is blue on clear days")
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if !strings.Contains(out, "remembered id=") {
		t.Errorf("output missing 'remembered id=':\n%s", out)
	}
}

func TestRememberCmdGatedNoise(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	// Every token is a built-in noise word — the attention gate drops it.
	out, err := runCmd(t, rememberCmd(), "--db", db, "lol ok")
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if !strings.Contains(out, "dropped reason=noise") {
		t.Errorf("output missing gated-drop line:\n%s", out)
	}
}

func TestRememberCmdArgsAndEngineErrors(t *testing.T) {
	isolateEnv(t)
	// Missing the content arg.
	if _, err := runCmd(t, rememberCmd()); err == nil {
		t.Error("expected error for missing arg")
	}
	// Engine construction failure.
	setGlobalConfigPath(t, filepath.Join(t.TempDir(), "nope.toml"))
	if _, err := runCmd(t, rememberCmd(), "some content"); err == nil {
		t.Error("expected error for bad config")
	}
}

func TestRecordCmd(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	out, err := runCmd(t, recordCmd(), "--db", db,
		"--agent", "tester", "--action", "ran tests",
		"--observation", "all green", "--outcome", "pass", "--tags", "ci")
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if !strings.Contains(out, "recorded id=") || !strings.Contains(out, "L1_episodic") {
		t.Errorf("output missing recorded line:\n%s", out)
	}
}

func TestRecordCmdRequiredFlags(t *testing.T) {
	isolateEnv(t)
	if _, err := runCmd(t, recordCmd(), "--agent", "a"); err == nil {
		t.Error("expected error when --action is missing")
	}
}

func TestRecordCmdEngineError(t *testing.T) {
	isolateEnv(t)
	setGlobalConfigPath(t, filepath.Join(t.TempDir(), "nope.toml"))
	if _, err := runCmd(t, recordCmd(), "--agent", "a", "--action", "b"); err == nil {
		t.Error("expected error for bad config")
	}
}

func TestRecallCmdTable(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	id := rememberFact(t, db, "the capital of France is Paris")
	out, err := runCmd(t, recallCmd(), "--db", db, "the capital of France is Paris")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(out, "recall:") || !strings.Contains(out, id) {
		t.Errorf("table output missing header or id %s:\n%s", id, out)
	}
}

func TestRecallCmdJSON(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	rememberFact(t, db, "water boils at 100 degrees Celsius")
	out, err := runCmd(t, recallCmd(), "--db", db, "--json", "--top-k", "3",
		"water boils at 100 degrees Celsius")
	if err != nil {
		t.Fatalf("recall --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	results, ok := payload["results"].([]any)
	if !ok || len(results) == 0 {
		t.Errorf("JSON payload missing results:\n%s", out)
	}
	if payload["query"] != "water boils at 100 degrees Celsius" {
		t.Errorf("JSON payload query = %v", payload["query"])
	}
}

func TestRecallCmdNoMatch(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	out, err := runCmd(t, recallCmd(), "--db", db, "nothing stored yet")
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(out, "no memories matched") {
		t.Errorf("output missing 'no memories matched':\n%s", out)
	}
}

func TestRecallCmdCodeOnly(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	// Nothing indexed — the code-only path still runs and reports no matches.
	out, err := runCmd(t, recallCmd(), "--db", db, "--code", "func main")
	if err != nil {
		t.Fatalf("recall --code: %v", err)
	}
	if !strings.Contains(out, "no memories matched") {
		t.Errorf("output missing 'no memories matched':\n%s", out)
	}
}

func TestRecallCmdEngineError(t *testing.T) {
	isolateEnv(t)
	setGlobalConfigPath(t, filepath.Join(t.TempDir(), "nope.toml"))
	if _, err := runCmd(t, recallCmd(), "q"); err == nil {
		t.Error("expected error for bad config")
	}
	if _, err := runCmd(t, recallCmd()); err == nil {
		t.Error("expected error for missing arg")
	}
}

func TestIndexCmd(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	root := t.TempDir()
	src := "package sample\n\n// Add sums two ints.\nfunc Add(a, b int) int { return a + b }\n"
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, indexCmd(), "--db", db, root)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if !strings.Contains(out, "indexed 1/1 files") {
		t.Errorf("output missing index summary:\n%s", out)
	}
	// --force / --languages flags parse and run.
	if _, err := runCmd(t, indexCmd(), "--db", db, "--force", "--languages", " go ,python", root); err != nil {
		t.Fatalf("index --force --languages: %v", err)
	}
}

func TestIndexCmdEngineError(t *testing.T) {
	isolateEnv(t)
	setGlobalConfigPath(t, filepath.Join(t.TempDir(), "nope.toml"))
	if _, err := runCmd(t, indexCmd(), t.TempDir()); err == nil {
		t.Error("expected error for bad config")
	}
	// A db path that is a directory fails inside engine.New.
	setGlobalConfigPath(t, "")
	if _, err := runCmd(t, indexCmd(), "--db", t.TempDir(), t.TempDir()); err == nil {
		t.Error("expected error for db path that is a directory")
	}
}

func TestConsolidateCmd(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	out, err := runCmd(t, consolidateCmd(), "--db", db)
	if err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if !strings.Contains(out, "consolidated 0 episodes") {
		t.Errorf("output missing consolidation summary:\n%s", out)
	}
	setGlobalConfigPath(t, filepath.Join(t.TempDir(), "nope.toml"))
	if _, err := runCmd(t, consolidateCmd()); err == nil {
		t.Error("expected error for bad config")
	}
}

func TestStatsCmd(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	rememberFact(t, db, "the moon orbits the earth")
	out, err := runCmd(t, statsCmd(), "--db", db)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if !strings.Contains(out, "LadyM stats") || !strings.Contains(out, "total memories: 1") {
		t.Errorf("output missing stats lines:\n%s", out)
	}
	// Workspace scoping goes through the -w flag.
	out, err = runCmd(t, statsCmd(), "--db", db, "-w", "default")
	if err != nil {
		t.Fatalf("stats -w: %v", err)
	}
	if !strings.Contains(out, "workspaces: default") {
		t.Errorf("output missing scoped workspace:\n%s", out)
	}
	setGlobalConfigPath(t, filepath.Join(t.TempDir(), "nope.toml"))
	if _, err := runCmd(t, statsCmd()); err == nil {
		t.Error("expected error for bad config")
	}
}

func TestForgetCmd(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	id := rememberFact(t, db, "forget-me fact about otters")
	out, err := runCmd(t, forgetCmd(), "--db", db, id)
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	if !strings.Contains(out, "forgot "+id) {
		t.Errorf("output missing 'forgot %s':\n%s", id, out)
	}
	// Args validation.
	if _, err := runCmd(t, forgetCmd()); err == nil {
		t.Error("expected error for missing arg")
	}
	setGlobalConfigPath(t, filepath.Join(t.TempDir(), "nope.toml"))
	if _, err := runCmd(t, forgetCmd(), "x"); err == nil {
		t.Error("expected error for bad config")
	}
}

func TestLinkCmd(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	a := rememberFact(t, db, "git rebase rewrites history")
	b := rememberFact(t, db, "force-push after rebase is dangerous")
	out, err := runCmd(t, linkCmd(), "--db", db, "-r", "causes", a, b)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if !strings.Contains(out, "linked "+a+" -[causes]-> "+b) {
		t.Errorf("output missing linked line:\n%s", out)
	}
	// Linking a missing memory surfaces the engine error branch.
	if _, err := runCmd(t, linkCmd(), "--db", db, a, "no-such-id"); err == nil {
		t.Error("expected error linking a missing memory")
	}
	// Args validation.
	if _, err := runCmd(t, linkCmd(), "only-one"); err == nil {
		t.Error("expected error for missing arg")
	}
	setGlobalConfigPath(t, filepath.Join(t.TempDir(), "nope.toml"))
	if _, err := runCmd(t, linkCmd(), "a", "b"); err == nil {
		t.Error("expected error for bad config")
	}
}

// ---- serve / worker ----

func TestServeCmd(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	// Guarantee stdin EOF so the MCP loop returns immediately.
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	_ = w.Close()
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldStdin })

	cmd := serveCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--db", db})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("serve with EOF stdin: %v", err)
	}
}

func TestServeCmdConfigError(t *testing.T) {
	isolateEnv(t)
	setGlobalConfigPath(t, filepath.Join(t.TempDir(), "nope.toml"))
	if _, err := runCmd(t, serveCmd()); err == nil {
		t.Error("expected error for bad config")
	}
}

func TestWorkerCmdOnce(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	if _, err := runCmd(t, workerCmd(), "--db", db, "--once"); err != nil {
		t.Fatalf("worker --once: %v", err)
	}
	setGlobalConfigPath(t, filepath.Join(t.TempDir(), "nope.toml"))
	if _, err := runCmd(t, workerCmd(), "--once"); err == nil {
		t.Error("expected error for bad config")
	}
	// A db path that is a directory fails inside engine.New.
	setGlobalConfigPath(t, "")
	if _, err := runCmd(t, workerCmd(), "--db", t.TempDir(), "--once"); err == nil {
		t.Error("expected error for db path that is a directory")
	}
}

func TestRunWorkerLoopOncePropagatesError(t *testing.T) {
	eng, err := engine.New(config.ForTesting(t.TempDir()))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	eng.Close() // cycles against a closed store fail
	if err := runWorkerLoop(eng, true, 0, ""); err == nil {
		t.Error("expected cycle error to propagate in --once mode")
	}
}

func TestRunWorkerLoopContinuesAfterError(t *testing.T) {
	eng, err := engine.New(config.ForTesting(t.TempDir()))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	eng.Close()
	// Loop mode logs the failure and sleeps instead of returning. The loop can
	// never exit on its own, so run it in a goroutine with a long interval and
	// only assert it does not return promptly (the leaked goroutine is sleeping
	// when the test process exits).
	done := make(chan error, 1)
	go func() { done <- runWorkerLoop(eng, false, 3600, "") }()
	select {
	case err := <-done:
		t.Fatalf("loop returned %v, want it to keep running", err)
	case <-time.After(300 * time.Millisecond):
	}
}

// ---- config subcommands (secret store) ----

func TestConfigCmdStructure(t *testing.T) {
	cmd := configCmd()
	for _, name := range []string{"set", "set-master-key", "reset-master-key", "list", "rm"} {
		if _, _, err := cmd.Find([]string{name}); err != nil {
			t.Errorf("config missing subcommand %q: %v", name, err)
		}
	}
	if _, err := runCmd(t, configCmd(), "extra-arg"); err == nil {
		t.Error("expected error for positional arg (NoArgs)")
	}
}

func TestConfigSetListRM(t *testing.T) {
	isolateEnv(t)
	// list on an empty store.
	out, err := runCmd(t, configListCmd())
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if !strings.Contains(out, "no secrets stored") {
		t.Errorf("output missing 'no secrets stored':\n%s", out)
	}
	// set without a master key fails.
	if _, err := runCmd(t, configSetCmd(), "FOO", "bar"); err == nil {
		t.Error("expected set to fail without a master key")
	}
	// Initialize the master key, then set/list/rm round-trip.
	if _, err := runCmd(t, configSetMasterKeyCmd(), "test-master-key"); err != nil {
		t.Fatalf("set-master-key: %v", err)
	}
	out, err = runCmd(t, configSetCmd(), "FOO", "bar")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if !strings.Contains(out, "stored FOO") {
		t.Errorf("output missing 'stored FOO':\n%s", out)
	}
	out, err = runCmd(t, configListCmd())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "FOO") {
		t.Errorf("list output missing FOO:\n%s", out)
	}
	out, err = runCmd(t, configRMCmd(), "FOO")
	if err != nil {
		t.Fatalf("rm: %v", err)
	}
	if !strings.Contains(out, "removed FOO") {
		t.Errorf("output missing 'removed FOO':\n%s", out)
	}
}

func TestConfigSetMasterKeyGenerated(t *testing.T) {
	isolateEnv(t)
	// No arg → a random key is generated and its path is printed.
	out, err := runCmd(t, configSetMasterKeyCmd())
	if err != nil {
		t.Fatalf("set-master-key: %v", err)
	}
	if !strings.Contains(out, "generated a random master key") {
		t.Errorf("output missing generated-key message:\n%s", out)
	}
}

func TestConfigSetMasterKeyExplicit(t *testing.T) {
	isolateEnv(t)
	out, err := runCmd(t, configSetMasterKeyCmd(), "my-key")
	if err != nil {
		t.Fatalf("set-master-key: %v", err)
	}
	if !strings.Contains(out, "master key set") {
		t.Errorf("output missing 'master key set':\n%s", out)
	}
	// Too many args.
	if _, err := runCmd(t, configSetMasterKeyCmd(), "a", "b"); err == nil {
		t.Error("expected error for too many args")
	}
}

func TestConfigResetMasterKey(t *testing.T) {
	isolateEnv(t)
	// Without an existing master key the reset fails.
	if _, err := runCmd(t, configResetMasterKeyCmd()); err == nil {
		t.Error("expected reset to fail without a master key")
	}
	if _, err := runCmd(t, configSetMasterKeyCmd(), "old-key"); err != nil {
		t.Fatalf("set-master-key: %v", err)
	}
	out, err := runCmd(t, configResetMasterKeyCmd(), "new-key")
	if err != nil {
		t.Fatalf("reset-master-key: %v", err)
	}
	if !strings.Contains(out, "master key reset") {
		t.Errorf("output missing reset message:\n%s", out)
	}
	// Too many args.
	if _, err := runCmd(t, configResetMasterKeyCmd(), "a", "b"); err == nil {
		t.Error("expected error for too many args")
	}
}

func TestConfigRMCmdArgs(t *testing.T) {
	isolateEnv(t)
	if _, err := runCmd(t, configRMCmd()); err == nil {
		t.Error("expected error for missing arg")
	}
}

func TestConfigSetMasterKeyRefusesExistingSecrets(t *testing.T) {
	isolateEnv(t)
	if _, err := runCmd(t, configSetMasterKeyCmd(), "k1"); err != nil {
		t.Fatalf("set-master-key: %v", err)
	}
	if _, err := runCmd(t, configSetCmd(), "FOO", "bar"); err != nil {
		t.Fatalf("set: %v", err)
	}
	// A fresh master key would orphan existing secrets — must refuse.
	if _, err := runCmd(t, configSetMasterKeyCmd(), "k2"); err == nil {
		t.Error("expected set-master-key to refuse when secrets exist")
	}
}

// TestConfigListAndRMStoreReadError covers the store-error branches of
// `config list` / `config rm` by making secrets.enc unreadable.
func TestConfigListAndRMStoreReadError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".ladyM")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	enc := filepath.Join(dir, "secrets.enc")
	if err := os.WriteFile(enc, []byte("A = B\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(enc, 0o600) })
	if _, err := runCmd(t, configListCmd()); err == nil {
		t.Error("expected list to fail on unreadable secrets.enc")
	}
	if _, err := runCmd(t, configRMCmd(), "A"); err == nil {
		t.Error("expected rm to fail on unreadable secrets.enc")
	}
}

// ---- root-level integration through newRootCmd ----

func TestRootCmdRememberAndStats(t *testing.T) {
	db := isolateEnv(t)
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", "", "remember", "--db", db, "integration fact via root"})
	out, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("root remember: %v\n%s", err, out)
	}
	if !strings.Contains(out, "remembered id=") {
		t.Errorf("output missing remembered line:\n%s", out)
	}
}
