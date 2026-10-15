package gitmeta

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

// ErrUntracked reports that a file has no git history: it is not in the
// index, or the repository has no commits yet.
var ErrUntracked = errors.New("gitmeta: file is not tracked")

// Uncommitted is the time Blame reports for lines that are not committed
// yet: newer than any commit (spec 7.5).
const Uncommitted int64 = math.MaxInt64

// zeroHash is the commit blame reports for uncommitted lines.
const zeroHash = "0000000000000000000000000000000000000000"

// Blame maps every line of path (relative to dir, 1-based) to the
// committer time of the commit that last changed it, as Unix seconds.
// Lines moved or copied within the file or from files changed in the same
// commit keep the time of their origin (git blame -M -C, which bounds the
// search to those files); should that fail, plain blame is used. Lines
// changed in the working tree or the index map to Uncommitted.
// Untracked files return ErrUntracked.
func Blame(dir, path string) (map[int]int64, error) {
	tracked, err := isTracked(dir, path)
	if err != nil {
		return nil, err
	}
	if !tracked {
		return nil, ErrUntracked
	}
	out, err := run(dir, "blame", "--porcelain", "-M", "-C", "--", path)
	if err != nil {
		out, err = run(dir, "blame", "--porcelain", "--", path)
	}
	if err != nil {
		return nil, err
	}
	return parseBlame(string(out)), nil
}

// isTracked reports whether path is in the index of a repository that has
// at least one commit.
func isTracked(dir, path string) (bool, error) {
	head, err := resolveCommit(dir, "HEAD")
	if err != nil || head == "" {
		return false, err
	}
	out, err := run(dir, "--literal-pathspecs", "ls-files", "-z", "--", path)
	if err != nil {
		return false, err
	}
	return len(out) > 0, nil
}

// parseBlame reads `git blame --porcelain` output. Each line group starts
// with "<hash> <orig> <final> [<count>]"; the commit's header fields,
// committer-time among them, follow only the first time a commit appears,
// and every source line is prefixed by a tab.
func parseBlame(out string) map[int]int64 {
	times := map[string]int64{}
	lines := map[int]int64{}
	hash, final := "", 0
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "\t"):
			lines[final] = blameTime(hash, times)
		case strings.HasPrefix(l, "committer-time "):
			at, err := strconv.ParseInt(strings.TrimPrefix(l, "committer-time "), 10, 64)
			if err == nil {
				times[hash] = at
			}
		default:
			if h, n, ok := blameHeader(l); ok {
				hash, final = h, n
			}
		}
	}
	return lines
}

// blameTime returns the time of hash, Uncommitted for the zero hash.
func blameTime(hash string, times map[string]int64) int64 {
	if hash == zeroHash {
		return Uncommitted
	}
	return times[hash]
}

// blameHeader parses "<hash> <orig line> <final line> [<count>]".
func blameHeader(l string) (string, int, bool) {
	f := strings.Fields(l)
	if len(f) < 3 || len(f[0]) < 40 || strings.Trim(f[0], "0123456789abcdef") != "" {
		return "", 0, false
	}
	n, err := strconv.Atoi(f[2])
	if err != nil {
		return "", 0, false
	}
	return f[0], n, true
}

// IsTracked reports whether path (relative to dir) is in the index of a
// repository that has at least one commit.
func IsTracked(dir, path string) (bool, error) { return isTracked(dir, path) }
