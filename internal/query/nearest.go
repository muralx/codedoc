package query

import (
	"errors"
	"fmt"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/stablekey"
	"github.com/muralx/codedoc/internal/store"
)

// ErrNearest reports an address that matched nothing and was answered
// with the nearest candidates (efficiency spec section 4): the response
// returned with it is the answer and the command succeeds. It is an
// ErrNotFound, so a caller that tries something else for an address no
// symbol has still does.
var ErrNearest = fmt.Errorf("%w: answered with the nearest candidates", ErrNotFound)

// ErrNotIndexed reports a path that exists but is outside the index;
// the error says why in one line.
var ErrNotIndexed = errors.New("is not indexed")

// nearestTitle titles the candidates an address that matched nothing is
// answered with; nearestCap is the most of them, unusedShown the most
// dependencies named for not having been searched, alsoShown the most
// other candidates the note of an ambiguous address names.
const (
	nearestTitle = "no exact match; nearest:"
	nearestCap   = 10
	alsoShown    = 3
	unusedShown  = 8
)

// exactness says what resolution does with an address that names no
// symbol or several: by default it answers (see unmatched and best).
type exactness struct {
	// noNearest makes an address that matches nothing ErrNotFound;
	// noBest makes one that matches several ErrAmbiguous.
	noNearest, noBest bool
}

// strictly makes the context resolve addresses as --strict does for the
// call it guards (a write is never made to a nearest or a best
// candidate); the function returned restores the context.
func (c *Ctx) strictly() func() {
	return c.resolving(exactness{noNearest: true, noBest: true})
}

// exactFirst keeps resolution from offering the nearest candidates for
// the call it guards: what the caller tries next for an address no
// symbol has (a module, a config key) comes before them.
func (c *Ctx) exactFirst() func() {
	return c.resolving(exactness{noNearest: true, noBest: c.exact.noBest})
}

// resolving sets how addresses are resolved and returns the function
// that restores it.
func (c *Ctx) resolving(e exactness) func() {
	old := c.exact
	c.exact = e
	return func() { c.exact = old }
}

// noNearest reports whether an address that matches nothing is an
// error; noBest whether one that matches several is.
func (c *Ctx) noNearest() bool { return c.Strict || c.exact.noNearest }

func (c *Ctx) noBest() bool { return c.Strict || c.exact.noBest }

// near is what resolution offers for an address that matched nothing:
// the nearest workspace symbols, else dependency symbols, else indexed
// paths, best first. one tells that the first is the only plausible
// reading of the address.
type near struct {
	syms  []store.Symbol
	deps  []depSym
	paths []output.Entry
	one   bool
	// byName tells that the candidates were found by their names, not
	// in the file the address names: full-text hits may follow them.
	byName bool
}

// only returns the one plausible candidate as a target, with its name;
// ok is false when there is none.
func (n near) only() (t target, name string, ok bool) {
	switch {
	case !n.one:
	case len(n.syms) > 0:
		return target{sym: n.syms[0]}, candidateName(n.syms[0]), true
	case len(n.deps) > 0:
		return target{dep: &n.deps[0]}, candidateName(n.deps[0].Symbol), true
	}
	return target{}, "", false
}

// entries lists what was offered.
func (n near) entries() []output.Entry {
	out := n.paths
	for _, sy := range n.syms {
		e := symbolEntry(sy)
		if isDocSymbol(sy) && sy.Qualified != "" {
			e.Name, e.Hide = sy.Qualified, output.HideDoc
		}
		out = append(out, e)
	}
	for _, d := range n.deps {
		out = append(out, depEntry(d))
	}
	return out
}

