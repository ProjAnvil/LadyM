package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	vectorpgx "github.com/pgvector/pgvector-go/pgx"
)

// PostgresStore is the PostgreSQL+pgvector Store implementation. It mirrors
// SQLiteStore method-by-method; differences are confined to SQL dialect
// ($N placeholders, ON CONFLICT ... (col), JSONB, vector(dim)) and to the
// lock/index mechanisms (pg advisory lock, pgvector HNSW index instead of the
// process-local InMemoryVectorIndex + flock file).
type PostgresStore struct {
	dim  int
	pool *pgxpool.Pool
}

var _ Store = (*PostgresStore)(nil)

// indexLockAdvisoryKey is the fixed pg advisory-lock key identifying ladyM's
// cross-process code-index lock (pg advisory locks take a single bigint; the
// value is arbitrary but must be stable across processes).
const indexLockAdvisoryKey int64 = 727274

// schemaSetupAdvisoryKey identifies ladyM's schema-setup lock — distinct from
// indexLockAdvisoryKey so two processes initialising a fresh database
// serialise their CREATE ... IF NOT EXISTS runs instead of racing.
const schemaSetupAdvisoryKey int64 = 727275

// pgSchema returns the idempotent schema statements (one per entry — pgx's
// extended protocol rejects multi-statement Exec). dim sizes the vector col.
func pgSchema(dim int) []string {
	memTable := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS memories (
    id              TEXT PRIMARY KEY,
    layer           TEXT NOT NULL,
    type            TEXT NOT NULL,
    content         TEXT NOT NULL,
    summary         TEXT NOT NULL DEFAULT '',
    tags            JSONB NOT NULL DEFAULT '[]',
    metadata        JSONB NOT NULL DEFAULT '{}',
    source          TEXT NOT NULL DEFAULT '',
    workspace       TEXT NOT NULL DEFAULT 'default',
    created_at      DOUBLE PRECISION NOT NULL,
    updated_at      DOUBLE PRECISION NOT NULL,
    last_access_at  DOUBLE PRECISION NOT NULL,
    access_count    INTEGER NOT NULL DEFAULT 0,
    activation      DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    content_hash    TEXT NOT NULL DEFAULT '',
    embedding       vector(%d)
)`, dim)
	return []string{
		`CREATE EXTENSION IF NOT EXISTS vector`,
		memTable,
		`CREATE INDEX IF NOT EXISTS idx_mem_layer_ws ON memories(layer, workspace)`,
		`CREATE INDEX IF NOT EXISTS idx_mem_type_ws ON memories(type, workspace)`,
		`CREATE INDEX IF NOT EXISTS idx_mem_ws ON memories(workspace)`,
		`CREATE INDEX IF NOT EXISTS idx_mem_source ON memories(source)`,
		`CREATE INDEX IF NOT EXISTS idx_mem_hash ON memories(content_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_mem_embedding ON memories USING hnsw (embedding vector_cosine_ops)`,
		`CREATE TABLE IF NOT EXISTS edges (
    id          TEXT PRIMARY KEY,
    src_id      TEXT NOT NULL,
    relation    TEXT NOT NULL,
    dst_id      TEXT NOT NULL,
    weight      DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    valid_from  DOUBLE PRECISION NOT NULL,
    valid_to    DOUBLE PRECISION,
    metadata    JSONB NOT NULL DEFAULT '{}',
    FOREIGN KEY (src_id) REFERENCES memories(id) ON DELETE CASCADE,
    FOREIGN KEY (dst_id) REFERENCES memories(id) ON DELETE CASCADE
)`,
		`CREATE INDEX IF NOT EXISTS idx_edge_src ON edges(src_id)`,
		`CREATE INDEX IF NOT EXISTS idx_edge_dst ON edges(dst_id)`,
		`CREATE TABLE IF NOT EXISTS code_symbols (
    memory_id       TEXT PRIMARY KEY,
    file_path       TEXT NOT NULL,
    symbol_kind     TEXT NOT NULL,
    qualified_name  TEXT NOT NULL,
    signature       TEXT NOT NULL DEFAULT '',
    docstring       TEXT NOT NULL DEFAULT '',
    line_start      INTEGER NOT NULL DEFAULT 0,
    line_end        INTEGER NOT NULL DEFAULT 0,
    language        TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE
)`,
		`CREATE INDEX IF NOT EXISTS idx_sym_file ON code_symbols(file_path)`,
		`CREATE INDEX IF NOT EXISTS idx_sym_qname ON code_symbols(qualified_name)`,
		`CREATE TABLE IF NOT EXISTS code_refs (
    src_symbol  TEXT NOT NULL,
    dst_symbol  TEXT NOT NULL,
    ref_kind    TEXT NOT NULL DEFAULT 'calls'
)`,
		`CREATE INDEX IF NOT EXISTS idx_ref_src ON code_refs(src_symbol)`,
		`CREATE INDEX IF NOT EXISTS idx_ref_dst ON code_refs(dst_symbol)`,
		`CREATE TABLE IF NOT EXISTS index_state (
    file_path   TEXT PRIMARY KEY,
    body_hash   TEXT NOT NULL,
    indexed_at  DOUBLE PRECISION NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS users (
    username      TEXT PRIMARY KEY,
    password_hash TEXT NOT NULL DEFAULT '',
    workspace     TEXT NOT NULL DEFAULT '',
    admin         BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    DOUBLE PRECISION NOT NULL
)`,
	}
}

// NewPostgresStore connects to the PostgreSQL database at dsn (pgxpool) and
// applies the schema idempotently. dim sizes the pgvector embedding column.
func NewPostgresStore(dsn string, dim int) (*PostgresStore, error) {
	ctx := context.Background()
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		// The vector extension must exist before the codec can resolve its
		// type OID (fresh databases hit this hook before schema setup).
		// CREATE EXTENSION ... IF NOT EXISTS is only idempotent when run
		// serially: two processes cold-starting a fresh database can still
		// race it into a pg_extension_name_index 23505. Serialise on the same
		// session-level advisory lock applyPGSchema uses, so extension +
		// schema setup across processes form one serialised phase.
		if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", schemaSetupAdvisoryKey); err != nil {
			return err
		}
		// Unlock on every exit path, with a background context so a cancelled
		// setup ctx cannot strand the session-level lock.
		defer func() {
			_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", schemaSetupAdvisoryKey)
		}()
		if _, err := conn.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
			return err
		}
		return vectorpgx.RegisterTypes(ctx, conn)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("cannot connect to postgres store: %w", err)
	}
	if err := applyPGSchema(ctx, pool, dim); err != nil {
		pool.Close()
		return nil, err
	}
	return &PostgresStore{dim: dim, pool: pool}, nil
}

// applyPGSchema runs the idempotent schema statements under an exclusive
// session-level advisory lock, so two processes opening a fresh database at
// the same time cannot race the CREATE ... IF NOT EXISTS sequence.
func applyPGSchema(ctx context.Context, pool *pgxpool.Pool, dim int) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres schema setup failed: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", schemaSetupAdvisoryKey); err != nil {
		return fmt.Errorf("postgres schema setup failed: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", schemaSetupAdvisoryKey)
	}()
	for _, stmt := range pgSchema(dim) {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("postgres schema setup failed: %w", err)
		}
	}
	return nil
}

// Close releases the connection pool.
func (s *PostgresStore) Close() error {
	s.pool.Close()
	return nil
}

// ph renders the n-th (1-based) pgx placeholder.
func ph(n int) string { return "$" + strconv.Itoa(n) }

// RebuildVectorIndex resets the vector index at a new dim: drops the HNSW
// index, nulls all embeddings, alters the column to vector(newDim) and
// recreates the index — one transaction. Mirrors SQLiteStore's reset of the
// in-memory index; the interface returns no error, so failures are warned.
func (s *PostgresStore) RebuildVectorIndex(newDim int) {
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: RebuildVectorIndex: begin tx: %v\n", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stmts := []string{
		`DROP INDEX IF EXISTS idx_mem_embedding`,
		`UPDATE memories SET embedding = NULL`,
		fmt.Sprintf(`ALTER TABLE memories ALTER COLUMN embedding TYPE vector(%d)`, newDim),
		`CREATE INDEX IF NOT EXISTS idx_mem_embedding ON memories USING hnsw (embedding vector_cosine_ops)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: RebuildVectorIndex: %v\n", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: RebuildVectorIndex: commit: %v\n", err)
		return
	}
	s.dim = newDim
}

