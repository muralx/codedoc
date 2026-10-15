package query

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// flowCap is the most nodes a flow tree lists.
const flowCap = 200

// FlowOpts control a flow query.
type FlowOpts struct {
	// Depth is how many calls deep the tree goes below the entry symbol.
	Depth int
	// DocumentedOnly prunes undocumented nodes; their documented
	// descendants attach to the nearest documented ancestor.
	DocumentedOnly bool
}

// flowNode is one node of a call tree: a symbol reached over a call
// edge, or an ambiguous call listing its candidates.
type flowNode struct {
	sy         store.Symbol
	edge       store.Edge
	candidates []store.Symbol
	// dep is the dependency symbol the call ends at, when deps.db holds
	// it (the edge then names its key and no workspace symbol).
	dep *depSym
	// caller is the symbol the call is in; parent the index of the node
	// it hangs under, -1 for the entry symbol.
	caller store.Symbol
	parent int
	depth  int
	cycle  bool
	// seen is set on a symbol expanded at another node: "see above" or
	// "see below" depending on where that node renders.
	seen string
}

// Flow renders the call tree below an entry symbol, callees in call-site
// order, each node as its signature and one-line doc. The tree is built
// breadth first, so every call at one depth is listed before any call
// below it, and each symbol is expanded once, at its shallowest node:
// its other nodes are tagged see above (or see below) and not expanded
// again. An ambiguous call is listed with its candidates and tagged
// ambiguous but not expanded; a call back to a symbol on the current path
// is tagged cycle and not expanded. The tree stops at flowCap nodes.
func Flow(c *Ctx, addr string, o FlowOpts) (*output.Response, error) {
	if o.Depth < 1 {
		return nil, errors.New("--depth must be at least 1")
	}
	sy, resp, err := c.graphTarget(addr)
	if err != nil {
		return resp, err
	}
	sec, err := c.flowSection(sy, o)
	if err != nil {
		return nil, err
	}
	return c.respond(c.Workspace.Repos, sec), nil
}

// flowSection is the section holding the call tree below sy.
func (c *Ctx) flowSection(sy store.Symbol, o FlowOpts) (output.Section, error) {
	w := &flowWalker{c: c, max: o.Depth, root: sy}
	if err := w.build(); err != nil {
		return output.Section{}, err
	}
	nodes := w.ordered()
	if o.DocumentedOnly {
		nodes = documentedOnly(nodes)
	}
	entries := []output.Entry{flowEntry(flowNode{sy: sy})}
	for _, n := range nodes {
		entries = append(entries, flowEntry(n))
	}
	if w.capped {
		c.notes = append(c.notes, fmt.Sprintf("stopped at %d nodes; lower --depth", flowCap))
	}
	return c.titled(about("flow from ", sy), entries), nil
}

// flowWalker builds a call tree breadth first.
type flowWalker struct {
	c      *Ctx
	max    int
	root   store.Symbol
	nodes  []flowNode
	capped bool
	// expanded maps each expanded symbol to its node (-1: the root).
	expanded map[int64]int
}

// build adds the nodes of the tree level by level, up to flowCap.
func (w *flowWalker) build() error {
	w.expanded = map[int64]int{w.root.ID: -1}
	frontier := []int{-1}
	for depth := 1; depth <= w.max && len(frontier) > 0; depth++ {
		var next []int
		for _, p := range frontier {
			more, err := w.expand(p, depth)
			if err != nil || w.capped {
				return err
			}
			next = append(next, more...)
		}
		frontier = next
	}
	return nil
}

// expand appends the callees of node p at depth and returns the nodes to
// expand at the next depth.
func (w *flowWalker) expand(p, depth int) ([]int, error) {
	caller := w.root
	if p >= 0 {
		caller = w.nodes[p].sy
	}
	edges, syms, err := w.calls(caller.ID)
	if err != nil {
		return nil, err
	}
	deps, err := w.c.depTargets(edges)
	if err != nil {
		return nil, err
	}
	var next []int
	for _, e := range edges {
		if len(w.nodes) == flowCap {
			w.capped = true
			return nil, nil
		}
		n := flowNode{sy: syms[e.Dst], edge: e, caller: caller, parent: p, depth: depth}
		n.reaches(syms, deps)
		_, done := w.expanded[e.Dst]
		switch {
		case e.Dst == 0:
		case w.onPath(p, e.Dst):
			n.cycle = true
		case done:
			n.seen = "see above"
		case depth < w.max:
			w.expanded[e.Dst] = len(w.nodes)
			next = append(next, len(w.nodes))
		}
		w.nodes = append(w.nodes, n)
	}
	return next, nil
}

