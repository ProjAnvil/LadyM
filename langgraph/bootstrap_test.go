//go:build !enterprise

package langgraph

import (
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/engine"
	lcmessages "github.com/projanvil/langchain-golang/core/messages"
	lgruntime "github.com/projanvil/langchain-golang/langgraph/runtime"
)

func TestBootstrapNodeWorkspace(t *testing.T) {
	e := newTestEngine(t)
	task, err := e.Scope("host").SetTaskState("task", "explicit host goal", "next", 60)
	if err != nil {
		t.Fatal(err)
	}
	node := CreateBootstrapNode(e, engine.BootstrapRequest{TaskID: "task"}, nil, WorkspaceFromUserID())
	result, err := node(lgruntime.Runtime{Context: map[string]any{"user_id": "host"}}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	messages := result.(map[string]any)["messages"].([]lcmessages.Message)
	if len(messages) != 1 || !strings.Contains(lcmessages.Text(messages[0]), task.ID) {
		t.Fatalf("node result: %+v", result)
	}
	node = CreateBootstrapNode(e, engine.BootstrapRequest{Unit: "tokens"}, nil, nil)
	if _, err := node(lgruntime.Runtime{}, nil); err == nil {
		t.Fatal("counter failure swallowed")
	}
}
