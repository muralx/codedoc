package query

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/docs"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/stablekey"
	"github.com/muralx/codedoc/internal/store"
)

// nodeCap is the most symbols a graph answer lists; walkRows caps the
// rows a walk reads, since one symbol can be reached over several paths.
const (
	nodeCap  = 500
	walkRows = 20 * nodeCap
)

// GraphOpts control the graph queries (spec section 9).
type GraphOpts struct {
	// Depth is how many edges a callers or callees walk follows; above 1
	// the answer is a tree.
	Depth int
	// NoTests drops test code (what is tagged test: test functions and
	// the symbols of test files) from the symbols listed and walked.
	NoTests bool
	// GroupByFile lists the entries in one section per file.
	GroupByFile bool
	// Kind limits refs to one reference kind: type, instantiate, field.
	Kind string
	// Transitive makes tests walk callers up to depth 3.
	Transitive bool
	// CrossRepo lets deps and dependents leave the start module's repo.
	CrossRepo bool
}

// Callers lists the symbols that call a symbol, including calls whose
// target is ambiguous among candidates the symbol is one of (tagged
// ambiguous). With Depth above 1 it walks callers of callers and renders
// a tree, breadth first, each symbol listed once. An address no
// workspace symbol has is looked up in the dependencies: the answer is
// then the workspace code that calls that dependency symbol.
func Callers(c *Ctx, addr string, o GraphOpts) (*output.Response, error) {
	return c.walkQuery(addr, o, true)
}

// Callees lists the symbols a symbol calls; an ambiguous call lists its
// candidates inline and is not followed, and a call into a dependency
// lists the dependency symbol, tagged dep.
func Callees(c *Ctx, addr string, o GraphOpts) (*output.Response, error) {
	return c.walkQuery(addr, o, false)
}

// graphTarget makes every repo fresh (edges come from anywhere) and
// resolves the start symbol of a graph query. --repo filters the answer,
// not the address.
func (c *Ctx) graphTarget(addr string) (store.Symbol, *output.Response, error) {
	t, resp, err := c.graphTargetIn(addr, depsRefused)
	return t.sym, resp, err
}

func (c *Ctx) walkQuery(addr string, o GraphOpts, inbound bool) (*output.Response, error) {
	if o.Depth < 1 {
		return nil, errors.New("--depth must be at least 1")
	}
	mode := depsRefused
	if inbound {
		mode = depsElse
	}
	t, resp, err := c.graphTargetIn(addr, mode)
	if err != nil {
		return resp, err
	}
	if t.dep != nil {
		return c.depInbound(*t.dep, o, "callers of ", nil, "calls")
	}
	return c.walk(t.sym, o, inbound)
}

// walk is walkQuery from a workspace symbol.
func (c *Ctx) walk(sy store.Symbol, o GraphOpts, inbound bool) (*output.Response, error) {
	entries, err := c.walkEntries(sy, o, inbound)
	if err != nil {
		return nil, err
	}
	title := about("callees of ", sy)
	if inbound {
		title = about("callers of ", sy)
		if err := c.noteOverCap(sy, "call"); err != nil {
			return nil, err
		}
	}
	return c.respond(c.Workspace.Repos, c.sections(title, entries, o.GroupByFile)...), nil
}

// walkEntries lists the callers (inbound) or the callees of sy to the
// depth of o, each with the lines of its calls: a tree unless the
// entries are grouped by file.
func (c *Ctx) walkEntries(sy store.Symbol, o GraphOpts, inbound bool) ([]output.Entry, error) {
	var err error
	wo := store.WalkOpts{Inbound: inbound, Kinds: []string{"calls"}, Depth: o.Depth, Limit: walkRows, NoTests: o.NoTests}
	if o.NoTests {
		if wo.TestFiles, err = c.Store.FileIDsWhere(docs.IsTestPath); err != nil {
			return nil, err
		}
	}
	steps, err := c.Store.Walk(sy.ID, wo)
	if err != nil {
		return nil, err
	}
	all := steps
	steps, capped := firstVisits(steps)
	g, err := c.loadGraph(steps)
	if err != nil {
		return nil, err
	}
	if err := c.loadRepeats(g, all); err != nil {
		return nil, err
	}
	if capped {
		c.notes = append(c.notes, fmt.Sprintf("stopped at %d symbols; lower --depth", nodeCap))
	}
	return c.treeEntries(g, sy.ID, !o.GroupByFile)
}

// firstVisits keeps the first step reaching each symbol (the shallowest,
// as the walk is breadth first) and every ambiguous outbound step, up to
// nodeCap; capped reports that steps were left out.
func firstVisits(steps []store.Step) ([]store.Step, bool) {
	seen := map[int64]bool{}
	var out []store.Step
	for _, st := range steps {
		if st.Node != 0 && seen[st.Node] {
			continue
		}
		if len(out) == nodeCap {
			return out, true
		}
		seen[st.Node] = true
		out = append(out, st)
	}
	return out, len(steps) == walkRows
}

