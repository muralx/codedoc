package query

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/exclude"
	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/store"
)

// lsDuration matches a duration back from now: minutes, hours, days or
// weeks.
var lsDuration = regexp.MustCompile(`^([0-9]+)([mhdw])$`)

// dateLayouts are the date forms --since and --until accept, read in
// the local zone.
var dateLayouts = []string{"2006-01-02", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02T15:04:05"}

// lsWindow resolves --since and --until for one repo; a zero time is no
// bound.
func (c *Ctx) lsWindow(r *config.Repo, o LsOpts) (since, until time.Time, err error) {
	if since, err = c.when(r, "--since", o.Since); err != nil {
		return since, until, err
	}
	until, err = c.when(r, "--until", o.Until)
	return since, until, err
}

// when resolves a time bound: a duration back from now (30m, 3h, 2d,
// 1w), a date (2026-10-09, optionally with a time), or a git ref, which
// stands for the date of its commit in the repo.
func (c *Ctx) when(r *config.Repo, flag, v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	if t, ok := TimeBound(v, c.now()); ok {
		return t, nil
	}
	at, err := gitmeta.CommitTime(r.Path, v)
	if errors.Is(err, gitmeta.ErrNotRepo) || errors.Is(err, gitmeta.ErrUnknownRef) {
		return time.Time{}, fmt.Errorf("%s %q: not a duration (2d, 3h), a date (2026-10-09) or a git ref of %s", flag, v, r.Name)
	}
	return time.Unix(at, 0), err
}

// Reasons a file under a listed directory is not indexed, in the order
// the note names them.
const (
	hiddenIgnored  = "ignored by git"
	hiddenBuiltin  = "built-in exclusion"
	hiddenExcluded = "workspace exclude"
	hiddenOther    = "not indexable (binary, over 2 MB or language not selected)"
)

// noteUnlisted notes, in one line, how many files under a directory
// target discovery left out and why (documents spec section 3). A
// directory git ignores as a whole counts once. Outside git, and for a
// glob or a single file, nothing is noted.
func (c *Ctx) noteUnlisted(t lsTarget, indexed []store.FileCount) error {
	if t.glob != "" || (len(indexed) == 1 && indexed[0].Path == t.dir) {
		return nil
	}
	listed, err := gitmeta.ListFiles(t.repo.Path)
	if errors.Is(err, gitmeta.ErrNotRepo) {
		return nil
	}
	if err != nil {
		return err
	}
	counts := c.countUnlisted(t, listed, indexed)
	ignored, err := gitmeta.IgnoredPaths(t.repo.Path, t.dir)
	if err != nil {
		return err
	}
	counts[hiddenIgnored] = len(ignored)
	if line := hiddenLine(t, counts, ignored); line != "" {
		c.notes = append(c.notes, line)
	}
	return nil
}

// countUnlisted counts, by reason, the files git lists under a target
// that are not among the indexed ones.
func (c *Ctx) countUnlisted(t lsTarget, listed []string, indexed []store.FileCount) map[string]int {
	known := make(map[string]bool, len(indexed))
	for _, f := range indexed {
		known[f.Path] = true
	}
	counts := map[string]int{}
	builtin, all := exclude.New(t.repo.Path, nil), exclude.New(t.repo.Path, c.Workspace.Exclude)
	for _, p := range listed {
		if !t.holds(p) || known[p] {
			continue
		}
		switch {
		case builtin.Path(p, false):
			counts[hiddenBuiltin]++
		case all.Path(p, false):
			counts[hiddenExcluded]++
		case isFile(t.repo.Path, p):
			counts[hiddenOther]++
		}
	}
	return counts
}

// isFile reports whether a repo-relative path is a regular file on
// disk: git lists a tracked file that was deleted, which hides nothing.
func isFile(root, rel string) bool {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && info.Mode().IsRegular()
}

// hiddenLine renders the counts by reason; "" when nothing is hidden.
func hiddenLine(t lsTarget, counts map[string]int, ignored []string) string {
	var parts []string
	for _, reason := range []string{hiddenIgnored, hiddenBuiltin, hiddenExcluded, hiddenOther} {
		n := counts[reason]
		if n == 0 {
			continue
		}
		part := fmt.Sprintf("%d %s", n, reason)
		if d := countDirs(ignored); reason == hiddenIgnored && d > 0 {
			part += fmt.Sprintf(" (%d whole %s)", d, plural(d, "directory", "directories"))
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("hidden under %s:%s: %s", t.repo.Name, t.dir, strings.Join(parts, ", "))
}

// countDirs counts the entries that are directories (they end in '/').
func countDirs(paths []string) int {
	n := 0
	for _, p := range paths {
		if strings.HasSuffix(p, "/") {
			n++
		}
	}
	return n
}
