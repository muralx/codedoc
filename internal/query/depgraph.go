package query

import (
	"fmt"
	"strings"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/stablekey"
	"github.com/muralx/codedoc/internal/store"
)

// depTag marks the target of an edge that lies in a dependency.
const depTag = "dep"

// graphTargetIn is graphTarget for the queries that also answer for a
// dependency symbol (see depMode).
func (c *Ctx) graphTargetIn(addr string, mode depMode) (target, *output.Response, error) {
	if err := c.EnsureFresh(c.Workspace.Repos...); err != nil {
		return target{}, nil, err
	}
	filter := c.Filter
	c.Filter = false
	defer func() { c.Filter = filter }()
	t, resp, err := c.target(addr, mode)
	c.draws = append(c.draws, t.sym.RepoID)
	return t, resp, err
}

// depInbound answers callers and refs for a dependency symbol: the
// workspace symbols whose edges of kinds end at its stable key, at the
// edge's location (deps spec section 6). A use through an inherited
// member carries the key of the declaring symbol, which is where an
// address through a subtype leads.
func (c *Ctx) depInbound(d depSym, o GraphOpts, title string, keep func(store.Edge) bool, kinds ...string) (*output.Response,
	error) {
	edges, err := c.Store.EdgesToDep(d.StableKey, kinds...)
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
	if note := d.declaredNote(); note != "" {
		c.notes = append(c.notes, note)
	}
	if o.Depth > 1 {
		c.notes = append(c.notes, "--depth applies to workspace symbols; the direct uses of the dependency symbol are listed")
	}
	return c.respond(c.Workspace.Repos, c.sections(about(title, d.Symbol).plus(" ["+d.pkg.tag()+"]"), entries, o.GroupByFile)...), nil
}

// declaredNote says, for a member asked through a type that inherits
// it, where it is declared: "Server.revision is declared on Protocol".
// It is "" for a symbol asked by its own name.
func (d depSym) declaredNote() string {
	if d.via == "" {
		return ""
	}
	return fmt.Sprintf("%s.%s is declared on %s", d.via, d.Name, containerName(d.Symbol))
}

// depTargets reads the dependency symbols the edges of a graph point at.
func (c *Ctx) depTargets(edges []store.Edge) (map[string]depSym, error) {
	var keys []string
	for _, e := range edges {
		if e.DepKey != "" {
			keys = append(keys, e.DepKey)
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	return c.deps().byKeys(keys)
}

// depKeyName is the name a dependency key ends with, for an entry whose
// symbol deps.db no longer holds.
func depKeyName(key string) string {
	k, err := stablekey.Parse(key)
	if err != nil {
		return ""
	}
	return k.Qualified[strings.LastIndex(k.Qualified, "/")+1:]
}

// depStepEntry renders an outbound step that ends in a dependency: the
// key of the dependency symbol at the call site, tagged dep, with its
// signature and doc when deps.db still holds it.
func (g *graph) depStepEntry(st store.Step, e store.Edge, loc string) output.Entry {
	out := output.Entry{Key: e.DepKey, Name: depKeyName(e.DepKey), Loc: loc, RepoID: g.syms[e.Src].RepoID, Tags: []string{e.Confidence, depTag},
		Extra: map[string]any{"depth": st.Depth, "confidence": e.Confidence, "edge": e.Kind, "ref_kind": e.RefKind,
			"dep": true}}
	if d, ok := g.deps[e.DepKey]; ok {
		out.Name, out.Text, out.Doc = shortName(d.Symbol), d.Signature, firstSentence(d.DocText)
		out.Extra["kind"] = d.Kind
	}
	return out
}
