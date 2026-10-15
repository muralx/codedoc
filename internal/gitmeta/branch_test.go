package gitmeta_test

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/gitmeta"
)

// commitFile writes one file and commits it, returning the commit.
func commitFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	write(t, dir, name, body)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", name)
	return headOf(t, dir)
}

// branched is a repo whose main holds a.go and m.go and whose branch
// work, forked after a.go, holds w1.go, a merge of main and sub/w2.go.
func branched(t *testing.T) (dir string, commits map[string]string) {
	t.Helper()
	dir = t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	commits = map[string]string{"a": commitFile(t, dir, "a.go", "package a\n")}
	git(t, dir, "checkout", "-q", "-b", "work")
	commits["w1"] = commitFile(t, dir, "w1.go", "package a\n")
	git(t, dir, "checkout", "-q", "main")
	commits["m"] = commitFile(t, dir, "m.go", "package a\n")
	git(t, dir, "checkout", "-q", "work")
	git(t, dir, "merge", "-q", "--no-edit", "main")
	commits["w2"] = commitFile(t, dir, "sub/w2.go", "package sub\n")
	return dir, commits
}

func TestOwnCommitsLeaveOutTheDefaultBranchAndMerges(t *testing.T) {
	dir, c := branched(t)
	own, err := gitmeta.OwnCommits(dir, "HEAD", 0, "refs/heads/main")
	if err != nil || strings.Join(own, " ") != c["w2"]+" "+c["w1"] {
		t.Fatalf("%v %v, want w2 %s then w1 %s", own, err, c["w2"], c["w1"])
	}
	if own, _ = gitmeta.OwnCommits(dir, "HEAD", 1, "refs/heads/main"); len(own) != 1 || own[0] != c["w2"] {
		t.Fatalf("limit 1: %v", own)
	}
	if own, _ = gitmeta.OwnCommits(dir, "HEAD", 0, "refs/heads/main", c["w1"]); len(own) != 1 || own[0] != c["w2"] {
		t.Fatalf("since w1: %v", own)
	}
	if all, _ := gitmeta.OwnCommits(dir, "HEAD", 0); len(all) != 4 {
		t.Fatalf("without exclusion: %v", all)
	}
	if _, err := gitmeta.OwnCommits(dir, "nope", 0); err == nil {
		t.Fatal("an unknown head listed commits")
	}
}

func TestTouchedPathsOfCommitsRelativeToTheDirectory(t *testing.T) {
	dir, c := branched(t)
	paths, err := gitmeta.TouchedPaths(dir, []string{c["w2"], c["w1"], c["a"]})
	sort.Strings(paths)
	if err != nil || strings.Join(paths, ",") != "a.go,sub/w2.go,w1.go" {
		t.Fatalf("%v %v", paths, err)
	}
	if paths, err = gitmeta.TouchedPaths(dir+"/sub", []string{c["w2"], c["w1"]}); err != nil || strings.Join(paths, ",") != "w2.go" {
		t.Fatalf("below sub: %v %v", paths, err)
	}
	if paths, err = gitmeta.TouchedPaths(dir, nil); paths != nil || err != nil {
		t.Fatalf("no commits: %v %v", paths, err)
	}
	if _, err = gitmeta.TouchedPaths(dir, []string{"nope"}); err == nil {
		t.Fatal("an unknown commit listed paths")
	}
}

func TestParentTreeIsTheParentOrTheEmptyTree(t *testing.T) {
	dir, c := branched(t)
	if p, err := gitmeta.ParentTree(dir, c["w1"]); p != c["a"] || err != nil {
		t.Fatalf("%q %v", p, err)
	}
	empty, err := gitmeta.ParentTree(dir, c["a"])
	if err != nil || len(empty) != len(c["a"]) || empty == c["a"] {
		t.Fatalf("%q %v", empty, err)
	}
	changed, err := gitmeta.ChangesSince(dir, empty)
	if err != nil || len(changed) != 4 || changed[0].Status != 'A' {
		t.Fatalf("since the empty tree: %+v %v", changed, err)
	}
	if _, err := gitmeta.ParentTree(t.TempDir(), "HEAD"); !errors.Is(err, gitmeta.ErrNotRepo) {
		t.Fatalf("outside git: %v", err)
	}
}

func TestIsAncestor(t *testing.T) {
	dir, c := branched(t)
	for commit, want := range map[string]bool{c["a"]: true, c["w2"]: true, strings.Repeat("0", 40): false} {
		if got, err := gitmeta.IsAncestor(dir, commit, "HEAD"); got != want || err != nil {
			t.Fatalf("%s: %v %v", commit, got, err)
		}
	}
	if got, _ := gitmeta.IsAncestor(dir, c["w2"], "main"); got {
		t.Fatal("a branch commit is no ancestor of main")
	}
}

func TestDefaultBranch(t *testing.T) {
	dir, _ := branched(t)
	if name, err := gitmeta.DefaultBranch(dir); name != "main" || err != nil {
		t.Fatalf("%q %v", name, err)
	}
	refs, err := gitmeta.BranchRefs(dir, "main")
	if err != nil || len(refs) != 1 || refs[0] != "refs/heads/main" {
		t.Fatalf("%v %v", refs, err)
	}
	git(t, dir, "branch", "-m", "main", "trunk")
	if name, err := gitmeta.DefaultBranch(dir); name != "" || err != nil {
		t.Fatalf("neither main nor master: %q %v", name, err)
	}
	git(t, dir, "branch", "master", "trunk")
	if name, _ := gitmeta.DefaultBranch(dir); name != "master" {
		t.Fatalf("%q", name)
	}
	git(t, dir, "update-ref", "refs/remotes/origin/trunk", "trunk")
	git(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	if name, _ := gitmeta.DefaultBranch(dir); name != "trunk" {
		t.Fatalf("origin/HEAD names trunk, got %q", name)
	}
	if refs, _ = gitmeta.BranchRefs(dir, "trunk"); len(refs) != 2 {
		t.Fatalf("%v", refs)
	}
	if _, err := gitmeta.DefaultBranch(t.TempDir()); !errors.Is(err, gitmeta.ErrNotRepo) {
		t.Fatalf("outside git: %v", err)
	}
}
