package query

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/store"
)

// refKinds are the reference kinds refs --kind accepts.
var refKinds = map[string]bool{"type": true, "instantiate": true, "field": true, store.LinkRefKind: true}

// Refs lists the non-call references to a symbol (type uses,
// instantiations, field accesses), optionally of one kind. An address no
// workspace symbol has is looked up in the dependencies: the answer is
// then the workspace code that refers to that dependency symbol. For a
// document or a section (doc.md, doc.md#Heading) they are the sections
// of other documents that link to it (kind link).
func Refs(c *Ctx, addr string, o GraphOpts) (*output.Response, error) {
	if o.Kind != "" && !refKinds[o.Kind] {
		return nil, fmt.Errorf("--kind %q: want type, instantiate, field or link", o.Kind)
	}
	refKinds := []string{"type", "instantiate", "field"}
	if o.Kind != "" {
		refKinds = []string{o.Kind}
	}
	return c.inboundQuery(addr, o, inbound{title: "references to ", overCap: refKinds, deps: depsElse,
		keep: func(e store.Edge) bool { return o.Kind == "" || e.RefKind == o.Kind }}, "references")
}

// Impls lists the types that implement or extend a type.
func Impls(c *Ctx, addr string, o GraphOpts) (*output.Response, error) {
	return c.inboundQuery(addr, o, inbound{title: "implementations of "}, "implements", "extends")
}

// Supertypes lists the types a type implements or extends.
func Supertypes(c *Ctx, addr string, o GraphOpts) (*output.Response, error) {
	sy, resp, err := c.graphTarget(addr)
	if err != nil {
		return resp, err
	}
	edges, err := c.Store.EdgesFrom(sy.ID, "implements", "extends")
	if err != nil {
		return nil, err
	}
	keep, err := c.repoFilter()
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, e := range edges {
		ids = append(append(ids, e.Dst), e.Candidates...)
	}
	syms, err := c.Store.SymbolsByID(ids)
	if err != nil {
		return nil, err
	}
	var entries []output.Entry
	for _, e := range edges {
		targets := e.Candidates
		if e.Dst != 0 {
			targets = []int64{e.Dst}
		}
		for _, id := range targets {
			if t, ok := syms[id]; ok && keep(t) {
				entry := edgeEntry(t, e, fmt.Sprintf("%s:%d", t.Path, t.StartLine), t.RepoID, 1)
				c.site(&entry, t, t.StartLine)
				entries = append(entries, entry)
			}
		}
	}
	return c.respond(c.Workspace.Repos, c.sections(about("supertypes of ", sy), testsLast(entries), o.GroupByFile)...), nil
}

// inbound describes an inbound query: its title prefix, the edges kept
// (nil keeps all), and the reference kinds whose over-cap references
// named like the symbol are noted.
type inbound struct {
	title   string
	keep    func(store.Edge) bool
	overCap []string
	// deps says when the address may name a dependency symbol.
	deps depMode
}

// inboundQuery lists the sources of the edges of kinds ending at a
// symbol, kept by q.keep, at the edge's location.
func (c *Ctx) inboundQuery(addr string, o GraphOpts, q inbound, kinds ...string) (*output.Response, error) {
	t, resp, err := c.graphTargetIn(addr, q.deps)
	if err != nil {
		return resp, err
	}
	if t.dep != nil {
		return c.depInbound(*t.dep, o, q.title, q.keep, kinds...)
	}
	sy := t.sym
	if len(q.overCap) > 0 {
		if err := c.noteOverCap(sy, q.overCap...); err != nil {
			return nil, err
		}
	}
	keep := q.keep
	title := q.title
	edges, err := c.Store.EdgesTo(sy.ID, kinds...)
	if err != nil {
		return nil, err
	}
	var kept []store.Edge
	for _, e := range edges {
		if keep == nil || keep(e) {
			kept = append(kept, e)
		}
	}
	entries, err := c.sourceEntries(kept, func(src store.Symbol) bool { return !o.NoTests || !isTestSymbol(src) })
	if err != nil {
		return nil, err
	}
	return c.respond(c.Workspace.Repos, c.sections(about(title, sy), entries, o.GroupByFile)...), nil
}

// sourceEntries renders the source symbol of each edge at the edge's
// location, production code before tests and otherwise in path and line
// order, for the sources within the --repo repo that keepSrc keeps. A
// line that holds several of the edges is one entry (see sameSite).
func (c *Ctx) sourceEntries(edges []store.Edge, keepSrc func(store.Symbol) bool) ([]output.Entry, error) {
	keep, err := c.repoFilter()
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
	var rows []located
	for _, e := range edges {
		src, ok := srcs[e.Src]
		if ok && keep(src) && keepSrc(src) {
			rows = append(rows, located{src, e})
		}
	}
	sortLocated(rows)
	var out []output.Entry
	at := map[siteKey]int{}
	for _, r := range rows {
		key := siteKey{r.sy.ID, r.e.Line, r.e.Confidence == resolve.ConfidenceAmbiguous}
		if i, listed := at[key]; listed {
			sameSite(&out[i], r.e)
			continue
		}
		at[key] = len(out)
		e := edgeEntry(r.sy, r.e, fmt.Sprintf("%s:%d", r.sy.Path, r.e.Line), r.sy.RepoID, 1)
		c.site(&e, r.sy, r.e.Line)
		out = append(out, e)
	}
	return c.mergeSites(out), nil
}

// located is an edge with the symbol it starts in.
type located struct {
	sy store.Symbol
	e  store.Edge
}

