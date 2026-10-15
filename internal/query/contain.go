package query

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrOutside reports a path argument that leaves what a query may read:
// the repos of the workspace and the directories its dependencies are
// installed in.
var ErrOutside = errors.New("path is outside the repos of the workspace and the directories of its dependencies")

// outside is the error of a path argument that is not contained.
func outside(target string) error { return fmt.Errorf("%s: %w", target, ErrOutside) }

// contained reports whether a query may read the file or directory at
// an absolute path: cleaned, and with its symbolic links resolved, it
// must lie in a repo of the workspace or in a directory a dependency of
// the workspace is installed in (see readRoots). A symbolic link inside
// a repo that points elsewhere is therefore not contained. Every command
// that takes a path checks it here, through splitTarget or, for a file
// of a dependency, depFileAt.
func (c *Ctx) contained(abs string) bool {
	if !filepath.IsAbs(abs) {
		return false
	}
	resolved := resolveExisting(abs)
	for _, root := range c.readRoots() {
		if _, ok := relWithin(root, resolved); ok {
			return true
		}
	}
	return false
}

// readRoots returns the directories queries may read below, symbolic
// links resolved: the repos of the workspace and the package directories
// recorded for its dependency uses. They are read once per context.
func (c *Ctx) readRoots() []string {
	if c.roots != nil {
		return c.roots
	}
	c.roots = []string{}
	for _, r := range c.Workspace.Repos {
		c.roots = append(c.roots, resolveExisting(r.Path))
	}
	uses, err := c.Store.DepUses(0)
	if err != nil {
		return c.roots // without the uses only the repos are readable
	}
	for _, u := range uses {
		for _, dir := range []string{u.Dir, u.TypesDir} {
			if dir != "" && filepath.IsAbs(dir) {
				c.roots = append(c.roots, resolveExisting(dir))
			}
		}
	}
	return c.roots
}

// resolveExisting cleans an absolute path and resolves the symbolic
// links of its longest existing prefix; the rest, which names nothing on
// disk, is appended as written.
func resolveExisting(abs string) string {
	abs = filepath.Clean(abs)
	rest := ""
	for p := abs; ; {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return abs
		}
		rest, p = filepath.Join(filepath.Base(p), rest), parent
	}
}

// escapes reports whether a clean repo-relative slash path climbs out
// of its repo.
func escapes(p string) bool { return p == ".." || strings.HasPrefix(p, "../") }
