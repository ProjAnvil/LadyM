// Package ladym is the Go SDK facade for LadyM — a brain-inspired, multi-tier
// memory framework for LLM agents and codebase RAG.
//
// Usage:
//
//	eng, err := ladym.NewEngine(ladym.DefaultConfig())
//	eng.IndexCode("./src", false, "", nil)
//	resp, _ := eng.Recall("how does auth work", "", 0, nil, nil, 0)
package ladym

import (
	"github.com/ProjAnvil/LadyM/adapter"
	"github.com/ProjAnvil/LadyM/code"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
)

// Version is the LadyM version.
const Version = "0.3.1"

// Re-export the public types for a one-import SDK surface.
type (
	Config            = config.Config
	ActivationWeights = config.ActivationWeights
	CodeIndexConfig   = config.CodeIndexConfig
	ConsolidateConfig = config.ConsolidateConfig
	RecallConfig      = config.RecallConfig
	ModelRouting      = adapter.ModelRouting
	Engine            = engine.Engine
	Layer             = schema.Layer
	MemoryType        = schema.MemoryType
	Memory            = schema.Memory
	Edge              = schema.Edge
	CodeSymbol        = schema.CodeSymbol
	RecallResponse    = schema.RecallResponse
	RecallResult      = schema.RecallResult
	Stats             = schema.Stats
)

// Layer constants.
const (
	LayerWorking      = schema.LayerWorking
	LayerEpisodic     = schema.LayerEpisodic
	LayerSemantic     = schema.LayerSemantic
	LayerProcedural   = schema.LayerProcedural
	LayerAssociative  = schema.LayerAssociative
	LayerL5Mental     = schema.LayerL5Mental
	LayerL6Predictive = schema.LayerL6Predictive
)

// MemoryType constants.
const (
	TypeNote          = schema.TypeNote
	TypeEvent         = schema.TypeEvent
	TypeFact          = schema.TypeFact
	TypeCodeFile      = schema.TypeCodeFile
	TypeCodeSymbol    = schema.TypeCodeSymbol
	TypePlaybook      = schema.TypePlaybook
	TypeSnippet       = schema.TypeSnippet
	TypeMentalModel   = schema.TypeMentalModel
	TypeForwardIntent = schema.TypeForwardIntent
)

// NAMED_OPS mirrors providers.NAMED_OPS (the cognitive operations that can bind
// an LLM provider). A copy is returned so callers cannot mutate the registry.
func NamedOps() []string {
	out := make([]string, len(providers.NAMED_OPS))
	copy(out, providers.NAMED_OPS)
	return out
}

// NewEngine builds an Engine from cfg (nil → defaults).
func NewEngine(cfg *config.Config) (*engine.Engine, error) {
	return engine.New(cfg)
}

// NewEngineWithModels builds an Engine injecting host-owned models.
func NewEngineWithModels(cfg *config.Config, models *adapter.ModelRouting) (*engine.Engine, error) {
	return engine.NewWithModels(cfg, models)
}

// DefaultConfig returns a Config with all defaults populated.
func DefaultConfig() *config.Config {
	return config.Default()
}

// OpenEngine runs fn with a short-lived Engine, closing it on return. It is the
// Go equivalent of Python's `open_engine` context manager.
func OpenEngine(cfg *config.Config, dbPath, workspace string, fn func(*engine.Engine) error) error {
	if cfg == nil {
		cfg = config.Default()
	}
	if dbPath != "" || workspace != "" {
		c := cloneConfig(cfg)
		if dbPath != "" {
			c.DBPath = dbPath
		}
		if workspace != "" {
			c.Workspace = workspace
		}
		cfg = c
	}
	eng, err := engine.New(cfg)
	if err != nil {
		return err
	}
	defer eng.Close()
	return fn(eng)
}

func cloneConfig(cfg *config.Config) *config.Config {
	c := *cfg
	return &c
}

// Recall is a one-shot recall. Returns a RecallResponse.
func Recall(query, dbPath, workspace string, topK int) (*schema.RecallResponse, error) {
	var resp *schema.RecallResponse
	err := OpenEngine(nil, dbPath, workspace, func(eng *engine.Engine) error {
		var err error
		resp, err = eng.Recall(query, "", topK, nil, nil, 0)
		return err
	})
	return resp, err
}

// Remember is a one-shot write of a semantic fact.
func Remember(content, dbPath, workspace string, tags []string, source string) (*schema.Memory, error) {
	var mem *schema.Memory
	err := OpenEngine(nil, dbPath, workspace, func(eng *engine.Engine) error {
		var err error
		mem, err = eng.Remember(content, schema.LayerSemantic, schema.TypeFact, tags, nil, source, "")
		return err
	})
	return mem, err
}

// IndexCode is a one-shot codebase index.
func IndexCode(root, dbPath, workspace string, force bool) (*code.IndexReport, error) {
	var report *code.IndexReport
	err := OpenEngine(nil, dbPath, workspace, func(eng *engine.Engine) error {
		var err error
		report, err = eng.IndexCode(root, force, "", nil)
		return err
	})
	return report, err
}
