package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/jackc/pgx/v5"
)

func pgMigrationSchema() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS embedding_migration (
			id INTEGER PRIMARY KEY CHECK(id=1), data TEXT NOT NULL, internal INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO embedding_migration(id,data) VALUES(1,'{"state":"idle"}') ON CONFLICT(id) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS embedding_staging (
			id TEXT PRIMARY KEY, vector TEXT NOT NULL, generation TEXT NOT NULL, fingerprint TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS embedding_backup (id TEXT PRIMARY KEY, vector TEXT NOT NULL)`,
		`CREATE OR REPLACE FUNCTION ladym_embedding_gate() RETURNS trigger LANGUAGE plpgsql AS $$
		DECLARE frozen boolean;
		BEGIN
			SELECT internal=0 AND data::jsonb->>'state'<>'idle' INTO frozen FROM embedding_migration WHERE id=1 FOR UPDATE;
			IF frozen AND TG_OP='INSERT' THEN RAISE EXCEPTION 'embedding migration write freeze'; END IF;
			IF frozen AND TG_OP='UPDATE' AND
				(NEW.id,NEW.content,NEW.summary,NEW.tags,NEW.metadata,NEW.source,NEW.workspace,NEW.layer,NEW.type,
				 NEW.embedding::text,NEW.content_hash,NEW.created_at,NEW.updated_at) IS DISTINCT FROM
				(OLD.id,OLD.content,OLD.summary,OLD.tags,OLD.metadata,OLD.source,OLD.workspace,OLD.layer,OLD.type,
				 OLD.embedding::text,OLD.content_hash,OLD.created_at,OLD.updated_at)
			THEN RAISE EXCEPTION 'embedding migration write freeze'; END IF;
			RETURN NEW;
		END $$`,
		`CREATE OR REPLACE FUNCTION ladym_embedding_delete() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			DELETE FROM embedding_backup WHERE id=OLD.id;
			DELETE FROM embedding_staging WHERE id=OLD.id;
			IF EXISTS(SELECT 1 FROM embedding_migration WHERE id=1 AND data::jsonb->>'state'='staging') THEN
				DELETE FROM embedding_staging;
				DELETE FROM embedding_backup;
				UPDATE embedding_migration SET data='{"state":"idle"}' WHERE id=1;
			END IF;
			RETURN OLD;
		END $$`,
		`CREATE OR REPLACE FUNCTION ladym_embedding_meta_gate() RETURNS trigger LANGUAGE plpgsql AS $$
		DECLARE k text; frozen boolean;
		BEGIN
			IF TG_OP='DELETE' THEN k:=OLD.key; ELSE k:=NEW.key; END IF;
			SELECT internal=0 AND data::jsonb->>'state'<>'idle' INTO frozen FROM embedding_migration WHERE id=1 FOR UPDATE;
			IF (k IN ('embedding_identity','embedding_fingerprint','embedding_dim','embedding_provider','embedding_tokenizer')
				OR (TG_OP='UPDATE' AND OLD.key IN ('embedding_identity','embedding_fingerprint','embedding_dim','embedding_provider','embedding_tokenizer')))
				AND frozen
			THEN RAISE EXCEPTION 'embedding migration metadata freeze'; END IF;
			IF TG_OP='DELETE' THEN RETURN OLD; END IF;
			RETURN NEW;
		END $$`,
		`DO $$ BEGIN
			IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgname='embedding_freeze' AND tgrelid='memories'::regclass) THEN
				CREATE TRIGGER embedding_freeze BEFORE INSERT OR UPDATE ON memories FOR EACH ROW EXECUTE FUNCTION ladym_embedding_gate();
				CREATE TRIGGER embedding_delete AFTER DELETE ON memories FOR EACH ROW EXECUTE FUNCTION ladym_embedding_delete();
				CREATE TRIGGER embedding_meta_freeze BEFORE INSERT OR UPDATE OR DELETE ON meta FOR EACH ROW EXECUTE FUNCTION ladym_embedding_meta_gate();
			END IF;
		END $$`,
	}
}

func (s *PostgresStore) beginMigration() (migrationTransaction, error) {
	return s.beginMemoryTransaction(MemoryBatch{})
}

func (tx *postgresMemoryTransaction) checkIdentity() error {
	if tx.store == nil {
		return nil
	}
	return migrationCheckIdentity(tx, tx.store.embeddingMigrations)
}

func pgMigrationQuery(query string) string {
	if query == "SELECT data FROM embedding_migration WHERE id=1" {
		query += " FOR UPDATE"
	}
	var out strings.Builder
	n := 0
	for _, c := range query {
		if c == '?' {
			n++
			out.WriteString(ph(n))
		} else {
			out.WriteRune(c)
		}
	}
	return out.String()
}

func (tx *postgresMemoryTransaction) exec(query string, args ...any) error {
	_, err := tx.tx.Exec(context.Background(), pgMigrationQuery(query), args...)
	if err == nil && query == "UPDATE embedding_migration SET internal=1 WHERE id=1" {
		tx.migrationAuthorized = true
	}
	return err
}

func (tx *postgresMemoryTransaction) scan(query string, args []any, dest ...any) error {
	return tx.tx.QueryRow(context.Background(), pgMigrationQuery(query), args...).Scan(dest...)
}

func (tx *postgresMemoryTransaction) memoriesAfter(cursor string, limit int) ([]*schema.Memory, error) {
	return tx.queryMemories("SELECT "+memoryColsPG+` FROM memories
		WHERE id COLLATE "C" > $1 AND NOT(type='resource' AND COALESCE(metadata->>'resource_identity','')='path')
		ORDER BY id COLLATE "C" LIMIT $2`, cursor, limit)
}

func (tx *postgresMemoryTransaction) vectorForID(id string) ([]float32, error) {
	var raw *string
	err := tx.tx.QueryRow(context.Background(), "SELECT embedding::text FROM memories WHERE id=$1", id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil || raw == nil {
		return nil, err
	}
	var vector []float32
	err = json.Unmarshal([]byte(*raw), &vector)
	return vector, err
}

func (tx *postgresMemoryTransaction) vectors(table string) ([]migrationVector, error) {
	column := "vector"
	if table == "memories" {
		column = "embedding::text"
	}
	rows, err := tx.tx.Query(context.Background(), "SELECT id,"+column+" FROM "+table+" ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []migrationVector
	for rows.Next() {
		var row migrationVector
		var raw *string
		if err := rows.Scan(&row.ID, &raw); err != nil {
			return nil, err
		}
		if raw != nil {
			if err := json.Unmarshal([]byte(*raw), &row.Vector); err != nil {
				return nil, err
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (tx *postgresMemoryTransaction) switchVectors(table string, dim int) error {
	if dim < 1 {
		return errors.New("migration dimension must be positive")
	}

	for _, query := range []string{
		"DROP INDEX IF EXISTS idx_mem_embedding",
		fmt.Sprintf("ALTER TABLE memories ALTER COLUMN embedding TYPE vector(%d) USING NULL::vector(%d)", dim, dim),
		fmt.Sprintf("UPDATE memories m SET embedding=CASE WHEN s.vector='null' THEN NULL ELSE s.vector::vector(%d) END FROM %s s WHERE m.id=s.id", dim, table),
		"CREATE INDEX idx_mem_embedding ON memories USING hnsw (embedding vector_cosine_ops)",
	} {
		if err := tx.exec(query); err != nil {
			return err
		}
	}
	return nil
}

func (tx *postgresMemoryTransaction) ensureDimension(dim int) error {
	var storedDim int
	if err := tx.scan("SELECT atttypmod FROM pg_attribute WHERE attrelid='memories'::regclass AND attname='embedding'", nil, &storedDim); err != nil {
		return err
	}
	if storedDim == dim {
		return nil
	}
	for _, query := range []string{
		"DROP INDEX IF EXISTS idx_mem_embedding",
		fmt.Sprintf("ALTER TABLE memories ALTER COLUMN embedding TYPE vector(%d) USING embedding::vector(%d)", dim, dim),
		"CREATE INDEX idx_mem_embedding ON memories USING hnsw (embedding vector_cosine_ops)",
	} {
		if err := tx.exec(query); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) embeddingDimension() (int, error) {
	value, err := s.GetMeta("embedding_dim")
	if err != nil {
		return 0, err
	}
	dim := s.dim
	if value != "" {
		if _, err := fmt.Sscan(value, &dim); err != nil {
			return 0, err
		}
	}
	return dim, nil
}