// sortLocated orders edges by where they start: production code before
// tests, then by path and line.
func sortLocated(rows []located) {
	sort.SliceStable(rows, func(i, j int) bool {
		if ti, tj := isTestSymbol(rows[i].sy), isTestSymbol(rows[j].sy); ti != tj {
			return tj
		}
		if rows[i].sy.Path != rows[j].sy.Path {
			return rows[i].sy.Path < rows[j].sy.Path
		}
		return rows[i].e.Line < rows[j].e.Line
	})
}

// siteKey names the line of a symbol that edges start at; the certain
// and the ambiguous ones are listed apart.
type siteKey struct {
	src       int64
	line      int
	ambiguous bool
}

// sameSite adds to e, the entry of a line, another edge that starts on
// that line: the line is listed once, and a reference of another kind
// (a type named and instantiated on one line) adds its kind to the
// tags --detail prints and to ref_kinds in JSON.
func sameSite(e *output.Entry, again store.Edge) {
	if again.Kind != "references" || slices.Contains(e.Tags, again.RefKind) {
		return
	}
	kinds, _ := e.Extra["ref_kinds"].([]string)
	if kinds == nil {
		kinds = []string{fmt.Sprint(e.Extra["ref_kind"])}
	}
	e.Extra["ref_kinds"] = append(kinds, again.RefKind)
	at := slices.Index(e.Tags, kinds[len(kinds)-1]) + 1
	e.Tags = slices.Insert(e.Tags, at, again.RefKind)
}

// testDepth is how far tests --transitive walks callers.
const testDepth = 3

// viaPrefix starts the text that names the symbols between a test and
// the symbol it reaches through them.
const viaPrefix = "via "

// Tests lists the test symbols that call or reference a symbol, or with
// Transitive those reaching it through callers up to depth 3, each with
// the path it takes. An address that names no symbol is tried as a
// module: the tests reaching any of its symbols directly. One that
// names a dependency symbol lists the tests of the workspace that use
// it (see depTests).
func Tests(c *Ctx, addr string, o GraphOpts) (*output.Response, error) {
	sy, _, err := c.graphTargetExact(addr)
	if errors.Is(err, ErrNotFound) {
		mod, mresp, merr := c.oneModuleExact(addr)
		switch {
		case merr == nil:
			return c.moduleTests(mod)
		case !errors.Is(merr, ErrNotFound):
			return mresp, merr
		}
	}
	if err != nil { // no workspace symbol as written: the nearest, or a dependency's
		t, resp, err := c.graphTargetIn(addr, depsElse)
		if err != nil {
			return resp, err
		}
		if t.dep != nil {
			return c.depTestsAnswer(*t.dep, o)
		}
		sy = t.sym
	}
	title := about("tests of ", sy)
	if !o.Transitive {
		edges, err := c.Store.EdgesTo(sy.ID, "calls", "references")
		if err != nil {
			return nil, err
		}
		entries, err := c.testEntries(edges)
		return c.respond(c.Workspace.Repos, c.titled(title, entries)), err
	}
	entries, err := c.transitiveTests(sy)
	if err != nil {
		return nil, err
	}
	return c.respond(c.Workspace.Repos, c.titled(title, entries)), nil
}

// testEntries renders the test sources of edges (test functions, not
// the helpers of test files), each test once.
func (c *Ctx) testEntries(edges []store.Edge) ([]output.Entry, error) {
	all, err := c.sourceEntries(edges, func(src store.Symbol) bool { return src.IsTest })
	var out []output.Entry
	seen := map[string]bool{}
	for _, e := range all {
		if !seen[e.Key] {
			seen[e.Key] = true
			out = append(out, e)
		}
	}
	return out, err
}

// transitiveTests walks callers and references up to testDepth and
// lists the tests reached, each with the symbols between it and sy.
func (c *Ctx) transitiveTests(sy store.Symbol) ([]output.Entry, error) {
	steps, err := c.Store.Walk(sy.ID, store.WalkOpts{Inbound: true, Kinds: []string{"calls", "references"},
		Depth: testDepth, Limit: walkRows})
	if err != nil {
		return nil, err
	}
	steps, _ = firstVisits(steps)
	g, err := c.loadGraph(steps)
	if err != nil {
		return nil, err
	}
	keep, err := c.repoFilter()
	if err != nil {
		return nil, err
	}
	parent := map[int64]int64{}
	for _, st := range steps {
		parent[st.Node] = st.Parent
	}
	var out []output.Entry
	for _, st := range steps {
		if t := g.syms[st.Node]; t.IsTest && keep(t) {
			e := g.entry(st)
			if via := g.via(st.Parent, sy.ID, parent); via != "" {
				e.Doc = viaPrefix + via
			}
			out = append(out, e)
		}
	}
	return out, nil
}

// via names the symbols from node up to (not including) root, joined
// by " > ".
func (g *graph) via(node, root int64, parent map[int64]int64) string {
	var names []string
	for n := node; n != root && n != 0 && len(names) <= testDepth; n = parent[n] {
		names = append(names, shortName(g.syms[n]))
	}
	return strings.Join(names, " > ")
}

// moduleTests lists the tests that call or reference a symbol of mod.
func (c *Ctx) moduleTests(mod store.Module) (*output.Response, error) {
	c.draws = append(c.draws, mod.RepoID)
	edges, err := c.Store.EdgesToModule(mod.ID, "calls", "references")
	if err != nil {
		return nil, err
	}
	entries, err := c.testEntries(edges)
	return c.respond(c.Workspace.Repos, c.section("tests of module "+mod.Identity, entries)), err
}
