package query

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// ImpactSections are the questions of an impact answer, in the order
// they are printed: the values --only takes.
var ImpactSections = []string{"callers", "refs", "impls", "tests", "mentions", "config"}

// ImpactOpts control the impact query.
type ImpactOpts struct {
	// Only prints one of ImpactSections alone, which --offset then pages;
	// "" prints them all.
	Only string
}

// Impact answers "I am about to change this symbol" in one answer under
// one budget: the symbol with its signature and the first sentence of
// its doc, then, each only when it has entries and titled with their
// number, its callers as their call lines (see impact.callers), the
// other references to it, the types or members that implement or
// override it, the tests that reach it, the documents and doc comments
// that name it, the config keys it reads, and one line saying whether
// it is exported and which other repos of the workspace use it (not for
// a document or a section, whose mentions are also the sections that
// link to it). The budget is shared (see Ctx.share): no section costs
// another its place, and a section that is cut ends with the command
// that prints the rest of it. An address no workspace symbol has is
// looked up in the dependencies: the answer is then the workspace code
// that uses that dependency symbol (see depImpact).
func Impact(c *Ctx, addr string, o ImpactOpts) (*output.Response, error) {
	if o.Only != "" && !slices.Contains(ImpactSections, o.Only) {
		return nil, fmt.Errorf("--only %q: want %s", o.Only, strings.Join(ImpactSections, ", "))
	}
	if o.Only == "" && c.Offset > 0 {
		return nil, errors.New("--offset pages one section: name it with --only (" + strings.Join(ImpactSections, ", ") + ")")
	}
	t, resp, err := c.graphTargetIn(addr, depsElse)
	if err != nil {
		return resp, err
	}
	if t.dep != nil {
		return c.depImpact(*t.dep, addr, o)
	}
	sy := t.sym
	im := &impact{c: c, sy: sy, addr: c.resolvedAddr(addr, sy), only: o.Only}
	secs, err := im.sections()
	if err != nil {
		return nil, err
	}
	return c.share(c.respond(c.Workspace.Repos, secs...)), nil
}

// resolvedAddr is the address the follow-up commands of an answer about
// sy name it by, quoted for a shell: the one that was given, or the
// stable key of sy when the address was answered for a nearest or a best
// candidate (the notes of the context say so), so that every command
// asks about the same symbol.
func (c *Ctx) resolvedAddr(addr string, sy store.Symbol) string {
	if len(c.notes) > 0 {
		return shellQuote(sy.StableKey)
	}
	return shellQuote(addr)
}

// impact builds the sections of an impact answer about sy, which the
// follow-up commands address as addr (quoted for a shell).
type impact struct {
	c    *Ctx
	sy   store.Symbol
	addr string
	only string
}

// sections builds the answer: the line of the symbol, the sections of
// ImpactSections (the one --only names, else all) and, with all of
// them, the line saying whether the symbol is exported.
func (im *impact) sections() ([]output.Section, error) {
	secs := []output.Section{oneSymbol(symbolEntry(im.sy))}
	parts := map[string]func() ([]output.Section, error){"callers": im.callers, "refs": im.refs, "impls": im.impls,
		"tests": im.tests, "mentions": im.mentions, "config": im.config}
	for _, name := range ImpactSections {
		if im.only != "" && im.only != name {
			continue
		}
		more, err := parts[name]()
		if err != nil {
			return nil, err
		}
		secs = append(secs, more...)
	}
	if im.only != "" || isDocSymbol(im.sy) {
		return secs, nil
	}
	line, err := im.exported()
	return append(secs, line), err
}

// command is the command that answers one question about the symbol
// alone: codedoc <name> <address> with flags and the scope of the query.
func (im *impact) command(name string, flags ...string) string {
	return strings.Join(append([]string{"codedoc", name, im.addr}, flags...), " ") + im.c.Scope
}

