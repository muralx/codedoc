package store_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func TestDocStateReads(t *testing.T) {
	s := openTemp(t)
	repoID, fileID := symbolFixture(t, s)
	modID, err := s.UpsertModule(store.Module{RepoID: repoID, Path: "p", Lang: "go", Kind: "go-package", Identity: "x/p"})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := s.ReplaceFileSymbols(fileID, modID, sampleSymbols())
	if err != nil {
		t.Fatal(err)
	}
	get, typ := ids["r:go:p:T.Get"], ids["r:go:p:T"]
	err = s.Batch(func(b *store.Batch) error {
		if err := b.InsertEdge(store.Edge{Src: typ, Dst: get, Kind: "calls", RefKind: "call", Confidence: "local",
			FileID: fileID, Line: 5}); err != nil {
			return err
		}
		priors, err := b.DocPriors(fileID)
		if p := priors["r:go:p:T"]; err != nil || p.DocHash != "dh" || p.BodyHash != "b" {
			t.Fatalf("priors %+v %v", priors, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	checkGapReads(t, s, repoID)
	checkCounts(t, s, repoID)
	checkReview(t, s, get, typ)
}

func checkGapReads(t *testing.T, s *store.Store, repoID int64) {
	t.Helper()
	gaps, err := s.GapSymbols(repoID)
	if err != nil || len(gaps) != 2 {
		t.Fatalf("gap symbols %+v %v", gaps, err)
	}
	if g := gaps[1]; g.Qualified != "T.Get" || g.FanIn != 1 || g.ContainerKind != "struct" || g.ContainerDoc != "ok" {
		t.Fatalf("gap symbol %+v", g)
	}
	mods, err := s.GapModules(repoID)
	if err != nil || len(mods) != 1 || mods[0].Identity != "x/p" {
		t.Fatalf("gap modules %+v %v", mods, err)
	}
}

func checkCounts(t *testing.T, s *store.Store, repoID int64) {
	t.Helper()
	n, err := s.RepoDocCounts(repoID)
	if err != nil || n.Symbols["ok"] != 1 || n.Symbols["missing"] != 2 || n.Modules["missing"] != 1 {
		t.Fatalf("counts %+v %v", n, err)
	}
	syms, err := s.RepoSymbols(repoID, "go")
	if err != nil || len(syms) != 3 {
		t.Fatalf("repo symbols %d %v", len(syms), err)
	}
}

func checkReview(t *testing.T, s *store.Store, get, typ int64) {
	t.Helper()
	if err := s.MarkDocReviewed(get); err != nil {
		t.Fatal(err)
	}
	if sy, _ := s.SymbolByID(get); sy.DocState != "missing" {
		t.Fatalf("an undocumented symbol stays missing: %+v", sy)
	}
	if err := s.MarkDocReviewed(typ); err != nil {
		t.Fatal(err)
	}
	if sy, _ := s.SymbolByID(typ); sy.DocState != "ok" || sy.DocBodyHash != "b" {
		t.Fatalf("reviewed %+v", sy)
	}
}

func TestRepoReferencesNamed(t *testing.T) {
	s := openTemp(t)
	repoID, fileID := symbolFixture(t, s)
	ids, err := s.ReplaceFileSymbols(fileID, 0, sampleSymbols())
	if err != nil {
		t.Fatal(err)
	}
	err = s.Batch(func(b *store.Batch) error {
		return b.ReplaceFileReferences(fileID, []store.Reference{
			{SymbolID: ids["r:go:p:T.Get"], Kind: "call", Name: "ListenAndServe", Qualifier: "http", Line: 13},
			{Kind: "call", Name: "ListenAndServe", Line: 1},
			{SymbolID: ids["r:go:p:T.Get"], Kind: "call", Name: "Other", Line: 13},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	refs, err := s.RepoReferencesNamed(repoID, []string{"ListenAndServe"})
	if err != nil || len(refs) != 1 || refs[0].Qualifier != "http" {
		t.Fatalf("%+v %v", refs, err)
	}
	if refs, err := s.RepoReferencesNamed(repoID, nil); err != nil || refs != nil {
		t.Fatalf("no names: %+v %v", refs, err)
	}
}
