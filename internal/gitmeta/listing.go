package gitmeta

import (
	"fmt"
	"strconv"
	"strings"
)

// IgnoredPaths returns the paths under the directory under of the
// working tree at dir (both relative to dir; "." for all of it) that
// git ignores, relative to dir. A directory ignored as a whole is one
// entry ending in a slash: its files are not listed.
func IgnoredPaths(dir, under string) ([]string, error) {
	out, err := run(dir, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory", "--", under)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// CommitTime returns the committer date of the commit ref names in the
// repository at dir, as Unix seconds; ErrUnknownRef when it names none.
func CommitTime(dir, ref string) (int64, error) {
	commit, err := resolveCommit(dir, ref)
	if err != nil {
		return 0, err
	}
	if commit == "" {
		return 0, fmt.Errorf("%w %q", ErrUnknownRef, ref)
	}
	out, err := run(dir, "show", "-s", "--format=%ct", commit)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
}
