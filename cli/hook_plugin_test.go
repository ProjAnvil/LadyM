//go:build !enterprise

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// loadJSON reads and unmarshals a JSON file, failing the test on any error.
func loadJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}

// hookEvents returns the sorted event names under the top-level "hooks" key
// and fails unless every handler in the file satisfies check.
func hookEvents(t *testing.T, path string, check func(t *testing.T, ev string, handler map[string]any)) []string {
	t.Helper()
	doc := loadJSON(t, path)
	hooks, ok := doc["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("%s: top-level \"hooks\" object missing", path)
	}
	events := make([]string, 0, len(hooks))
	for ev, groups := range hooks {
		events = append(events, ev)
		list, ok := groups.([]any)
		if !ok {
			t.Fatalf("%s: event %s is not an array of matcher groups", path, ev)
		}
		for _, g := range list {
			group, ok := g.(map[string]any)
			if !ok {
				t.Fatalf("%s: event %s has a non-object group", path, ev)
			}
			hs, ok := group["hooks"].([]any)
			if !ok {
				t.Fatalf("%s: event %s group lacks \"hooks\" array", path, ev)
			}
			for _, h := range hs {
				handler, ok := h.(map[string]any)
				if !ok {
					t.Fatalf("%s: event %s has a non-object handler", path, ev)
				}
				check(t, ev, handler)
			}
		}
	}
	sort.Strings(events)
	return events
}

func wantEvents(t *testing.T, path string, got, want []string) {
	t.Helper()
	sorted := append([]string(nil), want...)
	sort.Strings(sorted)
	if len(got) != len(sorted) {
		t.Fatalf("%s: events = %v, want exactly %v", path, got, sorted)
	}
	for i := range got {
		if got[i] != sorted[i] {
			t.Fatalf("%s: events = %v, want exactly %v", path, got, sorted)
		}
	}
}

func TestPluginClaudeHooks(t *testing.T) {
	path := filepath.Join("..", "plugins", "ladym", "hooks", "hooks.json")
	events := hookEvents(t, path, func(t *testing.T, ev string, h map[string]any) {
		if cmd, _ := h["command"].(string); cmd != "ladym hook --harness claude-code" {
			t.Errorf("%s %s: command = %q, want %q", path, ev, cmd, "ladym hook --harness claude-code")
		}
		if typ, _ := h["type"].(string); typ != "command" {
			t.Errorf("%s %s: type = %q, want \"command\"", path, ev, typ)
		}
	})
	wantEvents(t, path, events, []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit",
		"PostToolUse", "PostToolUseFailure", "Stop",
	})
}

func TestPluginCodexHooks(t *testing.T) {
	path := filepath.Join("..", "plugins", "ladym-codex", "hooks", "hooks.json")
	events := hookEvents(t, path, func(t *testing.T, ev string, h map[string]any) {
		if cmd, _ := h["command"].(string); cmd != "ladym hook --harness codex" {
			t.Errorf("%s %s: command = %q, want %q", path, ev, cmd, "ladym hook --harness codex")
		}
		if _, hasTimeout := h["timeout"]; !hasTimeout {
			t.Errorf("%s %s: handler lacks timeout", path, ev)
		}
	})
	wantEvents(t, path, events, []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit", "PostToolUse", "Stop",
	})
}

func TestPluginManifests(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "plugins", "ladym", ".claude-plugin", "plugin.json"),
		filepath.Join("..", "plugins", "ladym-codex", ".codex-plugin", "plugin.json"),
	} {
		doc := loadJSON(t, path)
		if name, _ := doc["name"].(string); name != "ladym" {
			t.Errorf("%s: name = %q, want \"ladym\"", path, name)
		}
		if ver, _ := doc["version"].(string); ver == "" {
			t.Errorf("%s: version is empty", path)
		}
		if desc, _ := doc["description"].(string); desc == "" {
			t.Errorf("%s: description is empty", path)
		}
	}
}

func TestPluginMCPConfigs(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "plugins", "ladym", ".mcp.json"),
		filepath.Join("..", "plugins", "ladym-codex", ".mcp.json"),
	} {
		doc := loadJSON(t, path)
		servers, ok := doc["mcpServers"].(map[string]any)
		if !ok {
			t.Fatalf("%s: mcpServers object missing", path)
		}
		srv, ok := servers["ladym"].(map[string]any)
		if !ok {
			t.Fatalf("%s: mcpServers.ladym missing", path)
		}
		if cmd, _ := srv["command"].(string); cmd != "ladym" {
			t.Errorf("%s: mcpServers.ladym.command = %q, want \"ladym\"", path, cmd)
		}
		args, _ := srv["args"].([]any)
		found := false
		for _, a := range args {
			if s, _ := a.(string); s == "serve" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: mcpServers.ladym.args = %v, want to contain \"serve\"", path, args)
		}
	}
}

func TestPluginMarketplace(t *testing.T) {
	path := filepath.Join("..", "plugins", ".claude-plugin", "marketplace.json")
	doc := loadJSON(t, path)
	owner, _ := doc["owner"].(map[string]any)
	if name, _ := owner["name"].(string); name == "" {
		t.Errorf("%s: owner.name is empty", path)
	}
	plugins, ok := doc["plugins"].([]any)
	if !ok || len(plugins) == 0 {
		t.Fatalf("%s: plugins array missing or empty", path)
	}
	p0, _ := plugins[0].(map[string]any)
	if name, _ := p0["name"].(string); name != "ladym" {
		t.Errorf("%s: plugins[0].name = %q, want \"ladym\"", path, name)
	}
	if src, _ := p0["source"].(string); src != "./ladym" {
		t.Errorf("%s: plugins[0].source = %q, want \"./ladym\"", path, src)
	}
}
