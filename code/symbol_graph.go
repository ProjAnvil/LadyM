package code

import (
	"regexp"
	"strings"
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

// ExtractSymbols extracts symbols from src for the given language.
func ExtractSymbols(src []byte, lang, moduleName, filePath string, maxBodyLines int) []RawSymbol {
	if maxBodyLines <= 0 {
		maxBodyLines = 40
	}
	switch lang {
	case "python":
		return extractPython(src, moduleName, maxBodyLines)
	case "go":
		return extractGo(src, moduleName, maxBodyLines)
	case "javascript", "typescript":
		return extractJS(src, moduleName, maxBodyLines)
	case "rust":
		return extractRust(src, moduleName, maxBodyLines)
	case "java":
		return extractJava(src, moduleName, maxBodyLines)
	case "c", "cpp":
		return extractC(src, moduleName, maxBodyLines)
	default:
		return nil
	}
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

// ---- shared helpers ----

func splitLines(src []byte) []string {
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	return strings.Split(text, "\n")
}

func firstNLines(body string, n int) string {
	lines := strings.Split(body, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func leadingSpaces(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

// ---- Python ----

var (
	pyDefRe   = regexp.MustCompile(`^(\s*)def\s+([A-Za-z_]\w*)\s*\((.*)\)\s*(->[^:]*)?:?\s*$`)
	pyClassRe = regexp.MustCompile(`^(\s*)class\s+([A-Za-z_]\w*)\s*(\([^)]*\))?\s*:?\s*$`)
)

type pyClass struct {
	indent int
	qname  string
}

func extractPython(src []byte, moduleName string, maxBodyLines int) []RawSymbol {
	lines := splitLines(src)
	var out []RawSymbol
	var stack []pyClass

	lineNo := func(i int) int { return i + 1 }

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := leadingSpaces(line)

		// pop classes we've exited (indent <= class indent)
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		if m := pyClassRe.FindStringSubmatch(trimmed); m != nil {
			name := m[2]
			parentQname := ""
			if len(stack) > 0 {
				parentQname = stack[len(stack)-1].qname
			}
			qname := qualify(moduleName, parentQname, name)
			sig := strings.TrimSpace("class " + name)
			if m[3] != "" {
				sig = strings.TrimSpace("class " + name + " " + strings.TrimSpace(m[3]))
			}
			doc := pyDocstring(lines, i, indent)
			end := blockEnd(lines, i, indent)
			body := firstNLines(strings.Join(lines[i:end], "\n"), maxBodyLines)
			out = append(out, RawSymbol{
				Kind: "class", Name: name, QualifiedName: qname, Signature: sig,
				Docstring: doc, LineStart: lineNo(i), LineEnd: lineNo(end - 1),
				Body: body, Calls: extractCalls(body),
			})
			stack = append(stack, pyClass{indent: indent, qname: qname})
			continue
		}

		if m := pyDefRe.FindStringSubmatch(trimmed); m != nil {
			name := m[2]
			parentQname := ""
			if len(stack) > 0 {
				parentQname = stack[len(stack)-1].qname
			}
			qname := qualify(moduleName, parentQname, name)
			kind := "function"
			if parentQname != "" {
				kind = "method"
			}
			params := strings.TrimSpace(m[3])
			sig := "def " + name + "(" + params + ")"
			if m[4] != "" {
				sig += " " + strings.TrimSpace(m[4])
			}
			doc := pyDocstring(lines, i, indent)
			end := blockEnd(lines, i, indent)
			body := firstNLines(strings.Join(lines[i:end], "\n"), maxBodyLines)
			out = append(out, RawSymbol{
				Kind: kind, Name: name, QualifiedName: qname, Signature: sig,
				Docstring: doc, LineStart: lineNo(i), LineEnd: lineNo(end - 1),
				Body: body, Calls: extractCalls(body),
			})
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

func blockEnd(lines []string, start, indent int) int {
	j := start + 1
	for j < len(lines) {
		line := lines[j]
		if strings.TrimSpace(line) == "" {
			j++
			continue
		}
		if leadingSpaces(line) <= indent {
			break
		}
		j++
	}
	return j
}

func pyDocstring(lines []string, defIdx, defIndent int) string {
	j := defIdx + 1
	for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
		j++
	}
	if j >= len(lines) || leadingSpaces(lines[j]) <= defIndent {
		return ""
	}
	trimmed := strings.TrimSpace(lines[j])
	if strings.HasPrefix(trimmed, `"""`) || strings.HasPrefix(trimmed, `'''`) {
		quote := `"""`
		if strings.HasPrefix(trimmed, `'''`) {
			quote = `'''`
		}
		if len(trimmed) >= len(quote)*2 && strings.HasSuffix(trimmed, quote) {
			return strings.TrimSuffix(strings.TrimPrefix(trimmed, quote), quote)
		}
		// multi-line: collect until closing quote
		body := strings.TrimPrefix(trimmed, quote)
		for j+1 < len(lines) {
			j++
			if strings.Contains(lines[j], quote) {
				body += "\n" + strings.SplitN(lines[j], quote, 2)[0]
				break
			}
			body += "\n" + lines[j]
		}
		return strings.TrimSpace(body)
	}
	if strings.HasPrefix(trimmed, `"`) || strings.HasPrefix(trimmed, `'`) {
		quote := string(trimmed[0])
		rest := trimmed[1:]
		if idx := strings.Index(rest, quote); idx >= 0 {
			return rest[:idx]
		}
	}
	return ""
}

// ---- Go ----

var (
	goFuncRe = regexp.MustCompile(`^func\s+(\(\s*([a-zA-Z_]\w*\s+)?\*?([a-zA-Z_]\w*)\s*\)\s*)?([a-zA-Z_]\w*)\s*\(`)
	goTypeRe = regexp.MustCompile(`^type\s+([a-zA-Z_]\w*)\s+(struct|interface)\b`)
)

func extractGo(src []byte, moduleName string, maxBodyLines int) []RawSymbol {
	lines := splitLines(src)
	var out []RawSymbol
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := goFuncRe.FindStringSubmatch(trimmed); m != nil {
			recvType := m[3]
			name := m[4]
			kind := "function"
			if recvType != "" {
				kind = "method"
			}
			// Match the Python tree-sitter port: Go methods are top-level nodes,
			// so the receiver type appears only in the signature, not the qname.
			qname := qualify(moduleName, "", name)
			sig := strings.TrimSpace(trimmed)
			if strings.HasSuffix(sig, "{") {
				sig = strings.TrimSpace(strings.TrimSuffix(sig, "{"))
			}
			end := braceBlockEnd(lines, i)
			body := firstNLines(strings.Join(lines[i:end], "\n"), maxBodyLines)
			out = append(out, RawSymbol{
				Kind: kind, Name: name, QualifiedName: qname, Signature: sig,
				LineStart: i + 1, LineEnd: end, Body: body, Calls: extractCalls(body),
			})
			continue
		}
		if m := goTypeRe.FindStringSubmatch(trimmed); m != nil {
			name := m[1]
			qname := qualify(moduleName, "", name)
			out = append(out, RawSymbol{
				Kind: "class", Name: name, QualifiedName: qname,
				Signature: "type " + name + " " + m[2],
				LineStart: i + 1, LineEnd: i + 1,
				Body: trimmed, Calls: extractCalls(trimmed),
			})
		}
	}
	return out
}

func braceBlockEnd(lines []string, start int) int {
	depth := 0
	for j := start; j < len(lines); j++ {
		depth += strings.Count(lines[j], "{") - strings.Count(lines[j], "}")
		if depth == 0 && strings.Contains(lines[j], "{") {
			// when depth returns to 0 after opening, block is done
			if j > start {
				return j + 1
			}
		}
		if depth <= 0 && j > start && !strings.Contains(lines[j], "{") {
			return j + 1
		}
		if depth == 0 && j > start {
			return j + 1
		}
	}
	return len(lines)
}

// ---- JavaScript / TypeScript ----

var (
	jsFuncRe   = regexp.MustCompile(`^(\s*)(export\s+)?(async\s+)?function\s+([a-zA-Z_$][\w$]*)\s*\(`)
	jsClassRe  = regexp.MustCompile(`^(\s*)(export\s+)?class\s+([a-zA-Z_$][\w$]*)`)
	jsMethodRe = regexp.MustCompile(`^(\s*)(async\s+)?([a-zA-Z_$][\w$]*)\s*\(([^)]*)\)\s*\{`)
	jsArrowRe  = regexp.MustCompile(`^(\s*)(const|let|var)\s+([a-zA-Z_$][\w$]*)\s*=\s*(async\s*)?\([^)]*\)\s*=>`)
)

func extractJS(src []byte, moduleName string, maxBodyLines int) []RawSymbol {
	lines := splitLines(src)
	var out []RawSymbol
	classDepth := -1
	className := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		indent := leadingSpaces(line)
		if m := jsClassRe.FindStringSubmatch(trimmed); m != nil {
			className = m[3]
			classDepth = indent
			qname := qualify(moduleName, "", className)
			out = append(out, RawSymbol{
				Kind: "class", Name: className, QualifiedName: qname,
				Signature: "class " + className, LineStart: i + 1, LineEnd: i + 1,
				Body: trimmed, Calls: extractCalls(trimmed),
			})
			continue
		}
		if classDepth >= 0 && indent <= classDepth && trimmed != "" {
			classDepth = -1
			className = ""
		}
		parent := ""
		if classDepth >= 0 {
			parent = className
		}
		if m := jsFuncRe.FindStringSubmatch(trimmed); m != nil {
			name := m[4]
			qname := qualify(moduleName, parent, name)
			out = append(out, RawSymbol{
				Kind: "function", Name: name, QualifiedName: qname,
				Signature: strings.TrimSuffix(trimmed, "{"), LineStart: i + 1,
				LineEnd: i + 1, Body: trimmed, Calls: extractCalls(trimmed),
			})
			continue
		}
		if m := jsMethodRe.FindStringSubmatch(trimmed); m != nil && classDepth >= 0 {
			name := m[3]
			qname := qualify(moduleName, parent, name)
			out = append(out, RawSymbol{
				Kind: "method", Name: name, QualifiedName: qname,
				Signature: strings.TrimSuffix(trimmed, "{"), LineStart: i + 1,
				LineEnd: i + 1, Body: trimmed, Calls: extractCalls(trimmed),
			})
			continue
		}
		if m := jsArrowRe.FindStringSubmatch(trimmed); m != nil {
			name := m[3]
			qname := qualify(moduleName, parent, name)
			out = append(out, RawSymbol{
				Kind: "function", Name: name, QualifiedName: qname,
				Signature: strings.TrimSuffix(trimmed, "{"), LineStart: i + 1,
				LineEnd: i + 1, Body: trimmed, Calls: extractCalls(trimmed),
			})
		}
	}
	return out
}

// ---- Rust ----

var (
	rustFnRe   = regexp.MustCompile(`^\s*(pub\s+)?fn\s+([a-zA-Z_]\w*)\s*\(`)
	rustTypeRe = regexp.MustCompile(`^\s*(pub\s+)?(struct|enum|trait)\s+([a-zA-Z_]\w*)`)
)

func extractRust(src []byte, moduleName string, maxBodyLines int) []RawSymbol {
	lines := splitLines(src)
	var out []RawSymbol
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := rustFnRe.FindStringSubmatch(trimmed); m != nil {
			name := m[2]
			qname := qualify(moduleName, "", name)
			out = append(out, RawSymbol{
				Kind: "function", Name: name, QualifiedName: qname,
				Signature: strings.TrimSuffix(trimmed, "{"), LineStart: i + 1,
				LineEnd: i + 1, Body: trimmed, Calls: extractCalls(trimmed),
			})
		} else if m := rustTypeRe.FindStringSubmatch(trimmed); m != nil {
			name := m[3]
			qname := qualify(moduleName, "", name)
			out = append(out, RawSymbol{
				Kind: "class", Name: name, QualifiedName: qname,
				Signature: m[2] + " " + name, LineStart: i + 1, LineEnd: i + 1,
				Body: trimmed, Calls: extractCalls(trimmed),
			})
		}
	}
	return out
}

// ---- Java ----

var (
	javaClassRe  = regexp.MustCompile(`^\s*(public\s+|private\s+|protected\s+)?(class|interface)\s+([a-zA-Z_]\w*)`)
	javaMethodRe = regexp.MustCompile(`^\s*(public\s+|private\s+|protected\s+|static\s+|final\s+|synchronized\s+)*[a-zA-Z_][\w<>\[\],\s]*\s+([a-zA-Z_]\w*)\s*\(([^)]*)\)\s*\{`)
)

func extractJava(src []byte, moduleName string, maxBodyLines int) []RawSymbol {
	lines := splitLines(src)
	var out []RawSymbol
	className := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := javaClassRe.FindStringSubmatch(trimmed); m != nil {
			className = m[3]
			qname := qualify(moduleName, "", className)
			out = append(out, RawSymbol{
				Kind: "class", Name: className, QualifiedName: qname,
				Signature: m[2] + " " + className, LineStart: i + 1, LineEnd: i + 1,
				Body: trimmed, Calls: extractCalls(trimmed),
			})
		} else if m := javaMethodRe.FindStringSubmatch(trimmed); m != nil && !strings.Contains(trimmed, "=") {
			name := m[len(m)-2]
			if name == "class" || name == "interface" || name == "if" || name == "for" || name == "while" || name == "return" {
				continue
			}
			parent := ""
			kind := "function"
			if className != "" {
				parent = className
				kind = "method"
			}
			qname := qualify(moduleName, parent, name)
			out = append(out, RawSymbol{
				Kind: kind, Name: name, QualifiedName: qname,
				Signature: strings.TrimSuffix(trimmed, "{"), LineStart: i + 1,
				LineEnd: i + 1, Body: trimmed, Calls: extractCalls(trimmed),
			})
		}
	}
	return out
}

// ---- C / C++ ----

var cFuncRe = regexp.MustCompile(`^\s*[a-zA-Z_][\w\s\*<>]*\s+([a-zA-Z_]\w*)\s*\(([^;]*)\)\s*\{`)

func extractC(src []byte, moduleName string, maxBodyLines int) []RawSymbol {
	lines := splitLines(src)
	var out []RawSymbol
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if m := cFuncRe.FindStringSubmatch(trimmed); m != nil {
			name := m[1]
			if callKeywords[name] {
				continue
			}
			qname := qualify(moduleName, "", name)
			out = append(out, RawSymbol{
				Kind: "function", Name: name, QualifiedName: qname,
				Signature: strings.TrimSuffix(trimmed, "{"), LineStart: i + 1,
				LineEnd: i + 1, Body: trimmed, Calls: extractCalls(trimmed),
			})
		}
	}
	return out
}
