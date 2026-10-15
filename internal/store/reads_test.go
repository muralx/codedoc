package store_test

import (
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

// inBatch runs fn inside a batch and fails the test on error.
func inBatch(t *testing.T, s *store.Store, fn func(b *store.Batch) error) {
	t.Helper()
	if err := s.Batch(fn); err != nil {
		t.Fatal(err)
	}
}

func TestBatchImportsScoped(t *testing.T) {
	s := openTemp(t)
	fx := seedImports(t, s)
	inBatch(t, s, func(b *store.Batch) error {
		all, err := b.Imports(nil)
		if err != nil || len(all) != 3 {
			t.Errorf("all %+v %v", all, err)
		}
		some, err := b.Imports([]int64{fx.goFile})
		if err != nil || len(some) != 1 || some[0].Raw != "fmt" {
			t.Errorf("scoped %+v %v", some, err)
		}
		none, err := b.Imports([]int64{})
		if err != nil || len(none) != 0 {
			t.Errorf("empty scope %+v %v", none, err)
		}
		return nil
	})
}

func TestBatchImportsInDir(t *testing.T) {
	s := openTemp(t)
	fx := seedImports(t, s)
	deep, _ := s.UpsertFile(store.File{RepoID: fx.repo, Path: "src/x/c.ts", Lang: "typescript", Size: 1, Mtime: 1, Hash: "h"})
	sib, _ := s.UpsertFile(store.File{RepoID: fx.repo, Path: "src0.ts", Lang: "typescript", Size: 1, Mtime: 1, Hash: "h"})
	inBatch(t, s, func(b *store.Batch) error {
		for _, f := range []int64{deep, sib} {
			if err := b.ReplaceFileImports(f, []store.Import{{Raw: "q", Line: 1}}); err != nil {
				return err
			}
		}
		return nil
	})
	inBatch(t, s, func(b *store.Batch) error {
		src, err := b.ImportsInDir(fx.repo, "src")
		if err != nil || len(src) != 2 || src[0].FileID != fx.tsFile {
			t.Errorf("src %+v %v", src, err)
		}
		root, err := b.ImportsInDir(fx.repo, ".")
		if err != nil || len(root) != 2 || root[0].FileID != fx.goFile || root[1].FileID != sib {
			t.Errorf("root %+v %v", root, err)
		}
		return nil
	})
}

func TestBatchModuleLookups(t *testing.T) {
	s := openTemp(t)
	fx := seedImports(t, s)
	deep, _ := s.UpsertModule(store.Module{RepoID: fx.repo, Path: "z/copy", Lang: "ts", Kind: "npm-package", Identity: "p"})
	inBatch(t, s, func(b *store.Batch) error {
		mods, err := b.Modules()
		if err != nil || len(mods) != 3 || mods[2].ID != deep {
			t.Errorf("modules %+v %v", mods, err)
		}
		if m, err := b.ModuleByIdentity(fx.repo, "ts", "p"); err != nil || m.ID != fx.pkg {
			t.Errorf("shallowest module first: %+v %v", m, err)
		}
		if m, err := b.ModuleByPath(fx.repo, "ts", "src"); err != nil || m.ID != fx.src {
			t.Errorf("by path %+v %v", m, err)
		}
		return nil
	})
}

func TestBatchProvidersAndFiles(t *testing.T) {
	s := openTemp(t)
	fx := seedImports(t, s)
	if err := s.ReplaceRepoIdentities(fx.repo, []store.RepoIdentity{{Lang: "ts", Identity: "p", Path: "package.json"}}); err != nil {
		t.Fatal(err)
	}
	inBatch(t, s, func(b *store.Batch) error {
		provs, err := b.Providers()
		if err != nil || len(provs) != 1 || provs[0].Repo.ID != fx.repo || provs[0].Identity != "p" {
			t.Errorf("providers %+v %v", provs, err)
		}
		if repos, err := b.ReposProviding("ts", "p"); err != nil || len(repos) != 1 {
			t.Errorf("repos providing %+v %v", repos, err)
		}
		if ok, err := b.AnyFile(fx.repo, []string{"nope.ts", "src/a.ts"}); err != nil || !ok {
			t.Errorf("AnyFile %v %v", ok, err)
		}
		if ok, err := b.AnyFile(fx.repo, []string{"nope.ts"}); err != nil || ok {
			t.Errorf("AnyFile missing %v %v", ok, err)
		}
		return nil
	})
}

func TestReplaceModuleEdgesFrom(t *testing.T) {
	s := openTemp(t)
	fx := seedImports(t, s)
	other, _ := s.UpsertModule(store.Module{RepoID: fx.repo, Path: "o", Lang: "ts", Kind: "npm-dir", Identity: "p/o"})
	inBatch(t, s, func(b *store.Batch) error {
		return b.ReplaceModuleEdges([]store.ModuleEdge{
			{Src: fx.src, Dst: fx.pkg, Kind: "imports", Confidence: "import"},
			{Src: other, Dst: fx.pkg, Kind: "imports", Confidence: "import"},
		})
	})
	inBatch(t, s, func(b *store.Batch) error {
		return b.ReplaceModuleEdgesFrom([]int64{fx.src}, []store.ModuleEdge{{Src: fx.src, Dst: other, Kind: "imports", Confidence: "import"}})
	})
	if e, _ := s.ModuleEdgesFrom(fx.src); len(e) != 1 || e[0].Dst != other {
		t.Errorf("src edges %+v", e)
	}
	if e, _ := s.ModuleEdgesFrom(other); len(e) != 1 || e[0].Dst != fx.pkg {
		t.Errorf("other edges must be kept: %+v", e)
	}
}

func TestSetImportModuleOnMissingRowIsBenign(t *testing.T) {
	s := openTemp(t)
	inBatch(t, s, func(b *store.Batch) error { return b.SetImportModule(12345, 0) })
}

func TestDuplicateIdentities(t *testing.T) {
	s := openTemp(t)
	a, _ := s.UpsertRepo(store.Repo{Workspace: "w", Name: "a", Path: "/a"})
	b, _ := s.UpsertRepo(store.Repo{Workspace: "w", Name: "b", Path: "/b"})
	c, _ := s.UpsertRepo(store.Repo{Workspace: "w", Name: "c", Path: "/c"})
	shared := store.RepoIdentity{Lang: "go", Identity: "example.com/m", Path: "go.mod"}
	for _, id := range []int64{a, b} {
		if err := s.ReplaceRepoIdentities(id, []store.RepoIdentity{shared}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ReplaceRepoIdentities(c, []store.RepoIdentity{{Lang: "go", Identity: "example.com/c", Path: "go.mod"}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.DuplicateIdentities()
	want := []store.SharedIdentity{{Lang: "go", Identity: "example.com/m", Repos: []string{"a", "b"}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v %v", got, err)
	}
}
