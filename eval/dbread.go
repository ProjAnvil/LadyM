//go:build eval

package eval

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

// Row is one memories row exactly as persisted.
type Row struct {
	ID        string
	Layer     string
	Type      string
	Content   string
	Summary   string
	Tags      string
	Metadata  string
	Workspace string
	Source    string
}

// Edge is one edges row exactly as persisted.
type Edge struct {
	ID       string
	SrcID    string
	Relation string
	DstID    string
	Weight   float64
	Metadata string
}

// episodicLayer matches schema.LayerEpisodic. It is spelled out rather than
// imported so this package keeps reading the database as an external observer
// would, not through the engine's own abstractions.
const episodicLayer = "L1_episodic"

// absent reports whether dbPath is missing. Ignored events never reach a write,
// so SQLite lazily skips creating the file entirely; that is expected behavior,
// not a failure, and callers must read it as "nothing was persisted".
func absent(dbPath string) bool {
	_, err := os.Stat(dbPath)
	return err != nil
}

// ReadEpisodes returns every episodic row for workspace, oldest first. A
// missing database file yields no rows and no error.
func ReadEpisodes(dbPath, workspace string) ([]Row, error) {
	if absent(dbPath) {
		return nil, nil
	}
	db, err := openDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id, layer, type, content, summary, tags, metadata, workspace, source
		FROM memories
		WHERE layer = ? AND workspace = ?
		ORDER BY created_at ASC, id ASC`, episodicLayer, workspace)
	if err != nil {
		return nil, fmt.Errorf("query episodes: %w", err)
	}
	return scanRows(rows)
}

// ReadAll returns every memory row for workspace regardless of layer, so callers
// can resolve resource entities alongside episodes.
func ReadAll(dbPath, workspace string) ([]Row, error) {
	if absent(dbPath) {
		return nil, nil
	}
	db, err := openDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id, layer, type, content, summary, tags, metadata, workspace, source
		FROM memories
		WHERE workspace = ?
		ORDER BY created_at ASC, id ASC`, workspace)
	if err != nil {
		return nil, fmt.Errorf("query all rows: %w", err)
	}
	return scanRows(rows)
}

// ReadEdges returns every edge row in the database. A missing database file
// yields no edges and no error.
func ReadEdges(dbPath string) ([]Edge, error) {
	if absent(dbPath) {
		return nil, nil
	}
	db, err := openDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id, src_id, relation, dst_id, weight, metadata
		FROM edges`)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	defer rows.Close()

	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.ID, &e.SrcID, &e.Relation, &e.DstID, &e.Weight, &e.Metadata); err != nil {
			return nil, fmt.Errorf("scan edge: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// openDB opens the SQLite file read-only, with a busy timeout so a lingering
// writer can never make measurement fail spuriously.
func openDB(dbPath string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbPath, err)
	}
	return db, nil
}

// scanRows materialises a memories row cursor into a slice.
func scanRows(rows *sql.Rows) ([]Row, error) {
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(
			&r.ID, &r.Layer, &r.Type, &r.Content, &r.Summary,
			&r.Tags, &r.Metadata, &r.Workspace, &r.Source,
		); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
