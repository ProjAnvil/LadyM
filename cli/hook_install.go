package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hookCommandPrefix is the identity marker of every hook entry LadyM
// installs; idempotent install and precise uninstall both match on it.
const hookCommandPrefix = "ladym hook "

// claudeHookEvents / codexHookEvents are the V1 event sets (spec §5.1).
var (
	claudeHookEvents = []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit", "PostToolUse", "PostToolUseFailure", "Stop",
	}
	codexHookEvents = []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit", "PostToolUse", "Stop",
	}
)

// installHookFile merges LadyM hook entries into the JSON config at path.
// Existing user hooks and non-hook keys are preserved verbatim; an event that
// already carries our prefix is skipped (idempotent). The original file is
// backed up once to path+".ladym-bak" before the first write. A corrupt
// (unparseable) file is an error — we never overwrite what we cannot read.
func installHookFile(path, harness string, events []string, withTimeout bool) error {
	doc, err := readHookConfig(path)
	if err != nil {
		return err
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for _, ev := range events {
		if hasOurHook(hooks, ev) {
			continue
		}
		entry := map[string]any{"hooks": []any{newHookEntry(harness, withTimeout)}}
		if list, ok := hooks[ev].([]any); ok {
			hooks[ev] = append(list, entry)
		} else if hooks[ev] == nil {
			hooks[ev] = []any{entry}
		} else {
			return fmt.Errorf("hook install: hooks.%s has unexpected shape", ev)
		}
	}
	doc["hooks"] = hooks
	return writeHookConfig(path, doc)
}

// newHookEntry builds our observer entry: a plain command invocation tagged
// with the harness so the Agent field is set on capture.
func newHookEntry(harness string, withTimeout bool) map[string]any {
	e := map[string]any{"type": "command", "command": hookCommandPrefix + "--harness " + harness}
	if withTimeout {
		e["timeout"] = 10
	}
	return e
}

// readHookConfig loads the whole config document at path (not just its hooks
// object) so non-hook keys like "model" survive a rewrite. A missing file is
// an empty document; an unparseable one is an error.
func readHookConfig(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("hook install: %s is not valid JSON: %w", path, err)
	}
	if doc == nil {
		doc = map[string]any{} // file held a bare JSON null
	}
	return doc, nil
}

// writeHookConfig writes doc as pretty JSON, creating parent directories and
// taking the one-time .ladym-bak backup of the pre-existing file first.
func writeHookConfig(path string, doc map[string]any) error {
	if _, err := os.Stat(path); err == nil {
		bak := path + ".ladym-bak"
		if _, err := os.Stat(bak); os.IsNotExist(err) {
			if err := copyFile(path, bak); err != nil {
				return err
			}
		}
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// hasOurHook reports whether event ev already carries a command starting
// with our prefix (tolerates --harness flag differences).
func hasOurHook(hooks map[string]any, ev string) bool {
	list, ok := hooks[ev].([]any)
	if !ok {
		return false
	}
	for _, g := range list {
		group, ok := g.(map[string]any)
		if !ok {
			continue
		}
		hs, _ := group["hooks"].([]any)
		for _, h := range hs {
			entry, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if cmd, _ := entry["command"].(string); strings.HasPrefix(cmd, hookCommandPrefix) {
				return true
			}
		}
	}
	return false
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// uninstallHookFile removes every LadyM hook entry from the config at path,
// leaving user entries and non-hook keys untouched. Empty event arrays and an
// empty hooks object are pruned; a file that held nothing but our hooks is
// removed entirely; an absent file is a no-op.
func uninstallHookFile(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // absent file: nothing to uninstall
	}
	doc, err := readHookConfig(path)
	if err != nil {
		return err
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for ev := range hooks {
		list, ok := hooks[ev].([]any)
		if !ok {
			continue
		}
		var kept []any
		for _, g := range list {
			group, ok := g.(map[string]any)
			if !ok {
				kept = append(kept, g)
				continue
			}
			hs, _ := group["hooks"].([]any)
			var keptHooks []any
			for _, h := range hs {
				if entry, ok := h.(map[string]any); ok {
					if cmd, _ := entry["command"].(string); strings.HasPrefix(cmd, hookCommandPrefix) {
						continue
					}
				}
				keptHooks = append(keptHooks, h)
			}
			if len(keptHooks) == 0 {
				continue // group emptied: drop it
			}
			group["hooks"] = keptHooks
			kept = append(kept, group)
		}
		if len(kept) == 0 {
			delete(hooks, ev)
		} else {
			hooks[ev] = kept
		}
	}
	if len(hooks) == 0 && len(doc) == 1 {
		if _, hadHooks := doc["hooks"]; hadHooks {
			// The doc held nothing but (now-empty) hooks: remove the file.
			return os.Remove(path)
		}
	}
	doc["hooks"] = hooks
	return writeHookConfig(path, doc)
}
