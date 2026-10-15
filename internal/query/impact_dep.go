package query

import (
	"maps"
	"slices"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// depUsers returns the edges of kinds from workspace code to a
// dependency symbol.
func (c *Ctx) depUsers(d depSym, kinds ...string) ([]store.Edge, error) {
	return c.Store.EdgesToDep(d.StableKey, kinds...)
}

// depTests lists the tests of the workspace that use a dependency
// symbol: those calling or referring to it and, with transitive, those
// reaching it through the workspace code that does, each with the path
// it takes ("via app.run"). Each test is listed once, the direct ones
// first.
func (c *Ctx) depTests(d depSym, transitive bool) ([]output.Entry, error) {
	edges, err := c.depUsers(d, "calls", "references")
	if err != nil {
		return nil, err
	}
	out, err := c.testEntries(edges)
	if err != nil || !transitive {
		return out, err
	}
	ids := make([]int64, len(edges))
	for i, e := range edges {
		ids[i] = e.Src
	}
	users, err := c.Store.SymbolsByID(ids)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, e := range out {
		seen[e.Key] = true
	}
	for _, id := range slices.Sorted(maps.Keys(users)) {
		user := users[id]
		if user.IsTest {
			continue
		}
		reached, err := c.testsThrough(user, seen)
		if err != nil {
			return nil, err
		}
		out = append(out, reached...)
	}
	return out, nil
}

// testsThrough lists the tests that reach user, a workspace symbol
// using a dependency symbol, without those seen already (which it adds
// to), each with the path it takes ending at user.
func (c *Ctx) testsThrough(user store.Symbol, seen map[string]bool) ([]output.Entry, error) {
	reached, err := c.transitiveTests(user)
	if err != nil {
		return nil, err
	}
	var out []output.Entry
	for _, e := range reached {
		if seen[e.Key] {
			continue
		}
		seen[e.Key] = true
		if e.Doc == "" {
			e.Doc = viaPrefix + shortName(user)
		} else {
			e.Doc += " > " + shortName(user)
		}
		out = append(out, e)
	}
	return out, nil
}

// depTestsAnswer is the answer of codedoc tests for a dependency symbol
// (see depTests); the note says where an inherited member is declared.
func (c *Ctx) depTestsAnswer(d depSym, o GraphOpts) (*output.Response, error) {
	entries, err := c.depTests(d, o.Transitive)
	if err != nil {
		return nil, err
	}
	if note := d.declaredNote(); note != "" {
		c.notes = append(c.notes, note)
	}
	return c.respond(c.Workspace.Repos, c.titled(about("tests of ", d.Symbol).plus(" ["+d.pkg.tag()+"]"), entries)), nil
}

// depImpact answers codedoc impact for a dependency symbol: what in the
// workspace a new version of it would touch. The symbol with its
// package, then the workspace code that calls it, the other references
// to it and the tests that reach it through that code, each section as
// impact prints it for a workspace symbol and continued by the command
// that lists it alone. A dependency symbol has no implementations,
// mentions or config keys in the index.
func (c *Ctx) depImpact(d depSym, addr string, o ImpactOpts) (*output.Response, error) {
	im := &impact{c: c, addr: shellQuote(addr), only: o.Only}
	head := depEntry(d)
	head.Hide = 0
	secs := []output.Section{oneSymbol(head)}
	parts := map[string]func() ([]output.Section, error){
		"callers": func() ([]output.Section, error) { return im.depUses(d, "callers", "callers", "calls") },
		"refs":    func() ([]output.Section, error) { return im.depUses(d, "references", "refs", "references") },
		"tests": func() ([]output.Section, error) {
			entries, err := c.depTests(d, true)
			return im.page("tests", im.command("tests", "--transitive"), testLines(entries), nil), err
		},
	}
	for _, name := range ImpactSections {
		part := parts[name]
		if part == nil || (o.Only != "" && o.Only != name) {
			continue
		}
		more, err := part()
		if err != nil {
			return nil, err
		}
		secs = append(secs, more...)
	}
	if note := d.declaredNote(); note != "" {
		c.notes = append(c.notes, note)
	}
	return c.share(c.respond(c.Workspace.Repos, secs...)), nil
}

// depUses is the section of an impact answer listing the workspace
// code with edges of kind to a dependency symbol, continued by the
// command name.
func (im *impact) depUses(d depSym, title, name, kind string) ([]output.Section, error) {
	edges, err := im.c.depUsers(d, kind)
	if err != nil {
		return nil, err
	}
	entries, err := im.c.sourceEntries(edges, func(store.Symbol) bool { return true })
	return im.page(title, im.command(name), entries, nil), err
}
