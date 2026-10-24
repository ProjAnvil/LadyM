package operations

import (
	"slices"
	"strings"

	"github.com/ProjAnvil/LadyM/config"
)

// Built-in canonical action vocabulary, grouped by domain (spec §5.5).
var (
	actionsKnowledgeData = []string{
		"read", "write", "create", "update", "delete",
		"query", "search", "analyze", "summarize",
	}
	actionsCode = []string{
		"index", "fix", "refactor", "test", "review", "build", "lint",
	}
	actionsOps = []string{
		"deploy", "migrate", "configure", "monitor", "rollback",
		"restart", "execute", "schedule", "backup", "restore",
	}
	actionsGenCommAccess = []string{
		"generate", "translate", "notify", "validate", "auth",
	}
)

// defaultActions is the flat built-in canonical word list.
var defaultActions = buildDefaultActions()

func buildDefaultActions() []string {
	var out []string
	for _, g := range [][]string{
		actionsKnowledgeData, actionsCode, actionsOps, actionsGenCommAccess,
	} {
		out = append(out, g...)
	}
	return out
}

// defaultAliases maps common synonyms onto canonical words (spec §5.5).
// deploy→deploy is identity and therefore omitted.
var defaultAliases = map[string]string{
	"run":    "execute",
	"launch": "execute",
	"rm":     "delete",
	"grep":   "search",
	"fetch":  "read",
	"get":    "read",
	"modify": "update",
	"list":   "query",
	"ls":     "query",
	"add":    "create",
}

// NormalizeAction maps a raw action string onto a canonical vocabulary word.
// Matching is lowercased, trimmed, and exact against the merged alias table
// the merged alias table (cfg.Resource.ActionAliases over builtins — config
// wins on conflicts) and then the merged canonical word list
// (cfg.Resource.Actions over builtins).
// Unmatched input returns "" — the caller keeps the raw string for later
// backfill instead of forcing a semantic merge.
func NormalizeAction(raw string, cfg *config.Config) string {
	w := strings.ToLower(strings.TrimSpace(raw))
	if w == "" {
		return ""
	}
	for _, alias := range []map[string]string{cfg.Resource.ActionAliases, defaultAliases} {
		if canon, ok := alias[w]; ok {
			return canon
		}
	}
	for _, words := range [][]string{defaultActions, cfg.Resource.Actions} {
		if slices.Contains(words, w) {
			return w
		}
	}
	return ""
}
