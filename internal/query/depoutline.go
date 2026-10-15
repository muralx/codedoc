package query

import (
	"fmt"
	"strings"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// inheritedCap is the most inherited members the outline of a
// dependency type lists.
const inheritedCap = 30

// depOutline outlines a dependency target (deps spec section 7), tried
// when the workspace has no such path or module: a package (its id, its
// name or name@version) lists its modules; a module (an import
// specifier such as @scope/pkg/server) its symbols; a path inside a
// directory a package is installed in the symbols of that file; and a
// type (a stable key, or with --deps a name) its members with the public
// ones it inherits under a heading of their own. ok is false when target
// names none of them.
func (c *Ctx) depOutline(target string) (*output.Response, bool, error) {
	repos := c.scope()
	if err := c.EnsureFresh(repos...); err != nil {
		return nil, false, err
	}
	v := c.deps()
	if v == nil {
		return nil, false, nil
	}
	name := strings.TrimSuffix(target, "/")
	var secs []output.Section
	var err error
	if pkgs := v.named(name); len(pkgs) > 0 {
		secs, err = c.packageOutlines(v, pkgs)
	} else if secs, err = c.moduleOutlines(v, name); err == nil && len(secs) == 0 {
		if secs, err = c.depFileOutline(target); err == nil && len(secs) == 0 {
			secs, err = c.typeOutline(v, target)
		}
	}
	if err != nil || len(secs) == 0 {
		return nil, false, err
	}
	return c.respond(repos, secs...), true, nil
}

// packageOutlines lists the modules of each package version: identity,
// directory, symbol count and the first sentence of the module doc (for
// the root module, of the README). Modules that declare nothing and
// have no doc are counted in the note.
func (c *Ctx) packageOutlines(v *depView, pkgs []*depPkg) ([]output.Section, error) {
	var secs []output.Section
	for _, p := range pkgs {
		mods, err := v.s.ModulesByRepo(p.repoID)
		if err != nil {
			return nil, err
		}
		var entries []output.Entry
		empty := 0
		for _, m := range mods {
			syms, err := v.s.SymbolsInModule(m.ID)
			if err != nil {
				return nil, err
			}
			if len(syms) == 0 && m.DocText == "" {
				empty++
				continue
			}
			entries = append(entries, output.Entry{Key: m.Identity, Loc: m.Path, Text: fmt.Sprintf("%s %s, %d %s", m.Lang,
				m.Kind, len(syms), plural(len(syms), "symbol", "symbols")), Doc: firstSentence(readmeLead(m.DocText)),
				Extra: map[string]any{"kind": "module", "dep": p.id.String(), "symbols": len(syms)}})
		}
		if empty > 0 {
			c.notes = append(c.notes, fmt.Sprintf("%d %s of %s without declarations not listed", empty,
				plural(empty, "module", "modules"), p.id))
		}
		secs = append(secs, c.section(p.id.String()+" modules", entries))
	}
	return secs, nil
}

// moduleOutlines lists the symbols of the module an import specifier
// names in each package version that answers to it.
func (c *Ctx) moduleOutlines(v *depView, identity string) ([]output.Section, error) {
	var secs []output.Section
	for _, p := range v.pkgs {
		if !strings.HasPrefix(identity, p.id.Name+"/") {
			continue
		}
		ids, err := v.s.RepoIdentities(p.repoID)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if id.Identity != identity {
				continue
			}
			m, err := v.s.ModuleByPath(p.repoID, id.Lang, id.Path)
			if err != nil {
				return nil, err
			}
			syms, err := v.s.SymbolsInModule(m.ID)
			if err != nil {
				return nil, err
			}
			secs = append(secs, c.section(fmt.Sprintf("%s [%s]", m.Identity, p.tag()), collapsed(syms, p)))
		}
	}
	return secs, nil
}

// collapsed lists dependency symbols with their members folded into a
// "+N members" tag on the outermost container (see fold): a module of a
// package spans several files, so a member counts wherever it is
// declared.
func collapsed(syms []store.Symbol, p *depPkg) []output.Entry {
	listed, members := fold(syms, false)
	entries := make([]output.Entry, len(listed))
	for i, sy := range listed {
		entries[i] = depOutlineEntry(depSym{Symbol: sy, pkg: p}, members[sy.ID])
	}
	return entries
}

// depOutlineEntry is an outline line of a dependency symbol (see
// outlined); the title of its section names the package, so it carries
// no dep tag, and text prints its doc sentence only with --detail.
func depOutlineEntry(d depSym, members int) output.Entry {
	e := depEntry(d)
	e.Tags = nil
	e.Hide = output.HideDoc
	return outlined(e, d.Symbol, members)
}

// depFileOutline lists the symbols of the dependency file a path names
// (see depFileAt).
func (c *Ctx) depFileOutline(target string) ([]output.Section, error) {
	f, ok, err := c.depFileAt(target)
	if err != nil || !ok {
		return nil, err
	}
	syms, err := c.view.s.SymbolsInFile(f.file.ID)
	if err != nil {
		return nil, err
	}
	title := fmt.Sprintf("%s [%s], %d %s", f.file.Path, f.pkg.tag(), f.file.Lines, plural(f.file.Lines, "line", "lines"))
	sec := c.section(title, collapsed(syms, f.pkg))
	sec.Path = f.file.Path
	return []output.Section{sec}, nil
}

// typeOutline lists the members of the one dependency type an address
// names, then the public members its supertypes declare, nearest first
// and at most inheritedCap, each saying where it is declared.
func (c *Ctx) typeOutline(v *depView, addr string) ([]output.Section, error) {
	_, found, _, err := c.pick(addr, depsOnFlag)
	if err != nil || len(found) != 1 || !memberKinds[found[0].Kind] {
		return nil, err
	}
	typ := found[0]
	own, err := v.members(typ.Symbol)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	entries := []output.Entry{depOutlineEntry(typ, 0)}
	entries[0].Tags = []string{typ.pkg.tag()}
	for _, m := range own {
		names[m.Name] = true
		e := depOutlineEntry(depSym{Symbol: m, pkg: typ.pkg}, 0)
		e.Depth = 1
		entries = append(entries, e)
	}
	inherited, err := c.inheritedEntries(v, typ, names)
	if err != nil {
		return nil, err
	}
	secs := []output.Section{c.titled(about("members of ", typ.Symbol), entries)}
	if len(inherited) > 0 {
		secs = append(secs, c.section("inherited members", inherited))
	}
	return secs, nil
}

// inheritedEntries lists the public members the supertypes of typ
// declare that neither typ nor a nearer supertype declares (names holds
// the ones seen), capped; what the cap leaves out is noted.
func (c *Ctx) inheritedEntries(v *depView, typ depSym, names map[string]bool) ([]output.Entry, error) {
	supers, err := v.supers(typ.Symbol)
	if err != nil {
		return nil, err
	}
	var out []output.Entry
	total := 0
	for _, sup := range supers {
		members, err := v.members(sup)
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			if names[m.Name] || m.Visibility != "public" {
				continue
			}
			names[m.Name] = true
			if total++; total > inheritedCap {
				continue
			}
			e := depOutlineEntry(depSym{Symbol: m, pkg: typ.pkg}, 0)
			e.Tags = []string{"declared on " + sup.Name}
			out = append(out, e)
		}
	}
	if total > inheritedCap {
		c.notes = append(c.notes, fmt.Sprintf("%d more inherited members not listed", total-inheritedCap))
	}
	return out, nil
}
