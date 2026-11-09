package storage

import (
	"context"
	"github.com/ProjAnvil/LadyM/schema"
)

// Store is the persistence contract used by engine / operations / layers /
// code. SQLiteStore is the reference implementation; the interface exists so
// no package outside storage depends on the concrete type or the raw *sql.DB.
type Store interface {
	WithContext(context.Context) Store
	// lifecycle
	Close() error
	BeginEmbeddingMigration(schema.EmbeddingIdentity) error
	BeginEmbeddingMigrationGeneration(schema.EmbeddingIdentity) (string, error)
	BeginAutomaticEmbeddingMigrationGeneration(schema.EmbeddingIdentity) (string, error)
	StageEmbeddingMigration(id string, vector []float32, fingerprint, generation string) error
	ActivateEmbeddingMigration(generation string) error
	RollbackEmbeddingMigration() error
	FinalizeEmbeddingMigration() error
	FinalizeAutomaticEmbeddingMigration(generation, targetFingerprint string) error
	// InitializeEmbeddingIdentity atomically accepts a matching identity or
	// initializes an unmarked store only when it has no persisted vectors.
	InitializeEmbeddingIdentity(schema.EmbeddingIdentity) error
	AdoptEmbeddingIdentity(schema.EmbeddingIdentity) error
	EmbeddingMigrationStatus() (*schema.MigrationStatus, error)
	EmbeddingMigrationBatch(generation string, limit int) ([]*schema.Memory, error)
	GetEmbedding(id string) ([]float32, error)

	// Ping checks storage connectivity (health probe): SQLite runs SELECT 1,
	// Postgres pings the pool.
	Ping() error

	// memory CRUD
	PutMemory(mem *schema.Memory, vector []float32) error
	GetMemory(id string) (*schema.Memory, error)
	DeleteMemory(id string) error
	// DeleteMemorySafely rechecks access and usable direct references atomically.
	// autoBefore==0 explicitly deletes and invalidates recursive dependants.
	// A positive cutoff skips newer memories and protects referenced evidence.
	DeleteMemorySafely(id string, autoBefore float64) (deleted bool, protected bool, err error)
	InvalidateDependants(id string) (int, error)
	// ApplyMemoryBatch commits guards, writes, edge closures and new edges
	// together. Guard conflicts wrap ErrMemoryConflict; no provider work runs here.
	// Optional Quota counts marked persisted/proposed workspace content bytes
	// under the same writer lock; capacity errors wrap ErrMemoryQuota.
	ApplyMemoryBatch(batch MemoryBatch) error
	// EvidenceReferences returns the same direct dependency protection used by
	// safe deletion, scoped to workspace (empty means all workspaces).
	EvidenceReferences(workspace string) (map[string]bool, error)
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
	// SearchMemories applies eligibility before exact top-k truncation and
	// returns input/database errors. Empty filter fields preserve global scope.
	SearchMemories(queryVec []float32, topK int, filter MemoryFilter) ([]SearchHit, error)

	// L4 associative graph
	PutEdge(e *schema.Edge) error
	Neighbors(memID, relation string) ([]*schema.Edge, error)
	CountEdges() (int, error)
	NeighborCounts() (map[string]int, error)
	InvalidateEdge(edgeID string, t float64) error // UPDATE edges SET valid_to=t WHERE id=edgeID
	// SweepDanglingEdges deletes valid edges whose endpoint memories no
	// longer exist (legacy pre-FK databases) and returns how many went.
	SweepDanglingEdges() (int, error)

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

	// Cross-process mutex for System2 worker cycles, so redundant worker
	// replicas never run the same cycle concurrently (repeat LLM calls, write
	// conflicts). Callers acquire per cycle and release afterwards; when the
	// lock is already held it returns an error matching ErrWorkerLockHeld —
	// callers skip the cycle instead of queueing.
	TryAcquireWorkerLock() (func(), error)

	// RebuildVectorIndex resets the vector index at a new dim (engine's
	// enforceEmbeddingDim on dim change).
	RebuildVectorIndex(newDim int)

	// The two deletes the code/indexer.go write path needs.
	DeleteMemoriesByTypeSource(typ, source, workspace string) error // DELETE FROM memories WHERE type=? AND source=? AND workspace=?
	DeleteSymbolMemories(qualifiedName, workspace string) error     // DELETE FROM memories WHERE type='code_symbol' AND workspace=? AND id IN (SELECT memory_id FROM code_symbols WHERE qualified_name=?)
	// DeleteCodeArtifactsByFile removes every code artifact of one file
	// (re-index cleanup, spec §6.4): code_refs touching the file's symbols in
	// either direction, the code_symbols projection rows, and the code_symbol
	// memories. SQLite also syncs its in-process vector index.
	DeleteCodeArtifactsByFile(workspace, filePath string) error
}
