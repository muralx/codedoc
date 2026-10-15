package store

import (
	"path/filepath"
	"testing"
)

// bulkStore opens a database holding one file and returns it with its
// path and the file id.
func bulkStore(t *testing.T) (*Store, string, int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ws.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	repoID, err := s.UpsertRepo(Repo{Workspace: "w", Name: "r", Path: "/r"})
	if err != nil {
		t.Fatal(err)
	}
	fileID, err := s.UpsertFile(File{RepoID: repoID, Path: "a.go", Size: 1, Mtime: 1, Hash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	return s, path, fileID
}

func insertNamed(t *testing.T, s *Store, fileID int64, name string) {
	t.Helper()
	mustExec(t, s, `INSERT INTO symbol(file_id, kind, name, qualified_name, stable_key, start_line, end_line,
		start_byte, end_byte, sig_start_line, sig_end_line, signature, visibility, body_hash)
		VALUES(?, 'func', ?, ?, ?, 1, 2, 0, 10, 1, 1, '', 'public', 'b')`, fileID, name, "pkg."+name, "k:"+name)
	mustExec(t, s, `INSERT INTO module(repo_id, path, lang, kind, identity) VALUES(1, ?, 'go', 'package', ?)`,
		"dir"+name, "mod"+name)
}

func assertRebuildPending(t *testing.T, s *Store, want bool) {
	t.Helper()
	if _, pending, err := s.Meta(ftsRebuildKey); err != nil || pending != want {
		t.Fatalf("fts_rebuild_pending set = %v (%v), want %v", pending, err, want)
	}
}

func TestSuspendedFTSIsRebuiltOnResume(t *testing.T) {
	s, _, fileID := bulkStore(t)
	defer func() { _ = s.Close() }()
	insertNamed(t, s, fileID, "Before")
	if err := s.SuspendFTS(); err != nil {
		t.Fatal(err)
	}
	assertRebuildPending(t, s, true)
	insertNamed(t, s, fileID, "During")
	mustExec(t, s, `DELETE FROM symbol WHERE name = 'Before'`)
	assertFTSCount(t, s, "symbol_fts", "name:During", 0)
	if err := s.ResumeFTS(); err != nil {
		t.Fatal(err)
	}
	assertRebuildPending(t, s, false)
	assertFTSCount(t, s, "symbol_fts", "name:During", 1)
	assertFTSCount(t, s, "symbol_fts", "name:Before", 0)
	assertFTSCount(t, s, "module_fts", "identity:modDuring", 1)
	insertNamed(t, s, fileID, "After")
	assertFTSCount(t, s, "symbol_fts", "name:After", 1)
}

// A process that dies between SuspendFTS and ResumeFTS leaves the
// rebuild to the next Open.
func TestOpenFinishesInterruptedBulkWrite(t *testing.T) {
	s, path, fileID := bulkStore(t)
	if err := s.SuspendFTS(); err != nil {
		t.Fatal(err)
	}
	insertNamed(t, s, fileID, "During")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	assertRebuildPending(t, s, false)
	assertFTSCount(t, s, "symbol_fts", "name:During", 1)
	insertNamed(t, s, fileID, "After")
	assertFTSCount(t, s, "symbol_fts", "name:After", 1)
}

// Open leaves a bulk write alone while its writer holds the write lock;
// the next WriteLock finishes it if the writer never does.
func TestBulkWriteOfLockHolderIsFinishedByNextWriteLock(t *testing.T) {
	s, path, fileID := bulkStore(t)
	defer func() { _ = s.Close() }()
	unlock, err := s.WriteLock()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SuspendFTS(); err != nil {
		t.Fatal(err)
	}
	insertNamed(t, s, fileID, "During")
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	assertRebuildPending(t, other, true)
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	unlock, err = other.WriteLock()
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	assertRebuildPending(t, s, false)
	assertFTSCount(t, s, "symbol_fts", "name:During", 1)
}

// A missing trigger without the meta key (a database edited by hand)
// is recreated too.
func TestOpenRecreatesMissingFTSTrigger(t *testing.T) {
	s, path, fileID := bulkStore(t)
	mustExec(t, s, `DROP TRIGGER symbol_ai`)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	insertNamed(t, s, fileID, "After")
	assertFTSCount(t, s, "symbol_fts", "name:After", 1)
}
