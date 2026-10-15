// Package testutil holds helpers shared by tests.
package testutil

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// WorkspaceDir returns the absolute path of testdata/ws.
func WorkspaceDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "ws")
}

// ModCacheDir returns the absolute path of testdata/ws/gomodcache, a
// fake Go module cache; tests select it with CODEDOC_GOMODCACHE and only
// read it.
func ModCacheDir() string { return filepath.Join(WorkspaceDir(), "gomodcache") }

// CopyFixture copies testdata/ws/<name> to a temp directory, initialises a
// git repository there with one commit, and returns the path. Fixture sources
// stay plain files in the project tree; git history exists only in the copy.
// The files carry FixtureTime as their modification time.
func CopyFixture(t *testing.T, name string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), name)
	if err := os.CopyFS(dst, os.DirFS(filepath.Join(WorkspaceDir(), name))); err != nil {
		t.Fatal(err)
	}
	fixTimes(t, dst)
	gitInit(t, dst)
	return dst
}

// FixtureTime is the date of the one commit of a fixture repository and
// the modification time of its files, so answers that print either are
// reproducible.
var FixtureTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fixTimes sets the modification time of every file under dir to
// FixtureTime.
func fixTimes(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		return os.Chtimes(p, FixtureTime, FixtureTime)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// SourceRepo writes files (path relative to the repo: content) to a temp
// directory named name, commits them like CopyFixture, and returns the
// path.
func SourceRepo(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), name)
	for p, content := range files {
		full := filepath.Join(dst, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fixTimes(t, dst)
	gitInit(t, dst)
	return dst
}

// gitInit initialises a git repository in dir with one commit of every
// file, at a fixed date.
func gitInit(t *testing.T, dst string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-qm", "fixture: initial"}} {
		cmd := exec.Command("git", append([]string{"-c", "user.email=f@x", "-c", "user.name=fixture"}, args...)...)
		cmd.Dir = dst
		// Fixed dates make the commit hash reproducible, so snapshot tests can
		// compare the response header exactly.
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}