// own is the command that prints one section of the impact answer.
func (im *impact) own(section string) string { return im.command("impact", "--only", section) }

// page is the section of one question: the ranked entries, from the
// offset on when the section is printed alone, as shape lays them out
// (it may fold several into one line; nil leaves them), titled with
// their number and continued by command. No entries, no section.
func (im *impact) page(title, command string, entries []output.Entry,
	shape func(entries []output.Entry, offset int) []output.Entry) []output.Section {
	if len(entries) == 0 {
		return nil
	}
	off := 0
	if im.only != "" && im.c.Offset > 0 {
		if off = min(im.c.Offset, len(entries)); off == len(entries) {
			im.c.notePastEnd(len(entries))
		}
	}
	shown := entries[off:]
	if shape != nil {
		shown = shape(shown, off)
	}
	return []output.Section{{Title: title, Entries: shown, Offset: off, Total: len(entries), Command: command}}
}

// impls lists what implements or overrides the symbol. For a type they
// are the types implementing or extending it; for a member of a type,
// the member of that name its supertypes declare (what it implements or
// overrides) and the one its subtypes declare (what overrides it).
func (im *impact) impls() ([]output.Section, error) {
	c, sy := im.c, im.sy
	if sy.ContainerID == 0 {
		edges, err := c.Store.EdgesTo(sy.ID, "implements", "extends")
		if err != nil {
			return nil, err
		}
		entries, err := c.sourceEntries(edges, func(store.Symbol) bool { return true })
		return im.page("implementations", im.command("impls"), entries, nil), err
	}
	supers, subs, err := im.related()
	if err != nil {
		return nil, err
	}
	named, err := c.Store.SymbolsByName(sy.Name, "", 0)
	if err != nil {
		return nil, err
	}
	var over, under []output.Entry
	for _, m := range named {
		switch {
		case m.ID == sy.ID || m.ContainerID == 0:
		case supers[m.ContainerID]:
			over = append(over, memberEntry(m))
		case subs[m.ContainerID]:
			under = append(under, memberEntry(m))
		}
	}
	return append(im.page("implements or overrides", im.own("impls"), over, nil),
		im.page("implemented or overridden by", im.own("impls"), testsLast(under), nil)...), nil
}

// related returns the rows of the types the container of the symbol
// implements or extends (the candidates of an ambiguous edge among
// them) and of the types that implement or extend it.
func (im *impact) related() (supers, subs map[int64]bool, err error) {
	up, err := im.c.Store.EdgesFrom(im.sy.ContainerID, "implements", "extends")
	if err != nil {
		return nil, nil, err
	}
	down, err := im.c.Store.EdgesTo(im.sy.ContainerID, "implements", "extends")
	if err != nil {
		return nil, nil, err
	}
	supers, subs = map[int64]bool{}, map[int64]bool{}
	for _, e := range up {
		for _, id := range append(e.Candidates, e.Dst) {
			supers[id] = true
		}
	}
	for _, e := range down {
		subs[e.Src] = true
	}
	return supers, subs, nil
}

// memberEntry is the line of a member that an impact answer relates to
// the symbol: its name, where it is declared and its signature.
func memberEntry(m store.Symbol) output.Entry {
	e := symbolEntry(m)
	e.Loc, e.Hide = fmt.Sprintf("%s:%d", m.Path, m.StartLine), output.HideDoc
	return e
}

// tests lists the tests that reach the symbol, those calling or
// referring to it first, then those reaching it through up to two
// callers with the path they take (via ...): names and locations, in
// the order of codedoc tests --transitive, which continues the list. A
// file with more than crowdedFile of them lists its first shownSites.
func (im *impact) tests() ([]output.Section, error) {
	entries, err := im.c.transitiveTests(im.sy)
	if err != nil {
		return nil, err
	}
	entries = testLines(entries)
	command := im.command("tests", "--transitive")
	return im.page("tests", command, entries, func(entries []output.Entry, offset int) []output.Entry {
		return crowd(entries, crowding{what: "tests in this file", command: command, offset: offset, wide: true})
	}), nil
}

