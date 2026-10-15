package upkeep

import (
	"github.com/muralx/codedoc/internal/autodoc"
	"github.com/muralx/codedoc/internal/gitmeta"
)

// Git is the git access upkeep needs; CLI is the real one.
type Git interface {
	// Committer tells a dirty working tree (untracked files that are
	// not ignored count) and commits the run's files.
	autodoc.Committer
	// Head returns the branch and commit checked out at root; the
	// branch is "HEAD" when detached.
	Head(root string) (gitmeta.HeadState, error)
	// Scope returns what a branch at head changed since last, its last
	// documented commit ("" before the first run); see CLI.Scope.
	Scope(root, branch, head, last string, firstRun int) (Scope, error)
}

// Scope is what upkeep looks at on a branch: the commits that are the
// branch's own since its last documented commit.
type Scope struct {
	// Base is the commit or tree those commits started from: the
	// parent of the oldest one, the empty tree when it has none. It is
	// "" when no commit is in scope.
	Base string
	// Paths are the repo-relative files those commits touched.
	Paths []string
	// Anchor is set when the commits were selected by the first-run
	// rule and started from a commit: that commit, which upkeep records
	// as the branch's last documented commit, so a later check or a
	// retry looks at the same commits and what came after.
	Anchor string
}

// CLI is the Git backed by the git command line. Commits use the
// repository's configured identity.
type CLI struct{ autodoc.Git }

// Head returns the branch and commit checked out at root.
func (CLI) Head(root string) (gitmeta.HeadState, error) { return gitmeta.Head(root) }

// Scope selects the commits reachable from head that are the branch's
// own: not reachable from the default branch (see gitmeta.DefaultBranch;
// on the default branch itself, or without one, nothing is left out)
// and, merge commits aside, newer than last. When last is "" or no
// longer an ancestor of head (the branch was rebased or reset), it is
// the first-run rule instead: the newest firstRun of the branch's own
// commits, with Scope.Anchor set.
func (CLI) Scope(root, branch, head, last string, firstRun int) (Scope, error) {
	exclude, err := othersCommits(root, branch)
	if err != nil {
		return Scope{}, err
	}
	known := false
	if last != "" {
		if known, err = gitmeta.IsAncestor(root, last, head); err != nil {
			return Scope{}, err
		}
	}
	if known {
		exclude, firstRun = append(exclude, last), 0
	}
	commits, err := gitmeta.OwnCommits(root, head, firstRun, exclude...)
	if err != nil || len(commits) == 0 {
		return Scope{}, err
	}
	base, err := gitmeta.ParentTree(root, commits[len(commits)-1])
	if err != nil {
		return Scope{}, err
	}
	sc := Scope{Base: base}
	if !known {
		if sc.Anchor, err = anchor(root, base, head); err != nil {
			return Scope{}, err
		}
	}
	sc.Paths, err = gitmeta.TouchedPaths(root, commits)
	return sc, err
}

// anchor returns base when it is a commit, an ancestor of head, and ""
// when it is the empty tree.
func anchor(root, base, head string) (string, error) {
	ok, err := gitmeta.IsAncestor(root, base, head)
	if err != nil || !ok {
		return "", err
	}
	return base, nil
}

// othersCommits returns the refs whose commits are not a branch's own:
// those of the default branch, none on the default branch itself or in
// a repository without one.
func othersCommits(root, branch string) ([]string, error) {
	def, err := gitmeta.DefaultBranch(root)
	if err != nil || def == "" || def == branch {
		return nil, err
	}
	return gitmeta.BranchRefs(root, def)
}
