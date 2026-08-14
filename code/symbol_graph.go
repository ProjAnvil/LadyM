package code

import (
	"regexp"
	"strings"

	"github.com/odvcencio/gotreesitter"
)

// RawSymbol is a symbol extracted from source (mirrors the Python RawSymbol).
type RawSymbol struct {
	Kind          string // function | class | method | module | chunk
	Name          string // local identifier
	QualifiedName string // module[.Class].name
	Signature     string
	Docstring     string
	LineStart     int // 1-indexed
	LineEnd       int // 1-indexed, inclusive
	Body          string
	Calls         []string
}

// RawRef is a cross-reference between two symbols.
type RawRef struct {
	SrcSymbol string
	DstSymbol string
	RefKind   string
}

// ExtractSymbols extracts symbols from src for the given language, using the
// gotreesitter grammar when a detailed spec exists, otherwise returning nil so
// the indexer falls back to line-window chunking.
func ExtractSymbols(src []byte, lang, moduleName, filePath string, maxBodyLines int) []RawSymbol {
	if maxBodyLines <= 0 {
		maxBodyLines = 40
	}
	spec := GetSpec(lang)
	if len(spec.DefinitionKinds) == 0 || spec.Grammar == nil {
		return nil
	}
	grammar := spec.Grammar()
	if grammar == nil {
		return nil
	}
	parser := gotreesitter.NewParser(grammar)
	tree, err := parser.Parse(src)
	if err != nil {
		return nil
	}
	return extractTree(tree.RootNode(), src, spec, grammar, moduleName, maxBodyLines)
}

func extractTree(root *gotreesitter.Node, src []byte, spec *LanguageSpec, grammar *gotreesitter.Language, moduleName string, maxBodyLines int) []RawSymbol {
	var out []RawSymbol

	var emit func(node *gotreesitter.Node, parentQname, kind string)
	emit = func(node *gotreesitter.Node, parentQname, kind string) {
		name := identifierName(node, spec, grammar, src)
		if name == "" {
			return
		}
		qname := qualify(moduleName, parentQname, name)
		body := node.Text(src)
		sig := signature(node, src, spec, grammar, kind)
		doc := docstring(node, src, spec, grammar)
		if strings.Count(body, "\n")+1 > maxBodyLines {
			body = firstNLines(body, maxBodyLines)
		}
		out = append(out, RawSymbol{
			Kind:          kind,
			Name:          name,
			QualifiedName: qname,
			Signature:     sig,
			Docstring:     doc,
			LineStart:     int(node.StartPoint().Row) + 1,
			LineEnd:       int(node.EndPoint().Row) + 1,
			Body:          body,
			Calls:         extractCalls(body),
		})
		// recurse into class bodies for methods
		if kind == "class" {
			classBody := node.ChildByFieldName(spec.BodyField, grammar)
			if classBody == nil {
				classBody = node
			}
			for _, child := range classBody.Children() {
				if ck := classify(child, spec, grammar); ck != "" {
					emit(child, qname, ck)
				}
			}
		}
	}

	for _, child := range root.Children() {
		if k := classify(child, spec, grammar); k != "" {
			emit(child, "", k)
		}
	}
	return out
}

// classify maps a tree-sitter node type to a LadyM symbol kind (mirrors Python).
func classify(node *gotreesitter.Node, spec *LanguageSpec, grammar *gotreesitter.Language) string {
	t := node.Type(grammar)
	// unwrap decorated definitions
	if t == "decorated_definition" {
		for _, ch := range node.Children() {
			if r := classify(ch, spec, grammar); r != "" {
				return r
			}
		}
		return ""
	}
	if !contains(spec.DefinitionKinds, t) {
		return ""
	}
	if strings.Contains(t, "class") || strings.Contains(t, "struct") || strings.Contains(t, "interface") {
		return "class"
	}
	if strings.Contains(t, "method") || strings.Contains(t, "constructor") {
		return "method"
	}
	if strings.Contains(t, "function") || t == "function_declaration" || t == "arrow_function" {
		return "function"
	}
	if t == "variable_declarator" {
		return "function"
	}
	if strings.Contains(t, "type") || strings.Contains(t, "interface") || strings.Contains(t, "enum") {
		return "class"
	}
	return "function"
}

// identifierName pulls the identifier from the name field or first identifier child.
func identifierName(node *gotreesitter.Node, spec *LanguageSpec, grammar *gotreesitter.Language, src []byte) string {
	if cand := node.ChildByFieldName(spec.NameField, grammar); cand != nil {
		return cand.Text(src)
	}
	for _, child := range node.Children() {
		t := child.Type(grammar)
		if t == "identifier" || t == "type_identifier" || t == "property_identifier" {
			return child.Text(src)
		}
	}
	return ""
}

