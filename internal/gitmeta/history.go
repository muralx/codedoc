package gitmeta

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrNoPath reports that a path does not exist at a ref.
var ErrNoPath = errors.New("gitmeta: path does not exist at ref")

// ErrUnknownRef reports a ref that names no commit in the repository.
var ErrUnknownRef = errors.New("gitmeta: unknown ref")

// historyFormat prints one commit per line: hash, committer time, author
// and subject separated by NUL bytes.
const historyFormat = "--format=%H%x00%ct%x00%an%x00%s"

// LogRange returns the commits that changed lines start..end (1-based,
// inclusive) of path, relative to dir, newest first, following the lines
// through earlier edits (git log -L). limit caps the result; 0 means all.
func LogRange(dir, path string, start, end, limit int) ([]Commit, error) {
	if start < 1 || end < start {
		return nil, fmt.Errorf("gitmeta: invalid line range %d,%d", start, end)
	}
	args := []string{"log", "-s", historyFormat, fmt.Sprintf("-L%d,%d:%s", start, end, path)}
	if limit > 0 {
		args = append(args, fmt.Sprintf("-n%d", limit))
	}
	out, err := run(dir, args...)
	if err != nil {
		return nil, err
	}
	return parseHistory(string(out)), nil
}

// RecentCommits returns the newest n commits that touched dir (all of the
// repository when dir is its root), newest first. A repository without
// commits has none.
func RecentCommits(dir string, n int) ([]Commit, error) {
	head, err := resolveCommit(dir, "HEAD")
	if err != nil || head == "" {
		return nil, err
	}
	out, err := run(dir, "log", fmt.Sprintf("-n%d", n), historyFormat, head, "--", ".")
	if err != nil {
		return nil, err
	}
	return parseHistory(string(out)), nil
}

// parseHistory reads historyFormat lines; anything else (git log -L may
// print blank lines between records) is skipped.
func parseHistory(out string) []Commit {
	var commits []Commit
	for _, l := range strings.Split(out, "\n") {
		f := strings.SplitN(l, "\x00", 4)
		if len(f) != 4 {
			continue
		}
		at, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			continue
		}
		commits = append(commits, Commit{Commit: f[0], At: at, Author: f[2], Subject: f[3]})
	}
	return commits
}

// Show returns the content of path (relative to dir) at ref. It returns
// ErrNoPath when the ref exists but the path does not exist there.
// The ref may also name a tree, such as the empty tree ResolveSince
// returns.
func Show(dir, ref, path string) ([]byte, error) {
	commit, err := resolveCommit(dir, ref)
	if err == nil && commit == "" {
		commit, err = resolveTree(dir, ref)
	}
	if err != nil {
		return nil, err
	}
	if commit == "" {
		return nil, fmt.Errorf("gitmeta: unknown ref %q", ref)
	}
	object := commit + ":./" + path
	if _, err := run(dir, "cat-file", "-e", object); err != nil {
		if exitCode(err) == 128 || exitCode(err) == 1 {
			return nil, fmt.Errorf("%w: %s at %s", ErrNoPath, path, ref)
		}
		return nil, err
	}
	return run(dir, "cat-file", "blob", object)
}

// durationRE matches the --since durations: a count of hours, days or
// weeks.
var durationRE = regexp.MustCompile(`^([0-9]+)([hdw])$`)

// ResolveSince resolves a --since argument to a full commit hash. A
// duration (12h, 3d, 2w) names the newest commit on HEAD older than now
// minus the duration; when every commit is newer, it resolves to the empty
// tree, so diffs against it list every file as added. Anything else must
// name a commit.
func ResolveSince(dir, since string) (string, error) {
	if m := durationRE.FindStringSubmatch(since); m != nil {
		return commitBefore(dir, sinceCutoff(m[1], m[2]))
	}
	commit, err := resolveCommit(dir, since)
	if err != nil {
		return "", err
	}
	if commit == "" {
		return "", fmt.Errorf("%w %q", ErrUnknownRef, since)
	}
	return commit, nil
}

// sinceCutoff returns now minus count units (h, d or w).
func sinceCutoff(count, unit string) time.Time {
	n, _ := strconv.Atoi(count) // durationRE guarantees digits
	hours := map[string]int{"h": 1, "d": 24, "w": 24 * 7}[unit]
	return time.Now().Add(-time.Duration(n*hours) * time.Hour)
}

// commitBefore returns the newest commit reachable from HEAD whose
// committer date is not after cutoff, or the empty tree when there is none.
func commitBefore(dir string, cutoff time.Time) (string, error) {
	head, err := resolveCommit(dir, "HEAD")
	if err != nil {
		return "", err
	}
	if head != "" {
		out, err := run(dir, "rev-list", "-1", fmt.Sprintf("--before=@%d", cutoff.Unix()), head)
		if err != nil {
			return "", err
		}
		if commit := strings.TrimSpace(string(out)); commit != "" {
			return commit, nil
		}
	}
	out, err := run(dir, "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// resolveTree returns the full hash of the tree rev names, or "" when it
// names none.
func resolveTree(dir, rev string) (string, error) {
	out, err := run(dir, "rev-parse", "-q", "--verify", rev+"^{tree}")
	if err != nil {
		if exitCode(err) == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ChangedSince returns the paths under dir (relative to it) that differ
// between ref (a commit or tree) and the working tree, uncommitted
// changes included, plus untracked files that are not ignored. Renames
// list both the old and the new path. Paths come sorted and unique.
func ChangedSince(dir, ref string) ([]string, error) {
	diff, err := run(dir, "diff", "--name-only", "--no-renames", "--relative", "-z", ref, "--", ".")
	if err != nil {
		return nil, err
	}
	others, err := run(dir, "ls-files", "-z", "--others", "--exclude-standard", "--", ".")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(string(diff)+string(others), "\x00") {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}
