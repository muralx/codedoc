package store

import (
	"path/filepath"
	"testing"
)

// TestSchemaElevenMigratesToCurrent opens a database at schema 11, the
// last one released before the dependency index (migration 12), the
// documents (13) and the receiver types of references (14), with rows in
// the tables they alter, and checks the migrations apply on top of it
// and keep those rows.
func TestSchemaElevenMigratesToCurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	all := migrations
	migrations = all[:11]
	s, err := Open(path)
	migrations = all
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, s, `INSERT INTO workspace(id, name, created_at) VALUES(1, 'w', 0)`)
	mustExec(t, s, `INSERT INTO repo(id, workspace_id, name, path) VALUES(1, 1, 'a', '/a')`)
	mustExec(t, s, `INSERT INTO file(id, repo_id, path, size, mtime, hash) VALUES(1, 1, 'x.go', 1, 1, 'h')`)
	mustExec(t, s, `INSERT INTO symbol(id, file_id, kind, name, qualified_name, stable_key, start_line, end_line,
		start_byte, end_byte, sig_start_line, sig_end_line, signature, visibility, body_hash)
		VALUES(1, 1, 'func', 'F', 'F', 'k1', 1, 2, 0, 10, 1, 1, 'func F()', 'public', 'b')`)
	mustExec(t, s, `INSERT INTO edge(src_symbol_id, dst_symbol_id, kind, ref_kind, confidence, file_id, line)
		VALUES(1, 1, 'calls', 'call', 'local', 1, 2)`)
	mustExec(t, s, `INSERT INTO reference(file_id, symbol_id, kind, name, qualifier, line, arity)
		VALUES(1, 1, 'call', 'G', 'x', 2, 0)`)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if v, err := s.SchemaVersion(); err != nil || v != len(migrations) || len(migrations) < 15 {
		t.Fatalf("schema %d of %d: %v", v, len(migrations), err)
	}
	checkMigratedRows(t, s)
	mustExec(t, s, `INSERT INTO section_fts(rowid, text) VALUES(1, 'retries')`)
	mustExec(t, s, `INSERT INTO doc_link(file_id, symbol_id, target, anchor, line) VALUES(1, 1, 'y.md', '', 1)`)
	mustExec(t, s, `UPDATE edge SET dst_symbol_id = NULL, dst_dep_key = 'npm/x@1:ts:x:X'`)
}

// checkMigratedRows checks the rows written at schema 11 read back with
// the defaults of the columns added since.
func checkMigratedRows(t *testing.T, s *Store) {
	t.Helper()
	var title string
	var edges, depEdges int
	if err := s.db.QueryRow(`SELECT title FROM file WHERE id = 1`).Scan(&title); err != nil || title != "" {
		t.Fatalf("file title %q: %v", title, err)
	}
	err := s.db.QueryRow(`SELECT count(*), count(dst_dep_key) FROM edge`).Scan(&edges, &depEdges)
	if err != nil || edges != 1 || depEdges != 0 {
		t.Fatalf("edges %d, to dependencies %d: %v", edges, depEdges, err)
	}
	var refs []Reference
	err = s.Batch(func(b *Batch) (err error) {
		refs, err = b.References(nil, nil)
		return err
	})
	if err != nil || len(refs) != 1 || refs[0].RecvType != "" || refs[0].Qualifier != "x" {
		t.Fatalf("references %+v: %v", refs, err)
	}
}
