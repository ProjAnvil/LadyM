//go:build !enterprise

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/ProjAnvil/LadyM/schema"
)

const vectorGenerationSQL = `
CREATE TABLE IF NOT EXISTS memory_vector_generation (
	id INTEGER PRIMARY KEY CHECK (id=1), generation INTEGER NOT NULL
);
INSERT OR IGNORE INTO memory_vector_generation VALUES (1,0);
CREATE TRIGGER IF NOT EXISTS memories_vector_insert AFTER INSERT ON memories BEGIN
	UPDATE memory_vector_generation SET generation=generation+1 WHERE id=1;
END;
CREATE TRIGGER IF NOT EXISTS memories_vector_delete AFTER DELETE ON memories BEGIN
	UPDATE memory_vector_generation SET generation=generation+1 WHERE id=1;
END;
CREATE TRIGGER IF NOT EXISTS memories_vector_update AFTER UPDATE OF embedding ON memories
WHEN NEW.embedding IS NOT OLD.embedding BEGIN
	UPDATE memory_vector_generation SET generation=generation+1 WHERE id=1;
END;
`

// SQLite cannot index membership of a JSON array. This narrow materialized
// lookup keeps the deletion recheck keyed by source ID, including raw SQL writes.
const legacyEvidenceIndexSQL = `
CREATE TABLE IF NOT EXISTS memory_evidence_sources (
	memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
	source_id TEXT NOT NULL,
	PRIMARY KEY(memory_id,source_id)
);
CREATE INDEX IF NOT EXISTS idx_evidence_source ON memory_evidence_sources(source_id);
DROP TRIGGER IF EXISTS memories_evidence_insert;
DROP TRIGGER IF EXISTS memories_evidence_update;
CREATE TRIGGER memories_evidence_insert AFTER INSERT ON memories BEGIN
	INSERT INTO memory_evidence_sources
		SELECT NEW.id,json_extract(NEW.metadata,'$.source_episode')
		WHERE json_type(NEW.metadata,'$.source_episode')='text'
			AND json_extract(NEW.metadata,'$.source_episode')<>''
		ON CONFLICT(memory_id,source_id) DO NOTHING;
	INSERT INTO memory_evidence_sources
		SELECT NEW.id,value FROM json_each(NEW.metadata,'$.source_episodes')
		WHERE json_type(NEW.metadata,'$.source_episodes')='array' AND type='text' AND value<>''
		ON CONFLICT(memory_id,source_id) DO NOTHING;
END;
CREATE TRIGGER memories_evidence_update AFTER UPDATE OF metadata ON memories
WHEN NEW.metadata IS NOT OLD.metadata BEGIN
	DELETE FROM memory_evidence_sources WHERE memory_id=NEW.id;
	INSERT INTO memory_evidence_sources
		SELECT NEW.id,json_extract(NEW.metadata,'$.source_episode')
		WHERE json_type(NEW.metadata,'$.source_episode')='text'
			AND json_extract(NEW.metadata,'$.source_episode')<>''
		ON CONFLICT(memory_id,source_id) DO NOTHING;
	INSERT INTO memory_evidence_sources
		SELECT NEW.id,value FROM json_each(NEW.metadata,'$.source_episodes')
		WHERE json_type(NEW.metadata,'$.source_episodes')='array' AND type='text' AND value<>''
		ON CONFLICT(memory_id,source_id) DO NOTHING;
END;
`