// signature renders a one-line signature (mirrors Python's _signature).
func signature(node *gotreesitter.Node, src []byte, spec *LanguageSpec, grammar *gotreesitter.Language, kind string) string {
	text := node.Text(src)
	firstLine := strings.TrimRight(strings.SplitN(text, "\n", 2)[0], " ")
	if kind == "class" {
		name := identifierName(node, spec, grammar, src)
		return strings.TrimSpace("class " + name)
	}
	sig := firstLine
	if strings.Contains(sig, "{") {
		sig = strings.TrimRight(strings.SplitN(sig, "{", 2)[0], " ")
	}
	if strings.Contains(sig, "=>") && strings.HasSuffix(strings.TrimRight(sig, " "), "=>") {
		idx := strings.LastIndex(sig, "=>")
		sig = strings.TrimRight(sig[:idx], " ")
	}
	if spec.Name == "python" && strings.HasSuffix(strings.TrimRight(sig, " "), ":") {
		s := strings.TrimRight(sig, " ")
		sig = strings.TrimRight(s[:len(s)-1], " ")
	}
	return strings.TrimSpace(sig)
}

// docstring grabs the first comment/string preceding or inside the def.
func docstring(node *gotreesitter.Node, src []byte, spec *LanguageSpec, grammar *gotreesitter.Language) string {
	if body := node.ChildByFieldName(spec.BodyField, grammar); body != nil {
		children := body.Children()
		if len(children) > 0 {
			first := children[0]
			// Python docstring: a string literal is the first body statement,
			// either directly or wrapped in expression_statement (older grammars).
			if spec.Name == "python" {
				if s := pyDocString(first, src, grammar); s != "" {
					return s
				}
			}
			if ft := first.Type(grammar); contains(spec.DocNodeKinds, ft) {
				if ft == "string" {
					return stripQuotes(strings.TrimSpace(first.Text(src)))
				}
				return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(first.Text(src)), "#"))
			}
		}
	}
	if prev := node.PrevSibling(); prev != nil && contains(spec.DocNodeKinds, prev.Type(grammar)) {
		return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(prev.Text(src)), "#/*"))
	}
	return ""
}

// pyDocString extracts a stripped Python docstring from a string literal node,
// handling both the direct `string` child and the wrapped `expression_statement`.
func pyDocString(node *gotreesitter.Node, src []byte, grammar *gotreesitter.Language) string {
	switch node.Type(grammar) {
	case "string":
		return stripQuotes(strings.TrimSpace(node.Text(src)))
	case "expression_statement":
		for _, ch := range node.Children() {
			if ch.Type(grammar) == "string" {
				return stripQuotes(strings.TrimSpace(ch.Text(src)))
			}
		}
	}
	return ""
}

// stripQuotes removes leading/trailing quote characters (', ").
func stripQuotes(s string) string {
	return strings.Trim(s, "\"'")
}

// BuildRefs converts each symbol's calls into intra-file refs (caller→callee).
func BuildRefs(symbols []RawSymbol, filePath string) []RawRef {
	nameToQname := map[string]string{}
	for _, s := range symbols {
		nameToQname[s.Name] = s.QualifiedName
	}
	var refs []RawRef
	seen := map[[2]string]bool{}
	for _, sym := range symbols {
		for _, callee := range sym.Calls {
			if dstQ, ok := nameToQname[callee]; ok && callee != sym.Name {
				key := [2]string{sym.QualifiedName, dstQ}
				if seen[key] {
					continue
				}
				seen[key] = true
				refs = append(refs, RawRef{SrcSymbol: sym.QualifiedName, DstSymbol: dstQ, RefKind: "calls"})
			}
		}
	}
	return refs
}

var callRe = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`)

var callKeywords = map[string]bool{
	"def": true, "class": true, "if": true, "elif": true, "for": true, "while": true,
	"return": true, "import": true, "from": true, "print": true, "with": true,
	"assert": true, "lambda": true, "not": true, "in": true, "is": true, "and": true,
	"or": true, "else": true, "try": true, "except": true, "finally": true, "raise": true,
	"func": true, "type": true, "struct": true, "interface": true, "fn": true, "impl": true,
	"switch": true, "case": true, "default": true, "package": true, "new": true, "go": true,
	"defer": true, "match": true, "await": true, "yield": true, "typeof": true, "delete": true,
	"void": true, "sizeof": true,
}

func extractCalls(body string) []string {
	var out []string
	for _, m := range callRe.FindAllStringSubmatch(body, -1) {
		name := m[1]
		if callKeywords[name] {
			continue
		}
		out = append(out, name)
	}
	return dedupStrings(out)
}

func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func qualify(moduleName, parentQname, name string) string {
	if parentQname != "" {
		return parentQname + "." + name
	}
	if moduleName == "" {
		return name
	}
	return moduleName + "." + name
}

func firstNLines(body string, n int) string {
	lines := strings.Split(body, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
