package store

import (
	"path/filepath"
	"testing"
)

func TestSymbolFTSFollowsSymbolRows(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	repoID, err := s.UpsertRepo(Repo{Workspace: "w", Name: "r", Path: "/r"})
	if err != nil {
		t.Fatal(err)
	}
	fileID, err := s.UpsertFile(File{RepoID: repoID, Path: "a.go", Size: 1, Mtime: 1, Hash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, s, `INSERT INTO symbol(id, file_id, kind, name, qualified_name, stable_key, start_line, end_line,
		start_byte, end_byte, sig_start_line, sig_end_line, signature, visibility, body_hash)
		VALUES(1, ?, 'func', 'Alpha', 'pkg.Alpha', 'k1', 1, 2, 0, 10, 1, 1, 'func Alpha()', 'public', 'b')`, fileID)
	assertFTSCount(t, s, "symbol_fts", "name:Alpha", 1)
	mustExec(t, s, `UPDATE symbol SET name='Beta' WHERE id=1`)
	assertFTSCount(t, s, "symbol_fts", "name:Alpha", 0)
	assertFTSCount(t, s, "symbol_fts", "name:Beta", 1)
	mustExec(t, s, `DELETE FROM symbol WHERE id=1`)
	assertFTSCount(t, s, "symbol_fts", "name:Beta", 0)
}

func TestModuleFTSFollowsModuleRows(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	repoID, err := s.UpsertRepo(Repo{Workspace: "w", Name: "r", Path: "/r"})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, s, `INSERT INTO module(id, repo_id, path, lang, kind, identity, doc_text)
		VALUES(1, ?, 'pkg', 'go', 'package', 'example.com/pkg', 'parses widgets')`, repoID)
	assertFTSCount(t, s, "module_fts", "widgets", 1)
	mustExec(t, s, `UPDATE module SET doc_text='renders gadgets' WHERE id=1`)
	assertFTSCount(t, s, "module_fts", "widgets", 0)
	assertFTSCount(t, s, "module_fts", "gadgets", 1)
	mustExec(t, s, `DELETE FROM module WHERE id=1`)
	assertFTSCount(t, s, "module_fts", "gadgets", 0)
}

func mustExec(t *testing.T, s *Store, q string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func assertFTSCount(t *testing.T, s *Store, table, term string, want int) {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE `+table+` MATCH ?`, term).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("%s MATCH %q: got %d rows, want %d", table, term, n, want)
	}
}

func TestImportResolvedIndexExists(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='import_resolved'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("import_resolved index: %d %v", n, err)
	}
	if v, _ := s.SchemaVersion(); v != len(migrations) {
		t.Fatalf("version %d", v)
	}
}

func TestOpenSetsPageCacheSize(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var kib int
	if err := s.db.QueryRow(`PRAGMA cache_size`).Scan(&kib); err != nil {
		t.Fatal(err)
	}
	if kib != -32768 {
		t.Fatalf("cache_size = %d, want -32768 (32 MiB)", kib)
	}
}
