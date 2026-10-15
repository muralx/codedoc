package query

import (
	"cmp"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/store"
)

// nearFiles is the most files with the name of a path that matched none
// whose symbols are offered.
const nearFiles = 5

// filePart is the file an address that matched nothing names, as
// splitTarget takes it, with what was asked of it: the name of a path
// form, the line of a line address, neither for a bare address that
// reads as a path. typos tells whether a path a typo away may stand for
// it.
type filePart struct {
	target, name string
	line         int
	typos        bool
}

// filePartOf returns the file part of an address; ok is false for an
// address without one (a name, a stable key).
func filePartOf(a Addr) (filePart, bool) {
	switch {
	case a.Kind == AddrLine:
		return filePart{target: a.Path, line: a.Line, typos: true}, true
	case a.Kind == AddrPath && a.Repo != "":
		return filePart{target: a.Repo + ":" + a.Path, name: a.Name, typos: true}, true
	case a.Kind == AddrPath:
		return filePart{target: a.Path, name: a.Name, typos: true}, true
	case a.Kind == AddrBare && strings.ContainsAny(a.Name, "/:"):
		return filePart{target: a.Name}, true
	}
	return filePart{}, false
}

// nearInFiles offers, for an address with a file part that named no
// symbol, what its file holds (efficiency spec section 4): the symbols
// of the file, those nearest to the name first (see closest), or for a
// line address and a bare path in line order. A path the index does not
// have stands for the indexed files it may mean (see pathsLike): the
// symbol at the line of the only such file, else their symbols, else
// the paths. A file that exists outside the index is ErrNotIndexed and
// a path that leaves the workspace ErrOutside. found is false when no
// path has anything to offer: the name alone is then looked up.
func (c *Ctx) nearInFiles(p filePart) (n near, found bool, err error) {
	if n, found, err := c.nearInDepFile(p); err != nil || found {
		return n, found, err
	}
	repos, rel, err := c.addrFile(p)
	if err != nil || len(repos) == 0 {
		return near{}, false, err
	}
	files, typo, err := c.pathsLike(repos, rel, false)
	if err != nil || len(files) == 0 || typo && !p.typos {
		return near{}, false, err
	}
	if typo || len(files) > nearFiles {
		return near{paths: c.pathEntries(files)}, true, nil
	}
	return c.nearInIndexed(files, p)
}

// nearInIndexed offers what the indexed files a file part stands for
// hold: their symbols by nearness to its name (see closest), else by
// its line (see nearLine).
func (c *Ctx) nearInIndexed(files []nearPath, p filePart) (n near, found bool, err error) {
	var syms []store.Symbol
	for _, f := range files {
		in, err := c.Store.SymbolsInFile(f.file.ID)
		if err != nil {
			return near{}, false, err
		}
		syms = append(syms, in...)
	}
	if p.name == "" {
		return c.nearLine(files, syms, p.line)
	}
	n.syms, n.one = closest(syms, p.name)
	return n, len(n.syms) > 0, nil
}

// nearInDepFile is nearInFiles for a path that names an indexed file
// of a dependency (see depFileAt): its symbols, those nearest to the
// name first.
func (c *Ctx) nearInDepFile(p filePart) (near, bool, error) {
	f, ok, err := c.depFileAt(p.target)
	if err != nil || !ok {
		return near{}, false, err
	}
	syms, err := c.view.s.SymbolsInFile(f.file.ID)
	if err != nil {
		return near{}, false, err
	}
	one := false
	if p.name != "" {
		syms, one = closest(syms, p.name)
	} else if syms, _ = fold(syms, true); p.line > 0 && len(syms) > 0 {
		c.notes = append(c.notes, noSymbolEncloses(p.line, f.file))
	}
	return near{deps: c.view.wrap(syms), one: one}, len(syms) > 0, nil
}

// noSymbolEncloses is the note of a line address in an indexed file
// that no symbol encloses, when the symbols of the file are the answer.
func noSymbolEncloses(line int, f store.File) string {
	return fmt.Sprintf("no symbol encloses line %d of %s (%d lines)", line, f.Path, f.Lines)
}

// nearLine offers the symbols of the files a line address or a bare
// path may mean: the innermost one enclosing the line when there is one
// file, else all of them in line order.
func (c *Ctx) nearLine(files []nearPath, syms []store.Symbol, line int) (near, bool, error) {
	if len(files) == 1 && line > 0 {
		sy, err := c.Store.SymbolByLocation(files[0].file.ID, line)
		if err == nil {
			return near{syms: []store.Symbol{sy}, one: true}, true, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return near{}, false, err
		}
	}
	listed, _ := fold(syms, true)
	if len(files) == 1 && line > 0 && len(listed) > 0 {
		c.notes = append(c.notes, noSymbolEncloses(line, files[0].file))
	}
	return near{syms: listed}, len(listed) > 0, nil
}

// addrFile returns the repos the file part of an address is looked up
// in and its path relative to them: the repo splitTarget finds, else,
// for a path without a repo outside every repo, all of them. A path
// that leaves the workspace is ErrOutside; one that exists on disk
// without a row in the index is ErrNotIndexed, saying why (see whyNot).
func (c *Ctx) addrFile(p filePart) ([]*config.Repo, string, error) {
	repos, rel := c.Workspace.Repos, path.Clean("./"+filepath.ToSlash(p.target))
	r, in, err := c.splitTarget(p.target)
	switch {
	case errors.Is(err, ErrOutside):
		return nil, "", err
	case err == nil:
		repos, rel = []*config.Repo{r}, in
	case filepath.IsAbs(p.target) || strings.Contains(p.target, ":"):
		return nil, "", nil
	case escapes(rel):
		return nil, "", outside(p.target)
	}
	for _, r := range repos {
		id, err := c.repoID(r)
		if err != nil {
			return nil, "", err
		}
		if _, err := c.Store.FileByPath(id, rel); !errors.Is(err, store.ErrNotFound) {
			if err != nil {
				return nil, "", err
			}
			continue
		}
		if err := c.notIndexed(r, rel); err != nil {
			return nil, "", err
		}
	}
	return repos, rel, nil
}

// closest orders the symbols of the files a path address names by how
// near their name is to the one asked for: the near ones, members
// included (equal ignoring case, then holding it or held by it, then
// sharing a word with it, then a typo away), then the others the
// outline of the file lists, in file order. one tells that the first is
// the only plausible reading: the only symbol with the name ignoring
// case, else the only one whose name holds it or is held by it.
func closest(syms []store.Symbol, want string) (out []store.Symbol, one bool) {
	want = want[strings.LastIndex(want, ".")+1:]
	tier := map[int64]int{}
	count := make([]int, nearNot+1)
	listed, _ := fold(syms, true)
	for _, sy := range listed {
		tier[sy.ID] = nearNot
	}
	for _, sy := range syms {
		t, _ := nearness(strings.ToLower(sy.Name), strings.ToLower(want))
		switch {
		case t == nearSuffix:
			t = nearPart
		case t > nearWord && sharesWord(sy.Name, want):
			t = nearWord
		}
		if t != nearNot {
			tier[sy.ID] = t
			count[t]++
		}
	}
	for _, sy := range syms {
		if _, ok := tier[sy.ID]; ok {
			out = append(out, sy)
		}
	}
	slices.SortStableFunc(out, func(a, b store.Symbol) int { return cmp.Compare(tier[a.ID], tier[b.ID]) })
	return out, count[nearEqual] == 1 || count[nearEqual] == 0 && count[nearPart] == 1
}
