// Package gitmeta reads repository metadata through the git CLI: branch,
// HEAD commit, dirty files, the file listing, and the last commit per file.
package gitmeta

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// ErrNotRepo reports that a directory is not inside a git working tree.
var ErrNotRepo = errors.New("gitmeta: not a git repository")

// HeadState is the checked-out branch and commit of a working tree.
type HeadState struct {
	// Branch is the short branch name, or "HEAD" when detached.
	Branch string
	// Commit is the full HEAD hash, empty before the first commit.
	Commit string
}

// Commit identifies a commit: the newest one that touched a file
// (LastCommits) or one entry of a history listing (LogRange,
// RecentCommits), which also fills Author and Subject.
type Commit struct {
	Commit string
	// At is the committer date as Unix seconds.
	At int64
	// Author is the author name; empty in LastCommits results.
	Author string
	// Subject is the first line of the message; empty in LastCommits results.
	Subject string
}

// run executes git in dir. Exit status 128 with a "not a git repository"
// message maps to ErrNotRepo; other failures carry git's stderr.
func run(dir string, args ...string) ([]byte, error) {
	return runInput(dir, "", args...)
}

// runInput is run with stdin. Every git command of codedoc goes through
// it, so paths print unquoted (core.quotePath=false) and no command
// takes a lock it can do without (--no-optional-locks): the daemon runs
// git status in the background, which would otherwise lock the index to
// refresh it and make a git command the user runs at that moment fail.
func runInput(dir, stdin string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"--no-optional-locks", "-c", "core.quotePath=false"}, args...)...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 128 && strings.Contains(stderr.String(), "not a git repository") {
		return nil, ErrNotRepo
	}
	return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
}

// Output runs git in dir and returns its stdout without trailing
// newlines; failures carry git's stderr (see run).
func Output(dir string, args ...string) (string, error) {
	out, err := run(dir, args...)
	return strings.TrimRight(string(out), "\n"), err
}

// Input runs git in dir with stdin, discarding its stdout.
func Input(dir, stdin string, args ...string) error {
	_, err := runInput(dir, stdin, args...)
	return err
}

// exitCode returns the process exit status carried by err, or -1.
func exitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// Head returns the branch and HEAD commit of the working tree at dir.
func Head(dir string) (HeadState, error) {
	if _, err := run(dir, "rev-parse", "--git-dir"); err != nil {
		return HeadState{}, err
	}
	var h HeadState
	out, err := run(dir, "symbolic-ref", "--short", "-q", "HEAD")
	switch {
	case err == nil:
		h.Branch = strings.TrimSpace(string(out))
	case exitCode(err) == 1:
		h.Branch = "HEAD"
	default:
		return HeadState{}, err
	}
	commit, err := resolveCommit(dir, "HEAD")
	h.Commit = commit
	return h, err
}

// resolveCommit returns the full hash of rev, or "" when rev does not name
// a commit (for example HEAD before the first commit).
func resolveCommit(dir, rev string) (string, error) {
	out, err := run(dir, "rev-parse", "-q", "--verify", rev+"^{commit}")
	if err != nil {
		if exitCode(err) == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// DirtyFiles returns the paths under dir with uncommitted changes,
// including untracked files that are not ignored, relative to dir (which
// may be a subdirectory of the working tree). A rename or copy, staged or
// in the working tree, reports the new path.
func DirtyFiles(dir string) ([]string, error) {
	prefix, err := run(dir, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	out, err := run(dir, "status", "--porcelain", "-z", "--untracked-files=all", "--", ".")
	if err != nil {
		return nil, err
	}
	return parseStatus(string(out), strings.TrimSpace(string(prefix))), nil
}

// StatusPaths lists every path `git status` reports in the working tree
// at top (its top directory), untracked files included and renames as
// their two sides: the old path deleted, the new one added.
func StatusPaths(top string) ([]string, error) {
	out, err := run(top, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if err != nil {
		return nil, err
	}
	return parseStatus(string(out), ""), nil
}

// parseStatus reads `git status --porcelain -z` output, whose paths are
// relative to the top of the working tree, and keeps the paths under
// prefix with prefix removed.
func parseStatus(out, prefix string) []string {
	var paths []string
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		if strings.ContainsAny(f[:2], "RC") {
			i++ // with -z the original path follows as its own field
		}
		if p, ok := strings.CutPrefix(f[3:], prefix); ok {
			paths = append(paths, p)
		}
	}
	return paths
}

// ListFiles returns tracked files plus untracked files that are not
// ignored, sorted.
func ListFiles(dir string) ([]string, error) {
	out, err := run(dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files, nil
}

// logMark starts each commit record in LastCommits' log output.
const logMark = "\x01"

// LastCommits maps each path under dir, relative to dir, touched in
// since..head to its newest commit. head is a commit already resolved by
// the caller (usually HeadState.Commit); an empty head means the repo has
// no commits yet. An empty since, or one that is not an ancestor of head
// (history was rewritten or another branch is checked out), reads the
// full history. When paths are given, only those paths are looked up.
func LastCommits(dir, head, since string, paths ...string) (map[string]Commit, error) {
	if head == "" {
		return map[string]Commit{}, nil
	}
	rng := head
	if since != "" {
		ok, err := isAncestor(dir, since, head)
		if err != nil {
			return nil, err
		}
		if ok {
			rng = since + ".." + head
		}
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	args := []string{"--literal-pathspecs", "log", "-z", "--relative", "--name-only",
		"--format=" + logMark + "%H %ct", rng, "--"}
	out, err := run(dir, append(args, paths...)...)
	if err != nil {
		return nil, err
	}
	return parseLog(string(out)), nil
}

// isAncestor reports whether commit is reachable from head; an unknown
// commit is not.
func isAncestor(dir, commit, head string) (bool, error) {
	_, err := run(dir, "merge-base", "--is-ancestor", commit, head)
	switch code := exitCode(err); {
	case err == nil:
		return true, nil
	case code == 1 || code == 128:
		return false, nil
	default:
		return false, err
	}
}

// parseLog reads `git log -z --name-only --format=<logMark>%H %ct` output,
// newest commit first, keeping the first commit seen for each path. Each
// record is NUL-separated: the commit line, then the file names, the
// first one preceded by a newline.
func parseLog(out string) map[string]Commit {
	m := map[string]Commit{}
	var cur Commit
	first := false
	for _, f := range strings.Split(out, "\x00") {
		if c, ok := parseCommitLine(f); ok {
			cur, first = c, true
			continue
		}
		if first {
			f, first = strings.TrimPrefix(f, "\n"), false
		}
		if _, seen := m[f]; f != "" && !seen {
			m[f] = cur
		}
	}
	return m
}

// parseCommitLine parses "<logMark><hash> <unix time>".
func parseCommitLine(f string) (Commit, bool) {
	rest, ok := strings.CutPrefix(f, logMark)
	if !ok {
		return Commit{}, false
	}
	hash, ts, ok := strings.Cut(rest, " ")
	at, err := strconv.ParseInt(ts, 10, 64)
	if !ok || err != nil || hash == "" || strings.Trim(hash, "0123456789abcdef") != "" {
		return Commit{}, false
	}
	return Commit{Commit: hash, At: at}, true
}
