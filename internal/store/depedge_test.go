package store_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

const depKey = "npm/zod@3.23.8:ts:zod:index.d.ts/object"

func TestEdgesIntoDependenciesAreReadByKey(t *testing.T) {
	s, ids, fileID, _ := graphFixture(t)
	inBatch(t, s, func(b *store.Batch) error {
		for _, e := range []store.Edge{
			{Src: ids["B"], Kind: "calls", RefKind: "call", Confidence: "import", FileID: fileID, Line: 6, DepKey: depKey},
			{Src: ids["C"], Kind: "references", RefKind: "type", Confidence: "import", FileID: fileID, Line: 10, DepKey: depKey},
			{Src: ids["C"], Dst: ids["A"], Kind: "calls", RefKind: "call", Confidence: "local", FileID: fileID, Line: 11},
		} {
			if err := b.InsertEdge(e); err != nil {
				return err
			}
		}
		return nil
	})
	calls, err := s.EdgesToDep(depKey, "calls")
	if err != nil || len(calls) != 1 || calls[0].Src != ids["B"] || calls[0].DepKey != depKey || calls[0].Dst != 0 {
		t.Fatalf("calls into the dependency: %+v %v", calls, err)
	}
	if len(calls[0].Candidates) != 0 {
		t.Errorf("a dependency edge is not ambiguous: %+v", calls[0])
	}
	if none, err := s.EdgesToDep("other", "calls"); err != nil || len(none) != 0 {
		t.Errorf("unknown key: %+v %v", none, err)
	}
}

func TestDependencyEdgesLeaveAndCount(t *testing.T) {
	s, ids, fileID, _ := graphFixture(t)
	inBatch(t, s, func(b *store.Batch) error {
		for _, e := range []store.Edge{
			{Src: ids["C"], Kind: "references", RefKind: "type", Confidence: "import", FileID: fileID, Line: 10, DepKey: depKey},
			{Src: ids["C"], Dst: ids["A"], Kind: "calls", RefKind: "call", Confidence: "local", FileID: fileID, Line: 11},
			{Src: ids["B"], Kind: "calls", RefKind: "call", Confidence: "import", FileID: fileID, Line: 6, DepKey: depKey},
		} {
			if err := b.InsertEdge(e); err != nil {
				return err
			}
		}
		return nil
	})
	from, err := s.EdgesFrom(ids["C"], "calls", "references")
	if err != nil || len(from) != 2 || from[0].DepKey != depKey || from[1].DepKey != "" {
		t.Errorf("edges leaving C: %+v %v", from, err)
	}
	n, err := s.DepFanIns([]string{depKey, "npm/zod@3.23.8:ts:zod:index.d.ts/unused"})
	eq(t, "fan-in by key", n, map[string]int{depKey: 2})
	if err != nil {
		t.Fatal(err)
	}
	used, err := s.DepKeysUsed("npm/zod@3.23.8", 5)
	eq(t, "keys used of the package", used, []store.DepUseCount{{Key: depKey, N: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if none, err := s.DepKeysUsed("npm/zod@3.23", 5); err != nil || len(none) != 0 {
		t.Errorf("another version: %+v %v", none, err)
	}
}

func TestIndexedDepUsesAndFilesImporting(t *testing.T) {
	s := openTemp(t)
	repoA, _ := seedDeps(t, s)
	inBatch(t, s, func(b *store.Batch) error { return b.SetDepState("npm", "zod", "3.23.8", store.DepIndexed, "") })
	var uses []store.DepUse
	inBatch(t, s, func(b *store.Batch) (err error) { uses, err = b.IndexedDepUses(); return err })
	if len(uses) != 2 || uses[0].Name != "zod" || uses[0].RepoID != repoA || uses[1].Name != "zod" {
		t.Fatalf("indexed uses: %+v", uses)
	}
	var files []int64
	for _, p := range []string{"a.ts", "b.ts", "c.ts"} {
		id, err := s.UpsertFile(store.File{RepoID: repoA, Path: p, Lang: "typescript", Hash: p})
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, id)
	}
	inBatch(t, s, func(b *store.Batch) error {
		if err := b.ReplaceFileImports(files[0], []store.Import{{Raw: "zod", Line: 1}, {Raw: "zod/v4", Line: 2}}); err != nil {
			return err
		}
		if err := b.ReplaceFileImports(files[1], []store.Import{{Raw: "zod/v4/core", Line: 1}}); err != nil {
			return err
		}
		return b.ReplaceFileImports(files[2], []store.Import{{Raw: "zodiac", Line: 1}, {Raw: "./zod", Line: 2}})
	})
	got, err := s.FilesImporting(repoA, []string{"zod", "left-pad", "zod"})
	eq(t, "files importing zod", got, files[:2])
	if err != nil {
		t.Fatal(err)
	}
	if none, err := s.FilesImporting(repoA, nil); err != nil || none != nil {
		t.Errorf("no names: %v %v", none, err)
	}
}

func TestSearchFilterLimitsToRepoSet(t *testing.T) {
	s, _, _, _ := graphFixture(t)
	all, err := s.SymbolMatches(`"A"`, store.SearchFilter{}, 10)
	if err != nil || len(all) != 1 {
		t.Fatalf("unfiltered: %v %v", all, err)
	}
	repo, err := s.RepoByPath("/r")
	if err != nil {
		t.Fatal(err)
	}
	in, err := s.SymbolMatches(`"A"`, store.SearchFilter{RepoIDs: []int64{repo.ID}}, 10)
	eq(t, "in the set", in, all)
	if err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]store.SearchFilter{"other": {RepoIDs: []int64{repo.ID + 7}}, "empty": {RepoIDs: []int64{}}} {
		if out, err := s.SymbolMatches(`"A"`, f, 10); err != nil || len(out) != 0 {
			t.Errorf("%s set: %v %v", name, out, err)
		}
		if out, err := s.ModuleMatches(`"p"`, f, 10); err != nil || len(out) != 0 {
			t.Errorf("%s set, modules: %v %v", name, out, err)
		}
	}
	if mods, err := s.ModuleMatches(`"p"`, store.SearchFilter{RepoIDs: []int64{repo.ID}}, 10); err != nil || len(mods) != 1 {
		t.Errorf("modules in the set: %v %v", mods, err)
	}
}
