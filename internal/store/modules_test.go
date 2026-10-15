package store_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func repoIDs(rs []store.Repo) []int64 {
	var ids []int64
	for _, r := range rs {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestRepoIdentities(t *testing.T) {
	s := openTemp(t)
	a, _ := s.UpsertRepo(store.Repo{Workspace: "w", Name: "a", Path: "/a"})
	b, _ := s.UpsertRepo(store.Repo{Workspace: "w", Name: "b", Path: "/b"})
	ids := []store.RepoIdentity{{Lang: "go", Identity: "example.com/m", Path: "go.mod"}}
	for _, id := range []int64{b, a} {
		if err := s.ReplaceRepoIdentities(id, ids); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ReposProviding("go", "example.com/m")
	if err != nil || !reflect.DeepEqual(repoIDs(got), []int64{a, b}) || got[0].Name != "a" {
		t.Fatalf("providers %+v %v", got, err)
	}
	if err := s.ReplaceRepoIdentities(a, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ReposProviding("go", "example.com/m"); !reflect.DeepEqual(repoIDs(got), []int64{b}) {
		t.Fatalf("after replace %+v", got)
	}
	if got, _ := s.RepoIdentities(b); !reflect.DeepEqual(got, ids) {
		t.Fatalf("identities %+v", got)
	}
}

func TestModules(t *testing.T) {
	s := openTemp(t)
	r, _ := s.UpsertRepo(store.Repo{Workspace: "w", Name: "a", Path: "/a"})
	root := store.Module{RepoID: r, Path: ".", Lang: "ts", Kind: "npm-package", Identity: "@x/d"}
	rootID, err := s.UpsertModule(root)
	if err != nil {
		t.Fatal(err)
	}
	sub := store.Module{RepoID: r, Path: "src", Lang: "ts", Kind: "npm-dir", Identity: "@x/d/src", ParentID: rootID}
	subID, err := s.UpsertModule(sub)
	if err != nil {
		t.Fatal(err)
	}
	sub.Identity = "@x/d/lib"
	if again, err := s.UpsertModule(sub); err != nil || again != subID {
		t.Fatalf("upsert keyed by repo, lang, path: %d %v", again, err)
	}
	sub.ID, sub.DocState = subID, "missing"
	checkModuleLookups(t, s, r, rootID, sub)
	checkModuleDoc(t, s, sub)
}

func checkModuleDoc(t *testing.T, s *store.Store, sub store.Module) {
	t.Helper()
	subID := sub.ID
	err := s.Batch(func(b *store.Batch) error { return b.SetModuleDoc(subID, "Lib does things.", "src/README.md", "ok") })
	if err != nil {
		t.Fatal(err)
	}
	var got store.Module
	err = s.Batch(func(b *store.Batch) (err error) { got, err = b.ModuleByID(subID); return err })
	if err != nil || got.DocText != "Lib does things." || got.DocSource != "src/README.md" || got.DocState != "ok" {
		t.Fatalf("module doc %+v %v", got, err)
	}
	if again, err := s.UpsertModule(sub); err != nil || again != subID {
		t.Fatal(err)
	}
	if got, err := s.ModuleByID(subID); err != nil || got.DocText != "Lib does things." {
		t.Fatalf("upsert keeps the doc: %+v %v", got, err)
	}
}

func checkModuleLookups(t *testing.T, s *store.Store, r, rootID int64, sub store.Module) {
	t.Helper()
	if got, err := s.ModuleByIdentity(r, "ts", "@x/d/lib"); err != nil || got != sub {
		t.Fatalf("by identity %+v %v", got, err)
	}
	if got, err := s.ModuleByPath(r, "ts", "."); err != nil || got.ID != rootID || got.Identity != "@x/d" {
		t.Fatalf("by path %+v %v", got, err)
	}
	if _, err := s.ModuleByPath(r, "go", "."); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing module: %v", err)
	}
	all, err := s.ModulesByRepo(r)
	if err != nil || len(all) != 2 || all[0].ID != rootID {
		t.Fatalf("by repo %+v %v", all, err)
	}
}

func TestDeleteModulesUnlinksReferences(t *testing.T) {
	s := openTemp(t)
	r, _ := s.UpsertRepo(store.Repo{Workspace: "w", Name: "a", Path: "/a"})
	root, _ := s.UpsertModule(store.Module{RepoID: r, Path: ".", Lang: "ts", Kind: "npm-package", Identity: "p"})
	sub, _ := s.UpsertModule(store.Module{RepoID: r, Path: "s", Lang: "ts", Kind: "npm-dir", Identity: "p/s", ParentID: root})
	f, _ := s.UpsertFile(store.File{RepoID: r, Path: "s/a.ts", Size: 1, Mtime: 1, Hash: "h"})
	err := s.Batch(func(b *store.Batch) error { return b.ReplaceFileImports(f, []store.Import{{Raw: "p", Line: 1}}) })
	if err != nil {
		t.Fatal(err)
	}
	imps, err := s.FileImports(f)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Batch(func(b *store.Batch) error {
		if err := b.SetImportModule(imps[0].RowID, root); err != nil {
			return err
		}
		if err := b.ReplaceModuleEdges([]store.ModuleEdge{{Src: sub, Dst: root, Kind: "imports", Confidence: "import"}}); err != nil {
			return err
		}
		return b.DeleteModules([]int64{root})
	})
	if err != nil {
		t.Fatal(err)
	}
	if m, err := s.ModuleByPath(r, "ts", "s"); err != nil || m.ParentID != 0 {
		t.Fatalf("child %+v %v", m, err)
	}
	if imps, _ := s.FileImports(f); len(imps) != 1 || imps[0].ResolvedModuleID != 0 {
		t.Fatalf("imports %+v", imps)
	}
	if edges, _ := s.ModuleEdgesFrom(sub); len(edges) != 0 {
		t.Fatalf("edges %+v", edges)
	}
}
