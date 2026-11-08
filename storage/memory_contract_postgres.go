package storage

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
)

type postgresMemoryTransaction struct {
	tx pgx.Tx
}

func (s *PostgresStore) beginMemoryTransaction(batch MemoryBatch) (*postgresMemoryTransaction, error) {
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	// Metadata-only references have no FK/key lock. Serialize the dependency
	// predicate with publication/deletion, including ordinary SQL writers.
	if _, err := tx.Exec(ctx, "LOCK TABLE memories, edges IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	var ids []string
	for _, g := range batch.Guards {
		ids = append(ids, g.Expected.ID)
	}
	for _, w := range batch.Writes {
		ids = append(ids, w.Memory.ID)
		ids = append(ids, legacySources(w.Memory)...)
	}
	for _, e := range batch.Edges {
		ids = append(ids, e.SrcID, e.DstID)
	}
	ids = append(ids, batch.CloseEdgesFor...)
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		rows, err := tx.Query(ctx, "SELECT id FROM memories WHERE id=$1 FOR UPDATE", id)
		if err != nil {
			tx.Rollback(ctx)
			return nil, err
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			tx.Rollback(ctx)
			return nil, err
		}
	}
	return &postgresMemoryTransaction{tx: tx}, nil
}

func (tx *postgresMemoryTransaction) commit() error {
	return tx.tx.Commit(context.Background())
}
func (tx *postgresMemoryTransaction) rollback() {
	_ = tx.tx.Rollback(context.Background())
}

