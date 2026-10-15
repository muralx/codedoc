package autodoc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/muralx/codedoc/internal/gitmeta"
)

// treeState is what the runner records of the git working tree before a
// group, to find afterwards whatever the agent changed without going
// through `codedoc doc set` (which journals every file it writes).
type treeState struct {
	top   string            // absolute top of the working tree; "" outside git
	head  string            // HEAD commit; "" when unborn
	dirty map[string]string // absolute dirty path → content hash
}

// treeChanges are the paths whose state differs from a treeState.
type treeChanges struct {
	// clean were clean before: HEAD holds their pre-run content.
	clean []string
	// dirty were already dirty before (only under --allow-dirty): their
	// pre-run content exists nowhere else, so nothing may discard it.
	dirty     []string
	headMoved bool
}

// snapshotTree records the working tree containing root: HEAD and the
// content hash of every dirty or untracked path. Outside a git working
// tree it records nothing.
func snapshotTree(root string) (treeState, error) {
	top, err := gitmeta.Output(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return treeState{}, nil
	}
	st := treeState{top: top, dirty: map[string]string{}}
	st.head, _ = gitmeta.Output(top, "rev-parse", "-q", "--verify", "HEAD")
	paths, err := dirtyPaths(top)
	if err != nil {
		return treeState{}, err
	}
	for _, p := range paths {
		st.dirty[p] = contentHash(p)
	}
	return st, nil
}

// changes compares the working tree with st, leaving out the journaled
// paths (absolute), which only codedoc doc set wrote.
func (st treeState) changes(journaled map[string]bool) (treeChanges, error) {
	var c treeChanges
	if st.top == "" {
		return c, nil
	}
	head, _ := gitmeta.Output(st.top, "rev-parse", "-q", "--verify", "HEAD")
	c.headMoved = head != st.head
	now, err := dirtyPaths(st.top)
	if err != nil {
		return c, err
	}
	seen := map[string]bool{}
	for _, p := range now {
		seen[p] = true
		before, wasDirty := st.dirty[p]
		switch {
		case journaled[p]:
		case !wasDirty:
			c.clean = append(c.clean, p)
		case contentHash(p) != before:
			c.dirty = append(c.dirty, p)
		}
	}
	for p := range st.dirty {
		if !seen[p] && !journaled[p] {
			c.dirty = append(c.dirty, p) // reverted or committed by the agent
		}
	}
	return c, nil
}

// any reports whether anything outside the journal changed.
func (c treeChanges) any() bool { return len(c.clean) > 0 || len(c.dirty) > 0 || c.headMoved }

// describe names the changes for a failure reason.
func (c treeChanges) describe() string {
	var parts []string
	if len(c.clean) > 0 {
		parts = append(parts, "changed "+strings.Join(c.clean, ", "))
	}
	if len(c.dirty) > 0 {
		parts = append(parts, "changed already modified "+strings.Join(c.dirty, ", "))
	}
	if c.headMoved {
		parts = append(parts, "moved HEAD")
	}
	return strings.Join(parts, "; ")
}

// revertClean puts paths that were clean before the run back to their
// HEAD content, deleting those HEAD does not have. Their current content
// is first copied under backup, so nothing is lost even if someone other
// than the agent wrote them.
func revertClean(top, backup string, paths []string) error {
	var errs []error
	for _, p := range paths {
		rel, err := filepath.Rel(top, p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if data, err := os.ReadFile(p); err == nil {
			dst := filepath.Join(backup, rel)
			errs = append(errs, os.MkdirAll(filepath.Dir(dst), 0o700), os.WriteFile(dst, data, 0o600))
		}
		if _, err := gitmeta.Output(top, "cat-file", "-e", "HEAD:"+filepath.ToSlash(rel)); err != nil {
			_, rmErr := gitmeta.Output(top, "rm", "-q", "--cached", "--ignore-unmatch", "--", rel)
			errs = append(errs, rmErr, removeIfExists(p))
			continue
		}
		_, err = gitmeta.Output(top, "restore", "--source=HEAD", "--staged", "--worktree", "--", rel)
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func removeIfExists(p string) error {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// dirtyPaths lists the absolute paths git reports modified, staged,
// deleted or untracked under top.
func dirtyPaths(top string) ([]string, error) {
	rel, err := gitmeta.StatusPaths(top)
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(rel))
	for i, p := range rel {
		paths[i] = filepath.Join(top, filepath.FromSlash(p))
	}
	return paths, nil
}

// contentHash identifies the content of a path: a SHA-256 of a file,
// "absent" for nothing there, or the read error.
func contentHash(p string) string {
	data, err := os.ReadFile(p)
	switch {
	case os.IsNotExist(err):
		return "absent"
	case err != nil:
		return "unreadable: " + err.Error()
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
