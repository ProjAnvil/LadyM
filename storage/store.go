package storage

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/ProjAnvil/LadyM/schema"
	_ "modernc.org/sqlite"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS memories (
    id              TEXT PRIMARY KEY,
    layer           TEXT NOT NULL,
    type            TEXT NOT NULL,
    content         TEXT NOT NULL,
    summary         TEXT NOT NULL DEFAULT '',
    tags            TEXT NOT NULL DEFAULT '[]',
    metadata        TEXT NOT NULL DEFAULT '{}',
    source          TEXT NOT NULL DEFAULT '',
    workspace       TEXT NOT NULL DEFAULT 'default',
    created_at      REAL NOT NULL,
    updated_at      REAL NOT NULL,
    last_access_at  REAL NOT NULL,
    access_count    INTEGER NOT NULL DEFAULT 0,
    activation      REAL NOT NULL DEFAULT 0.0,
    content_hash    TEXT NOT NULL DEFAULT '',
    embedding       BLOB
);
CREATE INDEX IF NOT EXISTS idx_mem_layer_ws ON memories(layer, workspace);
CREATE INDEX IF NOT EXISTS idx_mem_type_ws ON memories(type, workspace);
CREATE INDEX IF NOT EXISTS idx_mem_ws ON memories(workspace);
CREATE INDEX IF NOT EXISTS idx_mem_source ON memories(source);
CREATE INDEX IF NOT EXISTS idx_mem_hash ON memories(content_hash);

CREATE TABLE IF NOT EXISTS edges (
    id          TEXT PRIMARY KEY,
    src_id      TEXT NOT NULL,
    relation    TEXT NOT NULL,
    dst_id      TEXT NOT NULL,
    weight      REAL NOT NULL DEFAULT 1.0,
    valid_from  REAL NOT NULL,
    valid_to    REAL,
    metadata    TEXT NOT NULL DEFAULT '{}',
    FOREIGN KEY (src_id) REFERENCES memories(id) ON DELETE CASCADE,
    FOREIGN KEY (dst_id) REFERENCES memories(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_edge_src ON edges(src_id);
CREATE INDEX IF NOT EXISTS idx_edge_dst ON edges(dst_id);

CREATE TABLE IF NOT EXISTS code_symbols (
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
);
CREATE INDEX IF NOT EXISTS idx_sym_file ON code_symbols(file_path);
CREATE INDEX IF NOT EXISTS idx_sym_qname ON code_symbols(qualified_name);

CREATE TABLE IF NOT EXISTS code_refs (
    src_symbol  TEXT NOT NULL,
    dst_symbol  TEXT NOT NULL,
    ref_kind    TEXT NOT NULL DEFAULT 'calls'
);
CREATE INDEX IF NOT EXISTS idx_ref_src ON code_refs(src_symbol);
CREATE INDEX IF NOT EXISTS idx_ref_dst ON code_refs(dst_symbol);

CREATE TABLE IF NOT EXISTS index_state (
    file_path   TEXT PRIMARY KEY,
    body_hash   TEXT NOT NULL,
    indexed_at  REAL NOT NULL
);

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

func encodeVec(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

func decodeVec(b []byte) []float32 {
	n := len(b) / 4
	out := make([]float32, n)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}

// SQLiteStore is the single persistence layer for LadyM. It owns the SQLite
// connection and the (in-memory) vector index.
type SQLiteStore struct {
	DBPath      string
	Dim         int
	db          *sql.DB
	vectorIndex VectorIndex
	usingVec    bool
}

// NewStore opens (or creates) a SQLite store at dbPath.
//
// NOTE: the Python port used the sqlite-vec loadable extension for persistent
// ANN search. The Go port always uses the pure-Go InMemoryVectorIndex (the
// sqlite-vec C extension has no Go binding); embeddings are still persisted as
// BLOBs and warmed into the index on reopen, so behaviour is identical. The
// preferSQLiteVec flag is accepted for config parity but has no effect.
func NewStore(dbPath string, dim int, preferSQLiteVec bool, enableWAL bool) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	// A single connection keeps PRAGMA state (foreign_keys, WAL) stable and
	// avoids SQLITE_BUSY on concurrent writes within one engine. A separate
	// Engine (worker) opens its own connection; WAL lets the two coexist.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, err
	}
	if enableWAL {
		if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, err
	}
	// migrate: add embedding column to pre-existing DBs (idempotent)
	cols, _ := db.Query("PRAGMA table_info(memories)")
	hasEmbedding := false
	if cols != nil {
		for cols.Next() {
			var cid int
			var name, ctype string
			var notnull, pk int
			var dflt sql.NullString
			if err := cols.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err == nil && name == "embedding" {
				hasEmbedding = true
			}
		}
		cols.Close()
	}
	if !hasEmbedding {
		if _, err := db.Exec("ALTER TABLE memories ADD COLUMN embedding BLOB"); err != nil {
			db.Close()
			return nil, err
		}
	}

	s := &SQLiteStore{DBPath: dbPath, Dim: dim, db: db, vectorIndex: NewInMemoryVectorIndex(dim)}
	s.warmIndexFromBlobs()
	return s, nil
}

// UsingSQLiteVec always returns false (see NewStore).
func (s *SQLiteStore) UsingSQLiteVec() bool { return s.usingVec }

func (s *SQLiteStore) warmIndexFromBlobs() {
	rows, err := s.db.Query("SELECT id, embedding FROM memories WHERE embedding IS NOT NULL")
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			continue
		}
		vec := decodeVec(blob)
		if len(vec) == s.Dim {
			_ = s.vectorIndex.Upsert(id, vec)
		}
	}
}

