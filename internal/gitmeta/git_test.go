package gitmeta_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/gitmeta"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@x", "-c", "user.name=t"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// initRepo commits a.go, then b.go, then modifies a.go and creates an
// untracked c.go and an ignored d.log.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, ".gitignore", "*.log\n")
	write(t, dir, "a.go", "package a\n")
	git(t, dir, "add", "a.go", ".gitignore")
	git(t, dir, "commit", "-qm", "a", "--date=2026-01-01T00:00:00Z")
	write(t, dir, "b.go", "package b\n")
	git(t, dir, "add", "b.go")
	git(t, dir, "commit", "-qm", "b")
	write(t, dir, "a.go", "package a // changed\n")
	write(t, dir, "c.go", "package c\n")
	write(t, dir, "d.log", "ignored\n")
	return dir
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	h, err := gitmeta.Head(dir)
	if err != nil {
		t.Fatal(err)
	}
	return h.Commit
}

func TestHeadAndDirty(t *testing.T) {
	dir := initRepo(t)
	h, err := gitmeta.Head(dir)
	if err != nil || h.Branch != "main" || len(h.Commit) != 40 {
		t.Fatalf("%+v %v", h, err)
	}
	dirty, err := gitmeta.DirtyFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(dirty)
	if strings.Join(dirty, ",") != "a.go,c.go" {
		t.Fatalf("dirty %v", dirty)
	}
}

func TestDetachedHead(t *testing.T) {
	dir := initRepo(t)
	git(t, dir, "checkout", "-q", "--detach", "HEAD")
	h, err := gitmeta.Head(dir)
	if err != nil || h.Branch != "HEAD" || len(h.Commit) != 40 {
		t.Fatalf("%+v %v", h, err)
	}
}

func TestDirtyFilesHandlesRenames(t *testing.T) {
	dir := initRepo(t)
	git(t, dir, "mv", "b.go", "e.go")
	dirty, err := gitmeta.DirtyFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(dirty)
	if strings.Join(dirty, ",") != "a.go,c.go,e.go" {
		t.Fatalf("dirty %v", dirty)
	}
}