// calls returns the call edges of a symbol in call-site order with the
// symbols they reach or list as candidates.
func (w *flowWalker) calls(id int64) ([]store.Edge, map[int64]store.Symbol, error) {
	edges, err := w.c.Store.EdgesFrom(id, "calls")
	if err != nil {
		return nil, nil, err
	}
	sort.SliceStable(edges, func(i, j int) bool { return edges[i].Line < edges[j].Line })
	ids := make([]int64, 0, len(edges))
	for _, e := range edges {
		ids = append(append(ids, e.Dst), e.Candidates...)
	}
	syms, err := w.c.Store.SymbolsByID(ids)
	return edges, syms, err
}

// onPath reports whether symbol id is node p or one of its ancestors.
func (w *flowWalker) onPath(p int, id int64) bool {
	for ; p >= 0; p = w.nodes[p].parent {
		if w.nodes[p].sy.ID == id {
			return true
		}
	}
	return id == w.root.ID
}

// ordered returns the nodes depth first, each under its parent in
// call-site order, with see above turned into see below where the
// expanded node renders later.
func (w *flowWalker) ordered() []flowNode {
	children := map[int][]int{}
	for i, n := range w.nodes {
		children[n.parent] = append(children[n.parent], i)
	}
	pos := make([]int, len(w.nodes))
	var order []int
	var visit func(p int)
	visit = func(p int) {
		for _, i := range children[p] {
			pos[i] = len(order)
			order = append(order, i)
			visit(i)
		}
	}
	visit(-1)
	out := make([]flowNode, len(order))
	for k, i := range order {
		n := w.nodes[i]
		if at := w.expanded[n.sy.ID]; n.seen != "" && at >= 0 && pos[at] > k {
			n.seen = "see below"
		}
		out[k] = n
	}
	return out
}

// documentedOnly drops the nodes of undocumented symbols and lifts each
// kept node to the depth of its nearest kept ancestor plus one. Ambiguous
// calls are kept.
func documentedOnly(nodes []flowNode) []flowNode {
	var out []flowNode
	// kept[d] is the depth in the pruned tree of the nearest kept
	// ancestor at original depth d (0 for the entry symbol).
	kept := []int{0}
	for _, n := range nodes {
		kept = kept[:n.depth]
		parent := kept[n.depth-1]
		if n.edge.Dst != 0 && strings.TrimSpace(n.sy.DocText) == "" {
			kept = append(kept, parent)
			continue
		}
		n.depth = parent + 1
		kept = append(kept, n.depth)
		out = append(out, n)
	}
	return out
}

// flowEntry renders a node as `signature — one-line doc` with its
// definition, the call's confidence and the ambiguous or cycle tag. A
// call into a dependency is the dependency symbol, tagged with its
// package.
func flowEntry(n flowNode) output.Entry {
	if n.edge.DepKey != "" {
		return depFlowEntry(n)
	}
	if n.edge.ID != 0 && n.edge.Dst == 0 {
		keys, names := make([]string, len(n.candidates)), make([]string, len(n.candidates))
		for i, cand := range n.candidates {
			keys[i], names[i] = cand.StableKey, shortName(cand)
		}
		return output.Entry{Mark: possibly, Name: strings.Join(names, " | "), Hide: output.HideText,
			Loc: fmt.Sprintf("%s:%d", n.caller.Path, n.edge.Line), RepoID: n.caller.RepoID,
			Text: "one of " + strings.Join(keys, ", "),
			Tags: []string{"ambiguous"}, Depth: n.depth,
			Extra: map[string]any{"depth": n.depth, "candidates": keys, "confidence": n.edge.Confidence}}
	}
	e := symbolEntry(n.sy)
	if e.Doc != "" {
		e.Text += " — " + e.Doc
	}
	e.Doc, e.Depth = "", n.depth
	e.Extra["depth"] = n.depth
	if n.edge.ID != 0 {
		e.Tags = append([]string{n.edge.Confidence}, e.Tags...)
		e.Extra["confidence"] = n.edge.Confidence
	}
	if n.cycle {
		e.Tags = append(e.Tags, "cycle")
	}
	if n.seen != "" {
		e.Tags = append(e.Tags, n.seen)
	}
	return e
}
