package mcp

import (
	"encoding/json"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
)

// propSpec describes one expected JSON-Schema property.
type propSpec struct {
	typ      string
	hasDef   bool
	def      any
	itemsTyp string // non-empty for arrays
}

func assertSchema(t *testing.T, tool toolDef, required []string, props map[string]propSpec) {
	t.Helper()
	sch := tool.InputSchema
	if sch["type"] != "object" {
		t.Errorf("%s: schema type = %v, want object", tool.Name, sch["type"])
	}
	gotProps, ok := sch["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s: properties missing or wrong type", tool.Name)
	}
	if len(gotProps) != len(props) {
		t.Errorf("%s: got %d properties %v, want %d %v", tool.Name, len(gotProps), keysOf(gotProps), len(props), keysOfSpec(props))
	}
	for name, want := range props {
		p, ok := gotProps[name].(map[string]any)
		if !ok {
			t.Errorf("%s: property %q missing", tool.Name, name)
			continue
		}
		if p["type"] != want.typ {
			t.Errorf("%s.%s: type = %v, want %s", tool.Name, name, p["type"], want.typ)
		}
		if want.itemsTyp != "" {
			items, ok := p["items"].(map[string]any)
			if !ok || items["type"] != want.itemsTyp {
				t.Errorf("%s.%s: items = %v, want type %s", tool.Name, name, p["items"], want.itemsTyp)
			}
		}
		def, hasDef := p["default"]
		if hasDef != want.hasDef {
			t.Errorf("%s.%s: default presence = %v (%v), want %v", tool.Name, name, hasDef, def, want.hasDef)
		} else if hasDef && def != want.def {
			t.Errorf("%s.%s: default = %v, want %v", tool.Name, name, def, want.def)
		}
	}
	gotReq, _ := sch["required"].([]string)
	if len(gotReq) != len(required) {
		t.Errorf("%s: required = %v, want %v", tool.Name, gotReq, required)
	} else {
		set := map[string]bool{}
		for _, r := range gotReq {
			set[r] = true
		}
		for _, r := range required {
			if !set[r] {
				t.Errorf("%s: required missing %q (got %v)", tool.Name, r, gotReq)
			}
		}
	}
}

func keysOf(m map[string]any) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOfSpec(m map[string]propSpec) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestToolsListSchemas pins the full inputSchema of every tool, mirroring the
// signatures of the Python FastMCP server (src/ladym/mcp/server.py).
func TestToolsListSchemas(t *testing.T) {
	s := &server{}
	tools := map[string]toolDef{}
	for _, td := range s.tools() {
		tools[td.Name] = td
		if td.Description == "" {
			t.Errorf("%s: empty description", td.Name)
		}
	}

	want := map[string]struct {
		required []string
		props    map[string]propSpec
	}{
		"recall": {[]string{"query"}, map[string]propSpec{
			"query":     {typ: "string"},
			"top_k":     {typ: "integer", hasDef: true, def: 8},
			"code_only": {typ: "boolean", hasDef: true, def: false},
			"workspace": {typ: "string"},
		}},
		"remember": {[]string{"content"}, map[string]propSpec{
			"content":   {typ: "string"},
			"tags":      {typ: "array", itemsTyp: "string"},
			"source":    {typ: "string", hasDef: true, def: ""},
			"workspace": {typ: "string"},
		}},
		"record_event": {[]string{"agent", "action"}, map[string]propSpec{
			"agent":       {typ: "string"},
			"action":      {typ: "string"},
			"observation": {typ: "string", hasDef: true, def: ""},
			"outcome":     {typ: "string", hasDef: true, def: ""},
			"tags":        {typ: "array", itemsTyp: "string"},
			"workspace":   {typ: "string"},
		}},
		"search_code": {[]string{"query"}, map[string]propSpec{
			"query":     {typ: "string"},
			"top_k":     {typ: "integer", hasDef: true, def: 10},
			"workspace": {typ: "string"},
		}},
		"index_code": {[]string{"root"}, map[string]propSpec{
			"root":      {typ: "string"},
			"force":     {typ: "boolean", hasDef: true, def: false},
			"languages": {typ: "array", itemsTyp: "string"},
			"workspace": {typ: "string"},
		}},
		"consolidate": {nil, map[string]propSpec{
			"workspace": {typ: "string"},
		}},
		"stats": {nil, map[string]propSpec{}},
		"link": {[]string{"src", "dst"}, map[string]propSpec{
			"src":      {typ: "string"},
			"dst":      {typ: "string"},
			"relation": {typ: "string", hasDef: true, def: "related_to"},
		}},
		"forget": {[]string{"memory_id"}, map[string]propSpec{
			"memory_id": {typ: "string"},
		}},
	}

	if len(tools) != len(want) {
		t.Errorf("tools() returned %d tools, want %d", len(tools), len(want))
	}
	for name, w := range want {
		td, ok := tools[name]
		if !ok {
			t.Errorf("tool %q missing from tools/list", name)
			continue
		}
		assertSchema(t, td, w.required, w.props)
	}
}

// TestRememberEmptySourceFallsBackToMCP mirrors Python's `source or "mcp"`:
// an explicitly empty source must fall back to "mcp", not be stored as "".
func TestRememberEmptySourceFallsBackToMCP(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()
	s := &server{eng: eng, cfg: cfg}

	content := "quasar zebra mandelbrot unique-source-check fact"
	if _, err := s.call("remember", map[string]any{"content": content, "source": ""}); err != nil {
		t.Fatalf("remember: %v", err)
	}
	out, err := s.call("recall", map[string]any{"query": "quasar zebra mandelbrot"})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	var resp struct {
		Results []struct {
			Memory struct {
				Source string `json:"source"`
			} `json:"memory"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("recall output not JSON: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatalf("recall returned no results for stored memory")
	}
	if got := resp.Results[0].Memory.Source; got != "mcp" {
		t.Errorf("source = %q, want %q (empty source must fall back)", got, "mcp")
	}
}