// testLines turns the entries of tests --transitive into the lines of
// an impact answer: name, location and the path the test takes.
func testLines(entries []output.Entry) []output.Entry {
	for i := range entries {
		e := &entries[i]
		e.Hide |= output.HideText
		e.Tags = slices.DeleteFunc(slices.Clone(e.Tags), func(tag string) bool { return tag == testTag })
		if !strings.HasPrefix(e.Doc, viaPrefix) {
			e.Doc = ""
		}
	}
	return entries
}

// config lists the config keys the lines of the symbol read, each once
// at its first read, tagged undefined when no indexed config file
// defines it.
func (im *impact) config() ([]output.Section, error) {
	c, sy := im.c, im.sy
	reads, err := c.Store.ConfigReadsIn(sy.FileID, sy.StartLine, sy.EndLine)
	if err != nil {
		return nil, err
	}
	var entries []output.Entry
	seen := map[string]bool{}
	for _, r := range reads {
		if seen[r.Key] {
			continue
		}
		seen[r.Key] = true
		e := output.Entry{Key: r.Key, Name: r.Key, Loc: fmt.Sprintf("%s:%d", sy.Path, r.Line), RepoID: sy.RepoID,
			Extra: map[string]any{"kind": "config", "defined": r.Defined}}
		if !r.Defined {
			e.Tags = []string{undefinedTag}
		}
		c.site(&e, sy, r.Line)
		entries = append(entries, e)
	}
	secs := im.page("config keys read", im.own("config"), entries, nil)
	for i := range secs {
		secs[i].AllTags = true
	}
	return secs, nil
}

// exported is the last line of an impact answer: whether the symbol is
// visible outside its package or module, and, in a workspace of several
// repos, which other repos call, refer to, implement or extend it.
func (im *impact) exported() (output.Section, error) {
	c, sy := im.c, im.sy
	text := "no (" + sy.Visibility + ")"
	if sy.Visibility == "public" {
		text = "yes"
		if sy.ContainerID != 0 {
			cont, err := c.Store.SymbolByID(sy.ContainerID)
			if err != nil {
				return output.Section{}, err
			}
			text = "yes (public member of " + shortName(cont) + ")"
			if cont.Visibility != "public" {
				text = "no (public member of " + shortName(cont) + ", which is " + cont.Visibility + ")"
			}
		}
	}
	users, err := im.otherRepos()
	if err != nil {
		return output.Section{}, err
	}
	e := output.Entry{Key: "exported", Name: "exported", Text: text,
		Extra: map[string]any{"visibility": sy.Visibility, "repos": users}}
	switch {
	case len(users) > 0:
		e.Doc = "used by " + strings.Join(users, ", ")
	case len(c.Workspace.Repos) > 1:
		e.Doc = "no other repo of the workspace uses it"
	}
	sec := oneSymbol(e)
	sec.Title = "exported"
	return sec, nil
}

// otherRepos names the repos other than the symbol's own whose code
// calls, refers to, implements or extends it, in config order.
func (im *impact) otherRepos() ([]string, error) {
	c := im.c
	edges, err := c.Store.EdgesTo(im.sy.ID, "calls", "references", "implements", "extends")
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(edges))
	for i, e := range edges {
		ids[i] = e.Src
	}
	srcs, err := c.Store.SymbolsByID(ids)
	if err != nil {
		return nil, err
	}
	uses := map[string]bool{}
	for _, src := range srcs {
		if src.RepoID != im.sy.RepoID {
			uses[c.repoNames()[src.RepoID]] = true
		}
	}
	out := []string{}
	for _, r := range c.Workspace.Repos {
		if uses[r.Name] {
			out = append(out, r.Name)
		}
	}
	return out, nil
}
