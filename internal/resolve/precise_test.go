package resolve_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/store"
)

// TestHeuristicResolutionKeepsPreciseEdges marks edges precise the way a
// SCIP merge does, with targets heuristics would not pick, and checks
// that scoped and full heuristic passes leave them alone.
func TestHeuristicResolutionKeepsPreciseEdges(t *testing.T) {
	w := newWS(t, [2]string{"go-lib", "go-lib"})
	w.indexAll()
	lookup, memGet := w.sym(lib+"service:Service.Lookup"), w.sym(lib+"store:MemStore.Get")
	null, iface := w.sym(lib+"store:NullStore"), w.sym(lib+"store:Store")
	es, err := w.s.EdgesFrom(lookup, resolve.EdgeKind("call"))
	if err != nil || len(es) != 2 || es[0].Line != 18 {
		t.Fatalf("edges of Lookup: %+v %v", es, err)
	}
	get := es[0]
	nullSym, err := w.s.SymbolByID(null)
	if err != nil {
		t.Fatal(err)
	}
	err = w.s.Batch(func(b *store.Batch) error {
		get.ID, get.Dst, get.Confidence = 0, memGet, resolve.ConfidencePrecise
		if err := b.ReplaceReferenceEdges(get); err != nil {
			return err
		}
		return b.SetPreciseImplements(null, iface, nullSym.FileID, nullSym.StartLine)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []*resolve.RefScope{{Names: []string{"Get"}}, nil} {
		if err := resolve.ReferencesThen(w.s, scope, nil); err != nil {
			t.Fatal(err)
		}
		w.want(w.out(lib+"service:Service.Lookup", "calls"),
			lib+"service:Service.Lookup -> "+lib+"store:MemStore.Get [calls call precise]")
		if got := w.out(lib+"store:NullStore", "implements"); len(got) != 1 ||
			got[0] != lib+"store:NullStore -> "+lib+"store:Store [implements implement precise]" {
			t.Fatalf("NullStore implements: %v", got)
		}
	}
	if resolve.EdgeKind("config") != "" {
		t.Fatal("config references yield no symbol edge")
	}
}
