package store_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

// graphFixture stores symbols A, B, C (functions), I (interface) and T
// (a test) in one file of module p, references from B to A, from C to B
// and from T to A, and returns the store, the ids by name and the file
// and module ids.
func graphFixture(t *testing.T) (*store.Store, map[string]int64, int64, int64) {
	t.Helper()
	s := openTemp(t)
	repoID, fileID := symbolFixture(t, s)
	modID, err := s.UpsertModule(store.Module{RepoID: repoID, Path: "p", Lang: "go", Kind: "go-package", Identity: "p"})
	if err != nil {
		t.Fatal(err)
	}
	test := sym("function", "T", "", 20, 22)
	test.IsTest = true
	syms := []store.Symbol{sym("function", "A", "", 1, 3), sym("function", "B", "", 5, 7), sym("function", "C", "", 9, 11),
		sym("interface", "I", "", 13, 15), test}
	keys, err := s.ReplaceFileSymbols(fileID, modID, syms)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, sy := range syms {
		ids[sy.Qualified] = keys[sy.StableKey]
	}
	refs := []store.Reference{
		{SymbolID: ids["B"], Kind: "call", Name: "A", Line: 6, Arity: 0},
		{SymbolID: ids["C"], Kind: "call", Name: "B", Qualifier: "x", Line: 10, Arity: 1},
		{SymbolID: ids["T"], Kind: "call", Name: "A", Line: 21, Arity: 0},
		{Kind: "type", Name: "I", Line: 30, Arity: -1},
	}
	if err := s.Batch(func(b *store.Batch) error { return b.ReplaceFileReferences(fileID, refs) }); err != nil {
		t.Fatal(err)
	}
	return s, ids, fileID, modID
}

