package store_test

import (
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

type importFixture struct {
	repo, tsFile, goFile, src, pkg int64
}

// seedImports stores two files with imports, resolves the first import of
// the TS file to the package module and adds one module edge.
func seedImports(t *testing.T, s *store.Store) importFixture {
	t.Helper()
	var fx importFixture
	fx.repo, _ = s.UpsertRepo(store.Repo{Workspace: "w", Name: "a", Path: "/a"})
	fx.tsFile, _ = s.UpsertFile(store.File{RepoID: fx.repo, Path: "src/a.ts", Lang: "typescript", Size: 1, Mtime: 1, Hash: "h"})
	fx.goFile, _ = s.UpsertFile(store.File{RepoID: fx.repo, Path: "b.go", Lang: "go", Size: 1, Mtime: 1, Hash: "h"})
	fx.src, _ = s.UpsertModule(store.Module{RepoID: fx.repo, Path: "src", Lang: "ts", Kind: "npm-dir", Identity: "p/src"})
	fx.pkg, _ = s.UpsertModule(store.Module{RepoID: fx.repo, Path: ".", Lang: "ts", Kind: "npm-package", Identity: "p"})
	err := s.Batch(func(b *store.Batch) error {
		imps := []store.Import{{Raw: "p", Names: []string{"A", "B"}, Line: 1}, {Raw: "./x.js", Alias: "x", Line: 2}}
		if err := b.ReplaceFileImports(fx.tsFile, imps); err != nil {
			return err
		}
		return b.ReplaceFileImports(fx.goFile, []store.Import{{Raw: "fmt", Line: 3}})
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.FileImports(fx.tsFile)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Batch(func(b *store.Batch) error {
		if err := b.SetImportModule(stored[0].RowID, fx.pkg); err != nil {
			return err
		}
		return b.ReplaceModuleEdges([]store.ModuleEdge{{Src: fx.src, Dst: fx.pkg, Kind: "imports", Confidence: "import"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	return fx
}

func TestImportsRoundTrip(t *testing.T) {
	s := openTemp(t)
	fx := seedImports(t, s)
	got, err := s.FileImports(fx.tsFile)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	want := store.Import{RowID: got[0].RowID, FileID: fx.tsFile, Raw: "p", Names: []string{"A", "B"}, Line: 1,
		ResolvedModuleID: fx.pkg, RepoID: fx.repo, Path: "src/a.ts", Lang: "typescript"}
	if !reflect.DeepEqual(got[0], want) || got[1].Alias != "x" || got[1].Names != nil || got[1].ResolvedModuleID != 0 {
		t.Fatalf("got %+v", got)
	}
	if err := s.Batch(func(b *store.Batch) error { return b.ReplaceFileImports(fx.tsFile, nil) }); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.FileImports(fx.tsFile); len(left) != 0 {
		t.Fatalf("imports not replaced: %+v", left)
	}
}

func TestModuleEdges(t *testing.T) {
	s := openTemp(t)
	fx := seedImports(t, s)
	edges, err := s.ModuleEdgesFrom(fx.src)
	if err != nil || !reflect.DeepEqual(edges, []store.ModuleEdge{{Src: fx.src, Dst: fx.pkg, Kind: "imports", Confidence: "import"}}) {
		t.Fatalf("edges %+v %v", edges, err)
	}
	if err := s.Batch(func(b *store.Batch) error { return b.ReplaceModuleEdges(nil) }); err != nil {
		t.Fatal(err)
	}
	if edges, _ := s.ModuleEdgesFrom(fx.src); len(edges) != 0 {
		t.Fatalf("edges not replaced: %+v", edges)
	}
}

func TestImportsAtPathAndReposWithModule(t *testing.T) {
	s := openTemp(t)
	fx := seedImports(t, s)
	other, _ := s.UpsertRepo(store.Repo{Workspace: "w", Name: "b", Path: "/b"})
	if _, err := s.UpsertModule(store.Module{RepoID: other, Path: ".", Lang: "ts", Kind: "npm-package", Identity: "p"}); err != nil {
		t.Fatal(err)
	}
	err := s.Batch(func(b *store.Batch) error {
		imps, err := b.ImportsAtPath(fx.repo, "b.go")
		if err != nil || len(imps) != 1 || imps[0].Raw != "fmt" {
			t.Errorf("imports at b.go %+v %v", imps, err)
		}
		repos, err := b.ReposWithModule("ts", "p")
		if err != nil || !reflect.DeepEqual(repoIDs(repos), []int64{fx.repo, other}) || repos[1].Name != "b" {
			t.Errorf("repos with module %+v %v", repos, err)
		}
		none, err := b.ReposWithModule("go", "p")
		if err != nil || len(none) != 0 {
			t.Errorf("no go module p: %+v %v", none, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
