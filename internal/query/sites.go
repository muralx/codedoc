package query

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// staleTag marks an entry whose file changed since it was indexed: its
// lines could not be read, the signature stands in for them.
const staleTag = "stale"

// Context says how many lines of the file an answer shows around each
// fragment (a call site, a matched line), as grep's -B, -A and -C do.
type Context struct {
	// Before and After are the lines shown before and after a fragment.
	Before, After int
	// Symbol widens every fragment to its enclosing symbol or section.
	Symbol bool
	// File lifts the rule that a fragment never crosses its enclosing
	// symbol or section.
	File bool
	// Set is true when a flag chose the context: an answer with a default
	// of its own (search shows two lines around) applies it otherwise.
	Set bool
}

// ParseContext reads the value of --context (a number of lines, symbol
// or file; empty for none) with the values of -B and -A, which win over
// the number when they are set (negative means unset).
func ParseContext(value string, before, after int) (Context, error) {
	x := Context{Set: value != "" || before >= 0 || after >= 0}
	switch value {
	case "":
	case "symbol":
		x.Symbol = true
	case "file":
		x.File = true
	default:
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return x, fmt.Errorf("--context %q: want a number of lines, symbol or file", value)
		}
		x.Before, x.After = n, n
	}
	if before >= 0 {
		x.Before = before
	}
	if after >= 0 {
		x.After = after
	}
	return x, nil
}

// wide reports whether a fragment is more than its own line.
func (x Context) wide() bool { return x.Before > 0 || x.After > 0 || x.Symbol }

// around returns the lines the context shows for a fragment at line of
// a file of total lines whose enclosing symbol spans from..to.
func (x Context) around(line, from, to, total int) (first, last int) {
	first, last = line-x.Before, line+x.After
	if x.Symbol {
		first, last = min(from, first), max(to, last)
	}
	if !x.File && from <= line && line <= to {
		first, last = max(first, from), min(last, to)
	}
	return max(first, 1), min(last, total)
}

// fileLines returns the lines of an indexed file of the workspace read
// from disk, once per context. ok is false when its repo is stale or
// its size, modification time or content no longer match its row: what
// the index says about its lines may then be off.
func (c *Ctx) fileLines(repoID, fileID int64, path string) (lines []string, ok bool) {
	if l, done := c.files[fileID]; done {
		return l, l != nil
	}
	if c.files == nil {
		c.files = map[int64][]string{}
	}
	c.files[fileID] = nil
	r, err := c.repoNamed(c.repoNames()[repoID])
	if err != nil || !c.Fresh[r.Name].Fresh {
		return nil, false
	}
	f, err := c.Store.FileByID(fileID)
	if err != nil {
		return nil, false
	}
	p := filepath.Join(r.Path, filepath.FromSlash(path))
	info, err := os.Stat(p)
	if err != nil || info.Size() != f.Size || info.ModTime().UnixNano() != f.Mtime {
		return nil, false
	}
	src, err := os.ReadFile(p)
	if err != nil || index.ContentHash(src) != f.Hash {
		return nil, false
	}
	c.files[fileID] = append([]string{}, splitLines(string(src))...)
	return c.files[fileID], true
}

// site makes e, the entry of an edge at a line of the file of the
// symbol in, a call site: its fragment is that line of the file with
// the context lines around it, which text prints in place of the
// signature and the doc. When the file no longer matches the index the
// signature stays and the entry is tagged stale.
func (c *Ctx) site(e *output.Entry, in store.Symbol, line int) {
	if !c.addSite(e, in, line) {
		e.Tags = append(e.Tags, staleTag)
		e.Hide |= output.HideDoc
	}
}

// addSite adds the line of a call in the symbol in, with its context,
// to the fragments of e, kept in line order and joined where they
// overlap or touch; false when the file no longer matches the index.
func (c *Ctx) addSite(e *output.Entry, in store.Symbol, line int) bool {
	lines, ok := c.fileLines(in.RepoID, in.FileID, in.Path)
	if !ok || line < 1 || line > len(lines) {
		return false
	}
	first, last := c.Context.around(line, in.StartLine, in.EndLine, len(lines))
	spans := []lineSpan{{first, last}}
	for _, f := range e.Fragments {
		spans = append(spans, lineSpan{f.Start, f.End})
	}
	e.Site, e.Fragments = true, nil
	for _, sp := range joinSpans(spans) {
		e.Fragments = append(e.Fragments, output.Fragment{Start: sp.first, End: sp.last,
			Text: strings.Join(lines[sp.first-1:sp.last], "\n") + "\n"})
	}
	return true
}

// mergeSites joins each call site with the one before it when both are
// in the same symbol and their fragments overlap or touch, so lines
// shown with context are printed once. Sites of one line are left as
// they are.
func (c *Ctx) mergeSites(entries []output.Entry) []output.Entry {
	if !c.Context.wide() {
		return entries
	}
	var out []output.Entry
	for _, e := range entries {
		if n := len(out); n > 0 && joins(out[n-1], e) {
			prev, next := &out[n-1].Fragments[0], e.Fragments[0]
			if next.End > prev.End {
				tail := strings.SplitAfter(next.Text, "\n")[prev.End+1-next.Start:]
				prev.Text, prev.End = prev.Text+strings.Join(tail, ""), next.End
			}
			continue
		}
		out = append(out, e)
	}
	return out
}

// joins reports whether call site b continues call site a: the same
// symbol at the same depth of the same file, each with one fragment,
// that of b starting no later than the line after the one of a.
func joins(a, b output.Entry) bool {
	if !a.Site || !b.Site || a.Key == "" || a.Key != b.Key || a.Depth != b.Depth || a.Mark != b.Mark ||
		a.RepoID != b.RepoID || locPath(a.Loc) != locPath(b.Loc) || len(a.Fragments) != 1 || len(b.Fragments) != 1 {
		return false
	}
	fa, fb := a.Fragments[0], b.Fragments[0]
	return fb.Start >= fa.Start && fb.Start <= fa.End+1
}

// locPath is the path of a path:line location.
func locPath(loc string) string {
	if i := strings.LastIndex(loc, ":"); i >= 0 {
		return loc[:i]
	}
	return loc
}