func TestReferencesReadsAndScopes(t *testing.T) {
	s, _, fileID, _ := graphFixture(t)
	err := s.Batch(func(b *store.Batch) error {
		all, err := b.References(nil, nil)
		if err != nil || len(all) != 3 || all[1].Qualifier != "x" || all[1].Enclosing != "C" || all[1].Arity != 1 {
			t.Errorf("file-level references are not listed: %+v %v", all, err)
		}
		named, err := b.References([]int64{}, []string{"A"})
		if err != nil || len(named) != 2 {
			t.Errorf("%+v %v", named, err)
		}
		both, err := b.References([]int64{fileID}, []string{"A"})
		if err != nil || len(both) != 3 {
			t.Errorf("deduplicated: %+v %v", both, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnresolvedReferencesKeepTheirCandidateCount(t *testing.T) {
	s, ids, fileID, _ := graphFixture(t)
	err := s.Batch(func(b *store.Batch) error {
		all, err := b.References(nil, nil)
		if err != nil {
			return err
		}
		return b.SetReferenceCandidates(all[0].ID, 25)
	})
	if err != nil {
		t.Fatal(err)
	}
	un, err := s.UnresolvedReferences(fileID)
	if err != nil || len(un) != 4 || un[0].Candidates != 25 || un[0].SymbolID != ids["B"] {
		t.Fatalf("%+v %v", un, err)
	}
}

// edges stores B -> A, C -> B, and T -> {A, C} ambiguous, with the
// references they come from, and I <- C detected without a reference.
func edges(t *testing.T, s *store.Store, ids map[string]int64, fileID int64) {
	t.Helper()
	err := s.Batch(func(b *store.Batch) error {
		refs, err := b.References(nil, nil)
		if err != nil {
			return err
		}
		for _, e := range []store.Edge{
			{Src: ids["B"], Dst: ids["A"], Kind: "calls", RefKind: "call", Confidence: "local", FileID: fileID, Line: 6, ReferenceID: refs[0].ID},
			{Src: ids["C"], Dst: ids["B"], Kind: "calls", RefKind: "call", Confidence: "unique", FileID: fileID, Line: 10, ReferenceID: refs[1].ID},
			{Src: ids["T"], Kind: "calls", RefKind: "call", Confidence: "ambiguous", FileID: fileID, Line: 21,
				ReferenceID: refs[2].ID, Candidates: []int64{ids["A"], ids["C"]}},
			{Src: ids["C"], Dst: ids["I"], Kind: "implements", RefKind: "implement", Confidence: "local", FileID: fileID, Line: 9},
		} {
			if err := b.InsertEdge(e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEdgeReads(t *testing.T) {
	s, ids, fileID, _ := graphFixture(t)
	edges(t, s, ids, fileID)
	in, err := s.EdgesTo(ids["A"], "calls")
	if err != nil || len(in) != 2 || in[0].Src != ids["B"] || in[1].Dst != 0 || len(in[1].Candidates) != 2 {
		t.Fatalf("%+v %v", in, err)
	}
	out, err := s.EdgesFrom(ids["C"], "calls", "implements")
	if err != nil || len(out) != 2 || out[0].Line != 9 {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestEdgeReadsByIDAndCounts(t *testing.T) {
	s, ids, fileID, _ := graphFixture(t)
	edges(t, s, ids, fileID)
	in, _ := s.EdgesTo(ids["A"], "calls")
	byID, err := s.EdgesByID([]int64{in[1].ID})
	if err != nil || len(byID) != 1 || byID[0].Candidates[1] != ids["C"] {
		t.Fatalf("%+v %v", byID, err)
	}
	if n, m, err := s.EdgeCounts(ids["C"]); err != nil || n != 1 || m != 2 {
		t.Fatalf("C: in %d out %d %v", n, m, err)
	}
	if un, _ := s.UnresolvedReferences(fileID); len(un) != 1 {
		t.Fatalf("only the file-level reference has no edge: %+v", un)
	}
	checkFanIns(t, s, ids)
}

// checkFanIns checks that the batched fan-ins agree with EdgeCounts.
func checkFanIns(t *testing.T, s *store.Store, ids map[string]int64) {
	t.Helper()
	all := make([]int64, 0, len(ids))
	for _, id := range ids {
		all = append(all, id)
	}
	fan, err := s.FanIns(all)
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range ids {
		if in, _, _ := s.EdgeCounts(id); fan[id] != in {
			t.Fatalf("%s: fan-in %d, EdgeCounts %d", name, fan[id], in)
		}
	}
	if fan, err := s.FanIns(nil); err != nil || len(fan) != 0 {
		t.Fatalf("%v %v", fan, err)
	}
}

func TestWalk(t *testing.T) {
	s, ids, fileID, _ := graphFixture(t)
	edges(t, s, ids, fileID)
	up, err := s.Walk(ids["A"], store.WalkOpts{Inbound: true, Kinds: []string{"calls"}, Depth: 3, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	// A <- B, A <- T (candidate), then B <- C, and C <- T through its
	// second candidate.
	if len(up) != 4 || up[0].Depth != 1 || up[2].Node != ids["C"] || up[2].Parent != ids["B"] || up[3].Node != ids["T"] {
		t.Fatalf("%+v", up)
	}
}

func TestWalkSkipsTestsAndCaps(t *testing.T) {
	s, ids, fileID, _ := graphFixture(t)
	edges(t, s, ids, fileID)
	noTests, err := s.Walk(ids["A"], store.WalkOpts{Inbound: true, Kinds: []string{"calls"}, Depth: 3, Limit: 100, NoTests: true})
	if err != nil || len(noTests) != 2 {
		t.Fatalf("%+v %v", noTests, err)
	}
	all, err := s.FileIDsWhere(func(string) bool { return true })
	if err != nil || len(all) != 1 || all[0] != fileID {
		t.Fatalf("%v %v", all, err)
	}
	inTestFile, err := s.Walk(ids["A"], store.WalkOpts{Inbound: true, Kinds: []string{"calls"}, Depth: 3, Limit: 100,
		NoTests: true, TestFiles: all})
	if err != nil || len(inTestFile) != 0 {
		t.Fatalf("the symbols of a test file are tests: %+v %v", inTestFile, err)
	}
	down, err := s.Walk(ids["C"], store.WalkOpts{Kinds: []string{"calls"}, Depth: 1, Limit: 100, NoTests: true,
		TestFiles: all})
	if err != nil || len(down) != 0 {
		t.Fatalf("outbound --no-tests: %+v %v", down, err)
	}
	capped, err := s.Walk(ids["A"], store.WalkOpts{Inbound: true, Kinds: []string{"calls"}, Depth: 3, Limit: 1})
	if err != nil || len(capped) != 1 {
		t.Fatalf("%+v %v", capped, err)
	}
}

func TestWalkOutbound(t *testing.T) {
	s, ids, fileID, _ := graphFixture(t)
	edges(t, s, ids, fileID)
	down, err := s.Walk(ids["T"], store.WalkOpts{Kinds: []string{"calls"}, Depth: 2, Limit: 100})
	if err != nil || len(down) != 1 || down[0].Node != 0 {
		t.Fatalf("an ambiguous callee is listed, not followed: %+v %v", down, err)
	}
	down, err = s.Walk(ids["C"], store.WalkOpts{Kinds: []string{"calls"}, Depth: 1, Limit: 100})
	if err != nil || len(down) != 1 || down[0].Node != ids["B"] {
		t.Fatalf("%+v %v", down, err)
	}
}

func TestEdgeDeletes(t *testing.T) {
	s, ids, fileID, modID := graphFixture(t)
	edges(t, s, ids, fileID)
	err := s.Batch(func(b *store.Batch) error {
		refs, err := b.References(nil, nil)
		if err != nil {
			return err
		}
		if err := b.DeleteReferenceEdges([]int64{refs[0].ID}); err != nil {
			return err
		}
		return b.DeleteDetectedEdges("implements", []int64{modID})
	})
	if err != nil {
		t.Fatal(err)
	}
	if in, out, _ := s.EdgeCounts(ids["B"]); in != 1 || out != 0 {
		t.Fatalf("B: %d %d", in, out)
	}
	if in, _, _ := s.EdgeCounts(ids["I"]); in != 0 {
		t.Fatal("detected implements edge kept")
	}
	if err := s.Batch(func(b *store.Batch) error { return b.DeleteHeuristicEdges() }); err != nil {
		t.Fatal(err)
	}
	if in, _, _ := s.EdgeCounts(ids["A"]); in != 0 {
		t.Fatal("edges kept")
	}
}

func TestTargets(t *testing.T) {
	s, _, _, modID := graphFixture(t)
	err := s.Batch(func(b *store.Batch) error {
		all, err := b.Targets(nil)
		if err != nil || len(all) != 5 || all[0].Lang != "go" || all[0].ModuleID != modID {
			t.Errorf("%+v %v", all, err)
		}
		named, err := b.Targets([]string{"A", "I"})
		if err != nil || len(named) != 2 {
			t.Errorf("%+v %v", named, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTargetsOfKinds(t *testing.T) {
	s, _, _, modID := graphFixture(t)
	err := s.Batch(func(b *store.Batch) error {
		ifaces, err := b.TargetsOfKinds("go", []string{"interface"}, nil)
		if err != nil || len(ifaces) != 1 || ifaces[0].Name != "I" {
			t.Errorf("%+v %v", ifaces, err)
		}
		inMod, err := b.TargetsOfKinds("go", []string{"function"}, []int64{modID})
		if err != nil || len(inMod) != 4 {
			t.Errorf("%+v %v", inMod, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTargetsOfKindsOutsideModulesAndModuleEdges(t *testing.T) {
	s, _, _, modID := graphFixture(t)
	err := s.Batch(func(b *store.Batch) error {
		none, err := b.TargetsOfKinds("go", []string{"function"}, []int64{modID + 1})
		if err != nil || len(none) != 0 {
			t.Errorf("%+v %v", none, err)
		}
		me, err := b.ModuleEdges()
		if err != nil || len(me) != 0 {
			t.Errorf("%+v %v", me, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
