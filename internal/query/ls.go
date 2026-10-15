package query

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/docs"
	"github.com/muralx/codedoc/internal/extract/cfg"
	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// LsOpts sort and filter a file listing (documents spec section 3).
type LsOpts struct {
	// Sort is mtime (the default), committed, name, size or lines:
	// newest or largest first, names ascending.
	Sort string
	// Reverse inverts the order.
	Reverse bool
	// Since and Until bound the modification time: a duration back from
	// now (2d, 3h), a date, or a git ref (the date of its commit).
	Since, Until string
	// Kinds keeps the files of these kinds: code, doc, config, test, other.
	Kinds []string
	// Lang keeps the files of a language, family or config format.
	Lang string
	// Depth limits how deep below a directory argument files (or with
	// Dirs, directories) are listed; 0 is unlimited.
	Depth int
	// Dirs prints one line per directory instead of one per file.
	Dirs bool
	// Long adds to each file line its size, kind and language, symbol
	// count, title, the date of its last commit and whether git reports
	// it modified or untracked; without it a line is the path, the
	// modification time and the line count.
	Long bool
}

// LsSorts and LsKinds are the values --sort and --kind accept.
var (
	LsSorts = []string{"mtime", "committed", "name", "size", "lines"}
	LsKinds = []string{"code", "doc", "config", "test", "other"}
)

// lsTarget is one argument of ls resolved to a repo: the files under a
// directory ("." for the root), one file, or the files a glob matches
// (dir is then the root).
type lsTarget struct {
	repo *config.Repo
	dir  string
	glob string
}

// lsRow is one listed file.
type lsRow struct {
	store.FileCount
	repo *config.Repo
	// base is the directory argument the file was listed under.
	base string
	// dirty tells that git reports the file uncommitted.
	dirty bool
}

// Ls lists indexed files with their modification time and line count,
// and with Long their size, kind, symbol count and title: what `ls -lt`,
// `find -newer` and `tree` are used for. The files of one directory are
// listed together, the directories in the order of their first file. Arguments are paths (repo:path, or as outline takes
// them) or globs; without one it lists the working directory when that
// lies in a repo, else every repo in scope. Files that discovery leaves
// out are counted in the note, by reason. A path that names no indexed
// file is not found.
func Ls(c *Ctx, args []string, o LsOpts) (*output.Response, error) {
	if err := checkLsOpts(&o); err != nil {
		return nil, err
	}
	targets, err := c.lsTargets(args)
	if err != nil {
		return nil, err
	}
	repos := targetRepos(targets)
	if err := c.EnsureFresh(repos...); err != nil {
		return nil, err
	}
	var rows []lsRow
	for _, t := range targets {
		more, err := c.lsRows(t, o)
		if err != nil {
			return nil, err
		}
		rows = append(rows, more...)
	}
	title := strings.TrimSpace("ls " + strings.Join(args, " "))
	if o.Dirs {
		return c.respond(repos, c.section(title, dirEntriesOf(rows, o))), nil
	}
	sortBy(rows, o, lsRow.key)
	rows = byDirectory(rows)
	entries := make([]output.Entry, len(rows))
	for i, row := range rows {
		entries[i] = row.entry(o.Long)
	}
	sec := c.section(title, entries)
	sec.Dirs, sec.AllTags = true, o.Long
	if !o.Long {
		sec.Hide = output.HideDoc
	}
	return c.respond(repos, sec), nil
}

// byDirectory puts the rows of each directory next to one another, the
// directories in the order of their first row and the rows of one in
// the order they had.
func byDirectory(rows []lsRow) []lsRow {
	at := map[string]int{}
	var groups [][]lsRow
	for _, row := range rows {
		dir := row.repo.Name + ":" + path.Dir(row.Path)
		i, ok := at[dir]
		if !ok {
			i = len(groups)
			at[dir], groups = i, append(groups, nil)
		}
		groups[i] = append(groups[i], row)
	}
	return slices.Concat(groups...)
}

// checkLsOpts validates the options and applies the default sort.
func checkLsOpts(o *LsOpts) error {
	if o.Sort == "" {
		o.Sort = "mtime"
	}
	if !slices.Contains(LsSorts, o.Sort) {
		return fmt.Errorf("unknown --sort %q; valid: %s", o.Sort, strings.Join(LsSorts, ", "))
	}
	for _, k := range o.Kinds {
		if !slices.Contains(LsKinds, k) {
			return fmt.Errorf("unknown --kind %q; valid: %s", k, strings.Join(LsKinds, ", "))
		}
	}
	if o.Depth < 0 {
		return errors.New("--depth must not be negative")
	}
	return nil
}

