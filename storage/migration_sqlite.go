//go:build !enterprise

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ProjAnvil/LadyM/schema"
)

const sqliteMigrationSQL = `
CREATE TABLE IF NOT EXISTS embedding_migration (
	id INTEGER PRIMARY KEY CHECK(id=1), data TEXT NOT NULL, internal INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO embedding_migration(id,data) VALUES(1,'{"state":"idle"}');
CREATE TABLE IF NOT EXISTS embedding_staging (
	id TEXT PRIMARY KEY, vector TEXT NOT NULL, generation TEXT NOT NULL, fingerprint TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS embedding_backup (id TEXT PRIMARY KEY, vector TEXT NOT NULL);
CREATE TRIGGER IF NOT EXISTS embedding_freeze_insert BEFORE INSERT ON memories
WHEN (SELECT internal=0 AND json_extract(data,'$.state')<>'idle' FROM embedding_migration WHERE id=1)
BEGIN SELECT RAISE(ABORT,'embedding migration write freeze'); END;
CREATE TRIGGER IF NOT EXISTS embedding_freeze_update BEFORE UPDATE ON memories
WHEN (SELECT internal=0 AND json_extract(data,'$.state')<>'idle' FROM embedding_migration WHERE id=1)
AND (NEW.content IS NOT OLD.content OR NEW.summary IS NOT OLD.summary OR NEW.tags IS NOT OLD.tags
	OR NEW.metadata IS NOT OLD.metadata OR NEW.source IS NOT OLD.source OR NEW.workspace IS NOT OLD.workspace
	OR NEW.layer IS NOT OLD.layer OR NEW.type IS NOT OLD.type OR NEW.embedding IS NOT OLD.embedding
	OR NEW.content_hash IS NOT OLD.content_hash OR NEW.created_at IS NOT OLD.created_at
	OR NEW.updated_at IS NOT OLD.updated_at OR NEW.id IS NOT OLD.id)
BEGIN SELECT RAISE(ABORT,'embedding migration write freeze'); END;
CREATE TRIGGER IF NOT EXISTS embedding_delete AFTER DELETE ON memories BEGIN
	DELETE FROM embedding_backup WHERE id=OLD.id;
	DELETE FROM embedding_staging WHERE id=OLD.id;
	DELETE FROM embedding_staging WHERE (SELECT json_extract(data,'$.state')='staging' FROM embedding_migration WHERE id=1);
	DELETE FROM embedding_backup WHERE (SELECT json_extract(data,'$.state')='staging' FROM embedding_migration WHERE id=1);
	UPDATE embedding_migration SET data='{"state":"idle"}'
		WHERE id=1 AND json_extract(data,'$.state')='staging';
END;
CREATE TRIGGER IF NOT EXISTS embedding_meta_insert BEFORE INSERT ON meta
WHEN NEW.key IN ('embedding_identity','embedding_fingerprint','embedding_dim','embedding_provider','embedding_tokenizer')
AND (SELECT internal=0 AND json_extract(data,'$.state')<>'idle' FROM embedding_migration WHERE id=1)
BEGIN SELECT RAISE(ABORT,'embedding migration metadata freeze'); END;
CREATE TRIGGER IF NOT EXISTS embedding_meta_update BEFORE UPDATE ON meta
WHEN (NEW.key IN ('embedding_identity','embedding_fingerprint','embedding_dim','embedding_provider','embedding_tokenizer')
OR OLD.key IN ('embedding_identity','embedding_fingerprint','embedding_dim','embedding_provider','embedding_tokenizer'))
AND (SELECT internal=0 AND json_extract(data,'$.state')<>'idle' FROM embedding_migration WHERE id=1)
BEGIN SELECT RAISE(ABORT,'embedding migration metadata freeze'); END;
CREATE TRIGGER IF NOT EXISTS embedding_meta_delete BEFORE DELETE ON meta
WHEN OLD.key IN ('embedding_identity','embedding_fingerprint','embedding_dim','embedding_provider','embedding_tokenizer')
AND (SELECT internal=0 AND json_extract(data,'$.state')<>'idle' FROM embedding_migration WHERE id=1)
BEGIN SELECT RAISE(ABORT,'embedding migration metadata freeze'); END;
`

