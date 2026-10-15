package store_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

// moduleChain stores modules a -> b -> c in repo r1 and d -> b in repo r2.
func moduleChain(t *testing.T) (*store.Store, map[string]int64) {
	t.Helper()
	s := openTemp(t)
	ids := map[string]int64{}
	for _, rm := range [][3]string{{"r1", "/r1", "a"}, {"r1", "/r1", "b"}, {"r1", "/r1", "c"}, {"r2", "/r2", "d"}} {
		repoID, err := s.UpsertRepo(store.Repo{Workspace: "w", Name: rm[0], Path: rm[1]})
		if err != nil {
			t.Fatal(err)
		}
		if ids[rm[2]], err = s.UpsertModule(store.Module{RepoID: repoID, Path: rm[2], Lang: "go", Kind: "go-package",
			Identity: "x/" + rm[2]}); err != nil {
			t.Fatal(err)
		}
	}
	edges := []store.ModuleEdge{{Src: ids["a"], Dst: ids["b"]}, {Src: ids["b"], Dst: ids["c"]}, {Src: ids["d"], Dst: ids["b"]}}
	for i := range edges {
		edges[i].Kind, edges[i].Confidence = "imports", "import"
	}
	if err := s.Batch(func(b *store.Batch) error { return b.ReplaceModuleEdges(edges) }); err != nil {
		t.Fatal(err)
	}
	return s, ids
}

func TestModuleWalk(t *testing.T) {
	s, ids := moduleChain(t)
	down, err := s.ModuleWalk(ids["a"], store.ModuleWalkOpts{Depth: 5, Limit: 10})
	if err != nil || len(down) != 2 || down[1].Node != ids["c"] || down[1].Parent != ids["b"] || down[1].Depth != 2 {
		t.Fatalf("%+v %v", down, err)
	}
	b, _ := s.ModuleByID(ids["b"])
	up, err := s.ModuleWalk(ids["b"], store.ModuleWalkOpts{Inbound: true, Depth: 1, Limit: 10, RepoID: b.RepoID})
	if err != nil || len(up) != 1 || up[0].Node != ids["a"] {
		t.Fatalf("same repo only: %+v %v", up, err)
	}
	up, err = s.ModuleWalk(ids["b"], store.ModuleWalkOpts{Inbound: true, Depth: 1, Limit: 10})
	if err != nil || len(up) != 2 {
		t.Fatalf("%+v %v", up, err)
	}
}

func TestModuleLookups(t *testing.T) {
	s, ids := moduleChain(t)
	if m, err := s.ModuleByID(ids["c"]); err != nil || m.Identity != "x/c" {
		t.Fatalf("%+v %v", m, err)
	}
	if ms, err := s.ModulesByIdentity("x/d"); err != nil || len(ms) != 1 || ms[0].ID != ids["d"] {
		t.Fatalf("%+v %v", ms, err)
	}
	if es, err := s.ModuleEdgesTo(ids["b"]); err != nil || len(es) != 2 {
		t.Fatalf("%+v %v", es, err)
	}
}