func TestDirtyFilesHandlesWorktreeRenames(t *testing.T) {
	dir := initRepo(t)
	if err := os.Rename(filepath.Join(dir, "b.go"), filepath.Join(dir, "e.go")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-N", "e.go") // intent-to-add: status shows " R e.go\0b.go"
	dirty, err := gitmeta.DirtyFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(dirty)
	if strings.Join(dirty, ",") != "a.go,c.go,e.go" {
		t.Fatalf("dirty %q", dirty)
	}
}

// initMonorepo commits top.go, pkg/b.go and pkg/sub/c.go, then a second
// commit touching only top.go, and leaves changes inside and outside pkg/.
// It returns the repo root and its pkg directory.
func initMonorepo(t *testing.T) (root, pkg string) {
	t.Helper()
	root = t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	write(t, root, "top.go", "package top\n")
	write(t, root, "pkg/b.go", "package b\n")
	write(t, root, "pkg/sub/c.go", "package sub\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "one")
	write(t, root, "top.go", "package top // two\n")
	git(t, root, "commit", "-qam", "two")
	write(t, root, "top.go", "package top // dirty\n")
	write(t, root, "untracked.go", "package top\n")
	write(t, root, "pkg/b.go", "package b // dirty\n")
	write(t, root, "pkg/u.go", "package b\n")
	return root, filepath.Join(root, "pkg")
}

func TestSubdirectoryRepoIsScopedAndRelative(t *testing.T) {
	_, pkg := initMonorepo(t)
	dirty, err := gitmeta.DirtyFiles(pkg)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(dirty)
	if strings.Join(dirty, ",") != "b.go,u.go" {
		t.Fatalf("dirty %q", dirty)
	}
	files, err := gitmeta.ListFiles(pkg)
	if err != nil || strings.Join(files, ",") != "b.go,sub/c.go,u.go" {
		t.Fatalf("files %q %v", files, err)
	}
	head := headOf(t, pkg)
	m, err := gitmeta.LastCommits(pkg, head, "")
	if err != nil || len(m) != 2 || m["b.go"].Commit == "" || m["sub/c.go"].Commit != m["b.go"].Commit {
		t.Fatalf("%+v %v", m, err)
	}
	since, err := gitmeta.LastCommits(pkg, head, m["b.go"].Commit)
	if err != nil || len(since) != 0 {
		t.Fatalf("commit outside pkg/ leaked: %+v %v", since, err)
	}
}

func TestLastCommitsHandlesNewlinesInNames(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "odd\nname.go", "package odd\n")
	git(t, dir, "add", "odd\nname.go")
	git(t, dir, "commit", "-qm", "odd")
	m, err := gitmeta.LastCommits(dir, headOf(t, dir), "")
	if err != nil {
		t.Fatal(err)
	}
	if m["odd\nname.go"].Commit != headOf(t, dir) || len(m) != 4 {
		t.Fatalf("%+v", m)
	}
}

func TestTrackedAndUntrackedFiles(t *testing.T) {
	dir := initRepo(t)
	files, err := gitmeta.ListFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(files, ",") != ".gitignore,a.go,b.go,c.go" {
		t.Fatalf("%v", files)
	}
}

func TestLastCommitPerFile(t *testing.T) {
	dir := initRepo(t)
	m, err := gitmeta.LastCommits(dir, headOf(t, dir), "")
	if err != nil {
		t.Fatal(err)
	}
	if m["a.go"].Commit == m["b.go"].Commit || m["a.go"].At == 0 || len(m["b.go"].Commit) != 40 {
		t.Fatalf("%+v", m)
	}
}

func TestLastCommitsSince(t *testing.T) {
	dir := initRepo(t)
	all, _ := gitmeta.LastCommits(dir, headOf(t, dir), "")
	since, err := gitmeta.LastCommits(dir, headOf(t, dir), all["a.go"].Commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 1 || since["b.go"].Commit != all["b.go"].Commit {
		t.Fatalf("%+v", since)
	}
}

func TestNotRepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := gitmeta.Head(dir); !errors.Is(err, gitmeta.ErrNotRepo) {
		t.Fatalf("Head: %v", err)
	}
	if _, err := gitmeta.ListFiles(dir); !errors.Is(err, gitmeta.ErrNotRepo) {
		t.Fatalf("ListFiles: %v", err)
	}
	if _, err := gitmeta.DirtyFiles(dir); !errors.Is(err, gitmeta.ErrNotRepo) {
		t.Fatalf("DirtyFiles: %v", err)
	}
	if _, err := gitmeta.LastCommits(dir, "HEAD", ""); !errors.Is(err, gitmeta.ErrNotRepo) {
		t.Fatalf("LastCommits: %v", err)
	}
}

func TestEmptyRepoHasNoHead(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	h, err := gitmeta.Head(dir)
	if err != nil || h.Branch != "main" || h.Commit != "" {
		t.Fatalf("%+v %v", h, err)
	}
	m, err := gitmeta.LastCommits(dir, h.Commit, "")
	if err != nil || len(m) != 0 {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestLastCommitsSinceNonAncestorReadsFullHistory(t *testing.T) {
	dir := initRepo(t)
	all, _ := gitmeta.LastCommits(dir, headOf(t, dir), "")
	git(t, dir, "stash", "-q", "-u")
	git(t, dir, "checkout", "-q", "-b", "other", all["a.go"].Commit)
	write(t, dir, "x.go", "package x\n")
	git(t, dir, "add", "x.go")
	git(t, dir, "commit", "-qm", "x")
	m, err := gitmeta.LastCommits(dir, headOf(t, dir), all["b.go"].Commit)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m["b.go"]; ok || m["a.go"].Commit != all["a.go"].Commit || m["x.go"].Commit == "" {
		t.Fatalf("%+v", m)
	}
	unknown, err := gitmeta.LastCommits(dir, headOf(t, dir), strings.Repeat("0", 40))
	if err != nil || len(unknown) != len(m) {
		t.Fatalf("%+v %v", unknown, err)
	}
}

func TestLastCommitsForPaths(t *testing.T) {
	dir := initRepo(t)
	m, err := gitmeta.LastCommits(dir, headOf(t, dir), "", "b.go", "c.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m["b.go"].Commit == "" {
		t.Fatalf("%+v", m)
	}
}
