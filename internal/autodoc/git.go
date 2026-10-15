package autodoc

import (
	"errors"

	"github.com/muralx/codedoc/internal/gitmeta"
)

// Git is the Committer backed by the git command line. Commits use the
// repository's configured identity.
type Git struct{}

// Dirty reports whether root has uncommitted changes or untracked files
// that are not ignored.
func (Git) Dirty(root string) (bool, error) {
	files, err := gitmeta.DirtyFiles(root)
	return len(files) > 0, err
}

// Commit stages exactly paths (repo-relative) and commits only them with
// msg, leaving anything else in the index or working tree untouched.
// When the commit fails (a hook refuses it, no identity is configured),
// the paths are unstaged again, so restoring the files leaves a clean
// tree.
func (Git) Commit(root string, paths []string, msg string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := gitmeta.Input(root, "", append([]string{"add", "--"}, paths...)...); err != nil {
		return err
	}
	err := gitmeta.Input(root, msg, append([]string{"commit", "-q", "-F", "-", "--only", "--"}, paths...)...)
	if err != nil {
		return errors.Join(err, gitmeta.Input(root, "", append([]string{"reset", "-q", "--"}, paths...)...))
	}
	return nil
}