func (tx *postgresMemoryTransaction) queryMemories(query string, args ...any) ([]*schema.Memory, error) {
	rows, err := tx.tx.Query(context.Background(), query, args...)
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

func (tx *postgresMemoryTransaction) get(id string) (*schema.Memory, error) {
	memories, err := tx.queryMemories("SELECT "+memoryColsPG+" FROM memories WHERE id=$1 FOR UPDATE", id)
	if err != nil || len(memories) == 0 {
		return nil, err
	}
	return memories[0], nil
}

func (tx *postgresMemoryTransaction) memories(workspace string) ([]*schema.Memory, error) {
	return tx.queryMemories("SELECT "+memoryColsPG+" FROM memories WHERE ($1='' OR workspace=$1)", workspace)
}

func (tx *postgresMemoryTransaction) dependants(id, workspace string) ([]*schema.Memory, error) {
	return tx.queryMemories("SELECT "+memoryColsPG+` FROM memories
		WHERE workspace=$1 AND id IN (
			SELECT src_id FROM edges WHERE dst_id=$2 AND valid_to IS NULL AND relation IN ('produced_from','abstracts')
			UNION SELECT id FROM memories WHERE metadata->'source_episodes' @> jsonb_build_array($2::text)
			UNION SELECT id FROM memories WHERE metadata->>'source_episode'=$2)
		ORDER BY id COLLATE "C" FOR UPDATE`, workspace, id)
}

func (tx *postgresMemoryTransaction) edges(id string) ([]*schema.Edge, error) {
	rows, err := tx.tx.Query(context.Background(), `SELECT id,src_id,relation,dst_id,weight,valid_from,valid_to,metadata FROM edges
		WHERE ($1='' OR dst_id=$1) AND valid_to IS NULL AND relation IN ('produced_from','abstracts')`, id)
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

func (tx *postgresMemoryTransaction) put(w MemoryWrite, preserveAccess bool) error {
	var embedding any
	if w.Vector != nil && !w.PreserveEmbedding {
		embedding = pgvector.NewVector(w.Vector)
	}
	_, err := tx.tx.Exec(context.Background(), memoryUpsertSQL(true, w.PreserveEmbedding, preserveAccess), memoryArgs(w, embedding)...)
	return err
}

func (tx *postgresMemoryTransaction) putEdge(e *schema.Edge) error {
	meta, _ := json.Marshal(e.Metadata)
	_, err := tx.tx.Exec(context.Background(), `INSERT INTO edges (id,src_id,relation,dst_id,weight,valid_from,valid_to,metadata)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO UPDATE SET
		src_id=excluded.src_id, relation=excluded.relation, dst_id=excluded.dst_id, weight=excluded.weight,
		valid_from=excluded.valid_from, valid_to=excluded.valid_to, metadata=excluded.metadata`,
		e.ID, e.SrcID, e.Relation, e.DstID, e.Weight, e.ValidFrom, e.ValidTo, string(meta))
	return err
}

func (tx *postgresMemoryTransaction) closeEdges(ids []string, now float64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.tx.Exec(context.Background(), "UPDATE edges SET valid_to=$1 WHERE valid_to IS NULL AND (src_id=ANY($2) OR dst_id=ANY($2))", now, ids)
	return err
}

func (tx *postgresMemoryTransaction) remove(id string) error {
	_, err := tx.tx.Exec(context.Background(), "DELETE FROM memories WHERE id=$1", id)
	return err
}

func (s *PostgresStore) ApplyMemoryBatch(batch MemoryBatch) error {
	if err := validateBatch(batch, s.dim); err != nil {
		return err
	}
	tx, err := s.beginMemoryTransaction(batch)
	if err != nil {
		return err
	}
	return applyMemoryBatch(tx, batch, true)
}

func (s *PostgresStore) DeleteMemorySafely(id string, autoBefore float64) (bool, bool, error) {
	if err := validateDeleteCutoff(autoBefore); err != nil {
		return false, false, err
	}
	tx, err := s.beginMemoryTransaction(MemoryBatch{CloseEdgesFor: []string{id}})
	if err != nil {
		return false, false, err
	}
	return deleteMemorySafely(tx, id, autoBefore)
}

func (s *PostgresStore) EvidenceReferences(workspace string) (map[string]bool, error) {
	read, err := s.pool.BeginTx(context.Background(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	tx := &postgresMemoryTransaction{tx: read}
	defer tx.rollback()
	memories, err := tx.memories(workspace)
	if err != nil {
		return nil, err
	}
	edges, err := tx.edges("")
	if err != nil {
		return nil, err
	}
	return evidenceReferences(memories, edges), tx.commit()
}

func pgTruthy(key string) string {
	return `COALESCE(metadata->'` + key + `','null'::jsonb) NOT IN ('null'::jsonb,'false'::jsonb,'0'::jsonb,'""'::jsonb)`
}

func (s *PostgresStore) SearchMemories(queryVec []float32, topK int, filter MemoryFilter) ([]SearchHit, error) {
	if err := validateVector(queryVec, s.dim); err != nil {
		return nil, err
	}
	if topK <= 0 {
		return nil, errors.New("topK must be positive")
	}
	args := []any{pgvector.NewVector(queryVec), topK}
	where := "embedding IS NOT NULL"
	if filter.Workspace != "" {
		args = append(args, filter.Workspace)
		where += " AND workspace=" + ph(len(args))
	}
	if len(filter.Layers) > 0 {
		layers := make([]string, len(filter.Layers))
		for i, layer := range filter.Layers {
			layers[i] = string(layer)
		}
		args = append(args, layers)
		where += " AND layer=ANY(" + ph(len(args)) + ")"
	}
	if len(filter.Types) > 0 {
		types := make([]string, len(filter.Types))
		for i, typ := range filter.Types {
			types[i] = string(typ)
		}
		args = append(args, types)
		where += " AND type=ANY(" + ph(len(args)) + ")"
	}
	if filter.ExcludeRetired || filter.ExcludeUnusable {
		where += ` AND NOT (` + pgTruthy("superseded") + `)
			AND NOT (COALESCE(jsonb_typeof(metadata->'superseded_by'),'')='string' AND COALESCE(metadata->>'superseded_by','')<>'')`
	}
	if filter.ExcludeUnusable {
		where += ` AND COALESCE(metadata->>'knowledge_status','') NOT IN ('stale','rejected') AND metadata->'evidence_missing' IS DISTINCT FROM 'true'::jsonb`
	}
	// MATERIALIZED prevents an ANN preselection: sort the exact filtered set.
	query := `WITH eligible AS MATERIALIZED (SELECT id,embedding FROM memories WHERE ` + where + `)
		SELECT id, CASE WHEN vector_norm(embedding)=0 OR vector_norm($1::vector)=0 THEN 0::float8
		ELSE 1-(embedding <=> $1) END AS similarity FROM eligible
		ORDER BY similarity DESC, id COLLATE "C" ASC LIMIT $2`
	rows, err := s.pool.Query(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []SearchHit
	for rows.Next() {
		var hit SearchHit
		if err := rows.Scan(&hit.ID, &hit.Similarity); err != nil {
			return nil, err
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}