// lsTargets resolves the arguments of ls; see Ls for the default.
func (c *Ctx) lsTargets(args []string) ([]lsTarget, error) {
	if len(args) == 0 {
		if !c.Filter && c.Repo != nil && c.Cwd != "" {
			if rel, ok := relWithin(c.Repo.Path, c.Cwd); ok {
				return []lsTarget{{repo: c.Repo, dir: rel}}, nil
			}
		}
		var out []lsTarget
		for _, r := range c.scope() {
			out = append(out, lsTarget{repo: r, dir: "."})
		}
		return out, nil
	}
	var out []lsTarget
	for _, arg := range args {
		t, err := c.lsTarget(arg)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// lsTarget resolves one argument. A glob is relative to the named repo
// (repo:glob), else to the working directory inside the current repo,
// else to the current repo's root.
func (c *Ctx) lsTarget(arg string) (lsTarget, error) {
	if !strings.ContainsAny(arg, "*?[") {
		r, p, err := c.splitTarget(arg)
		if errors.Is(err, ErrOutside) {
			return lsTarget{}, err
		}
		if err != nil {
			return lsTarget{}, fmt.Errorf("ls %s: not inside a repo; write repo:path", arg)
		}
		return lsTarget{repo: r, dir: p}, nil
	}
	if repo, glob, ok := strings.Cut(arg, ":"); ok {
		r, err := c.repoNamed(repo)
		return lsTarget{repo: r, dir: ".", glob: glob}, err
	}
	if c.Repo == nil {
		return lsTarget{}, fmt.Errorf("ls %s: not inside a repo; write repo:glob", arg)
	}
	glob := filepath.ToSlash(arg)
	if rel, ok := relWithin(c.Repo.Path, c.Cwd); ok && !c.Filter && c.Cwd != "" && rel != "." {
		glob = path.Join(rel, glob)
	}
	return lsTarget{repo: c.Repo, dir: ".", glob: glob}, nil
}

// targetRepos returns the distinct repos of targets, in order.
func targetRepos(targets []lsTarget) []*config.Repo {
	var out []*config.Repo
	for _, t := range targets {
		if !slices.Contains(out, t.repo) {
			out = append(out, t.repo)
		}
	}
	return out
}

// nothingUnder is the error of a target without any indexed file, one
// line either way: a path that exists outside the index says why (see
// notIndexed), any other is not found, with the command that lists what
// the repo has.
func (c *Ctx) nothingUnder(t lsTarget) error {
	if t.glob == "" && !c.noNearest() {
		if err := c.notIndexed(t.repo, t.dir); err != nil {
			return err
		}
	}
	return fmt.Errorf("ls %s: %w: no indexed file under it; codedoc ls --repo %s lists the indexed files",
		t.name(), ErrNotFound, t.repo.Name)
}

// lsRows loads the indexed files of a target, notes the files hidden
// under a directory target, and keeps the rows the options select. A
// target without any indexed file is not found.
func (c *Ctx) lsRows(t lsTarget, o LsOpts) ([]lsRow, error) {
	id, err := c.repoID(t.repo)
	if err != nil {
		return nil, err
	}
	files, err := c.Store.SymbolCountsByFile(id, "")
	if err != nil {
		return nil, err
	}
	var under []store.FileCount
	for _, f := range files {
		if t.holds(f.Path) {
			under = append(under, f)
		}
	}
	if len(under) == 0 {
		return nil, c.nothingUnder(t)
	}
	if err := c.noteUnlisted(t, under); err != nil {
		return nil, err
	}
	since, until, err := c.lsWindow(t.repo, o)
	if err != nil {
		return nil, err
	}
	dirty := dirtySet(t.repo)
	var rows []lsRow
	for _, f := range under {
		row := lsRow{FileCount: f, repo: t.repo, base: t.dir, dirty: dirty[f.Path]}
		if row.selected(t, o, since, until) {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// name is the target as an argument names it.
func (t lsTarget) name() string {
	if t.glob != "" {
		return t.glob
	}
	return t.dir
}

// holds reports whether the target covers a repo-relative path.
func (t lsTarget) holds(p string) bool {
	if t.glob != "" {
		ok, _ := doublestar.Match(t.glob, p)
		return ok
	}
	return t.dir == "." || p == t.dir || strings.HasPrefix(p, t.dir+"/")
}

// depth is how deep a path lies below the target's directory: 1 for a
// file directly in it; 0 for a glob target, which has no depth.
func (t lsTarget) depth(p string) int {
	if t.glob != "" || p == t.dir {
		return 0
	}
	rel := p
	if t.dir != "." {
		rel = p[len(t.dir)+1:]
	}
	return strings.Count(rel, "/") + 1
}

// selected applies the depth (file listings only), kind, language and
// time filters to a row.
func (row lsRow) selected(t lsTarget, o LsOpts, since, until time.Time) bool {
	f := row.File
	if !o.Dirs && o.Depth > 0 && t.depth(f.Path) > o.Depth {
		return false
	}
	if len(o.Kinds) > 0 && !slices.Contains(o.Kinds, fileKind(f)) {
		return false
	}
	if o.Lang != "" && f.Lang != o.Lang && parse.Family(f.Lang) != o.Lang {
		return false
	}
	mtime := time.Unix(0, f.Mtime)
	return (since.IsZero() || !mtime.Before(since)) && (until.IsZero() || !mtime.After(until))
}

// fileKind classifies a file for --kind: doc (a document), config (a
// config format), test (code under the test conventions of
// docs.IsTestPath), code, or other (no language).
func fileKind(f store.File) string {
	switch {
	case parse.IsDoc(f.Lang):
		return "doc"
	case cfg.For(f.Lang) != nil:
		return "config"
	case parse.Family(f.Lang) == "":
		return "other"
	case docs.IsTestPath(f.Path):
		return "test"
	}
	return "code"
}

// dirtySet returns the uncommitted paths of a repo; none outside git.
func dirtySet(r *config.Repo) map[string]bool {
	paths, err := gitmeta.DirtyFiles(r.Path)
	if err != nil {
		return nil
	}
	out := make(map[string]bool, len(paths))
	for _, p := range paths {
		out[p] = true
	}
	return out
}