// unmatched answers an address that names no symbol (efficiency spec
// section 4). The only plausible candidate is returned as the target
// and noted ("answered for X"); several are the answer themselves,
// returned with ErrNearest; a path outside the index is ErrNotIndexed
// with the reason; with nothing to offer, or when resolution is exact,
// it is ErrNotFound.
func (c *Ctx) unmatched(addr string, repos []*config.Repo, mode depMode) (target, *output.Response, error) {
	missing := fmt.Errorf("symbol %s: %w", addr, ErrNotFound)
	if c.noNearest() {
		return target{}, c.respond(repos), missing
	}
	c.notes = nil // what the exact attempt noted is said by the answer
	n, err := c.nearest(ParseAddr(addr), mode)
	if err != nil {
		return target{}, nil, err
	}
	if t, name, ok := n.only(); ok {
		c.notes = append(c.notes, "answered for "+name)
		return t, nil, nil
	}
	entries := n.entries()
	if n.byName && len(entries) == 0 {
		entries = c.textHits(addrName(ParseAddr(addr)))
	}
	unused := c.notSearched(mode)
	if len(entries) == 0 {
		if unused != "" {
			missing = fmt.Errorf("%w; %s", missing, unused)
		}
		return target{}, nil, missing
	}
	if unused != "" && ParseAddr(addr).Kind == AddrBare {
		c.notes = append(c.notes, unused)
	}
	sec := c.section(nearestTitle, entries)
	sec.Hide = output.HideDoc // candidates are chosen by name and signature
	return target{}, c.respond(repos, sec), fmt.Errorf("symbol %s: %w", addr, ErrNearest)
}

// nearest finds what an address that matched nothing may mean: with a
// file part what its file holds (see nearInFiles), else, and for the
// name of an address no file answers, the symbols with that name (see
// named) and then the nearest names.
func (c *Ctx) nearest(a Addr, mode depMode) (near, error) {
	if p, ok := filePartOf(a); ok {
		n, found, err := c.nearInFiles(p)
		if err != nil || found {
			return n, err
		}
	}
	name := addrName(a)
	if name == "" {
		return near{byName: true}, nil
	}
	if a.Kind != AddrBare {
		if n, err := c.named(name, mode); err != nil || n.one {
			return n, err
		}
	}
	return c.nearNames(name, mode)
}

// named offers the symbol a name names exactly, for the name of a key
// or a path form whose key or file names nothing: of the workspace,
// else of a dependency as mode allows, the best-ranked of several (see
// rankCandidates).
func (c *Ctx) named(name string, mode depMode) (near, error) {
	syms, found, _, err := c.pick(name, mode)
	if err != nil || len(syms)+len(found) == 0 {
		return near{}, err
	}
	sortDeps(found)
	return near{syms: syms, deps: found, one: true}, c.rankCandidates(name, syms)
}

// addrName is the name an address asks for: of a stable key its
// qualified name without a file prefix, of a line address none.
func addrName(a Addr) string {
	if a.Kind != AddrKey {
		return a.Name
	}
	k, err := stablekey.Parse(a.Key)
	if err != nil {
		return ""
	}
	return k.Qualified[strings.LastIndex(k.Qualified, "/")+1:]
}

// candidateName names a candidate in a note: its short name, for a
// section its heading path.
func candidateName(sy store.Symbol) string {
	if isDocSymbol(sy) && sy.Qualified != "" {
		return sy.Qualified
	}
	return shortName(sy)
}

// notSearched names, for a query that answers for dependencies, the
// ones that were not searched because no source file imports them (in
// no repo that declares them): a name nothing exact was found for may
// be theirs. It is "" when every dependency was searched.
func (c *Ctx) notSearched(mode depMode) string {
	uses, err := c.Store.DepUses(0)
	if err != nil || mode == depsRefused {
		return ""
	}
	var names []string
	seen := map[string]bool{}
	for _, u := range uses {
		if u.State != store.DepUnused {
			seen[u.Name] = true // another manifest gets the package indexed
		}
	}
	for _, u := range uses {
		if !seen[u.Name] {
			seen[u.Name] = true
			names = append(names, u.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("not searched: %s (%s no source file imports; deps.include in the config indexes them)",
		shownNames(names, unusedShown), plural(len(names), "a dependency", "dependencies"))
}

// shownNames joins the first n names and counts the rest.
func shownNames(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, +%d more", strings.Join(names[:n], ", "), len(names)-n)
}
