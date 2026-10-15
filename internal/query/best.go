package query

import (
	"cmp"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/store"
)

// best answers an ambiguous address (efficiency spec section 4): the
// best-ranked candidate is the target and the note names the others in
// one line, "also: A (path:line), B (path:line), +N more". Workspace
// symbols rank (see rankCandidates) before dependency symbols, which
// keep their package, path and line order.
func (c *Ctx) best(addr string, syms []store.Symbol, found []depSym) (target, error) {
	if err := c.rankCandidates(addr, syms); err != nil {
		return target{}, err
	}
	sortDeps(found)
	var t target
	var others []string
	for i, sy := range syms {
		if i == 0 {
			t.sym = sy
			continue
		}
		others = append(others, fmt.Sprintf("%s (%s)", candidateName(sy), c.candidateLoc(sy)))
	}
	for i := range found {
		if len(syms) == 0 && i == 0 {
			t.dep = &found[0]
			continue
		}
		d := found[i]
		others = append(others, fmt.Sprintf("%s (%s:%d %s)", candidateName(d.Symbol), d.Path, d.StartLine, d.pkg.tag()))
	}
	c.notes = append(c.notes, "also: "+shownNames(others, alsoShown))
	return t, nil
}

// candidateLoc is where a candidate of an ambiguous address is:
// path:line, with its repo in a workspace of several.
func (c *Ctx) candidateLoc(sy store.Symbol) string {
	loc := fmt.Sprintf("%s:%d", sy.Path, sy.StartLine)
	if name := c.repoNames()[sy.RepoID]; name != "" && len(c.Workspace.Repos) > 1 {
		return name + ":" + loc
	}
	return loc
}

// rankCandidates orders the candidates of an ambiguous address, best
// first: a file of the working directory, then of a directory below it
// (see cwdRank);
// production code before tests; a symbol whose whole qualified name is
// the address (a top-level Find before Store.Find); the most referenced
// (fan-in); the shortest path; then path and line.
func (c *Ctx) rankCandidates(addr string, syms []store.Symbol) error {
	ids := make([]int64, len(syms))
	for i, sy := range syms {
		ids[i] = sy.ID
	}
	fanIn, err := c.Store.FanIns(ids)
	if err != nil {
		return err
	}
	near := map[int64]int{}
	for _, sy := range syms {
		near[sy.ID] = c.cwdRank(sy)
	}
	slices.SortStableFunc(syms, func(a, b store.Symbol) int {
		return cmp.Or(
			cmp.Compare(near[a.ID], near[b.ID]),
			cmp.Compare(boolRank(isTestSymbol(a)), boolRank(isTestSymbol(b))),
			cmp.Compare(boolRank(!isNamed(a, addr)), boolRank(!isNamed(b, addr))),
			cmp.Compare(fanIn[b.ID], fanIn[a.ID]),
			cmp.Compare(len(a.Path), len(b.Path)),
			cmp.Compare(a.Path, b.Path),
			cmp.Compare(a.StartLine, b.StartLine))
	})
	return nil
}

// isNamed reports whether the whole qualified name of a symbol (a
// TypeScript one after its file prefix) is name.
func isNamed(sy store.Symbol, name string) bool {
	return sy.Qualified == name || strings.HasSuffix(sy.Qualified, "/"+name)
}

// cwdRank is 0 for a symbol whose file lies in the working directory, 1
// below it and 2 elsewhere. At the root of a repo every file of the repo
// is below it: the root says which repo is meant, not which file.
func (c *Ctx) cwdRank(sy store.Symbol) int {
	r, err := c.repoOf(sy)
	if err != nil || c.Cwd == "" {
		return 2
	}
	rel, ok := relWithin(c.Cwd, filepath.Join(r.Path, filepath.FromSlash(sy.Path)))
	switch {
	case !ok:
		return 2
	case path.Dir(rel) == "." && filepath.Clean(c.Cwd) != filepath.Clean(r.Path):
		return 0
	}
	return 1
}

// withNamesakes adds, to the symbols a bare name names in other repos
// only, the symbols of the repo the query runs in whose qualified name
// is the address ignoring case: the working directory says which repo
// is meant before the case of a letter does, so they are candidates
// too, and rank first (see rankCandidates). Symbols of that repo among
// syms, another form of address, or no such repo leave syms as they
// are.
func (c *Ctx) withNamesakes(addr string, syms []store.Symbol) ([]store.Symbol, error) {
	if len(syms) == 0 || c.Repo == nil || c.Filter || c.noNearest() || ParseAddr(addr).Kind != AddrBare {
		return syms, nil
	}
	home, err := c.repoID(c.Repo)
	if err != nil {
		return nil, err
	}
	if slices.ContainsFunc(syms, func(sy store.Symbol) bool { return sy.RepoID == home }) {
		return syms, nil
	}
	namesakes, err := lookup{s: c.Store, repoID: home}.namesakes(addr)
	return append(syms, namesakes...), err
}

// namesakes returns the symbols of the lookup whose qualified name is,
// or ends with, addr ignoring case.
func (l lookup) namesakes(addr string) ([]store.Symbol, error) {
	names, err := l.names()
	if err != nil {
		return nil, err
	}
	var out []store.Symbol
	last := addr[strings.LastIndex(addr, ".")+1:]
	for _, name := range names {
		if !strings.EqualFold(name, last) {
			continue
		}
		found, err := l.named(name, "")
		if err != nil {
			return nil, err
		}
		for _, sy := range found {
			if rankOf(sy, nearEqual, addr) <= rankEnding {
				out = append(out, sy)
			}
		}
	}
	return out, nil
}
