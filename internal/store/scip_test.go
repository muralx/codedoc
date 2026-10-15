package store_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

// batchSteps runs steps in one batch, stopping at the first error.
func batchSteps(t *testing.T, s *store.Store, steps ...func(b *store.Batch) error) {
	t.Helper()
	err := s.Batch(func(b *store.Batch) error {
		for _, step := range steps {
			if err := step(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSCIPSymbolRowsReplaceByPrefix(t *testing.T) {
	s, _, _, _ := graphFixture(t)
	rows := []store.SCIPSymbol{{StableKey: "r:go:p:A", SCIP: "s A"}, {StableKey: "r:go:p:B", SCIP: "s B"},
		{StableKey: "gone:go:p:X", SCIP: "s X"}}
	batchSteps(t, s, func(b *store.Batch) error { return b.ReplaceSCIPSymbols(nil, rows) })
	got, err := s.SCIPSymbols()
	if err != nil || len(got) != 2 || got[0].SCIP != "s A" || got[0].Name != "A" || got[0].Kind != "function" {
		t.Fatalf("rows without a symbol are not listed: %+v %v", got, err)
	}
	batchSteps(t, s, func(b *store.Batch) error {
		return b.ReplaceSCIPSymbols([]string{"r:go:"}, []store.SCIPSymbol{{StableKey: "r:go:p:C", SCIP: "s C"}})
	})
	if got, err = s.SCIPSymbols(); err != nil || len(got) != 1 || got[0].StableKey != "r:go:p:C" {
		t.Fatalf("replaced: %+v %v", got, err)
	}
}

func TestPreciseReferenceEdges(t *testing.T) {
	s, ids, fileID, _ := graphFixture(t)
	refs, err := s.CodeReferencesInFile(fileID)
	if err != nil || len(refs) != 3 || refs[0].Line != 6 || refs[2].Line != 21 {
		t.Fatalf("code references: %+v %v", refs, err)
	}
	ref := refs[0]
	call := store.Edge{Src: ids["B"], Kind: "calls", RefKind: "call", Confidence: "ambiguous", FileID: fileID,
		Line: 6, ReferenceID: ref.ID, Candidates: []int64{ids["A"], ids["C"]}}
	precise := store.Edge{Src: ids["B"], Dst: ids["A"], Kind: "calls", RefKind: "call",
		Confidence: store.ConfidencePrecise, FileID: fileID, Line: 6, ReferenceID: ref.ID}
	var added int64
	batchSteps(t, s,
		func(b *store.Batch) error { return b.InsertEdge(call) },
		func(b *store.Batch) error { return b.SetReferenceCandidates(ref.ID, 2) },
		func(b *store.Batch) error { return b.ReplaceReferenceEdges(precise) },
		func(b *store.Batch) (err error) {
			added, err = b.InsertReference(store.Reference{FileID: fileID, SymbolID: ids["C"], Kind: "call",
				Name: "A", Line: 10, Arity: -1})
			return err
		},
		func(b *store.Batch) error {
			return b.InsertEdge(store.Edge{Src: ids["C"], Dst: ids["A"], Kind: "calls", RefKind: "call",
				Confidence: "local", FileID: fileID, Line: 10, ReferenceID: added})
		})
	es, err := s.EdgesFrom(ids["B"], "calls")
	if err != nil || len(es) != 1 || es[0].Dst != ids["A"] || es[0].Confidence != store.ConfidencePrecise ||
		len(es[0].Candidates) != 0 {
		t.Fatalf("edges of B: %+v %v", es, err)
	}
	checkPreciseKept(t, s, ids["A"], added, ref.ID)
}

// checkPreciseKept checks that ref, not added, has a precise edge into
// dst and that deleting heuristic edges keeps it alone.
func checkPreciseKept(t *testing.T, s *store.Store, dst, added, ref int64) {
	t.Helper()
	var all, some map[int64]bool
	batchSteps(t, s,
		func(b *store.Batch) (err error) { all, err = b.PreciseReferences(nil); return err },
		func(b *store.Batch) (err error) { some, err = b.PreciseReferences([]int64{added, ref}); return err },
		func(b *store.Batch) error { return b.DeleteHeuristicEdges() })
	if len(all) != 1 || !all[ref] || len(some) != 1 {
		t.Fatalf("precise references: %v %v", all, some)
	}
	if in, _, _ := s.EdgeCounts(dst); in != 1 {
		t.Fatalf("only the precise edge is kept: %d", in)
	}
}

func TestPreciseImplementsEdges(t *testing.T) {
	s, ids, fileID, _ := graphFixture(t)
	repo, err := s.RepoByPath("/r")
	if err != nil {
		t.Fatal(err)
	}
	refs, err := s.CodeReferencesInFile(fileID)
	if err != nil {
		t.Fatal(err)
	}
	impl := func(src int64, ref int64) store.Edge {
		return store.Edge{Src: src, Dst: ids["I"], Kind: "implements", RefKind: "implement", Confidence: "local",
			FileID: fileID, Line: 1, ReferenceID: ref}
	}
	var pairs map[[2]int64]bool
	var dropped int64
	batchSteps(t, s,
		// A: a detected edge becomes one precise detected edge.
		func(b *store.Batch) error { return b.InsertEdge(impl(ids["A"], 0)) },
		func(b *store.Batch) error { return b.SetPreciseImplements(ids["A"], ids["I"], fileID, 1) },
		// C: an edge from an implements clause becomes precise in place.
		func(b *store.Batch) error { return b.InsertEdge(impl(ids["C"], refs[1].ID)) },
		func(b *store.Batch) error { return b.SetPreciseImplements(ids["C"], ids["I"], fileID, 9) },
		func(b *store.Batch) (err error) { pairs, err = b.PreciseImplements(); return err },
		func(b *store.Batch) (err error) { dropped, err = b.DropPreciseDetected(fileID); return err },
		func(b *store.Batch) error { return b.SetSCIPCommit(repo.ID, "abc") })
	if len(pairs) != 2 || !pairs[[2]int64{ids["A"], ids["I"]}] || dropped != 1 {
		t.Fatalf("pairs %v, dropped %d", pairs, dropped)
	}
	es, err := s.EdgesTo(ids["I"], "implements")
	if err != nil || len(es) != 1 || es[0].Src != ids["C"] || es[0].Confidence != store.ConfidencePrecise {
		t.Fatalf("implements: %+v %v", es, err)
	}
	if r, err := s.RepoByID(repo.ID); err != nil || r.SCIPCommit != "abc" {
		t.Fatalf("scip commit: %+v %v", r, err)
	}
}
