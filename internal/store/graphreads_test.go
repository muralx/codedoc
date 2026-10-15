package store_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func TestBatchedReadsByID(t *testing.T) {
	s, ids, _, modID := graphFixture(t)
	syms, err := s.SymbolsByID([]int64{ids["A"], ids["C"], 999})
	if err != nil || len(syms) != 2 || syms[ids["C"]].Qualified != "C" {
		t.Fatalf("%+v %v", syms, err)
	}
	mods, err := s.ModulesByID([]int64{modID, 999})
	if err != nil || len(mods) != 1 || mods[modID].Identity != "p" {
		t.Fatalf("%+v %v", mods, err)
	}
}

func TestEdgesToModuleAndCounts(t *testing.T) {
	s, ids, fileID, modID := graphFixture(t)
	edges(t, s, ids, fileID)
	in, err := s.EdgesToModule(modID, "calls")
	if err != nil || len(in) != 3 {
		t.Fatalf("calls into p, the ambiguous one included: %+v %v", in, err)
	}
	if n, err := s.AmbiguousEdgesTo(ids["C"]); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if n, err := s.FileTotal(); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestOverCapReferences(t *testing.T) {
	s, _, _, _ := graphFixture(t)
	inBatch(t, s, func(b *store.Batch) error {
		all, err := b.References(nil, nil)
		if err != nil {
			return err
		}
		return b.SetReferenceCandidates(all[0].ID, 30)
	})
	if n, err := s.OverCapReferences("A", "call"); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if n, err := s.OverCapReferences("A", "type"); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestMetaRoundTrip(t *testing.T) {
	s := openTemp(t)
	if _, ok, err := s.Meta("k"); ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	inBatch(t, s, func(b *store.Batch) error { return b.SetMeta("k", "v") })
	if v, ok, err := s.Meta("k"); v != "v" || !ok || err != nil {
		t.Fatalf("%q %v %v", v, ok, err)
	}
	inBatch(t, s, func(b *store.Batch) error {
		if v, ok, err := b.Meta("k"); v != "v" || !ok || err != nil {
			t.Errorf("in batch: %q %v %v", v, ok, err)
		}
		return b.DeleteMeta("k")
	})
	if _, ok, err := s.Meta("k"); ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
}

func TestReferencesThroughAField(t *testing.T) {
	s, _, fileID, _ := graphFixture(t)
	inBatch(t, s, func(b *store.Batch) error {
		through, err := b.ReferencesThrough([]string{"x"})
		if err != nil || len(through) != 1 || through[0].Name != "B" {
			t.Errorf("through x: %+v %v", through, err)
		}
		q, err := b.FieldQualifierAt(fileID, 10, 0, "B")
		if err != nil || q != "" {
			t.Errorf("a call is no field reference: %q %v", q, err)
		}
		return nil
	})
}

func TestReferencesKeyedByAConstant(t *testing.T) {
	s, _, _, _ := graphFixture(t)
	inBatch(t, s, func(b *store.Batch) error {
		all, err := b.References(nil, nil)
		if err != nil {
			return err
		}
		if err := b.SetReferenceConfigKey(all[1].ID, "APP_MODE"); err != nil {
			return err
		}
		keyed, err := b.ReferencesKeyed([]string{"APP_MODE", "OTHER"})
		if err != nil || len(keyed) != 1 || keyed[0].ID != all[1].ID {
			t.Errorf("keyed: %+v %v", keyed, err)
		}
		if err := b.SetReferenceConfigKey(all[1].ID, ""); err != nil {
			return err
		}
		if keyed, _ := b.ReferencesKeyed([]string{"APP_MODE"}); len(keyed) != 0 {
			t.Errorf("cleared key still matches: %+v", keyed)
		}
		return nil
	})
}

func TestFieldQualifierAt(t *testing.T) {
	s, ids, fileID, _ := graphFixture(t)
	inBatch(t, s, func(b *store.Batch) error {
		return b.ReplaceFileReferences(fileID, []store.Reference{
			{SymbolID: ids["B"], Kind: "field", Name: "KEY", Qualifier: "cfg", Line: 6, Col: 12, Arity: -1}})
	})
	inBatch(t, s, func(b *store.Batch) error {
		q, err := b.FieldQualifierAt(fileID, 6, 12, "KEY")
		if err != nil || q != "cfg" {
			t.Errorf("%q %v", q, err)
		}
		return nil
	})
}

func TestPreciseEdgesAndDeletes(t *testing.T) {
	s, ids, fileID, _ := graphFixture(t)
	edges(t, s, ids, fileID)
	if got, err := s.PreciseEdgesInFiles(nil); got != nil || err != nil {
		t.Fatalf("%+v %v", got, err)
	}
	inBatch(t, s, func(b *store.Batch) error {
		return b.InsertEdge(store.Edge{Src: ids["A"], Dst: ids["B"], Kind: "calls", RefKind: "call",
			Confidence: store.ConfidencePrecise, FileID: fileID, Line: 2})
	})
	precise, err := s.PreciseEdgesInFiles([]int64{fileID})
	if err != nil || len(precise) != 1 || precise[0].Src != ids["A"] || precise[0].ReferenceID != 0 {
		t.Fatalf("%+v %v", precise, err)
	}
	inBatch(t, s, func(b *store.Batch) error {
		if err := b.DeleteEdge(precise[0].ID); err != nil {
			return err
		}
		refs, err := b.References(nil, nil)
		if err != nil {
			return err
		}
		return b.DeleteReference(refs[0].ID)
	})
	if got, _ := s.PreciseEdgesInFiles([]int64{fileID}); len(got) != 0 {
		t.Fatalf("edge not deleted: %+v", got)
	}
	if in, _ := s.EdgesTo(ids["A"], "calls"); len(in) != 1 {
		t.Fatalf("the edge of the deleted reference survived: %+v", in)
	}
}

func TestTargetReads(t *testing.T) {
	s, ids, fileID, modID := graphFixture(t)
	inBatch(t, s, func(b *store.Batch) error {
		if err := b.ReplaceFileReferences(fileID, []store.Reference{
			{SymbolID: ids["I"], Kind: "type", Name: "Other", Line: 14, Arity: -1}}); err != nil {
			return err
		}
		return b.InsertEdge(store.Edge{Src: ids["C"], Dst: ids["I"], Kind: "extends", RefKind: "extend",
			Confidence: "local", FileID: fileID, Line: 9})
	})
	inBatch(t, s, func(b *store.Batch) error {
		checkTargetsByName(t, b, ids, modID)
		embeds, err := b.Embeds("go")
		if err != nil || len(embeds) != 1 || embeds[0].Src != ids["I"] || embeds[0].Dst != 0 {
			t.Errorf("embeds: %+v %v", embeds, err)
		}
		sup, err := b.Supertypes(ids["C"])
		if err != nil || len(sup) != 1 || sup[0].ID != ids["I"] {
			t.Errorf("supertypes: %+v %v", sup, err)
		}
		return nil
	})
}

func TestNearestKeepsTheBestK(t *testing.T) {
	s := openTemp(t)
	repoID, _ := symbolFixture(t, s)
	vecs := map[string][]float32{"a": {1, 0, 0, 0, 0}, "b": {0, 1, 0, 0, 0}, "c": {0.9, 0.1, 0, 0, 0.2},
		"d": {0.8, 0, 0, 0, 0.6}, "e": {0, 0, 1, 0, 0}}
	ids := map[string]int64{}
	inBatch(t, s, func(b *store.Batch) error {
		for _, name := range []string{"a", "b", "c", "d", "e"} {
			id, err := b.UpsertModule(store.Module{RepoID: repoID, Path: name, Lang: "go", Kind: "package", Identity: name})
			if err != nil {
				return err
			}
			if err := b.SetModuleDoc(id, "Package "+name+".", name+"/doc.go", "ok"); err != nil {
				return err
			}
			if err := b.UpsertModuleVec(id, "h", vecs[name]); err != nil {
				return err
			}
			ids[name] = id
		}
		return nil
	})
	// Scores: d 1.1, a and c 1.0 (the tie goes to the lower id), b and e 0.
	got, err := s.NearestModules([]float32{1, 0, 0, 0, 0.5}, store.SearchFilter{}, 2)
	if err != nil || len(got) != 2 || got[0].ID != ids["d"] || got[1].ID != ids["a"] {
		t.Fatalf("%+v %v (ids %v)", got, err, ids)
	}
}

// checkTargetsInModules checks the target lookups by name and by
// container within modules.
func checkTargetsInModules(t *testing.T, b *store.Batch, ids map[string]int64, modID int64) {
	t.Helper()
	in, err := b.TargetsNamedIn("B", []int64{modID})
	if err != nil || len(in) != 1 || in[0].ID != ids["B"] {
		t.Errorf("named B in the module: %+v %v", in, err)
	}
	if out, err := b.TargetsNamedIn("B", []int64{modID + 100}); err != nil || len(out) != 0 {
		t.Errorf("named B in another module: %+v %v", out, err)
	}
	if fields, err := b.TargetMembers("B", "field", []int64{modID}); err != nil || len(fields) != 0 {
		t.Errorf("a function holds no field: %+v %v", fields, err)
	}
}

// checkTargetsByName checks the target lookups by qualified name and by
// kind and name.
func checkTargetsByName(t *testing.T, b *store.Batch, ids map[string]int64, modID int64) {
	t.Helper()
	byQ, err := b.TargetsByQualified("B", []int64{modID})
	if err != nil || len(byQ) != 1 || byQ[0].ID != ids["B"] || byQ[0].Lang != "go" {
		t.Errorf("by qualified: %+v %v", byQ, err)
	}
	checkTargetsInModules(t, b, ids, modID)
	named, err := b.TargetsOfKindsNamed("go", []string{"function"}, []string{"A", "I", "T"})
	if err != nil || len(named) != 2 {
		t.Errorf("functions A and T: %+v %v", named, err)
	}
}
