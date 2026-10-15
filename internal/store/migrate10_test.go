package store

import (
	"path/filepath"
	"testing"
)

// TestMigrationTenMovesExtractorVersionsToFileRows opens a database at
// schema 9, whose meta records the extractor version by repo path, and
// checks migration 10 copies it onto the repo's file rows and drops the
// path-keyed meta.
func TestMigrationTenMovesExtractorVersionsToFileRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	all := migrations
	migrations = all[:9]
	s, err := Open(path)
	migrations = all
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, s, `INSERT INTO workspace(id, name, created_at) VALUES(1, 'w', 0)`)
	mustExec(t, s, `INSERT INTO repo(id, workspace_id, name, path) VALUES(1, 1, 'a', '/a'), (2, 1, 'b', '/b')`)
	mustExec(t, s, `INSERT INTO file(repo_id, path, size, mtime, hash) VALUES(1, 'x.go', 1, 1, 'h'), (2, 'y.go', 1, 1, 'h')`)
	mustExec(t, s, `INSERT INTO meta(key, value) VALUES('extractor_version:/a', '5'), ('extractor_version:/gone', '4')`)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for repo, want := range map[int64]string{1: "5", 2: ""} {
		files, err := s.FilesByRepo(repo)
		if err != nil || len(files) != 1 || files[0].ExtractedVersion != want {
			t.Fatalf("repo %d: %+v %v, want extracted version %q", repo, files, err, want)
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM meta WHERE key LIKE 'extractor_version:%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("path-keyed versions left: %d %v", n, err)
	}
}
