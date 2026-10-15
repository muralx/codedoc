package query

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/parse"
)

// stampLayout prints a time to the minute, in the local zone.
const stampLayout = "2006-01-02 15:04"

// lsKey is what a file or directory line sorts by.
type lsKey struct {
	name             string
	mtime, committed int64 // Unix nanoseconds, Unix seconds
	size             int64
	lines            int
}

// key is the sort key of a file. A file without a commit of its own
// (untracked, modified, or outside git) sorts by its modification time
// under --sort committed, since it is newer than anything committed.
func (row lsRow) key() lsKey {
	f := row.File
	k := lsKey{name: row.repo.Name + ":" + f.Path, mtime: f.Mtime, committed: f.LastCommitAt, size: f.Size, lines: f.Lines}
	if row.dirty || f.LastCommitAt == 0 {
		k.committed = f.Mtime / int64(time.Second)
	}
	return k
}

// sortBy orders items by the --sort key of each: times, sizes and line
// counts descending, names ascending, ties by name; --reverse inverts
// the order.
func sortBy[T any](items []T, o LsOpts, key func(T) lsKey) {
	slices.SortStableFunc(items, func(x, y T) int {
		a, b := key(x), key(y)
		var c int
		switch o.Sort {
		case "committed":
			c = cmp.Compare(b.committed, a.committed)
		case "size":
			c = cmp.Compare(b.size, a.size)
		case "lines":
			c = cmp.Compare(b.lines, a.lines)
		case "mtime":
			c = cmp.Compare(b.mtime, a.mtime)
		}
		if c == 0 {
			c = cmp.Compare(a.name, b.name)
		}
		if o.Reverse {
			return -c
		}
		return c
	})
}

// entry renders a file line: path, modification time and lines; with
// long also the date of its last commit when that differs by more than
// a minute, size, kind and language, symbol count, then its title. A
// file git reports uncommitted is tagged modified, or untracked when no
// commit holds it, and shows no commit date.
func (row lsRow) entry(long bool) output.Entry {
	f := row.File
	parts := []string{stamp(f.Mtime / int64(time.Second))}
	e := output.Entry{Loc: f.Path, RepoID: f.RepoID, Doc: f.Title}
	status := ""
	switch {
	case row.dirty && f.LastCommit == "":
		status = "untracked"
	case row.dirty:
		status = "modified"
	case f.LastCommitAt != 0 && abs(f.LastCommitAt-f.Mtime/int64(time.Second)) > 60:
		parts = append(parts, "committed "+stamp(f.LastCommitAt))
	}
	if status != "" {
		e.Tags = []string{status}
	}
	parts = append(parts, sizeText(f.Size), countText(f.Lines, "line"), kindLang(row))
	if n, unit := row.contents(); n > 0 {
		parts = append(parts, countText(n, unit))
	}
	e.Text = strings.Join(parts, "  ")
	if !long {
		e.Text = stamp(f.Mtime/int64(time.Second)) + "  " + countText(f.Lines, "line")
	}
	e.Extra = map[string]any{"mtime": f.Mtime / int64(time.Second), "committed_at": f.LastCommitAt,
		"commit": f.LastCommit, "size": f.Size, "lines": f.Lines, "kind": fileKind(f), "lang": f.Lang,
		"symbols": row.Symbols, "title": f.Title, "status": status}
	return e
}

// contents counts what a file holds: the sections of a document (its
// document symbol aside), else its symbols.
func (row lsRow) contents() (int, string) {
	if parse.IsDoc(row.Lang) {
		return row.Symbols - 1, "section"
	}
	return row.Symbols, "symbol"
}

// kindLang is the kind/language column: code/go, doc/markdown, other.
func kindLang(row lsRow) string {
	kind := fileKind(row.File)
	if row.Lang == "" {
		return kind
	}
	return kind + "/" + row.Lang
}

// stamp prints Unix seconds to the minute in the local zone.
func stamp(sec int64) string { return time.Unix(sec, 0).Local().Format(stampLayout) }

// countText is "1 line" or "12 lines".
func countText(n int, unit string) string {
	return fmt.Sprintf("%d %s", n, plural(n, unit, unit+"s"))
}

// sizeText prints a size in bytes with a binary unit: 812B, 1.9K, 3.4M.
func sizeText(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%dB", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1fK", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// dirLine sums the files below one directory.
type dirLine struct {
	lsKey
	loc    string
	repoID int64
	files  int
}

// dirEntriesOf renders one line per directory holding rows: the
// directory argument itself and every directory below it down to
// o.Depth levels, each with the count, total lines, total size and
// newest modification time of all the files below it.
func dirEntriesOf(rows []lsRow, o LsOpts) []output.Entry {
	byDir := map[string]*dirLine{}
	var order []*dirLine
	for _, row := range rows {
		k := row.key()
		for depth, dir := range ancestors(row.base, row.Path) {
			if o.Depth > 0 && depth > o.Depth {
				break
			}
			name := row.repo.Name + ":" + dir
			d := byDir[name]
			if d == nil {
				d = &dirLine{lsKey: lsKey{name: name}, loc: dir + "/", repoID: row.RepoID}
				byDir[name] = d
				order = append(order, d)
			}
			d.files++
			d.lines, d.size = d.lines+k.lines, d.size+k.size
			d.mtime, d.committed = max(d.mtime, k.mtime), max(d.committed, k.committed)
		}
	}
	sortBy(order, o, func(d *dirLine) lsKey { return d.lsKey })
	out := make([]output.Entry, len(order))
	for i, d := range order {
		out[i] = output.Entry{Loc: d.loc, RepoID: d.repoID, Text: strings.Join([]string{countText(d.files, "file"),
			stamp(d.mtime / int64(time.Second)), countText(d.lines, "line"), sizeText(d.size)}, "  "),
			Extra: map[string]any{"files": d.files, "lines": d.lines, "size": d.size, "mtime": d.mtime / int64(time.Second)}}
	}
	return out
}

// ancestors lists the directories of a file from base down to its own:
// base, then one more path segment at a time ("." is the repo root). A
// file that is itself the argument has none.
func ancestors(base, p string) []string {
	rel := p
	if base != "." {
		if len(p) <= len(base) {
			return nil
		}
		rel = p[len(base)+1:]
	}
	out := []string{base}
	dir := base
	segs := strings.Split(rel, "/")
	for _, seg := range segs[:len(segs)-1] {
		if dir == "." {
			dir = seg
		} else {
			dir += "/" + seg
		}
		out = append(out, dir)
	}
	return out
}