// noteOverCap notes how many references of the given kinds named like
// sy were left unresolved for having more than MaxCandidates candidates:
// sy may be among them, but they are not listed.
func (c *Ctx) noteOverCap(sy store.Symbol, kinds ...string) error {
	n, err := c.Store.OverCapReferences(sy.Name, kinds...)
	if err != nil || n == 0 {
		return err
	}
	c.notes = append(c.notes, fmt.Sprintf("%d %s named %s had more than %d candidates and %s not listed", n,
		plural(n, "reference", "references"), sy.Name, resolve.MaxCandidates, plural(n, "is", "are")))
	return nil
}

// graph holds the steps of a walk with their edges and symbols; deps
// holds the dependency symbols its edges end at, by stable key, and
// repeats, by the edge of a step, the further edges between the same
// two symbols: the other places one calls the other.
type graph struct {
	steps   []store.Step
	edges   map[int64]store.Edge
	syms    map[int64]store.Symbol
	deps    map[string]depSym
	repeats map[int64][]store.Edge
}

// loadRepeats reads, for the steps of g, the edges of all (every step
// of the walk) that join the same two symbols as a step that was kept.
func (c *Ctx) loadRepeats(g *graph, all []store.Step) error {
	kept := map[[2]int64]int64{}
	for _, st := range g.steps {
		if st.Node != 0 {
			kept[[2]int64{st.Node, st.Parent}] = st.EdgeID
		}
	}
	by := map[int64]int64{}
	var ids []int64
	for _, st := range all {
		if first, ok := kept[[2]int64{st.Node, st.Parent}]; ok && first != st.EdgeID {
			by[st.EdgeID] = first
			ids = append(ids, st.EdgeID)
		}
	}
	edges, err := c.Store.EdgesByID(ids)
	g.repeats = map[int64][]store.Edge{}
	for _, e := range edges {
		g.repeats[by[e.ID]] = append(g.repeats[by[e.ID]], e)
	}
	return err
}

// loadGraph reads the edges of steps and every symbol they mention:
// nodes, parents, and candidates of ambiguous edges.
func (c *Ctx) loadGraph(steps []store.Step) (*graph, error) {
	g := &graph{steps: steps, edges: map[int64]store.Edge{}, syms: map[int64]store.Symbol{}}
	ids := make([]int64, len(steps))
	for i, st := range steps {
		ids[i] = st.EdgeID
	}
	es, err := c.Store.EdgesByID(ids)
	if err != nil {
		return nil, err
	}
	var symIDs []int64
	for _, e := range es {
		g.edges[e.ID] = e
		symIDs = append(append(symIDs, e.Src, e.Dst), e.Candidates...)
	}
	if g.syms, err = c.Store.SymbolsByID(symIDs); err != nil {
		return nil, err
	}
	g.deps, err = c.depTargets(es)
	return g, err
}

// treeEntries renders the walk depth first from the root, children
// ordered by call site; each entry carries the lines of every call
// between its two symbols. The --repo filter drops a symbol of another
// repo with everything below it. nested indents by depth.
func (c *Ctx) treeEntries(g *graph, root int64, nested bool) ([]output.Entry, error) {
	keep, err := c.repoFilter()
	if err != nil {
		return nil, err
	}
	var out []output.Entry
	listed := map[string]bool{}
	err = dfs(g.steps, root, g.less, func(st store.Step) (bool, error) {
		if st.Node != 0 && !keep(g.syms[st.Node]) {
			return false, nil
		}
		if key := g.edges[st.EdgeID].DepKey; key != "" {
			at := fmt.Sprintf("%d\x00%s", st.Parent, key)
			if listed[at] {
				return false, nil // one entry per caller and dependency symbol
			}
			listed[at] = true
		}
		e := g.entry(st)
		edge := g.edges[st.EdgeID]
		c.site(&e, g.syms[edge.Src], edge.Line)
		for _, again := range g.repeats[st.EdgeID] {
			c.addSite(&e, g.syms[again.Src], again.Line)
		}
		if nested {
			e.Depth = st.Depth - 1
		}
		out = append(out, e)
		return true, nil
	})
	return c.mergeSites(out), err
}

// less orders sibling steps production code first (the symbol reached,
// see isTestSymbol), then by the call site: the file of the symbol the
// call is in, then the line.
func (g *graph) less(a, b store.Step) bool {
	if ta, tb := g.isTest(a), g.isTest(b); ta != tb {
		return tb
	}
	pa, pb := g.site(a), g.site(b)
	if pa != pb {
		return pa < pb
	}
	return g.edges[a.EdgeID].Line < g.edges[b.EdgeID].Line
}

// isTest reports whether a step reaches test code; an ambiguous step
// reaches none.
func (g *graph) isTest(st store.Step) bool {
	return st.Node != 0 && isTestSymbol(g.syms[st.Node])
}

