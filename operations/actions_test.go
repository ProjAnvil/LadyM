package operations

import (
	"testing"

	"github.com/ProjAnvil/LadyM/config"
)

func TestNormalizeActionCanonical(t *testing.T) {
	cfg := config.Default()
	if got := NormalizeAction("migrate", cfg); got != "migrate" {
		t.Errorf("NormalizeAction(migrate) = %q, want migrate", got)
	}
}

func TestNormalizeActionBuiltinAlias(t *testing.T) {
	cfg := config.Default()
	cases := []struct{ raw, want string }{
		{"RUN", "execute"},   // case-insensitive
		{"run", "execute"},   // run → execute
		{"launch", "execute"},
		{"rm", "delete"},
		{"grep", "search"},
		{"fetch", "read"},
		{"get", "read"},
		{"modify", "update"},
		{"list", "query"},
		{"ls", "query"},
		{"add", "create"},
		{" deploy ", "deploy"}, // trimmed
	}
	for _, c := range cases {
		if got := NormalizeAction(c.raw, cfg); got != c.want {
			t.Errorf("NormalizeAction(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestNormalizeActionMiss(t *testing.T) {
	cfg := config.Default()
	if got := NormalizeAction("frobnicate", cfg); got != "" {
		t.Errorf("NormalizeAction(frobnicate) = %q, want empty", got)
	}
}

func TestNormalizeActionConfigActions(t *testing.T) {
	cfg := config.Default()
	cfg.Resource.Actions = []string{"frobnicate"}
	if got := NormalizeAction("frobnicate", cfg); got != "frobnicate" {
		t.Errorf("NormalizeAction(frobnicate) = %q, want frobnicate", got)
	}
}

func TestNormalizeActionConfigAlias(t *testing.T) {
	cfg := config.Default()
	cfg.Resource.ActionAliases = map[string]string{"zap": "execute"}
	if got := NormalizeAction("zap", cfg); got != "execute" {
		t.Errorf("NormalizeAction(zap) = %q, want execute", got)
	}
	// Builtins still resolve when config adds a non-conflicting alias.
	if got := NormalizeAction("rm", cfg); got != "delete" {
		t.Errorf("NormalizeAction(rm) = %q, want delete", got)
	}
}

func TestDefaultActionVocabulary(t *testing.T) {
	// Spec §5.5 enumerates the built-in canonical words by group (the prose
	// says "30" but the enumeration has 31; the enumeration is the contract).
	want := map[string]bool{
		// knowledge/data
		"read": true, "write": true, "create": true, "update": true,
		"delete": true, "query": true, "search": true, "analyze": true,
		"summarize": true,
		// code
		"index": true, "fix": true, "refactor": true, "test": true,
		"review": true, "build": true, "lint": true,
		// ops
		"deploy": true, "migrate": true, "configure": true, "monitor": true,
		"rollback": true, "restart": true, "execute": true, "schedule": true,
		"backup": true, "restore": true,
		// generate/communicate/access
		"generate": true, "translate": true, "notify": true, "validate": true,
		"auth": true,
	}
	seen := map[string]bool{}
	for _, w := range defaultActions {
		if seen[w] {
			t.Errorf("duplicate canonical action %q", w)
		}
		seen[w] = true
		if !want[w] {
			t.Errorf("unexpected canonical action %q", w)
		}
	}
	for w := range want {
		if !seen[w] {
			t.Errorf("missing canonical action %q", w)
		}
	}
}