// Close commits and closes the underlying connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// RebuildVectorIndex resets the index at a new dim (used on dim change).
func (s *SQLiteStore) RebuildVectorIndex(newDim int) {
	s.Dim = newDim
	s.vectorIndex = NewInMemoryVectorIndex(newDim)
}

// DB exposes the underlying *sql.DB for low-level queries (layers, operations).
func (s *SQLiteStore) DB() *sql.DB { return s.db }

// VectorIndex exposes the vector index.
func (s *SQLiteStore) VectorIndex() VectorIndex { return s.vectorIndex }

// ---- memory CRUD ----

func (s *SQLiteStore) PutMemory(mem *schema.Memory, vector []float32) error {
	var blob any
	if vector != nil {
		blob = encodeVec(vector)
	}
	tags, _ := json.Marshal(mem.Tags)
	meta, _ := json.Marshal(mem.Metadata)
	if _, err := s.db.Exec(
		`INSERT INTO memories (id, layer, type, content, summary, tags, metadata,
		                       source, workspace, created_at, updated_at,
		                       last_access_at, access_count, activation, content_hash, embedding)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		   layer=excluded.layer, type=excluded.type, content=excluded.content,
		   summary=excluded.summary, tags=excluded.tags, metadata=excluded.metadata,
		   source=excluded.source, workspace=excluded.workspace,
		   updated_at=excluded.updated_at, last_access_at=excluded.last_access_at,
		   access_count=excluded.access_count, activation=excluded.activation,
		   content_hash=excluded.content_hash, embedding=excluded.embedding`,
		mem.ID, mem.Layer, mem.Type, mem.Content, mem.Summary, string(tags), string(meta),
		mem.Source, mem.Workspace, mem.CreatedAt, mem.UpdatedAt,
		mem.LastAccessAt, mem.AccessCount, mem.Activation, mem.ContentHash, blob,
	); err != nil {
		return err
	}
	if vector != nil {
		return s.vectorIndex.Upsert(mem.ID, vector)
	}
	return nil
}

