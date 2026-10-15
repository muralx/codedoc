package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func headMoved(cs []Change) bool {
	for _, c := range cs {
		if c.HeadMoved {
			return true
		}
	}
	return false
}

func TestBranchSwitchDeliveredAsHeadMoved(t *testing.T) {
	root := newRepo(t, "a.go")
	git(t, root, "branch", "other")
	c := startRepo(t, root)
	git(t, root, "checkout", "-q", "other")
	c.waitFor("HeadMoved", headMoved)
}

func TestNewCommitDeliveredAsHeadMoved(t *testing.T) {
	root := newRepo(t, "a.go")
	c := startRepo(t, root)
	git(t, root, "commit", "-q", "--allow-empty", "-m", "next")
	c.waitFor("HeadMoved", headMoved)
}

func TestWorktreeHeadTracked(t *testing.T) {
	main := newRepo(t, "a.go")
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, main, "worktree", "add", "-q", "-b", "feature", wt)
	before := headState(wt)
	commit := git(t, wt, "rev-parse", "HEAD")
	if before != "ref: refs/heads/feature@"+commit {
		t.Fatalf("worktree head: %q", before)
	}
	c := startRepo(t, wt)
	git(t, wt, "commit", "-q", "--allow-empty", "-m", "on feature")
	c.waitFor("HeadMoved", headMoved)
	if headState(wt) == before {
		t.Fatal("head state unchanged")
	}
}

func TestHeadStateReadsPackedRefsAndDetachedHead(t *testing.T) {
	root := newRepo(t, "a.go")
	commit := git(t, root, "rev-parse", "HEAD")
	git(t, root, "pack-refs", "--all")
	if got := headState(root); got != "ref: refs/heads/main@"+commit {
		t.Fatalf("packed: %q", got)
	}
	git(t, root, "checkout", "-q", "--detach")
	if got := headState(root); got != commit+"@"+commit {
		t.Fatalf("detached: %q", got)
	}
}

func TestHeadStateEdgeCases(t *testing.T) {
	plain := t.TempDir()
	if headState(plain) != "" {
		t.Fatal("non-git dir has a head")
	}
	write(t, plain, ".git", "garbage\n")
	if headState(plain) != "" {
		t.Fatal("bad .git file has a head")
	}
	unborn := t.TempDir()
	git(t, unborn, "init", "-q")
	if got := headState(unborn); !strings.HasSuffix(got, "@") {
		t.Fatalf("unborn: %q", got)
	}
	if err := os.Remove(filepath.Join(unborn, ".git", "HEAD")); err != nil {
		t.Fatal(err)
	}
	if headState(unborn) != "" {
		t.Fatal("missing HEAD has a head")
	}
}

func TestRelativeGitdirFile(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, "store")
	write(t, gitDir, "HEAD", "ref: refs/heads/x\n")
	write(t, gitDir, "refs/heads/x", "abc\n")
	write(t, root, "tree/.git", "gitdir: ../store\n")
	if got := headState(filepath.Join(root, "tree")); got != "ref: refs/heads/x@abc" {
		t.Fatalf("got %q", got)
	}
}