func installSQLiteMigration(db *sql.DB) error {
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE memory_vector_generation SET generation=generation WHERE id=1"); err != nil {
		return err
	}
	if _, err := tx.Exec(sqliteMigrationSQL); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) beginMigration() (migrationTransaction, error) {
	return s.beginMemoryTransaction()
}

func (tx *sqliteMemoryTransaction) checkIdentity() error {
	return migrationCheckIdentity(tx, tx.store.embeddingMigrations)
}

func (tx *sqliteMemoryTransaction) exec(query string, args ...any) error {
	_, err := tx.tx.Exec(query, args...)
	if err == nil && query == "UPDATE embedding_migration SET internal=1 WHERE id=1" {
		tx.migrationAuthorized = true
	}
	return err
}

func (tx *sqliteMemoryTransaction) scan(query string, args []any, dest ...any) error {
	return tx.tx.QueryRow(query, args...).Scan(dest...)
}

func (tx *sqliteMemoryTransaction) memoriesAfter(cursor string, limit int) ([]*schema.Memory, error) {
	return tx.queryMemories("SELECT "+memoryReadCols+` FROM memories
		WHERE id COLLATE BINARY > ? AND NOT(type='resource' AND COALESCE(json_extract(metadata,'$.resource_identity'),'')='path')
		ORDER BY id COLLATE BINARY LIMIT ?`, cursor, limit)
}

func (tx *sqliteMemoryTransaction) vectorForID(id string) ([]float32, error) {
	var blob []byte
	err := tx.tx.QueryRow("SELECT embedding FROM memories WHERE id=?", id).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil || blob == nil {
		return nil, err
	}
	if len(blob)%4 != 0 {
		return nil, errors.New("invalid embedding blob")
	}
	return decodeVec(blob), nil
}

func (tx *sqliteMemoryTransaction) vectors(table string) ([]migrationVector, error) {
	column := "vector"
	if table == "memories" {
		column = "embedding"
	}
	rows, err := tx.tx.Query("SELECT id," + column + " FROM " + table + " ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []migrationVector
	for rows.Next() {
		var row migrationVector
		if table == "memories" {
			var blob []byte
			if err := rows.Scan(&row.ID, &blob); err != nil {
				return nil, err
			}
			if blob != nil {
				if len(blob)%4 != 0 {
					return nil, errors.New("invalid embedding blob")
				}
				row.Vector = decodeVec(blob)
			}
		} else {
			var raw string
			if err := rows.Scan(&row.ID, &raw); err != nil {
				return nil, err
			}
			if err := json.Unmarshal([]byte(raw), &row.Vector); err != nil {
				return nil, err
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (tx *sqliteMemoryTransaction) switchVectors(table string, dim int) error {
	vectors, err := tx.vectors(table)
	if err != nil {
		return err
	}

	if err := tx.exec("UPDATE memories SET embedding=NULL"); err != nil {
		return err
	}
	for _, row := range vectors {
		if row.Vector == nil {
			continue
		}
		if err := validateVector(row.Vector, dim); err != nil {
			return err
		}
		if err := tx.exec("UPDATE memories SET embedding=? WHERE id=?", encodeVec(row.Vector), row.ID); err != nil {
			return err
		}
	}
	return nil
}

func (tx *sqliteMemoryTransaction) ensureDimension(int) error {
	return tx.exec("UPDATE memory_vector_generation SET generation=generation+1 WHERE id=1")
}

func (s *SQLiteStore) embeddingDimension() (int, error) {
	value, err := s.GetMeta("embedding_dim")
	if err != nil {
		return 0, err
	}
	dim := s.Dim
	if value != "" {
		if _, err := fmt.Sscan(value, &dim); err != nil {
			return 0, err
		}
	}
	return dim, nil
}
