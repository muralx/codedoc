package index_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

func repoID(t *testing.T, s *store.Store, root string) int64 {
	t.Helper()
	r, err := s.RepoByPath(root)
	if err != nil {
		t.Fatal(err)
	}
	return r.ID
}

func TestIndexStoresModulesAndIdentities(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	id := repoID(t, s, rep.Path)
	ids, err := s.RepoIdentities(id)
	if err != nil || len(ids) != 1 || ids[0] != (store.RepoIdentity{Lang: "go", Identity: "github.com/fixture/lib", Path: "go.mod"}) {
		t.Fatalf("identities %+v %v", ids, err)
	}
	mod, err := s.ModuleByIdentity(id, "go", "github.com/fixture/lib/store")
	if err != nil || mod.Path != "store" || mod.Kind != "go-package" {
		t.Fatalf("module %+v %v", mod, err)
	}
	get, _ := s.SymbolByKey("go-lib:go:github.com/fixture/lib/store:MemStore.Get")
	if get.ModuleID != mod.ID {
		t.Fatalf("symbol module %d, want %d", get.ModuleID, mod.ID)
	}
}

func TestIndexTypeScriptModules(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "ts-domain")
	indexRepo(t, s, rep)
	id := repoID(t, s, rep.Path)
	root, err := s.ModuleByPath(id, "ts", ".")
	if err != nil || root.Identity != "@fixture/domain" || root.Kind != "npm-package" {
		t.Fatalf("root %+v %v", root, err)
	}
	src, err := s.ModuleByPath(id, "ts", "src")
	if err != nil || src.Identity != "@fixture/domain/src" || src.Kind != "npm-dir" || src.ParentID != root.ID {
		t.Fatalf("src %+v %v", src, err)
	}
	sy, err := s.SymbolByKey("ts-domain:ts:@fixture/domain:src/records.ts/MemoryRepo.load")
	if err != nil || sy.ModuleID != src.ID {
		t.Fatalf("%+v %v", sy, err)
	}
}

func TestIndexNestedPackageQualifiesFromPackageRoot(t *testing.T) {
	s := openTemp(t)
	root := t.TempDir()
	writeFile(t, root, "packages/web/package.json", []byte(`{"name": "@x/web"}`))
	writeFile(t, root, "packages/web/src/a.ts", []byte("export function f() {}\n"))
	indexRepo(t, s, &config.Repo{Path: root, Name: "r"})
	if _, err := s.SymbolByKey("r:ts:@x/web:src/a.ts/f"); err != nil {
		t.Fatal(err)
	}
}

func TestIndexRekeysFilesWhenTheModuleIdentityChanges(t *testing.T) {
	s := openTemp(t)
	root := t.TempDir()
	writeFile(t, root, "go.mod", []byte("module example.com/old\n"))
	writeFile(t, root, "p/a.go", []byte("package p\n\nfunc F() {}\n"))
	writeFile(t, root, "q/b.go", []byte("package q\n\nfunc G() {}\n"))
	rep := &config.Repo{Path: root, Name: "r"}
	indexRepo(t, s, rep)
	if _, err := s.SymbolByKey("r:go:example.com/old/p:F"); err != nil {
		t.Fatal(err)
	}
	q, _ := s.ModuleByPath(repoID(t, s, root), "go", "q")
	writeFile(t, root, "go.mod", []byte("module example.com/new\n"))
	if err := os.Remove(filepath.Join(root, "q", "b.go")); err != nil {
		t.Fatal(err)
	}
	f, err := index.Ensure(s, rep, index.Options{Workspace: "fixture", MaxFiles: 10})
	if err != nil || !f.Fresh {
		t.Fatalf("%+v %v", f, err)
	}
	if _, err := s.SymbolByKey("r:go:example.com/new/p:F"); err != nil {
		t.Fatalf("unchanged file not re-keyed: %v", err)
	}
	if _, err := s.SymbolByKey("r:go:example.com/old/p:F"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old key kept: %v", err)
	}
	if _, err := s.ModuleByPath(repoID(t, s, root), "go", "q"); !errors.Is(err, store.ErrNotFound) || q.ID == 0 {
		t.Fatalf("stale module kept: %v", err)
	}
}
