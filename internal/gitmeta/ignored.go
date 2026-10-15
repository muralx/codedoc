package gitmeta

import "errors"

// Ignored reports whether git ignores a path relative to dir (a
// .gitignore, .git/info/exclude or the user's excludes file names it and
// it is not tracked): the files ListFiles leaves out. A directory that
// is no git repository ignores nothing.
func Ignored(dir, path string) (bool, error) {
	_, err := run(dir, "check-ignore", "-q", "--", path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotRepo), exitCode(err) == 1:
		return false, nil
	}
	return false, err
}
