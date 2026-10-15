package gitmeta

import (
	"strconv"
	"strings"
)

// DefaultBranch returns the short name of the repository's default
// branch: the one origin/HEAD points at, else the first of main and
// master that exists locally or on origin; "" when there is none.
func DefaultBranch(dir string) (string, error) {
	out, err := run(dir, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD")
	if name, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "origin/"); err == nil && ok {
		return name, nil
	}
	for _, name := range []string{"main", "master"} {
		refs, err := BranchRefs(dir, name)
		if err != nil || len(refs) > 0 {
			return name, err
		}
	}
	return "", nil
}

// BranchRefs returns the refs that carry branch name in the repository
// at dir, of the local branch and the one on origin, those that exist.
func BranchRefs(dir, name string) ([]string, error) {
	var refs []string
	for _, ref := range []string{"refs/heads/" + name, "refs/remotes/origin/" + name} {
		commit, err := resolveCommit(dir, ref)
		if err != nil {
			return nil, err
		}
		if commit != "" {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

// IsAncestor reports whether commit is head or one of its ancestors; a
// commit the repository does not have is not.
func IsAncestor(dir, commit, head string) (bool, error) { return isAncestor(dir, commit, head) }

// OwnCommits returns the commits reachable from head and from none of
// exclude, merge commits left out, newest first and never a commit
// before one of its descendants; at most limit of them when limit > 0.
func OwnCommits(dir, head string, limit int, exclude ...string) ([]string, error) {
	args := []string{"rev-list", "--topo-order", "--no-merges"}
	if limit > 0 {
		args = append(args, "--max-count="+strconv.Itoa(limit))
	}
	args = append(args, head)
	for _, e := range exclude {
		args = append(args, "^"+e)
	}
	out, err := run(dir, append(args, "--")...)
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// TouchedPaths returns the paths under dir, relative to it, that the
// given commits changed against their first parent, each once.
func TouchedPaths(dir string, commits []string) ([]string, error) {
	if len(commits) == 0 {
		return nil, nil
	}
	out, err := runInput(dir, strings.Join(commits, "\n")+"\n", "log", "--no-walk=unsorted", "--stdin", "-z", "--relative",
		"--name-only", "--format=")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p = strings.TrimLeft(p, "\n"); p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// ParentTree returns what commit changed from: its first parent, or the
// empty tree for a commit without one. ChangesSince and OpenBlobs take
// either.
func ParentTree(dir, commit string) (string, error) {
	parent, err := resolveCommit(dir, commit+"^")
	if err != nil || parent != "" {
		return parent, err
	}
	out, err := runInput(dir, "", "hash-object", "-t", "tree", "--stdin")
	return strings.TrimSpace(string(out)), err
}
