package engine

import (
	"reflect"
	"testing"

	"github.com/ProjAnvil/LadyM/operations"
	"github.com/ProjAnvil/LadyM/providers"
)

func TestClassifierStructuredActionEnum(t *testing.T) {
	called := false
	provider := &providers.FakeLLMProvider{StructuredFn: func(messages []providers.Message, shape providers.JSONSchema) (map[string]any, error) {
		called = true
		properties, ok := shape["properties"].(map[string]any)
		if !ok {
			t.Fatalf("classifier properties = %v", shape["properties"])
		}
		action, ok := properties["action"].(map[string]any)
		if !ok || action["type"] != "string" || !reflect.DeepEqual(action["enum"], []string{"ADD", "UPDATE", "DELETE", "NOOP"}) {
			t.Errorf("classifier action schema must constrain the exact action enum: %v", action)
		}
		if !reflect.DeepEqual(shape["required"], []string{"action"}) {
			t.Errorf("classifier must require action: %v", shape["required"])
		}
		if len(messages) != 2 || messages[1].Content != "candidate: new evidence\nsimilar: [old fact]" {
			t.Errorf("classifier messages = %+v", messages)
		}
		return map[string]any{"action": "UPDATE", "new_text": "replacement"}, nil
	}}
	action, text, err := makeClassifier(provider)("new evidence", []string{"old fact"})
	if !called || err != nil || action != operations.ActionUpdate || text != "replacement" {
		t.Fatalf("classifier result: called=%v action=%s text=%q err=%v", called, action, text, err)
	}
}
