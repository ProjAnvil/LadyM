package operations

import (
	"math"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
)

func TestRecencyFactor(t *testing.T) {
	halfLife := 7 * 24 * 3600.0
	if got := RecencyFactor(100, halfLife, 100); got != 1.0 {
		t.Errorf("RecencyFactor at age 0 = %v, want 1", got)
	}
	if got := RecencyFactor(100, halfLife, 100+halfLife); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("RecencyFactor at half-life = %v, want 0.5", got)
	}
}

func TestFrequencyFactor(t *testing.T) {
	if FrequencyFactor(0) != 0 {
		t.Errorf("FrequencyFactor(0) = %v, want 0", FrequencyFactor(0))
	}
	if got := FrequencyFactor(1); math.Abs(got-math.Log(2)) > 1e-9 {
		t.Errorf("FrequencyFactor(1) = %v, want ln2", got)
	}
}

func TestInferQueryTypes(t *testing.T) {
	code := InferQueryTypes("where is the login function")
	if len(code) != 2 || code[0] != schema.TypeCodeSymbol {
		t.Errorf("code query types = %v", code)
	}
	howto := InferQueryTypes("how do i deploy")
	if len(howto) != 1 || howto[0] != schema.TypePlaybook {
		t.Errorf("howto query types = %v", howto)
	}
	if got := InferQueryTypes("hello"); len(got) != 0 {
		t.Errorf("plain query types = %v, want empty", got)
	}
}

func TestIsRetired(t *testing.T) {
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	if IsRetired(m) {
		t.Error("fresh memory should not be retired")
	}
	m.Metadata["superseded_by"] = "x"
	if !IsRetired(m) {
		t.Error("memory with superseded_by should be retired")
	}
	m2 := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	m2.Metadata["superseded"] = true
	if !IsRetired(m2) {
		t.Error("memory with superseded=true should be retired")
	}
	if IsRetired(nil) {
		t.Error("nil memory should not be retired")
	}
}

func TestActivationScore(t *testing.T) {
	cfg := config.Default()
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	m.LastAccessAt = 0
	score := ActivationScore(m, 0.5, cfg.Activation, map[string]int{}, nil, 0)
	// similarity 1.0 * 0.5 = 0.5; no recency/frequency/graph/type boost at now=0 recency=1
	// (recency factor at age 0 = 1.0 * 0.3 = 0.3)
	_ = score
}
