package query

import (
	"cmp"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// nearPath is an indexed file, or a directory holding indexed files, a
// path argument may mean.
type nearPath struct {
	repo *config.Repo
	path string     // relative to the repo
	file store.File // of a file
	dir  bool
	d    int // the edit distance of a typo
}

// addr is the address that names the path from any directory.
func (p nearPath) addr() string { return p.repo.Name + ":" + p.path }

// pathTypos is the most edits a path is offered for another: a longer
// path does not make a wrong letter more likely.
const pathTypos = 2

// pathsLike returns the indexed paths of repos a path may mean, by the
// first of these that finds any: the path itself; the paths that end
// with it after a slash; those with its base name, ignoring case; and,
// with typo set, those within typoLimit edits (pathTypos at most),
// nearest first. dirs adds
// the directories of the indexed files to the paths compared. When
// several are found and one lies in the working directory, it is the
// one meant.
func (c *Ctx) pathsLike(repos []*config.Repo, rel string, dirs bool) (found []nearPath, typo bool, err error) {
	all, err := c.indexedPaths(repos, dirs)
	if err != nil {
		return nil, false, err
	}
	base := strings.ToLower(path.Base(rel))
	steps := []func(p nearPath) bool{
		func(p nearPath) bool { return p.path == rel },
		func(p nearPath) bool { return strings.HasSuffix(p.path, "/"+rel) },
		func(p nearPath) bool { return strings.ToLower(path.Base(p.path)) == base },
	}
	for _, match := range steps {
		for _, p := range all {
			if match(p) {
				found = append(found, p)
			}
		}
		if len(found) > 0 {
			return c.inCwd(found), false, nil
		}
	}
	limit := min(typoLimit(len(rel)), pathTypos)
	for _, p := range all {
		if p.d = editDistance(strings.ToLower(p.path), strings.ToLower(rel), limit); p.d <= limit {
			found = append(found, p)
		}
	}
	slices.SortStableFunc(found, func(a, b nearPath) int { return cmp.Compare(a.d, b.d) })
	return found, true, nil
}

// indexedPaths lists the indexed files of repos in path order, and with
// dirs, before the files of each repo, the directories that hold them.
func (c *Ctx) indexedPaths(repos []*config.Repo, dirs bool) ([]nearPath, error) {
	var out []nearPath
	for _, r := range repos {
		id, err := c.repoID(r)
		if err != nil {
			return nil, err
		}
		files, err := c.Store.FilesByRepo(id)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{".": true}
		for _, f := range files {
			for dir := path.Dir(f.Path); dirs && !seen[dir]; dir = path.Dir(dir) {
				seen[dir] = true
				out = append(out, nearPath{repo: r, path: dir, dir: true})
			}
		}
		for _, f := range files {
			out = append(out, nearPath{repo: r, path: f.Path, file: f})
		}
	}
	return out, nil
}

// inCwd keeps, of several paths, the only one below the working
// directory when there is exactly one.
func (c *Ctx) inCwd(found []nearPath) []nearPath {
	if len(found) < 2 || c.Cwd == "" {
		return found
	}
	var below []nearPath
	for _, p := range found {
		if _, ok := relWithin(c.Cwd, filepath.Join(p.repo.Path, filepath.FromSlash(p.path))); ok {
			below = append(below, p)
		}
	}
	if len(below) == 1 {
		return below
	}
	return found
}

// pathEntries lists paths as the candidates of a path that names none:
// a file with its line count, a directory with a trailing slash.
func (c *Ctx) pathEntries(paths []nearPath) []output.Entry {
	out := make([]output.Entry, 0, min(len(paths), nearestCap))
	for _, p := range paths[:min(len(paths), nearestCap)] {
		e := output.Entry{Loc: p.path, Text: fmt.Sprintf("%d %s", p.file.Lines, plural(p.file.Lines, "line", "lines")),
			RepoID: p.file.RepoID, Extra: map[string]any{"kind": "file", "lines": p.file.Lines}}
		if p.dir {
			e.Loc, e.Text, e.Extra = p.path+"/", "directory", map[string]any{"kind": "directory"}
			e.RepoID, _ = c.repoID(p.repo)
		}
		out = append(out, e)
	}
	return out
}

// missingPath answers a path argument of cmd (outline, lines, ls) that
// names nothing in the index of r (efficiency spec section 4). The only
// path it may mean (see pathsLike) is answered by again and noted
// ("answered for P"); several are the answer themselves, returned with
// ErrNearest; a path that exists outside the index is ErrNotIndexed
// with the reason; with nothing to offer, or when resolution is exact,
// it is ErrNotFound with the response that says so.
func (c *Ctx) missingPath(cmd, target string, r *config.Repo, rel string, dirs bool,
	again func(addr string) (*output.Response, error)) (*output.Response, error) {
	repos := []*config.Repo{r}
	missing := fmt.Errorf("%s %s: %w: no such file%s in %s", cmd, target, ErrNotFound, orDirectory(dirs), r.Name)
	if c.noNearest() {
		return c.respond(repos), missing
	}
	if err := c.notIndexed(r, rel); err != nil {
		return nil, err
	}
	found, typo, err := c.pathsLike(repos, rel, dirs)
	switch {
	case err != nil:
		return nil, err
	case len(found) == 0:
		return nil, missing
	case len(found) == 1 && !typo:
		c.notes = append(c.notes, "answered for "+found[0].path)
		return again(found[0].addr())
	}
	return c.respond(repos, c.section(nearestTitle, c.pathEntries(found))),
		fmt.Errorf("%s %s: %w", cmd, target, ErrNearest)
}

// orDirectory completes "no such file" for a command that takes
// directories too.
func orDirectory(dirs bool) string {
	if dirs {
		return " or directory"
	}
	return ""
}