func installLegacyEvidenceIndex(db *sql.DB) error {
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE memory_vector_generation SET generation=generation WHERE id=1"); err != nil {
		return err
	}
	var exists int
	if err := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='memory_evidence_sources'").Scan(&exists); err != nil {
		return err
	}
	if _, err := tx.Exec(legacyEvidenceIndexSQL); err != nil {
		return err
	}
	if exists == 0 {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO memory_evidence_sources
			SELECT memories.id,json_extract(metadata,'$.source_episode') FROM memories
			WHERE json_type(metadata,'$.source_episode')='text' AND json_extract(metadata,'$.source_episode')<>''
			UNION
			SELECT memories.id,value FROM memories,json_each(memories.metadata,'$.source_episodes')
			WHERE json_type(memories.metadata,'$.source_episodes')='array' AND json_each.type='text' AND value<>''`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type sqliteMemoryTransaction struct {
	store               *SQLiteStore
	tx                  *sql.Tx
	migrationAuthorized bool
}

func (s *SQLiteStore) beginMemoryTransaction() (*sqliteMemoryTransaction, error) {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	// Acquire SQLite's writer reservation before taking any read snapshot.
	// This deliberately does not change the vector generation.
	if _, err := tx.Exec("UPDATE memory_vector_generation SET generation=generation WHERE id=1"); err != nil {
		tx.Rollback()
		return nil, err
	}
	return &sqliteMemoryTransaction{store: s, tx: tx}, nil
}

func (tx *sqliteMemoryTransaction) commit() error {
	if tx.migrationAuthorized {
		if err := tx.exec("UPDATE embedding_migration SET internal=0 WHERE id=1"); err != nil {
			return err
		}
	}
	return tx.tx.Commit()
}
func (tx *sqliteMemoryTransaction) rollback() { _ = tx.tx.Rollback() }

func (tx *sqliteMemoryTransaction) queryMemories(query string, args ...any) ([]*schema.Memory, error) {
	rows, err := tx.tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*schema.Memory
	for rows.Next() {
		m, err := tx.store.scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

const memoryReadCols = "id, layer, type, content, summary, tags, metadata, source, workspace, created_at, updated_at, last_access_at, access_count, activation, content_hash, NULL"

func (tx *sqliteMemoryTransaction) get(id string) (*schema.Memory, error) {
	memories, err := tx.queryMemories("SELECT "+memoryReadCols+" FROM memories WHERE id=?", id)
	if err != nil || len(memories) == 0 {
		return nil, err
	}
	return memories[0], nil
}

func (tx *sqliteMemoryTransaction) memories(workspace string) ([]*schema.Memory, error) {
	return tx.queryMemories("SELECT "+memoryReadCols+" FROM memories WHERE (?='' OR workspace=?)", workspace, workspace)
}

func (tx *sqliteMemoryTransaction) dependants(id, workspace string) ([]*schema.Memory, error) {
	return tx.queryMemories("SELECT "+memoryReadCols+` FROM memories
		WHERE workspace=? AND id IN (
			SELECT src_id FROM edges WHERE dst_id=? AND valid_to IS NULL AND relation IN ('produced_from','abstracts')
			UNION SELECT memory_id FROM memory_evidence_sources WHERE source_id=?)`, workspace, id, id)
}

func (tx *sqliteMemoryTransaction) edges(id string) ([]*schema.Edge, error) {
	rows, err := tx.tx.Query("SELECT id, src_id, relation, dst_id, weight, valid_from, valid_to, metadata FROM edges WHERE (?='' OR dst_id=?) AND valid_to IS NULL AND relation IN ('produced_from','abstracts')", id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*schema.Edge
	for rows.Next() {
		e, err := tx.store.scanEdge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (tx *sqliteMemoryTransaction) put(w MemoryWrite, preserveAccess bool) error {
	var blob any
	if w.Vector != nil && !w.PreserveEmbedding {
		blob = encodeVec(w.Vector)
	}
	_, err := tx.tx.Exec(memoryUpsertSQL(false, w.PreserveEmbedding, preserveAccess), memoryArgs(w, blob)...)
	return err
}

func (tx *sqliteMemoryTransaction) putEdge(e *schema.Edge) error {
	meta, _ := json.Marshal(e.Metadata)
	_, err := tx.tx.Exec(`INSERT INTO edges (id,src_id,relation,dst_id,weight,valid_from,valid_to,metadata)
		VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
		src_id=excluded.src_id, relation=excluded.relation, dst_id=excluded.dst_id, weight=excluded.weight,
		valid_from=excluded.valid_from, valid_to=excluded.valid_to, metadata=excluded.metadata`,
		e.ID, e.SrcID, e.Relation, e.DstID, e.Weight, e.ValidFrom, e.ValidTo, string(meta))
	return err
}

func (tx *sqliteMemoryTransaction) closeEdges(ids []string, now float64) error {
	for _, id := range ids {
		if _, err := tx.tx.Exec("UPDATE edges SET valid_to=? WHERE valid_to IS NULL AND (src_id=? OR dst_id=?)", now, id, id); err != nil {
			return err
		}
	}
	return nil
}

func (tx *sqliteMemoryTransaction) remove(id string) error {
	_, err := tx.tx.Exec("DELETE FROM memories WHERE id=?", id)
	return err
}

func (s *SQLiteStore) updateLocalVector(id string, vector []float32) error {
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	// A committed writer may reach this lock after a newer read snapshot.
	// Never patch a warm snapshot with an unversioned post-commit vector.
	if s.indexWarm {
		return nil
	}
	if vector == nil {
		s.vectorIndex.Delete(id)
		return nil
	}
	if index, ok := s.vectorIndex.(*InMemoryVectorIndex); ok && index.dim != len(vector) {
		s.vectorIndex = NewInMemoryVectorIndex(len(vector))
	}
	return s.vectorIndex.Upsert(id, vector)
}

func (s *SQLiteStore) ApplyMemoryBatch(batch MemoryBatch) error {
	dim, err := s.embeddingDimension()
	if err != nil {
		return err
	}
	if err := validateBatch(batch, dim); err != nil {
		return err
	}
	tx, err := s.beginMemoryTransaction()
	if err != nil {
		return err
	}
	return applyMemoryBatch(tx, batch, true)
}

func (s *SQLiteStore) DeleteMemorySafely(id string, autoBefore float64) (bool, bool, error) {
	if err := validateDeleteCutoff(autoBefore); err != nil {
		return false, false, err
	}
	tx, err := s.beginMemoryTransaction()
	if err != nil {
		return false, false, err
	}
	deleted, protected, err := deleteMemorySafely(tx, id, autoBefore)
	if deleted {
		s.indexMu.Lock()
		s.indexWarm = false
		s.vectorIndex.Delete(id)
		s.indexMu.Unlock()
	}
	return deleted, protected, err
}

func (s *SQLiteStore) EvidenceReferences(workspace string) (map[string]bool, error) {
	read, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer read.Rollback()
	tx := &sqliteMemoryTransaction{store: s, tx: read}
	memories, err := tx.memories(workspace)
	if err != nil {
		return nil, err
	}
	edges, err := tx.edges("")
	if err != nil {
		return nil, err
	}
	refs := evidenceReferences(memories, edges)
	return refs, read.Commit()
}

func (s *SQLiteStore) SearchMemories(queryVec []float32, topK int, filter MemoryFilter) ([]SearchHit, error) {
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	if topK <= 0 {
		return nil, errors.New("topK must be positive")
	}
	read, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer read.Rollback()
	if err := (&sqliteMemoryTransaction{store: s, tx: read}).checkIdentity(); err != nil {
		return nil, err
	}
	var generation int64
	if err := read.QueryRow("SELECT generation FROM memory_vector_generation WHERE id=1").Scan(&generation); err != nil {
		return nil, err
	}
	if s.indexRefreshHook != nil {
		s.indexRefreshHook()
	}
	if err := s.refreshIndex(read, generation); err != nil {
		return nil, err
	}
	if err := validateVector(queryVec, s.vectorIndex.(*InMemoryVectorIndex).dim); err != nil {
		return nil, err
	}
	eligible := map[string]bool{}
	// Eligibility is read from the same snapshot as the generation/vectors,
	// without copying embedding blobs for bookkeeping-only changes.
	q := "SELECT " + memoryReadCols + " FROM memories WHERE embedding IS NOT NULL"
	var args []any
	if filter.Workspace != "" {
		q += " AND workspace=?"
		args = append(args, filter.Workspace)
	}
	if len(filter.Layers) > 0 {
		q += " AND layer IN (" + strings.TrimSuffix(strings.Repeat("?,", len(filter.Layers)), ",") + ")"
		for _, layer := range filter.Layers {
			args = append(args, string(layer))
		}
	}
	if len(filter.Types) > 0 {
		q += " AND type IN (" + strings.TrimSuffix(strings.Repeat("?,", len(filter.Types)), ",") + ")"
		for _, typ := range filter.Types {
			args = append(args, string(typ))
		}
	}
	tx := &sqliteMemoryTransaction{store: s, tx: read}
	memories, err := tx.queryMemories(q, args...)
	if err != nil {
		return nil, err
	}
	for _, m := range memories {
		eligible[m.ID] = filter.matches(m)
	}
	// Filter before truncation; legacy VectorSearch passes an empty filter.
	var out []SearchHit
	for _, hit := range s.vectorIndex.Search(queryVec, max(1, s.vectorIndex.Len())) {
		if eligible[hit.ID] {
			out = append(out, hit)
			if len(out) == topK {
				break
			}
		}
	}
	return out, read.Commit()
}
