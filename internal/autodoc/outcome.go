package autodoc

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/docs/write"
)

// outcome is what one claude run changed.
type outcome struct {
	// paths are the journaled files inside the repo root, repo-relative
	// slash paths, sorted.
	paths []string
	// outside are journaled files outside the repo root (absolute).
	outside []string
	// tree holds what changed in the working tree besides the journal.
	tree treeChanges
	// top is the top of the working tree, "" outside git.
	top string
}

// fatal returns the error that stops the whole run, or nil: changes to
// files that were modified before the run, or a moved HEAD, cannot be
// undone without risking the user's own work.
func (c treeChanges) fatal() error {
	if len(c.dirty) == 0 && !c.headMoved {
		return nil
	}
	return fmt.Errorf("autodoc: stopping: the agent wrote outside codedoc doc set: %s; check them by hand", c.describe())
}

// outcome reads the journal of run id and compares the working tree
// with tree.
func (r *runner) outcome(id string, tree treeState) (outcome, error) {
	files, err := write.Files(id)
	if err != nil {
		return outcome{}, fmt.Errorf("autodoc: journal %s: %w", id, err)
	}
	o := outcome{top: tree.top}
	journaled := map[string]bool{}
	for _, f := range files {
		journaled[canonical(f)] = true
	}
	o.paths, o.outside = r.relPaths(files)
	o.tree, err = tree.changes(journaled)
	if err != nil {
		return outcome{}, fmt.Errorf("autodoc: comparing the working tree: %w", err)
	}
	return o, nil
}

// canonical resolves the symbolic links of a path's directory, as git
// reports paths below the resolved top of the working tree.
func canonical(p string) string {
	if dir, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(dir, filepath.Base(p))
	}
	return p
}

// relPaths turns the journal's absolute paths into sorted slash paths
// relative to the repo root, and returns the files outside it apart.
func (r *runner) relPaths(files []string) (in, outside []string) {
	for _, f := range files {
		rel, ok := relTo(r.roots(), f)
		if !ok {
			outside = append(outside, f)
			continue
		}
		in = append(in, rel)
	}
	slices.Sort(in)
	return in, outside
}

// relInRoot returns the repo-relative slash paths of the absolute paths
// inside the repo root.
func (r *runner) relInRoot(paths []string) []string {
	var out []string
	for _, p := range paths {
		if rel, ok := relTo(r.roots(), p); ok {
			out = append(out, rel)
		}
	}
	return out
}

// roots returns the repo root and, when it differs, its resolved form.
func (r *runner) roots() []string {
	roots := []string{r.opts.Root}
	if resolved, err := filepath.EvalSymlinks(r.opts.Root); err == nil && resolved != r.opts.Root {
		roots = append(roots, resolved)
	}
	return roots
}

// relTo returns f relative to the first of roots that contains it.
func relTo(roots []string, f string) (string, bool) {
	for _, root := range roots {
		rel, err := filepath.Rel(root, f)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel), true
		}
	}
	return "", false
}