// VectorSearch runs a cosine top-k search via pgvector. Ordering matches
// InMemoryVectorIndex.Search: similarity desc, id asc as deterministic
// tiebreak; the vector index is global (workspace filtering happens in the
// recall layer above). Query failures degrade to nil, mirroring the in-memory
// index's error-free Search signature (same tolerance as warmIndexFromBlobs).
//
// Degenerate vectors are handled outside the ANN path: pgvector's cosine
// distance is NaN for zero-norm vectors, and the HNSW index omits them
// entirely, while the in-memory baseline assigns them similarity 0. A
// zero-norm query therefore takes a plain id-ordered scan, and zero-norm
// stored vectors are appended via UNION ALL with a literal 0 similarity.
func (s *PostgresStore) VectorSearch(queryVec []float32, topK int) []SearchHit {
	if topK <= 0 {
		return nil
	}
	ctx := context.Background()
	if l2Norm(queryVec) == 0 {
		// COLLATE "C" forces byte-wise ordering to match the baseline's
		// Go string comparison, regardless of the database's locale.
		rows, err := s.pool.Query(ctx,
			`SELECT id FROM memories WHERE embedding IS NOT NULL ORDER BY id COLLATE "C" LIMIT $1`, topK)
		if err != nil {
			return nil
		}
		defer rows.Close()
		var out []SearchHit
		for rows.Next() {
			var h SearchHit
			if err := rows.Scan(&h.ID); err != nil {
				return nil
			}
			out = append(out, h)
		}
		if rows.Err() != nil {
			return nil
		}
		return out
	}
	rows, err := s.pool.Query(ctx,
		`(SELECT id, 1 - (embedding <=> $1) AS sim FROM memories
		  WHERE embedding IS NOT NULL AND vector_norm(embedding) > 0
		  ORDER BY embedding <=> $1 LIMIT $2)
		 UNION ALL
		 (SELECT id, 0.0::float8 AS sim FROM memories
		  WHERE embedding IS NOT NULL AND vector_norm(embedding) = 0)`,
		pgvector.NewVector(queryVec), topK)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.ID, &h.Similarity); err != nil {
			return nil
		}
		out = append(out, h)
	}
	if rows.Err() != nil {
		return nil
	}
	// Canonical sim-desc / id-asc ordering is enforced here so the ANN
	// pre-selection and the zero-norm UNION branch merge deterministically,
	// exactly matching InMemoryVectorIndex.Search.
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Similarity != out[b].Similarity {
			return out[a].Similarity > out[b].Similarity
		}
		return out[a].ID < out[b].ID
	})
	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

