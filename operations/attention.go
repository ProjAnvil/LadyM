package operations

import (
	"encoding/hex"
	"strings"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
	"golang.org/x/crypto/blake2b"
)

// GateDecision is the outcome of the attention gate.
type GateDecision struct {
	Action  string // "pass" | "rewrite" | "drop"
	Content string // populated only on rewrite
	Reason  string
}

var builtinNoise = map[string]bool{
	"lol": true, "ok": true, "test": true, "asdf": true,
	"foo": true, "bar": true, "todo": true,
}

func hash8(s string) string {
	h, _ := blake2b.New(8, nil)
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

const (
	gateSystemPrompt = "You are the attention gate. Decide if the user content is worth " +
		"storing as a long-term memory.\n" +
		"- pass: content with information value — facts, decisions, events, " +
		"preferences, code knowledge, etc.\n" +
		"- drop: content with no information value — greetings (hi/hey), " +
		"acknowledgements (ok/sure), small talk, fragments, pure emotion, etc.\n" +
		"- rewrite: content has value but is poorly worded; return the cleaned-up " +
		"text in `content`.\n" +
		"Reply JSON {action, content?, reason}. action in pass|rewrite|drop."
)

// AttentionGate applies the pre-remember filter to content destined for layer.
// getAgent resolves the LLM agent bound to "attention_gate" (nil for offline).
func AttentionGate(content string, cfg *config.Config, store *storage.SQLiteStore, getAgent func(string) (providers.LLMProvider, error), layer schema.Layer) (GateDecision, error) {
	if layer == schema.LayerWorking {
		return GateDecision{Action: "pass", Reason: "working memory never gated"}, nil
	}

	stripped := strings.TrimSpace(content)
	tokens := map[string]bool{}
	for _, w := range strings.Fields(stripped) {
		tokens[strings.ToLower(w)] = true
	}
	noise := map[string]bool{}
	for k := range builtinNoise {
		noise[k] = true
	}
	// Config noise words are used as-is (Python parity: they must be
	// pre-lowercased in the config; tokens are already lower-cased above).
	for _, w := range cfg.Attention.NoiseWords {
		noise[w] = true
	}
	allNoise := len(tokens) > 0
	for t := range tokens {
		if !noise[t] {
			allNoise = false
			break
		}
	}
	if allNoise {
		return GateDecision{Action: "drop", Reason: "noise"}, nil
	}

	now := schema.Now()
	window := cfg.Attention.DedupWindowS
	needle := hash8(content)
	since := now - window
	contents, err := store.EpisodicContentsSince(cfg.Workspace, since)
	if err != nil {
		return GateDecision{}, err
	}
	for _, c := range contents {
		if hash8(c) == needle {
			return GateDecision{Action: "drop", Reason: "recent duplicate"}, nil
		}
	}

	agent, err := getAgent("attention_gate")
	if err != nil {
		return GateDecision{}, err
	}
	if agent != nil {
		return llmGate(agent, content)
	}
	return GateDecision{Action: "pass"}, nil
}

func llmGate(provider providers.LLMProvider, content string) (GateDecision, error) {
	msgs := []providers.Message{
		{Role: "system", Content: gateSystemPrompt},
		{Role: "user", Content: content},
	}
	d, err := provider.CompleteStructured(msgs, `{"action": "pass|rewrite|drop", "content": "string?", "reason": "string"}`)
	if err != nil {
		return GateDecision{}, err
	}
	action, _ := d["action"].(string)
	newContent, _ := d["content"].(string)
	reason, _ := d["reason"].(string)
	return GateDecision{Action: action, Content: newContent, Reason: reason}, nil
}
