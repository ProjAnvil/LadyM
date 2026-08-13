// Package ladym is the Go SDK facade for LadyM — a brain-inspired, multi-tier
// memory framework for LLM agents and codebase RAG.
//
// Usage:
//
//	eng, err := ladym.NewEngine(ladym.Config(db_path: "ladym.db", workspace: "myteam"))
//	eng.IndexCode("./src", false, "", nil)
//	resp, _ := eng.Recall("how does auth work", "", 0, nil, nil, 0)
package ladym

import (
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
)

// Version is the LadyM version (mirrors the Python 0.2.1 release).
const Version = "0.2.1"

// Re-export the public types for a one-import SDK surface.
type (
	Config         = config.Config
	Engine         = engine.Engine
	Layer          = schema.Layer
	MemoryType     = schema.MemoryType
	Memory         = schema.Memory
	Edge           = schema.Edge
	CodeSymbol     = schema.CodeSymbol
	RecallResponse = schema.RecallResponse
	RecallResult   = schema.RecallResult
	Stats          = schema.Stats
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

// NewEngine builds an Engine from cfg (nil → defaults).
func NewEngine(cfg *config.Config) (*engine.Engine, error) {
	return engine.New(cfg)
}

// DefaultConfig returns a Config with all defaults populated.
func DefaultConfig() *config.Config {
	return config.Default()
}