// ---- memory CRUD ----

func (s *PostgresStore) PutMemory(mem *schema.Memory, vector []float32) error {
	var embedding any
	if vector != nil {
		embedding = pgvector.NewVector(vector)
	}
	tags, _ := json.Marshal(mem.Tags)
	meta, _ := json.Marshal(mem.Metadata)
	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO memories (id, layer, type, content, summary, tags, metadata,
		                       source, workspace, created_at, updated_at,
		                       last_access_at, access_count, activation, content_hash, embedding)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		 ON CONFLICT (id) DO UPDATE SET
		   layer=excluded.layer, type=excluded.type, content=excluded.content,
		   summary=excluded.summary, tags=excluded.tags, metadata=excluded.metadata,
		   source=excluded.source, workspace=excluded.workspace,
		   updated_at=excluded.updated_at, last_access_at=excluded.last_access_at,
		   access_count=excluded.access_count, activation=excluded.activation,
		   content_hash=excluded.content_hash, embedding=excluded.embedding`,
		mem.ID, string(mem.Layer), string(mem.Type), mem.Content, mem.Summary,
		string(tags), string(meta), mem.Source, mem.Workspace, mem.CreatedAt, mem.UpdatedAt,
		mem.LastAccessAt, mem.AccessCount, mem.Activation, mem.ContentHash, embedding)
	return err
}

func scanMemoryPG(row pgx.Rows) (*schema.Memory, error) {
	var (
		id, layer, typ, content, summary, source, workspace string
		tagsJSON, metaJSON                                  []byte
		createdAt, updatedAt, lastAccessAt, activation      float64
		accessCount                                         int
		contentHash                                         string
	)
	if err := row.Scan(&id, &layer, &typ, &content, &summary, &tagsJSON, &metaJSON, &source,
		&workspace, &createdAt, &updatedAt, &lastAccessAt, &accessCount, &activation,
		&contentHash); err != nil {
		return nil, err
	}
	var tags []string
	var meta map[string]any
	_ = json.Unmarshal(tagsJSON, &tags)
	_ = json.Unmarshal(metaJSON, &meta)
	if tags == nil {
		tags = []string{}
	}
	if meta == nil {
		meta = map[string]any{}
	}
	return &schema.Memory{
		ID: id, Layer: schema.Layer(layer), Type: schema.MemoryType(typ),
		Content: content, Summary: summary, Tags: tags, Metadata: meta, Source: source,
		Workspace: workspace, CreatedAt: createdAt, UpdatedAt: updatedAt,
		LastAccessAt: lastAccessAt, AccessCount: accessCount, Activation: activation,
		ContentHash: contentHash,
	}, nil
}

// memoryColsPG omits the embedding column (unlike the SQLite memoryCols):
// GetMemory never returns the vector and skipping it saves bandwidth.
const memoryColsPG = "id, layer, type, content, summary, tags, metadata, source, workspace, created_at, updated_at, last_access_at, access_count, activation, content_hash"

// GetMemory returns the memory with the given id, or nil.
func (s *PostgresStore) GetMemory(id string) (*schema.Memory, error) {
	rows, err := s.pool.Query(context.Background(),
		"SELECT "+memoryColsPG+" FROM memories WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	return scanMemoryPG(rows)
}

// DeleteMemory deletes a memory (the HNSW index entry goes with the row).
func (s *PostgresStore) DeleteMemory(id string) error {
	_, err := s.pool.Exec(context.Background(), "DELETE FROM memories WHERE id = $1", id)
	return err
}

// UpdateMemoryContent patches content/summary/tags/updated_at in place. A nil
// vector leaves the embedding column and content_hash alone (unlike the
// PutMemory upsert, which would NULL them); a non-nil vector rewrites the
// embedding and recomputes content_hash. A missing id is a no-op.
func (s *PostgresStore) UpdateMemoryContent(id, content, summary string, tags []string, vector []float32, now float64) error {
	if now == 0 {
		now = schema.Now()
	}
	tagsJSON, _ := json.Marshal(tags)
	var err error
	if vector == nil {
		_, err = s.pool.Exec(context.Background(),
			"UPDATE memories SET content = $1, summary = $2, tags = $3, updated_at = $4 WHERE id = $5",
			content, summary, string(tagsJSON), now, id)
	} else {
		_, err = s.pool.Exec(context.Background(),
			"UPDATE memories SET content = $1, summary = $2, tags = $3, updated_at = $4, embedding = $5, content_hash = $6 WHERE id = $7",
			content, summary, string(tagsJSON), now, pgvector.NewVector(vector), schema.ContentHash(content), id)
	}
	return err
}

// TouchMemories bumps access_count / last_access_at for every listed id in a
// single UPDATE (recall's access bookkeeping used to issue one UPDATE per id).
// An empty slice is a no-op.
func (s *PostgresStore) TouchMemories(ids []string, now float64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.pool.Exec(context.Background(),
		"UPDATE memories SET last_access_at = $1, access_count = access_count + 1 WHERE id = ANY($2)",
		now, ids)
	return err
}

// Ping checks storage connectivity (health probe).
func (s *PostgresStore) Ping() error {
	return s.pool.Ping(context.Background())
}

// IterMemories returns memories matching the optional filters.
func (s *PostgresStore) IterMemories(workspace, layer, typ string) ([]*schema.Memory, error) {
	q := "SELECT " + memoryColsPG + " FROM memories WHERE 1=1"
	var args []any
	if workspace != "" {
		args = append(args, workspace)
		q += " AND workspace = " + ph(len(args))
	}
	if layer != "" {
		args = append(args, layer)
		q += " AND layer = " + ph(len(args))
	}
	if typ != "" {
		args = append(args, typ)
		q += " AND type = " + ph(len(args))
	}
	rows, err := s.pool.Query(context.Background(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*schema.Memory
	for rows.Next() {
		m, err := scanMemoryPG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// FindByHash returns the memory with the given content hash (optionally
// scoped to workspace), or nil.
func (s *PostgresStore) FindByHash(contentHash, workspace string) (*schema.Memory, error) {
	q := "SELECT " + memoryColsPG + " FROM memories WHERE content_hash = $1"
	args := []any{contentHash}
	if workspace != "" {
		args = append(args, workspace)
		q += " AND workspace = " + ph(len(args))
	}
	rows, err := s.pool.Query(context.Background(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	return scanMemoryPG(rows)
}

// EpisodicContentsSince returns the content strings of episodic events in
// workspace created at or after since (attention gate recent-duplicate scan).
func (s *PostgresStore) EpisodicContentsSince(workspace string, since float64) ([]string, error) {
	rows, err := s.pool.Query(context.Background(),
		"SELECT content FROM memories WHERE workspace = $1 AND layer = $2 AND created_at >= $3",
		workspace, string(schema.LayerEpisodic), since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Count returns a "layer/type" → count map (optionally scoped to workspace).
func (s *PostgresStore) Count(workspace string) (map[string]int, error) {
	q := "SELECT layer, type, COUNT(*) FROM memories"
	var args []any
	if workspace != "" {
		args = append(args, workspace)
		q += " WHERE workspace = " + ph(len(args))
	}
	q += " GROUP BY layer, type"
	rows, err := s.pool.Query(context.Background(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var layer, typ string
		var n int
		if err := rows.Scan(&layer, &typ, &n); err != nil {
			return nil, err
		}
		out[fmt.Sprintf("%s/%s", layer, typ)] = n
	}
	return out, rows.Err()
}

// ---- edge CRUD ----

// PutEdge inserts or updates an edge.
func (s *PostgresStore) PutEdge(e *schema.Edge) error {
	meta, _ := json.Marshal(e.Metadata)
	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO edges (id, src_id, relation, dst_id, weight, valid_from, valid_to, metadata)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (id) DO UPDATE SET
		   src_id=excluded.src_id, relation=excluded.relation, dst_id=excluded.dst_id,
		   weight=excluded.weight, valid_from=excluded.valid_from,
		   valid_to=excluded.valid_to, metadata=excluded.metadata`,
		e.ID, e.SrcID, e.Relation, e.DstID, e.Weight, e.ValidFrom, e.ValidTo, string(meta))
	return err
}

