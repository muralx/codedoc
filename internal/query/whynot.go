package query

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// notIndexed returns ErrNotIndexed with the reason for a repo-relative
// path that exists on disk (the caller found no row for it), nil for
// one that does not exist.
func (c *Ctx) notIndexed(r *config.Repo, rel string) error {
	why, err := c.whyNot(r, rel)
	if err != nil || why == "" {
		return err
	}
	return fmt.Errorf("%s %w: %s", rel, ErrNotIndexed, why)
}

// whyNot says in one line why a path of a repo that exists on disk is
// outside the index (efficiency spec section 4): it lies in a package
// that is not indexed (see whyNotDep), or discovery leaves it out
// (index.Skipped: an excluded directory, a lock file, an exclude glob,
// .gitignore, the size limit, a binary file, a language the repo does
// not select); else the index has not read it yet. It is "" for a path
// that does not exist.
func (c *Ctx) whyNot(r *config.Repo, rel string) (string, error) {
	abs := filepath.Join(r.Path, filepath.FromSlash(rel))
	info, err := os.Lstat(abs)
	if err != nil {
		return "", nil
	}
	if why, err := c.whyNotDep(abs); why != "" || err != nil {
		return why, err
	}
	why, err := index.Skipped(r.Path, rel, c.Workspace.Exclude, r.FileLanguages())
	switch {
	case err != nil || why != "":
		return why, err
	case info.IsDir():
		return "no file below it is indexed", nil
	}
	return "the index has not read it yet; run codedoc index", nil
}

// whyNotDep says why a path inside a directory a dependency of the
// workspace is installed in is not indexed, by the state of that use
// (deps spec section 4); "" for a path in no such directory. A path in
// node_modules that no use installs belongs to a package no manifest of
// the workspace declares.
func (c *Ctx) whyNotDep(abs string) (string, error) {
	uses, err := c.Store.DepUses(0)
	if err != nil {
		return "", err
	}
	resolved := resolveExisting(abs)
	var in *store.DepUse
	depth := -1
	for i, u := range uses {
		for _, dir := range []string{u.Dir, u.TypesDir} {
			if dir == "" || len(dir) <= depth {
				continue
			}
			_, plain := relWithin(dir, abs)
			_, linked := relWithin(resolveExisting(dir), resolved)
			if plain || linked {
				in, depth = &uses[i], len(dir)
			}
		}
	}
	if in == nil {
		if inNodeModules(abs) {
			return "it belongs to a package no manifest of the workspace declares (dependencies of dependencies are not indexed)", nil
		}
		return "", nil
	}
	return depStateReason(*in), nil
}

// inNodeModules reports whether a path has a node_modules directory
// among its components.
func inNodeModules(abs string) bool {
	for dir := filepath.Dir(abs); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "node_modules" {
			return true
		}
	}
	return false
}

// depStateReason says why a file of the package of a dependency use is
// not indexed.
func depStateReason(u store.DepUse) string {
	switch u.State {
	case store.DepUnused:
		return fmt.Sprintf("no source file imports the dependency %s; name it under deps.include in the config to index it",
			u.Name)
	case store.DepPending:
		return fmt.Sprintf("the dependency %s waits to be indexed; run codedoc deps index", u.Name)
	case store.DepFailed:
		return fmt.Sprintf("indexing the dependency %s failed: %s", u.Name, u.Error)
	case store.DepMissing:
		return fmt.Sprintf("the dependency %s is not installed", u.Name)
	case store.DepSkipped:
		return ""
	}
	return fmt.Sprintf("of the dependency %s only the declarations its entry points export are indexed", u.Name)
}
