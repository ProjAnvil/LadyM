package storage

import (
	"github.com/ProjAnvil/LadyM/schema"
)

// Store is the persistence contract used by engine / operations / layers /
// code. SQLiteStore is the reference implementation; the interface exists so
// no package outside storage depends on the concrete type or the raw *sql.DB.
type Store interface {
	// lifecycle
	Close() error

	// Ping checks storage connectivity (health probe): SQLite runs SELECT 1,
	// Postgres pings the pool.
	Ping() error

	// memory CRUD
	PutMemory(mem *schema.Memory, vector []float32) error
	GetMemory(id string) (*schema.Memory, error)
	DeleteMemory(id string) error
	// UpdateMemoryContent patches one memory's content/summary/tags and bumps
	// updated_at (now==0 falls back to the wall clock). Unlike PutMemory's
	// upsert — where a nil vector NULLs the embedding column — a nil vector
	// here leaves embedding and content_hash untouched; a non-nil vector
	// rewrites the embedding and recomputes content_hash from content. A
	// missing id is a no-op (callers 404 beforehand).
	UpdateMemoryContent(id, content, summary string, tags []string, vector []float32, now float64) error
	TouchMemories(ids []string, now float64) error // one batched UPDATE; empty slice is a no-op
	IterMemories(workspace, layer, typ string) ([]*schema.Memory, error)
	FindByHash(contentHash, workspace string) (*schema.Memory, error)
	EpisodicContentsSince(workspace string, since float64) ([]string, error)
	Count(workspace string) (map[string]int, error)

	// vector retrieval (cosine similarity, deterministic top-k; semantics
	// anchored to InMemoryVectorIndex.Search)
	VectorSearch(queryVec []float32, topK int) []SearchHit

	// L4 associative graph
	PutEdge(e *schema.Edge) error
	Neighbors(memID, relation string) ([]*schema.Edge, error)
	CountEdges() (int, error)
	NeighborCounts() (map[string]int, error)
	InvalidateEdge(edgeID string, t float64) error // UPDATE edges SET valid_to=t WHERE id=edgeID

	// code projections
	PutCodeSymbol(sym *schema.CodeSymbol) error
	PutCodeRefs(refs []*schema.CodeRef) error
	SymbolsForFile(filePath string) ([]*schema.CodeSymbol, error)
	RefsForSymbol(qualifiedName, direction string) ([]*schema.CodeRef, error)
	CountCodeSymbols() (int, error)

	// index state / metadata
	GetIndexedHash(filePath string) (string, error)
	SetIndexed(filePath, bodyHash string, now float64) error
	Workspaces() ([]string, error)
	GetMeta(key string) (string, error)
	SetMeta(key, value string) error

	// users (HTTP data-plane Basic auth accounts)
	PutUser(u *schema.User) error                  // upsert by username
	GetUser(username string) (*schema.User, error) // nil when absent
	DeleteUser(username string) error              // missing username is a no-op
	ListUsers() ([]*schema.User, error)            // sorted by username

	// Cross-process mutex for code indexing. The SQLite implementation is the
	// existing flock(<db>.index.lock); it returns the release function. When
	// the lock is already held it returns an error matching ErrIndexLockHeld
	// — the user-facing IndexInProgressError type lives in the code package
	// (code imports storage, so storage cannot reference it) and the caller
	// performs the translation.
	TryAcquireIndexLock() (func(), error)

	// RebuildVectorIndex resets the vector index at a new dim (engine's
	// enforceEmbeddingDim on dim change).
	RebuildVectorIndex(newDim int)

	// The two deletes the code/indexer.go write path needs.
	DeleteMemoriesByTypeSource(typ, source, workspace string) error // DELETE FROM memories WHERE type=? AND source=? AND workspace=?
	DeleteSymbolMemories(qualifiedName, workspace string) error     // DELETE FROM memories WHERE type='code_symbol' AND workspace=? AND id IN (SELECT memory_id FROM code_symbols WHERE qualified_name=?)
}
