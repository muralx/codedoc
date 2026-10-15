package query

import (
	"fmt"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// reaches records what the call of a node may end at besides its
// symbol: the candidates of an ambiguous call, and the dependency
// symbol of a call into a package.
func (n *flowNode) reaches(syms map[int64]store.Symbol, deps map[string]depSym) {
	for _, cand := range n.edge.Candidates {
		n.candidates = append(n.candidates, syms[cand])
	}
	if d, ok := deps[n.edge.DepKey]; ok {
		n.dep = &d
	}
}

// depFlowEntry renders a call into a dependency: the dependency symbol
// as depEntry prints it, or, when deps.db no longer holds it, the name
// its key ends with at the call site, tagged dep.
func depFlowEntry(n flowNode) output.Entry {
	extra := map[string]any{"depth": n.depth, "confidence": n.edge.Confidence, "dep": true}
	if n.dep == nil {
		return output.Entry{Key: n.edge.DepKey, Name: depKeyName(n.edge.DepKey), RepoID: n.caller.RepoID,
			Loc: fmt.Sprintf("%s:%d", n.caller.Path, n.edge.Line), Tags: []string{n.edge.Confidence, depTag},
			Depth: n.depth, Extra: extra}
	}
	e := depEntry(*n.dep)
	if e.Doc != "" {
		e.Text += " — " + e.Doc
	}
	e.Doc, e.Depth = "", n.depth
	e.Tags = append([]string{n.edge.Confidence}, e.Tags...)
	e.Extra["depth"], e.Extra["confidence"] = n.depth, n.edge.Confidence
	return e
}
