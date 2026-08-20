// Package schema holds LadyM's core data models.
//
// All memory items — episodic events, consolidated facts, code symbols, and
// procedural playbooks — are represented by a single Memory record. This
// unification is what lets the recall pipeline treat "memory" and "codebase
// RAG" as one system (see ARCHITECTURE.md §1).
package schema

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"

	"golang.org/x/crypto/blake2b"
)

// Now returns the current Unix time as a float64 (seconds since epoch),
// mirroring Python's time.time().
func Now() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

// NewID returns a fresh 32-hex-char id, mirroring Python's uuid4().hex.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail on a supported platform; fall back to a
		// timestamp-derived value so callers never see an empty id.
		return hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.999999999")))
	}
	return hex.EncodeToString(b[:])
}

// ContentHash returns the blake2b-16 hex digest of text, mirroring
// ladym.layers.semantic.content_hash.
func ContentHash(text string) string {
	h, _ := blake2b.New(16, nil)
	h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil))
}

// Layer is one of LadyM's seven memory layers (ARCHITECTURE.md §1).
type Layer string

const (
	LayerWorking      Layer = "L0_working"     // in-process scratch
	LayerEpisodic     Layer = "L1_episodic"    // time-stamped events
	LayerSemantic     Layer = "L2_semantic"    // consolidated facts + code analysis
	LayerProcedural   Layer = "L3_procedural"  // how-to playbooks
	LayerAssociative  Layer = "L4_associative" // graph edges (handled via Edge)
	LayerL5Mental     Layer = "L5_mental"      // mental models
	LayerL6Predictive Layer = "L6_predictive"  // forward intent
)

// MemoryType is a sub-type within a layer. L2 is the most polymorphic.
type MemoryType string

const (
	TypeNote          MemoryType = "note"           // free-form working/semantic note
	TypeEvent         MemoryType = "event"          // episodic (agent, action, observation, outcome)
	TypeFact          MemoryType = "fact"           // consolidated semantic fact
	TypeCodeFile      MemoryType = "code_file"      // whole-file summary
	TypeCodeSymbol    MemoryType = "code_symbol"    // function/class/method/doc
	TypePlaybook      MemoryType = "playbook"       // procedural steps
	TypeSnippet       MemoryType = "snippet"        // verified reusable code
	TypeMentalModel   MemoryType = "mental_model"   // L5 mental model
	TypeForwardIntent MemoryType = "forward_intent" // L6 forward intent
)

// Memory is a single memory item; it maps to one row in the memories table.
type Memory struct {
	ID        string         `json:"id"`
	Layer     Layer          `json:"layer"`
	Type      MemoryType     `json:"type"`
	Content   string         `json:"content"`
	Summary   string         `json:"summary"`
	Tags      []string       `json:"tags"`
	Metadata  map[string]any `json:"metadata"`
	Source    string         `json:"source"`
	Workspace string         `json:"workspace"`

	CreatedAt    float64 `json:"created_at"`
	UpdatedAt    float64 `json:"updated_at"`
	LastAccessAt float64 `json:"last_access_at"`
	AccessCount  int     `json:"access_count"`
	Activation   float64 `json:"activation"`
	ContentHash  string  `json:"content_hash"`

	// Embedding is stored separately (BLOB) but kept on the model for
	// in-memory flows. Omitted from JSON when unset.
	Embedding []float32 `json:"embedding,omitempty"`
}

// NewMemory returns a Memory with the id/timestamps pre-populated. Layer and
// Type are required; everything else defaults.
func NewMemory(layer Layer, typ MemoryType) *Memory {
	now := Now()
	return &Memory{
		ID:           NewID(),
		Layer:        layer,
		Type:         typ,
		Tags:         []string{},
		Metadata:     map[string]any{},
		Workspace:    "default",
		CreatedAt:    now,
		UpdatedAt:    now,
		LastAccessAt: now,
	}
}

// Touch marks the memory as accessed (called by the retriever).
func (m *Memory) Touch() {
	m.LastAccessAt = Now()
	m.AccessCount++
}

// Meta returns the value for key in Metadata, or nil.
func (m *Memory) Meta(key string) any {
	if m.Metadata == nil {
		return nil
	}
	return m.Metadata[key]
}