func scanEdgePG(row pgx.Rows) (*schema.Edge, error) {
	var (
		id, srcID, relation, dstID string
		weight, validFrom          float64
		validTo                    *float64
		metaJSON                   []byte
	)
	if err := row.Scan(&id, &srcID, &relation, &dstID, &weight, &validFrom, &validTo, &metaJSON); err != nil {
		return nil, err
	}
	var meta map[string]any
	_ = json.Unmarshal(metaJSON, &meta)
	if meta == nil {
		meta = map[string]any{}
	}
	return &schema.Edge{
		ID: id, SrcID: srcID, Relation: relation, DstID: dstID,
		Weight: weight, ValidFrom: validFrom, ValidTo: validTo, Metadata: meta,
	}, nil
}

const edgeColsPG = "id, src_id, relation, dst_id, weight, valid_from, valid_to, metadata"

// Neighbors returns valid (valid_to IS NULL) edges touching memID.
func (s *PostgresStore) Neighbors(memID, relation string) ([]*schema.Edge, error) {
	q := "SELECT " + edgeColsPG + " FROM edges WHERE (src_id = $1 OR dst_id = $1)"
	args := []any{memID}
	if relation != "" {
		args = append(args, relation)
		q += " AND relation = " + ph(len(args))
	}
	q += " AND valid_to IS NULL"
	rows, err := s.pool.Query(context.Background(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*schema.Edge
	for rows.Next() {
		e, err := scanEdgePG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountEdges returns the number of edges.
func (s *PostgresStore) CountEdges() (int, error) {
	var n int
	err := s.pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM edges").Scan(&n)
	return n, err
}

// NeighborCounts returns {memory_id: neighbour_count} for spreading activation.
func (s *PostgresStore) NeighborCounts() (map[string]int, error) {
	rows, err := s.pool.Query(context.Background(),
		"SELECT src_id AS id FROM edges WHERE valid_to IS NULL UNION ALL "+
			"SELECT dst_id AS id FROM edges WHERE valid_to IS NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		counts[id]++
	}
	return counts, rows.Err()
}

// InvalidateEdge marks an edge no longer current (sets valid_to).
func (s *PostgresStore) InvalidateEdge(edgeID string, t float64) error {
	_, err := s.pool.Exec(context.Background(), "UPDATE edges SET valid_to = $1 WHERE id = $2", t, edgeID)
	return err
}

// ---- code symbol projections ----

// PutCodeSymbol inserts or updates a code symbol projection.
func (s *PostgresStore) PutCodeSymbol(sym *schema.CodeSymbol) error {
	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO code_symbols (memory_id, file_path, symbol_kind, qualified_name,
		                           signature, docstring, line_start, line_end, language)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 ON CONFLICT (memory_id) DO UPDATE SET
		   file_path=excluded.file_path, symbol_kind=excluded.symbol_kind,
		   qualified_name=excluded.qualified_name, signature=excluded.signature,
		   docstring=excluded.docstring, line_start=excluded.line_start,
		   line_end=excluded.line_end, language=excluded.language`,
		sym.MemoryID, sym.FilePath, sym.SymbolKind, sym.QualifiedName,
		sym.Signature, sym.Docstring, sym.LineStart, sym.LineEnd, sym.Language)
	return err
}

// PutCodeRefs bulk-inserts cross references atomically: one transaction, so a
// failure rolls back the whole batch.
func (s *PostgresStore) PutCodeRefs(refs []*schema.CodeRef) error {
	if len(refs) == 0 {
		return nil
	}
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, r := range refs {
		if _, err := tx.Exec(ctx,
			"INSERT INTO code_refs (src_symbol, dst_symbol, ref_kind) VALUES ($1,$2,$3)",
			r.SrcSymbol, r.DstSymbol, r.RefKind); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SymbolsForFile returns code symbols for a file, ordered by line.
func (s *PostgresStore) SymbolsForFile(filePath string) ([]*schema.CodeSymbol, error) {
	rows, err := s.pool.Query(context.Background(),
		"SELECT memory_id, file_path, symbol_kind, qualified_name, signature, docstring, line_start, line_end, language FROM code_symbols WHERE file_path = $1 ORDER BY line_start",
		filePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*schema.CodeSymbol
	for rows.Next() {
		var sym schema.CodeSymbol
		if err := rows.Scan(&sym.MemoryID, &sym.FilePath, &sym.SymbolKind, &sym.QualifiedName,
			&sym.Signature, &sym.Docstring, &sym.LineStart, &sym.LineEnd, &sym.Language); err != nil {
			return nil, err
		}
		out = append(out, &sym)
	}
	return out, rows.Err()
}

// RefsForSymbol returns cross references for a qualified name.
func (s *PostgresStore) RefsForSymbol(qualifiedName, direction string) ([]*schema.CodeRef, error) {
	ctx := context.Background()
	var out []*schema.CodeRef
	if direction == "out" || direction == "both" {
		rows, err := s.pool.Query(ctx, "SELECT src_symbol, dst_symbol, ref_kind FROM code_refs WHERE src_symbol = $1", qualifiedName)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r schema.CodeRef
			if err := rows.Scan(&r.SrcSymbol, &r.DstSymbol, &r.RefKind); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, &r)
		}
		rows.Close()
	}
	if direction == "in" || direction == "both" {
		rows, err := s.pool.Query(ctx, "SELECT src_symbol, dst_symbol, ref_kind FROM code_refs WHERE dst_symbol = $1", qualifiedName)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r schema.CodeRef
			if err := rows.Scan(&r.SrcSymbol, &r.DstSymbol, &r.RefKind); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, &r)
		}
		rows.Close()
	}
	return out, nil
}

// CountCodeSymbols returns the number of code symbol projections.
func (s *PostgresStore) CountCodeSymbols() (int, error) {
	var n int
	err := s.pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM code_symbols").Scan(&n)
	return n, err
}

// DeleteMemoriesByTypeSource deletes memories matching type+source+workspace
// (code indexer re-index write path).
func (s *PostgresStore) DeleteMemoriesByTypeSource(typ, source, workspace string) error {
	_, err := s.pool.Exec(context.Background(),
		"DELETE FROM memories WHERE type = $1 AND source = $2 AND workspace = $3",
		typ, source, workspace)
	return err
}

// DeleteSymbolMemories deletes the code_symbol memories projecting the given
// qualified name (code indexer re-index write path).
func (s *PostgresStore) DeleteSymbolMemories(qualifiedName, workspace string) error {
	_, err := s.pool.Exec(context.Background(),
		"DELETE FROM memories WHERE type = $1 AND workspace = $2 AND id IN (SELECT memory_id FROM code_symbols WHERE qualified_name = $3)",
		string(schema.TypeCodeSymbol), workspace, qualifiedName)
	return err
}

// ---- index_state ----

// GetIndexedHash returns the recorded body hash for a file, or "".
func (s *PostgresStore) GetIndexedHash(filePath string) (string, error) {
	var h string
	err := s.pool.QueryRow(context.Background(),
		"SELECT body_hash FROM index_state WHERE file_path = $1", filePath).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return h, err
}

// SetIndexed records the body hash for a file.
func (s *PostgresStore) SetIndexed(filePath, bodyHash string, now float64) error {
	if now == 0 {
		now = schema.Now()
	}
	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO index_state (file_path, body_hash, indexed_at) VALUES ($1,$2,$3)
		 ON CONFLICT (file_path) DO UPDATE SET body_hash=excluded.body_hash, indexed_at=excluded.indexed_at`,
		filePath, bodyHash, now)
	return err
}

// Workspaces lists distinct workspaces.
func (s *PostgresStore) Workspaces() ([]string, error) {
	rows, err := s.pool.Query(context.Background(), "SELECT DISTINCT workspace FROM memories ORDER BY workspace")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetMeta returns the value for a meta key, or "".
func (s *PostgresStore) GetMeta(key string) (string, error) {
	var v string
	err := s.pool.QueryRow(context.Background(), "SELECT value FROM meta WHERE key = $1", key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetMeta upserts a meta key/value.
func (s *PostgresStore) SetMeta(key, value string) error {
	_, err := s.pool.Exec(context.Background(),
		"INSERT INTO meta (key, value) VALUES ($1,$2) ON CONFLICT (key) DO UPDATE SET value=excluded.value",
		key, value)
	return err
}

// ---- users (HTTP data-plane Basic auth accounts) ----

// PutUser inserts or updates a user (upsert by username).
func (s *PostgresStore) PutUser(u *schema.User) error {
	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO users (username, password_hash, workspace, admin, created_at)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (username) DO UPDATE SET
		   password_hash=excluded.password_hash, workspace=excluded.workspace,
		   admin=excluded.admin, created_at=excluded.created_at`,
		u.Username, u.PasswordHash, u.Workspace, u.Admin, u.CreatedAt)
	return err
}

// GetUser returns the user with the given username, or nil.
func (s *PostgresStore) GetUser(username string) (*schema.User, error) {
	rows, err := s.pool.Query(context.Background(),
		"SELECT username, password_hash, workspace, admin, created_at FROM users WHERE username = $1", username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var u schema.User
	if err := rows.Scan(&u.Username, &u.PasswordHash, &u.Workspace, &u.Admin, &u.CreatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

// DeleteUser deletes a user; a missing username is a no-op.
func (s *PostgresStore) DeleteUser(username string) error {
	_, err := s.pool.Exec(context.Background(), "DELETE FROM users WHERE username = $1", username)
	return err
}

// ListUsers returns all users sorted by username.
func (s *PostgresStore) ListUsers() ([]*schema.User, error) {
	rows, err := s.pool.Query(context.Background(),
		"SELECT username, password_hash, workspace, admin, created_at FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*schema.User
	for rows.Next() {
		var u schema.User
		if err := rows.Scan(&u.Username, &u.PasswordHash, &u.Workspace, &u.Admin, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &u)
	}
	return out, rows.Err()
}

// ---- cross-process index lock (pg advisory lock) ----

// TryAcquireIndexLock takes the cross-process code-index lock as a pg
// advisory lock. Advisory locks are session-scoped, so a dedicated connection
// is acquired from the pool and held until the returned release function runs
// (pg_advisory_unlock + conn.Release). Contention fails fast with
// ErrIndexLockHeld — callers do not queue.
func (s *PostgresStore) TryAcquireIndexLock() (func(), error) {
	ctx := context.Background()
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", indexLockAdvisoryKey).Scan(&ok); err != nil {
		conn.Release()
		return nil, err
	}
	if !ok {
		conn.Release()
		return nil, ErrIndexLockHeld
	}
	return func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", indexLockAdvisoryKey)
		conn.Release()
	}, nil
}