// site is the path of the file holding a step's edge.
func (g *graph) site(st store.Step) string {
	return g.syms[g.edges[st.EdgeID].Src].Path
}

// entry renders one step: the symbol reached, the call site, and tags
// for the confidence and tests. An ambiguous outbound step lists its
// candidates instead of a symbol, one into a dependency the dependency
// symbol.
func (g *graph) entry(st store.Step) output.Entry {
	e := g.edges[st.EdgeID]
	loc := fmt.Sprintf("%s:%d", g.site(st), e.Line)
	if e.DepKey != "" {
		return g.depStepEntry(st, e, loc)
	}
	if st.Node == 0 {
		keys, names := make([]string, len(e.Candidates)), make([]string, len(e.Candidates))
		for i, id := range e.Candidates {
			keys[i], names[i] = g.syms[id].StableKey, shortName(g.syms[id])
		}
		return output.Entry{Mark: possibly, Name: strings.Join(names, " | "), Loc: loc, RepoID: g.syms[e.Src].RepoID,
			Text: "one of " + strings.Join(keys, ", "), Tags: []string{e.Confidence},
			Extra: map[string]any{"depth": st.Depth, "confidence": e.Confidence, "candidates": keys}}
	}
	return edgeEntry(g.syms[st.Node], e, loc, g.syms[e.Src].RepoID, st.Depth)
}

// edgeEntry renders the symbol at the other end of an edge with the
// edge's location (a path of repo locRepo) and confidence.
func edgeEntry(sy store.Symbol, e store.Edge, loc string, locRepo int64, depth int) output.Entry {
	out := symbolEntry(sy)
	out.Loc, out.RepoID = loc, locRepo
	out.Tags = append([]string{e.Confidence}, out.Tags...)
	if n := len(e.Candidates); e.Confidence == resolve.ConfidenceAmbiguous {
		out.Mark = possibly
		if n > 1 {
			out.Tags = append(out.Tags, fmt.Sprintf("1 of %d", n))
		}
	}
	if e.Kind == "references" {
		out.Tags = append([]string{e.RefKind}, out.Tags...)
	}
	out.Extra = map[string]any{"kind": sy.Kind, "depth": depth, "confidence": e.Confidence, "edge": e.Kind,
		"ref_kind": e.RefKind}
	return out
}

// possibly marks the entry of an edge whose target is one of several
// candidates, and titles the section such entries are listed in.
const possibly = "?"

// repoFilter returns a predicate keeping the symbols of the --repo repo,
// or every symbol without the flag.
func (c *Ctx) repoFilter() (func(store.Symbol) bool, error) {
	id, err := c.filterID()
	if err != nil {
		return nil, err
	}
	return func(sy store.Symbol) bool { return id == 0 || sy.RepoID == id }, nil
}

// sections returns the sections of a list of edges about title: the
// confident ones, then under "possibly ..." those whose target is one of
// several candidates (see possibly), so a guess is never read as a
// fact. A tree stays one section, its ambiguous steps marked in place.
func (c *Ctx) sections(title subject, entries []output.Entry, byFile bool) []output.Section {
	var sure, unsure []output.Entry
	for _, e := range entries {
		if e.Mark == possibly && !nests(entries) {
			unsure = append(unsure, e)
		} else {
			sure = append(sure, e)
		}
	}
	out := c.fileSections(title, sure, byFile)
	if len(unsure) > 0 {
		maybe := subject{name: "possibly " + title.name, key: "possibly " + title.key}
		out = append(out, c.fileSections(maybe, unsure, byFile)...)
	}
	return out
}

// nests reports whether entries form a tree.
func nests(entries []output.Entry) bool {
	return slices.ContainsFunc(entries, func(e output.Entry) bool { return e.Depth > 0 })
}

// fileSections returns one section titled title, or with byFile one
// section per file (repo:path) of the entries' locations, in path order
// with test files last.
func (c *Ctx) fileSections(title subject, entries []output.Entry, byFile bool) []output.Section {
	if !byFile {
		return []output.Section{c.titled(title, entries)}
	}
	files := map[string][]output.Entry{}
	var paths []string
	for _, e := range entries {
		p, _, _ := strings.Cut(e.Loc, ":")
		repo := stablekey.Repo(e.Key)
		if isDepKey(e.Key) {
			repo = c.repoNames()[e.RepoID] // the call site is in the workspace
		}
		if repo != "" {
			p = repo + ":" + p
		}
		if _, ok := files[p]; !ok {
			paths = append(paths, p)
		}
		files[p] = append(files[p], e)
	}
	sort.SliceStable(paths, func(i, j int) bool {
		ti, tj := isTestLoc(paths[i]), isTestLoc(paths[j])
		if ti != tj {
			return tj
		}
		return paths[i] < paths[j]
	})
	out := make([]output.Section, len(paths))
	for i, p := range paths {
		out[i] = c.titled(title.plus(" in "+p), files[p])
	}
	return out
}