func (s *SQLiteStore) scanMemory(row *sql.Rows) (*schema.Memory, error) {
	var (
		id, layer, typ, content, summary, tagsJSON, metaJSON, source, workspace string
		createdAt, updatedAt, lastAccessAt, activation                          float64
		accessCount                                                             int
		contentHash                                                             string
		embedding                                                               []byte
	)
	if err := row.Scan(&id, &layer, &typ, &content, &summary, &tagsJSON, &metaJSON, &source,
		&workspace, &createdAt, &updatedAt, &lastAccessAt, &accessCount, &activation,
		&contentHash, &embedding); err != nil {
		return nil, err
	}
	var tags []string
	var meta map[string]any
	_ = json.Unmarshal([]byte(tagsJSON), &tags)
	_ = json.Unmarshal([]byte(metaJSON), &meta)
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

const memoryCols = "id, layer, type, content, summary, tags, metadata, source, workspace, created_at, updated_at, last_access_at, access_count, activation, content_hash, embedding"

// GetMemory returns the memory with the given id, or nil.
func (s *SQLiteStore) GetMemory(id string) (*schema.Memory, error) {
	rows, err := s.db.Query("SELECT "+memoryCols+" FROM memories WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	return s.scanMemory(rows)
}

// DeleteMemory deletes a memory and its vector entry.
func (s *SQLiteStore) DeleteMemory(id string) error {
	if _, err := s.db.Exec("DELETE FROM memories WHERE id = ?", id); err != nil {
		return err
	}
	s.vectorIndex.Delete(id)
	return nil
}

// TouchMemory bumps access_count / last_access_at.
func (s *SQLiteStore) TouchMemory(id string, now float64) error {
	_, err := s.db.Exec(
		"UPDATE memories SET last_access_at = ?, access_count = access_count + 1 WHERE id = ?",
		now, id)
	return err
}

// IterMemories returns memories matching the optional filters.
func (s *SQLiteStore) IterMemories(workspace, layer, typ string) ([]*schema.Memory, error) {
	q := "SELECT " + memoryCols + " FROM memories WHERE 1=1"
	var args []any
	if workspace != "" {
		q += " AND workspace = ?"
		args = append(args, workspace)
	}
	if layer != "" {
		q += " AND layer = ?"
		args = append(args, layer)
	}
	if typ != "" {
		q += " AND type = ?"
		args = append(args, typ)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*schema.Memory
	for rows.Next() {
		m, err := s.scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// FindByHash returns the memory with the given content hash (optionally
// scoped to workspace), or nil.
func (s *SQLiteStore) FindByHash(contentHash, workspace string) (*schema.Memory, error) {
	q := "SELECT " + memoryCols + " FROM memories WHERE content_hash = ?"
	var args []any = []any{contentHash}
	if workspace != "" {
		q += " AND workspace = ?"
		args = append(args, workspace)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	return s.scanMemory(rows)
}

// EpisodicContentsSince returns the content strings of episodic events in
// workspace created at or after since (used by the attention gate's
// recent-duplicate scan; pushes the time-window cut into SQL).
func (s *SQLiteStore) EpisodicContentsSince(workspace string, since float64) ([]string, error) {
	rows, err := s.db.Query(
		"SELECT content FROM memories WHERE workspace = ? AND layer = ? AND created_at >= ?",
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
func (s *SQLiteStore) Count(workspace string) (map[string]int, error) {
	q := "SELECT layer, type, COUNT(*) FROM memories"
	var args []any
	if workspace != "" {
		q += " WHERE workspace = ?"
		args = append(args, workspace)
	}
	q += " GROUP BY layer, type"
	rows, err := s.db.Query(q, args...)
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
func (s *SQLiteStore) PutEdge(e *schema.Edge) error {
	var validTo any
	if e.ValidTo != nil {
		validTo = *e.ValidTo
	}
	meta, _ := json.Marshal(e.Metadata)
	_, err := s.db.Exec(
		`INSERT INTO edges (id, src_id, relation, dst_id, weight, valid_from, valid_to, metadata)
		 VALUES (?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		   src_id=excluded.src_id, relation=excluded.relation, dst_id=excluded.dst_id,
		   weight=excluded.weight, valid_from=excluded.valid_from,
		   valid_to=excluded.valid_to, metadata=excluded.metadata`,
		e.ID, e.SrcID, e.Relation, e.DstID, e.Weight, e.ValidFrom, validTo, string(meta))
	return err
}

func (s *SQLiteStore) scanEdge(row *sql.Rows) (*schema.Edge, error) {
	var (
		id, srcID, relation, dstID string
		weight, validFrom          float64
		validTo                    sql.NullFloat64
		metaJSON                   string
	)
	if err := row.Scan(&id, &srcID, &relation, &dstID, &weight, &validFrom, &validTo, &metaJSON); err != nil {
		return nil, err
	}
	var meta map[string]any
	_ = json.Unmarshal([]byte(metaJSON), &meta)
	if meta == nil {
		meta = map[string]any{}
	}
	var vt *float64
	if validTo.Valid {
		vt = &validTo.Float64
	}
	return &schema.Edge{
		ID: id, SrcID: srcID, Relation: relation, DstID: dstID,
		Weight: weight, ValidFrom: validFrom, ValidTo: vt, Metadata: meta,
	}, nil
}

const edgeCols = "id, src_id, relation, dst_id, weight, valid_from, valid_to, metadata"

// Neighbors returns valid (valid_to IS NULL) edges touching memID.
func (s *SQLiteStore) Neighbors(memID, relation string) ([]*schema.Edge, error) {
	q := "SELECT " + edgeCols + " FROM edges WHERE (src_id = ? OR dst_id = ?)"
	args := []any{memID, memID}
	if relation != "" {
		q += " AND relation = ?"
		args = append(args, relation)
	}
	q += " AND valid_to IS NULL"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*schema.Edge
	for rows.Next() {
		e, err := s.scanEdge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountEdges returns the number of edges.
func (s *SQLiteStore) CountEdges() (int, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM edges").Scan(&n)
	return n, err
}

// NeighborCounts returns {memory_id: neighbour_count} for spreading activation.
func (s *SQLiteStore) NeighborCounts() (map[string]int, error) {
	rows, err := s.db.Query(
		"SELECT src_id AS id FROM edges WHERE valid_to IS NULL UNION ALL " +
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

// ---- code symbol projections ----

// PutCodeSymbol inserts or updates a code symbol projection.
func (s *SQLiteStore) PutCodeSymbol(sym *schema.CodeSymbol) error {
	_, err := s.db.Exec(
		`INSERT INTO code_symbols (memory_id, file_path, symbol_kind, qualified_name,
		                           signature, docstring, line_start, line_end, language)
		 VALUES (?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(memory_id) DO UPDATE SET
		   file_path=excluded.file_path, symbol_kind=excluded.symbol_kind,
		   qualified_name=excluded.qualified_name, signature=excluded.signature,
		   docstring=excluded.docstring, line_start=excluded.line_start,
		   line_end=excluded.line_end, language=excluded.language`,
		sym.MemoryID, sym.FilePath, sym.SymbolKind, sym.QualifiedName,
		sym.Signature, sym.Docstring, sym.LineStart, sym.LineEnd, sym.Language)
	return err
}

// PutCodeRefs bulk-inserts cross references atomically: one transaction, so a
// failure rolls back the whole batch (Python: executemany in a single commit).
func (s *SQLiteStore) PutCodeRefs(refs []*schema.CodeRef) error {
	if len(refs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, r := range refs {
		if _, err := tx.Exec(
			"INSERT INTO code_refs (src_symbol, dst_symbol, ref_kind) VALUES (?,?,?)",
			r.SrcSymbol, r.DstSymbol, r.RefKind); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// SymbolsForFile returns code symbols for a file, ordered by line.
func (s *SQLiteStore) SymbolsForFile(filePath string) ([]*schema.CodeSymbol, error) {
	rows, err := s.db.Query(
		"SELECT memory_id, file_path, symbol_kind, qualified_name, signature, docstring, line_start, line_end, language FROM code_symbols WHERE file_path = ? ORDER BY line_start",
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
func (s *SQLiteStore) RefsForSymbol(qualifiedName, direction string) ([]*schema.CodeRef, error) {
	var out []*schema.CodeRef
	if direction == "out" || direction == "both" {
		rows, err := s.db.Query("SELECT src_symbol, dst_symbol, ref_kind FROM code_refs WHERE src_symbol = ?", qualifiedName)
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
		rows, err := s.db.Query("SELECT src_symbol, dst_symbol, ref_kind FROM code_refs WHERE dst_symbol = ?", qualifiedName)
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

// ---- index_state ----

// GetIndexedHash returns the recorded body hash for a file, or "".
func (s *SQLiteStore) GetIndexedHash(filePath string) (string, error) {
	var h string
	err := s.db.QueryRow("SELECT body_hash FROM index_state WHERE file_path = ?", filePath).Scan(&h)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return h, err
}

// SetIndexed records the body hash for a file.
func (s *SQLiteStore) SetIndexed(filePath, bodyHash string, now float64) error {
	if now == 0 {
		now = schema.Now()
	}
	_, err := s.db.Exec(
		`INSERT INTO index_state (file_path, body_hash, indexed_at) VALUES (?,?,?)
		 ON CONFLICT(file_path) DO UPDATE SET body_hash=excluded.body_hash, indexed_at=excluded.indexed_at`,
		filePath, bodyHash, now)
	return err
}

// Workspaces lists distinct workspaces.
func (s *SQLiteStore) Workspaces() ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT workspace FROM memories ORDER BY workspace")
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
func (s *SQLiteStore) GetMeta(key string) (string, error) {
	var v string
	err := s.db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// SetMeta upserts a meta key/value.
func (s *SQLiteStore) SetMeta(key, value string) error {
	_, err := s.db.Exec(
		"INSERT INTO meta (key, value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
		key, value)
	return err
}