// MetaString returns the string value for key in Metadata, or "".
func (m *Memory) MetaString(key string) string {
	v := m.Meta(key)
	s, _ := v.(string)
	return s
}

// MetaBool reports whether key is present with a truthy value in Metadata.
func (m *Memory) MetaBool(key string) bool {
	v := m.Meta(key)
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	case int:
		return t != 0
	default:
		return v != nil
	}
}

// MetaFloat returns the float value for key (accepting JSON numbers) and ok.
func (m *Memory) MetaFloat(key string) (float64, bool) {
	v := m.Meta(key)
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// Clone returns a shallow copy (Metadata and Tags are deep-copied so callers
// can mutate the copy without affecting the original).
func (m *Memory) Clone() *Memory {
	c := *m
	c.Tags = append([]string{}, m.Tags...)
	if m.Metadata != nil {
		c.Metadata = make(map[string]any, len(m.Metadata))
		for k, v := range m.Metadata {
			c.Metadata[k] = v
		}
	}
	return &c
}

// Edge is a Zettelkasten-style link between two memories (L4 associative).
type Edge struct {
	ID        string         `json:"id"`
	SrcID     string         `json:"src_id"`
	Relation  string         `json:"relation"`
	DstID     string         `json:"dst_id"`
	Weight    float64        `json:"weight"`
	ValidFrom float64        `json:"valid_from"`
	ValidTo   *float64       `json:"valid_to"` // nil = still valid
	Metadata  map[string]any `json:"metadata"`
}

// NewEdge returns an Edge with id/valid_from populated.
func NewEdge(srcID, relation, dstID string) *Edge {
	return &Edge{
		ID:        NewID(),
		SrcID:     srcID,
		Relation:  relation,
		DstID:     dstID,
		Weight:    1.0,
		ValidFrom: Now(),
		Metadata:  map[string]any{},
	}
}

// CodeSymbol is a structured projection of a code-bearing Memory.
type CodeSymbol struct {
	MemoryID      string `json:"memory_id"`
	FilePath      string `json:"file_path"`
	SymbolKind    string `json:"symbol_kind"`
	QualifiedName string `json:"qualified_name"`
	Signature     string `json:"signature"`
	Docstring     string `json:"docstring"`
	LineStart     int    `json:"line_start"`
	LineEnd       int    `json:"line_end"`
	Language      string `json:"language"`
}

// CodeRef is a cross-reference between two symbols (calls/imports/defines).
type CodeRef struct {
	SrcSymbol string `json:"src_symbol"`
	DstSymbol string `json:"dst_symbol"`
	RefKind   string `json:"ref_kind"`
}

// RecallResult is one hit returned by recall.
type RecallResult struct {
	Memory *Memory  `json:"memory"`
	Score  float64  `json:"score"`
	Tier   int      `json:"tier"`
	Via    []string `json:"via"`
}

// RecallResponse is the full response from a recall call.
type RecallResponse struct {
	Query               string          `json:"query"`
	Results             []*RecallResult `json:"results"`
	TierReached         int             `json:"tier_reached"`
	ReflectedSufficient bool            `json:"reflected_sufficient"`
	ElapsedMs           float64         `json:"elapsed_ms"`
}

// User is one row in the users table: a database-level account for the HTTP
// data-plane's Basic auth (see docs/superpowers/specs
// /2026-08-19-auth-console-client.md §2). PasswordHash holds a bcrypt hash;
// "" means the user is passwordless (only an empty password authenticates).
// Workspace is the forced workspace for non-admin users. The hash is produced
// and checked in the api/cli layers — the Store only persists the string.
type User struct {
	Username     string  `json:"username"`
	PasswordHash string  `json:"password_hash"`
	Workspace    string  `json:"workspace"`
	Admin        bool    `json:"admin"`
	CreatedAt    float64 `json:"created_at"`
}

// Stats holds aggregate statistics for the stats tool/command.
type Stats struct {
	TotalMemories      int            `json:"total_memories"`
	ByLayer            map[string]int `json:"by_layer"`
	ByType             map[string]int `json:"by_type"`
	Edges              int            `json:"edges"`
	CodeSymbols        int            `json:"code_symbols"`
	Workspaces         []string       `json:"workspaces"`
	DBPath             string         `json:"db_path"`
	AvgTokensPerMemory float64        `json:"avg_tokens_per_memory"`
}
