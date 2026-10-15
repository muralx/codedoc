package index_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
)

func openTemp(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func repoFixture(t *testing.T, name string) *config.Repo {
	t.Helper()
	return &config.Repo{Path: testutil.CopyFixture(t, name), Name: name}
}

func gitCommitAll(t *testing.T, dir, msg string) {
	t.Helper()
	cmd := exec.Command("git", "-c", "user.email=t@x", "-c", "user.name=t", "commit", "-qam", msg)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func fileRow(t *testing.T, s *store.Store, root, rel string) store.File {
	t.Helper()
	r, err := s.RepoByPath(root)
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.FileByPath(r.ID, rel)
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return f
}

func TestIndexRepoStoresFilesAndIsIncremental(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	res, err := index.Repo(s, rep, index.Options{Workspace: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesIndexed < 5 || res.FilesAdded != res.FilesIndexed || res.FilesSkipped != 0 {
		t.Fatalf("%+v", res)
	}
	res, _ = index.Repo(s, rep, index.Options{Workspace: "fixture"})
	if res.FilesIndexed != 0 || res.FilesSkipped < 5 {
		t.Fatalf("second run should skip unchanged: %+v", res)
	}
}

func TestIndexRepoRecordsHeadAndLastCommits(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	if _, err := index.Repo(s, rep, index.Options{Workspace: "fixture"}); err != nil {
		t.Fatal(err)
	}
	r, _ := s.RepoByPath(rep.Path)
	if r.Workspace != "fixture" || r.Branch != "main" || len(r.Commit) != 40 || r.IndexedAt == 0 {
		t.Fatalf("%+v", r)
	}
	if f := fileRow(t, s, rep.Path, "store/store.go"); f.LastCommit != r.Commit || f.LastCommitAt == 0 || f.Lang != "go" {
		t.Fatalf("%+v", f)
	}
}

func TestIndexRepoTracksAddedChangedAndDeletedFiles(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	if _, err := index.Repo(s, rep, index.Options{}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, rep.Path, "store/store.go", []byte("package store\n"))
	writeFile(t, rep.Path, "store/extra.go", []byte("package store\n"))
	if err := os.Remove(filepath.Join(rep.Path, "store", "null.go")); err != nil {
		t.Fatal(err)
	}
	res, err := index.Repo(s, rep, index.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesIndexed != 2 || res.FilesAdded != 1 || res.FilesDeleted != 1 {
		t.Fatalf("%+v", res)
	}
	if f := fileRow(t, s, rep.Path, "store/store.go"); f.Lines != 1 {
		t.Fatalf("%+v", f)
	}
}

func TestIndexRepoRefreshesLastCommitOfUnchangedContent(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	writeFile(t, rep.Path, "store/store.go", []byte("package store\n"))
	first, _ := index.Repo(s, rep, index.Options{})
	gitCommitAll(t, rep.Path, "shrink store")
	res, err := index.Repo(s, rep, index.Options{})
	if err != nil || res.FilesIndexed != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	f := fileRow(t, s, rep.Path, "store/store.go")
	if f.LastCommit != res.Commit || f.LastCommit == first.Commit {
		t.Fatalf("last commit %s, head %s, first %s", f.LastCommit, res.Commit, first.Commit)
	}
	if g := fileRow(t, s, rep.Path, "store/null.go"); g.LastCommit != first.Commit {
		t.Fatalf("untouched file lost its last commit: %+v", g)
	}
}

func TestIndexRepoHonoursExclude(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	if _, err := index.Repo(s, rep, index.Options{Exclude: []string{"service/**"}}); err != nil {
		t.Fatal(err)
	}
	r, _ := s.RepoByPath(rep.Path)
	if _, err := s.FileByPath(r.ID, "service/service.go"); err == nil {
		t.Fatal("excluded file indexed")
	}
	if _, err := index.Repo(s, rep, index.Options{Exclude: []string{"["}}); err == nil {
		t.Fatal("expected glob error")
	}
}

func TestIndexDirectoryThatIsNotARepo(t *testing.T) {
	s := openTemp(t)
	root := t.TempDir()
	writeFile(t, root, "a.go", []byte("package a\n"))
	res, err := index.Repo(s, &config.Repo{Path: root, Name: "plain"}, index.Options{})
	if err != nil || res.FilesAdded != 1 || res.Branch != "" || res.Commit != "" {
		t.Fatalf("%+v %v", res, err)
	}
	if f := fileRow(t, s, root, "a.go"); f.LastCommit != "" {
		t.Fatalf("%+v", f)
	}
}

func TestIndexRepoBackfillsLastCommitOfReincludedFiles(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	first, err := index.Repo(s, rep, index.Options{Exclude: []string{"service/**"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := index.Repo(s, rep, index.Options{})
	if err != nil || res.FilesAdded != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	if f := fileRow(t, s, rep.Path, "service/service.go"); f.LastCommit != first.Commit {
		t.Fatalf("%+v", f)
	}
}

func TestIndexRepoRefreshesMtimeWithoutReindexing(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	if _, err := index.Repo(s, rep, index.Options{}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(rep.Path, "store", "store.go")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	res, err := index.Repo(s, rep, index.Options{})
	if err != nil || res.FilesIndexed != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if f := fileRow(t, s, rep.Path, "store/store.go"); f.Mtime != later.UnixNano() {
		t.Fatalf("mtime %d want %d", f.Mtime, later.UnixNano())
	}
}

func TestIndexRepoAtSubdirectoryOfGitRepo(t *testing.T) {
	s := openTemp(t)
	root := t.TempDir()
	writeFile(t, root, "top.go", []byte("package top\n"))
	writeFile(t, root, "pkg/b.go", []byte("package b\n"))
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-qm", "one"}} {
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@x", "-c", "user.name=t"}, args...)...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	pkg := filepath.Join(root, "pkg")
	res, err := index.Repo(s, &config.Repo{Path: pkg, Name: "pkg"}, index.Options{})
	if err != nil || res.FilesAdded != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if f := fileRow(t, s, pkg, "b.go"); f.LastCommit != res.Commit || f.LastCommitAt == 0 {
		t.Fatalf("%+v", f)
	}
}

func TestIndexRepoWritesManyFilesInChunks(t *testing.T) {
	s := openTemp(t)
	root := t.TempDir()
	for i := 0; i < 1201; i++ {
		writeFile(t, root, fmt.Sprintf("d%d/f%d.go", i%7, i), []byte("package d\n"))
	}
	rep := &config.Repo{Path: root, Name: "many"}
	res, err := index.Repo(s, rep, index.Options{})
	if err != nil || res.FilesAdded != 1201 {
		t.Fatalf("%+v %v", res, err)
	}
	for i := 0; i < 1100; i++ {
		if err := os.Remove(filepath.Join(root, fmt.Sprintf("d%d/f%d.go", i%7, i))); err != nil {
			t.Fatal(err)
		}
	}
	res, err = index.Repo(s, rep, index.Options{})
	if err != nil || res.FilesDeleted != 1100 || res.FilesSkipped != 101 {
		t.Fatalf("%+v %v", res, err)
	}
	r, _ := s.RepoByPath(root)
	if files, _, err := s.RepoStats(r.ID); err != nil || files != 101 {
		t.Fatalf("files %d %v", files, err)
	}
}

func TestIndexRepoCountsUnreadableFileAsSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads files regardless of permissions")
	}
	s := openTemp(t)
	root := t.TempDir()
	writeFile(t, root, "a.go", []byte("package a\n"))
	writeFile(t, root, "b.go", []byte("package a\n"))
	p := filepath.Join(root, "b.go")
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	res, err := index.Repo(s, &config.Repo{Path: root, Name: "r"}, index.Options{})
	if err != nil || res.FilesAdded != 1 || res.FilesSkipped != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestIndexRepoCountsDirtyFiles(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	if err := os.WriteFile(filepath.Join(rep.Path, "extra.go"), []byte("package lib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := index.Repo(s, rep, index.Options{Workspace: "fixture"})
	if err != nil || res.Dirty != 1 {
		t.Fatalf("dirty %d, err %v", res.Dirty, err)
	}
}
