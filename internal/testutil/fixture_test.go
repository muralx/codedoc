package testutil_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/testutil"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestCopyFixtureCreatesCleanReproducibleRepo(t *testing.T) {
	a := testutil.CopyFixture(t, "go-lib")
	b := testutil.CopyFixture(t, "go-lib")
	if a == b || filepath.Base(a) != "go-lib" {
		t.Fatalf("paths %s %s", a, b)
	}
	if _, err := os.Stat(filepath.Join(a, "store", "store.go")); err != nil {
		t.Fatal(err)
	}
	if gitOut(t, a, "branch", "--show-current") != "main" {
		t.Fatal("branch is not main")
	}
	if st := gitOut(t, a, "status", "--porcelain"); st != "" {
		t.Fatalf("copy is dirty: %s", st)
	}
	if ha, hb := gitOut(t, a, "rev-parse", "HEAD"), gitOut(t, b, "rev-parse", "HEAD"); ha != hb {
		t.Fatalf("commit not reproducible: %s %s", ha, hb)
	}
}

func TestFixtureSourcesHaveNoGitDir(t *testing.T) {
	err := filepath.WalkDir(testutil.WorkspaceDir(), func(p string, d os.DirEntry, err error) error {
		if err == nil && (d.Name() == ".git" || d.Name() == ".fixture") {
			t.Errorf("unexpected %s", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSourceRepoCommitsItsFiles(t *testing.T) {
	dir := testutil.SourceRepo(t, "app", map[string]string{"a/b.go": "package a\n", "c.txt": "c\n"})
	if filepath.Base(dir) != "app" {
		t.Fatalf("path %s", dir)
	}
	if got := gitOut(t, dir, "ls-files"); got != "a/b.go\nc.txt" {
		t.Errorf("committed files %q", got)
	}
	if got := gitOut(t, dir, "status", "--porcelain"); got != "" {
		t.Errorf("not clean: %q", got)
	}
}
