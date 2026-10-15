package index

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/exclude"
	"github.com/muralx/codedoc/internal/store"
)

// chunkFiles is how many paths Paths and EnsureContext write per
// transaction-and-resolution round; ctx is checked between rounds.
var chunkFiles = 5000

// Paths re-indexes the repo-relative paths a file watcher reported (see
// Files). A path names a file or a directory, a directory standing for
// every file below it: a file that exists is re-extracted unless an
// exclude glob matches it, and a path that no longer exists loses its
// row together with the rows of every file below it. Directories that
// still exist are checked for stored files that disappeared from them.
// The work goes in rounds of at most chunkFiles paths, each written and
// resolved in full, and stops between rounds once ctx ends, returning
// its error: the rest is found by the next freshness pass.
func Paths(ctx context.Context, s *store.Store, r *config.Repo, opts Options, paths []string) error {
	row, err := s.RepoByPath(r.Path)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	rows, err := s.FilesByRepo(row.ID)
	if err != nil {
		return err
	}
	stored := sortedPaths(rows)
	m := exclude.New(r.Path, opts.Exclude)
	set := map[string]bool{}
	for _, p := range paths {
		p = path.Clean(p)
		info, err := os.Lstat(filepath.Join(r.Path, filepath.FromSlash(p)))
		switch {
		case err == nil && info.IsDir(), errors.Is(err, os.ErrNotExist):
			addBelow(set, stored, p)
			if err != nil {
				set[p] = true
			}
		case err != nil:
			return err
		case !m.Path(p, false):
			set[p] = true
		}
	}
	list := make([]string, 0, len(set))
	for p := range set {
		list = append(list, p)
	}
	sort.Strings(list)
	return filesChunked(ctx, s, r, opts, list)
}

// filesChunked runs Files over paths in rounds of chunkFiles, checking
// ctx before each. Only the last round resolves: the rounds before leave
// their resolution pending (see savePending), so it runs once over what
// they all touched; a run stopped between rounds leaves it to the next
// Ensure, Files or index run.
func filesChunked(ctx context.Context, s *store.Store, r *config.Repo, opts Options, paths []string) error {
	for len(paths) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(len(paths), chunkFiles)
		sy, err := filesLocked(s, r, opts, paths[:n], n == len(paths))
		if err != nil {
			return err
		}
		if err := embedTouched(s, opts, sy); err != nil {
			return err
		}
		paths = paths[n:]
	}
	return nil
}

// sortedPaths returns the stored paths, sorted.
func sortedPaths(rows []store.File) []string {
	out := make([]string, len(rows))
	for i, f := range rows {
		out[i] = f.Path
	}
	sort.Strings(out)
	return out
}

// addBelow adds the stored paths under dir ("." for the whole repo),
// found by binary search in the sorted stored paths.
func addBelow(set map[string]bool, stored []string, dir string) {
	if dir == "." {
		for _, p := range stored {
			set[p] = true
		}
		return
	}
	prefix := dir + "/"
	for i := sort.SearchStrings(stored, prefix); i < len(stored) && strings.HasPrefix(stored[i], prefix); i++ {
		set[stored[i]] = true
	}
}
