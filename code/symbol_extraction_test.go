//go:build !enterprise

package code

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/storage"
	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// --- small pure helpers -------------------------------------------------

func TestItoa(t *testing.T) {
	cases := map[int]string{0: "0", 7: "7", 42: "42", -7: "-7", 1234: "1234", -1000: "-1000"}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("short", 10); got != "short" {
		t.Errorf("truncateStr short = %q", got)
	}
	if got := truncateStr("abcdefghij", 4); got != "abcd" {
		t.Errorf("truncateStr long = %q, want %q", got, "abcd")
	}
	// multi-byte runes must be truncated by rune count, not bytes
	if got := truncateStr("日本語abc", 3); got != "日本語" {
		t.Errorf("truncateStr unicode = %q, want %q", got, "日本語")
	}
}

func TestIsIdentifier(t *testing.T) {
	cases := map[string]bool{
		"": false, "_": true, "_x": true, "abc": true, "a1": true,
		"1a": false, "a-b": false, "a b": false, "9": false, "é": true,
	}
	for in, want := range cases {
		if got := isIdentifier(in); got != want {
			t.Errorf("isIdentifier(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestQualify(t *testing.T) {
	if got := qualify("mod", "Cls", "m"); got != "Cls.m" {
		t.Errorf("qualify with parent = %q", got)
	}
	if got := qualify("", "", "f"); got != "f" {
		t.Errorf("qualify bare = %q", got)
	}
	if got := qualify("mod", "", "f"); got != "mod.f" {
		t.Errorf("qualify with module = %q", got)
	}
}

func TestFirstNLines(t *testing.T) {
	body := "a\nb\nc\nd"
	if got := firstNLines(body, 2); got != "a\nb" {
		t.Errorf("firstNLines truncate = %q", got)
	}
	if got := firstNLines(body, 10); got != body {
		t.Errorf("firstNLines passthrough = %q", got)
	}
}

func TestModuleName(t *testing.T) {
	cases := map[string]string{
		"a/b.py":       "a.b",
		"a\\b.go":      "a.b",
		"pkg/index.js": "pkg",
		".py":          "module",
		"main.rs":      "main",
	}
	for rel, want := range cases {
		if got := moduleName(rel, DetectLanguage(rel)); got != want {
			t.Errorf("moduleName(%q) = %q, want %q", rel, got, want)
		}
	}
}

func TestShouldIgnore(t *testing.T) {
	if !shouldIgnore("proj/node_modules/pkg/index.js", nil) {
		t.Error("skipDirs path not ignored")
	}
	if !shouldIgnore("proj/generated_api.py", []string{"generated_*"}) {
		t.Error("glob match not ignored")
	}
	if !shouldIgnore("proj/build.log", []string{"build/"}) {
		t.Error("trailing-slash glob not ignored")
	}
	if shouldIgnore("proj/keep.py", []string{"generated_*"}) {
		t.Error("clean file ignored")
	}
}

func TestFileSummaryNoSymbols(t *testing.T) {
	got := fileSummary("data.txt", "text", nil)
	if !strings.Contains(got, "no symbols extracted") {
		t.Errorf("fileSummary empty = %q", got)
	}
}

// --- walkFiles ----------------------------------------------------------

func TestWalkFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("keep.py")
	write("node_modules/pkg/dep.js")
	write("gen/out_generated.py")

	paths, err := walkFiles(dir, []string{"_generated*"})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || filepath.Base(paths[0]) != "keep.py" {
		t.Errorf("walkFiles = %v, want just keep.py", paths)
	}

	// A missing root surfaces the walk error to the callback, which skips it.
	paths, err = walkFiles(filepath.Join(dir, "does-not-exist"), nil)
	if err != nil {
		t.Fatalf("walkFiles missing root err = %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("walkFiles missing root = %v, want empty", paths)
	}
}

// --- classify / extraction across languages ------------------------------

func extractOne(t *testing.T, src, lang string) []RawSymbol {
	t.Helper()
	syms, err := ExtractSymbols([]byte(src), lang, "m", "m."+lang, 40)
	if err != nil {
		t.Fatalf("ExtractSymbols(%s): %v", lang, err)
	}
	return syms
}

func kindsByName(syms []RawSymbol) map[string]string {
	out := map[string]string{}
	for _, s := range syms {
		out[s.Name] = s.Kind
	}
	return out
}

func TestClassifyJavaScript(t *testing.T) {
	syms := extractOne(t, `
function plain() { return 1 }

class Widget {
  render() { return plain() }
}
`, "javascript")
	kinds := kindsByName(syms)
	if kinds["plain"] != "function" {
		t.Errorf("plain kind = %q", kinds["plain"])
	}
	if kinds["Widget"] != "class" {
		t.Errorf("Widget kind = %q", kinds["Widget"])
	}
	if kinds["render"] != "method" {
		t.Errorf("render kind = %q", kinds["render"])
	}
}

// variable_declarator / arrow_function sit under a lexical_declaration, so
// extractTree never visits them; classify them directly.
func TestClassifyVariableDeclarator(t *testing.T) {
	src := []byte("const handler = () => { return 2 }\n")
	grammar := grammars.JavascriptLanguage()
	tree, err := gotreesitter.NewParser(grammar).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	spec := GetSpec("javascript")
	lex := tree.RootNode().Children()[0]
	var vd, arrow *gotreesitter.Node
	for _, ch := range lex.Children() {
		switch ch.Type(grammar) {
		case "variable_declarator":
			vd = ch
		}
	}
	if vd == nil {
		t.Fatal("variable_declarator not found")
	}
	for _, ch := range vd.Children() {
		if ch.Type(grammar) == "arrow_function" {
			arrow = ch
		}
	}
	if arrow == nil {
		t.Fatal("arrow_function not found")
	}
	if got := classify(vd, spec, grammar); got != "function" {
		t.Errorf("classify(variable_declarator) = %q, want function", got)
	}
	if got := classify(arrow, spec, grammar); got != "function" {
		t.Errorf("classify(arrow_function) = %q, want function", got)
	}
	// a node that is not a definition kind classifies to ""
	if got := classify(lex, spec, grammar); got != "" {
		t.Errorf("classify(lexical_declaration) = %q, want empty", got)
	}

	// arrow signature: the trailing "=>" is trimmed
	sig := signature(vd, src, spec, grammar, "function")
	if strings.HasSuffix(sig, "=>") || strings.HasSuffix(sig, "{") {
		t.Errorf("arrow signature = %q, want trimmed", sig)
	}
	if !strings.Contains(sig, "handler") {
		t.Errorf("arrow signature = %q", sig)
	}
}

func TestClassifyTypeScript(t *testing.T) {
	syms := extractOne(t, `
interface Greeter {
  greet(name: string): string
}

type Alias = string

function top(): number { return 1 }
`, "typescript")
	kinds := kindsByName(syms)
	if kinds["Greeter"] != "class" {
		t.Errorf("interface kind = %q", kinds["Greeter"])
	}
	if kinds["Alias"] != "class" {
		t.Errorf("type alias kind = %q", kinds["Alias"])
	}
	if kinds["top"] != "function" {
		t.Errorf("top kind = %q", kinds["top"])
	}
}

func TestClassifyRust(t *testing.T) {
	syms := extractOne(t, `
struct Point { x: i32 }

enum Color { Red }

fn distance(a: i32) -> i32 { a }
`, "rust")
	kinds := kindsByName(syms)
	if kinds["Point"] != "class" {
		t.Errorf("struct kind = %q", kinds["Point"])
	}
	if kinds["Color"] != "class" {
		t.Errorf("enum kind = %q", kinds["Color"])
	}
	if kinds["distance"] != "function" {
		t.Errorf("fn kind = %q", kinds["distance"])
	}
}

func TestClassifyJava(t *testing.T) {
	syms := extractOne(t, `
class Service {
  Service() {}
  void run() {}
}
`, "java")
	kinds := kindsByName(syms)
	if kinds["Service"] != "class" && kinds["Service"] != "method" {
		t.Errorf("Service kind = %q", kinds["Service"])
	}
	if kinds["run"] != "method" {
		t.Errorf("run kind = %q", kinds["run"])
	}
}

func TestClassifyGo(t *testing.T) {
	syms := extractOne(t, `package m

// Doc comment for Foo.
type Foo struct{ X int }

// Doc comment for Bar.
func Bar() int { return 1 }

// Doc comment for Method.
func (f *Foo) Method() int { return Bar() }
`, "go")
	kinds := kindsByName(syms)
	if kinds["Bar"] != "function" {
		t.Errorf("Bar kind = %q", kinds["Bar"])
	}
	if kinds["Method"] != "method" {
		t.Errorf("Method kind = %q", kinds["Method"])
	}
	// doc comments preceding a def are picked up from the previous sibling
	for _, s := range syms {
		if s.Name == "Bar" && !strings.Contains(s.Docstring, "Doc comment for Bar") {
			t.Errorf("Bar docstring = %q", s.Docstring)
		}
	}
}

// --- docstring / pyDocString ---------------------------------------------

func parsePythonRoot(t *testing.T, src string) (*gotreesitter.Node, *gotreesitter.Language) {
	t.Helper()
	grammar := grammars.PythonLanguage()
	tree, err := gotreesitter.NewParser(grammar).Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return tree.RootNode(), grammar
}

func TestPyDocString(t *testing.T) {
	// bare string statement: the grammar exposes a direct "string" node
	src := []byte("\"hello module\"\n")
	root, grammar := parsePythonRoot(t, string(src))
	strNode := root.Children()[0]
	if got := pyDocString(strNode, src, grammar); got != "hello module" {
		t.Errorf("pyDocString string = %q", got)
	}
	// assignment statement: no string child → ""
	root2, grammar2 := parsePythonRoot(t, "x = 1\n")
	assign := root2.Children()[0]
	if got := pyDocString(assign, []byte("x = 1\n"), grammar2); got != "" {
		t.Errorf("pyDocString assignment = %q, want empty", got)
	}
}

// docstring consults spec.DocNodeKinds for the first body statement; drive it
// directly with custom specs to reach both the string and non-string branches.
func TestDocstringBodyNodeKinds(t *testing.T) {
	src := []byte("def f():\n    \"\"\"inner doc\"\"\"\n    pass\n")
	root, grammar := parsePythonRoot(t, string(src))
	fn := root.Children()[0]

	// non-python spec: pyDocString is skipped, the "string" branch strips quotes
	jsLike := &LanguageSpec{Name: "javascript", BodyField: "body", DocNodeKinds: []string{"string"}}
	if got := docstring(fn, src, jsLike, grammar); got != "inner doc" {
		t.Errorf("docstring string kind = %q, want %q", got, "inner doc")
	}
	// non-string doc node kind: returned with leading #/* trimmed
	passSpec := &LanguageSpec{Name: "javascript", BodyField: "body", DocNodeKinds: []string{"string", "pass_statement"}}
	src2 := []byte("def f():\n    pass\n")
	root2, grammar2 := parsePythonRoot(t, string(src2))
	fn2 := root2.Children()[0]
	if got := docstring(fn2, src2, passSpec, grammar2); got != "pass" {
		t.Errorf("docstring non-string kind = %q, want %q", got, "pass")
	}
	// no matching doc node anywhere → ""
	none := &LanguageSpec{Name: "javascript", BodyField: "body", DocNodeKinds: []string{"comment"}}
	if got := docstring(fn2, src2, none, grammar2); got != "" {
		t.Errorf("docstring no match = %q, want empty", got)
	}
}

func TestDocstringInsideBodyComment(t *testing.T) {
	// JS: a comment preceding the def is picked up from the previous sibling.
	syms := extractOne(t, `// outer doc
function f() {
  return 1
}
`, "javascript")
	for _, s := range syms {
		if s.Name == "f" && !strings.Contains(s.Docstring, "outer doc") {
			t.Errorf("f docstring = %q", s.Docstring)
		}
	}
}

// --- identifierName fallbacks --------------------------------------------

func TestIdentifierName(t *testing.T) {
	src := []byte("def foo():\n    pass\n")
	root, grammar := parsePythonRoot(t, string(src))
	spec := GetSpec("python")

	fn := root.Children()[0]
	if got := identifierName(fn, spec, grammar, src); got != "foo" {
		t.Errorf("identifierName name field = %q", got)
	}
	// a spec whose NameField matches nothing falls back to scanning children
	weird := &LanguageSpec{NameField: "no_such_field"}
	if got := identifierName(fn, weird, grammar, src); got != "foo" {
		t.Errorf("identifierName child scan = %q", got)
	}
	// a node with neither a name field nor an identifier child → ""
	root2, grammar2 := parsePythonRoot(t, "1 + 2\n")
	if got := identifierName(root2.Children()[0], spec, grammar2, []byte("1 + 2\n")); got != "" {
		t.Errorf("identifierName leaf = %q, want empty", got)
	}
}

// --- ExtractSymbols edge cases -------------------------------------------

func TestExtractSymbolsDefaultsAndTruncation(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("def big():\n")
	for i := 0; i < 50; i++ {
		sb.WriteString("    x = 1\n")
	}
	// maxBodyLines <= 0 defaults to 40 → body truncated to 40 lines
	syms, err := ExtractSymbols([]byte(sb.String()), "python", "m", "m.py", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 {
		t.Fatalf("symbols = %d, want 1", len(syms))
	}
	if got := strings.Count(syms[0].Body, "\n") + 1; got != 40 {
		t.Errorf("body lines = %d, want 40 (truncated)", got)
	}
}

func TestExtractSymbolsNilGrammar(t *testing.T) {
	specs["nillang"] = &LanguageSpec{
		Name:            "nillang",
		DefinitionKinds: []string{"function_definition"},
		Grammar:         func() *gotreesitter.Language { return nil },
	}
	t.Cleanup(func() { delete(specs, "nillang") })
	syms, err := ExtractSymbols([]byte("whatever"), "nillang", "m", "m.nl", 40)
	if err != nil || syms != nil {
		t.Errorf("ExtractSymbols nil grammar = %v, %v; want nil, nil", syms, err)
	}
}

func TestExtractSymbolsDecoratedDropped(t *testing.T) {
	// decorated definitions classify as functions but carry no direct name
	// field on the wrapper node, so they are skipped (mirrors the Python port).
	syms := extractOne(t, `@decorator
def wrapped():
    pass
`, "python")
	for _, s := range syms {
		if s.Name == "wrapped" {
			t.Errorf("decorated def produced symbol %+v, want none", s)
		}
	}
}

func TestExtractCallsNonIdentifierTarget(t *testing.T) {
	// A call whose target is itself a call expression has no identifier tail
	// and is filtered out.
	syms := extractOne(t, `def factory():
    pass


def user():
    factory()()
    factory()
`, "python")
	for _, s := range syms {
		if s.Name == "user" {
			for _, c := range s.Calls {
				if !isIdentifier(c) {
					t.Errorf("non-identifier call leaked: %q (calls=%v)", c, s.Calls)
				}
			}
			if len(s.Calls) != 2 { // factory()() → "factory", factory() → "factory"
				t.Errorf("user calls = %v, want [factory factory]", s.Calls)
			}
		}
	}
}

// --- IndexCodebase paths ---------------------------------------------------

func TestIndexCodebaseFiltersAndForce(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.py":      "def fa():\n    pass\n",
		"b.go":      "package b\n\nfunc Fb() {}\n",
		"README.md": "# not source\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	store, emb := newTestStore(t)
	cfg := config.ForTesting(t.TempDir())

	// language filter: only go files are indexed
	report, err := IndexCodebase(dir, store, emb, cfg, "test", false, []string{"go"})
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesSkippedUnsupported != 1 {
		t.Errorf("skipped_unsupported = %d, want 1 (README.md)", report.FilesSkippedUnsupported)
	}
	if report.FilesIndexed != 1 {
		t.Errorf("files_indexed = %d, want 1 (b.go only)", report.FilesIndexed)
	}

	// force re-indexes even unchanged files
	report, err = IndexCodebase(dir, store, emb, cfg, "test", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesIndexed != 2 {
		t.Errorf("force files_indexed = %d, want 2", report.FilesIndexed)
	}
	if report.FilesSkippedUnchanged != 0 {
		t.Errorf("force skipped_unchanged = %d, want 0", report.FilesSkippedUnchanged)
	}
}

func TestIndexCodebaseDefaultWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.py"), []byte("def fa():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, emb := newTestStore(t)
	cfg := config.ForTesting(t.TempDir())
	// empty workspace argument falls back to cfg.Workspace
	if _, err := IndexCodebase(dir, store, emb, cfg, "", false, nil); err != nil {
		t.Fatal(err)
	}
	syms, err := store.SymbolsForFile("a.py")
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) == 0 {
		t.Fatal("no symbols indexed")
	}
}

func TestIndexCodebaseUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read anything")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "secret.py")
	if err := os.WriteFile(bad, []byte("def f():\n    pass\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o644) })

	store, emb := newTestStore(t)
	cfg := config.ForTesting(t.TempDir())
	report, err := IndexCodebase(dir, store, emb, cfg, "test", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Errors) != 1 {
		t.Fatalf("errors = %v, want 1 read error", report.Errors)
	}
	if report.FilesIndexed != 0 {
		t.Errorf("files_indexed = %d, want 0", report.FilesIndexed)
	}
}

func TestIndexCodebaseEmbedFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.py"), []byte("def fa():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, _ := newTestStore(t)
	cfg := config.ForTesting(t.TempDir())

	boom := errors.New("embed boom")
	failing := storage.NewCallableEmbedding(func(string) ([]float32, error) { return nil, boom }, 256)
	if _, err := IndexCodebase(dir, store, failing, cfg, "test", false, nil); !errors.Is(err, boom) {
		t.Errorf("err = %v, want embed boom", err)
	}
}

func TestIndexCodebaseSymbolEmbedFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.py"), []byte("def fa():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, emb := newTestStore(t)
	cfg := config.ForTesting(t.TempDir())

	// first embed (file memory) succeeds, second (symbol memory) fails
	boom := errors.New("symbol embed boom")
	calls := 0
	flaky := storage.NewCallableEmbedding(func(text string) ([]float32, error) {
		calls++
		if calls >= 2 {
			return nil, boom
		}
		return emb.Embed(text)
	}, 256)
	if _, err := IndexCodebase(dir, store, flaky, cfg, "test", false, nil); !errors.Is(err, boom) {
		t.Errorf("err = %v, want symbol embed boom", err)
	}
}

func TestIndexCodebaseClosedStore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.py"), []byte("def fa():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewStore(filepath.Join(t.TempDir(), "db.sqlite"), 256, false, false)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	emb := storage.NewHashingEmbedding(256)
	cfg := config.ForTesting(t.TempDir())

	// incremental path: GetIndexedHash on a closed DB errors out
	if _, err := IndexCodebase(dir, store, emb, cfg, "test", false, nil); err == nil {
		t.Error("expected error from closed store (incremental)")
	}
	// force path: the file-memory DELETE hits the closed DB instead
	if _, err := IndexCodebase(dir, store, emb, cfg, "test", true, nil); err == nil {
		t.Error("expected error from closed store (force)")
	}
}
