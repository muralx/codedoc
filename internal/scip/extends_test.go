package scip

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
)

// indexedRepo indexes a copy of a fixture repo into a fresh database.
func indexedRepo(t *testing.T, name string) (*store.Store, *config.Repo) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	cfg := &config.Repo{Path: testutil.CopyFixture(t, name), Name: name}
	opts := index.Options{Workspace: "fx", RepoOrder: []string{name}}
	if _, err := index.Repo(s, cfg, opts); err != nil {
		t.Fatal(err)
	}
	if err := index.ResolvePending(s, opts); err != nil {
		t.Fatal(err)
	}
	return s, cfg
}

func TestMergeSkipsImplementsBesideExtends(t *testing.T) {
	s, cfg := indexedRepo(t, "py-lib")
	src, err := os.ReadFile(filepath.Join(cfg.Path, "src/pylib/store.py"))
	if err != nil {
		t.Fatal(err)
	}
	const pkg = "scip-python python py-lib 0.1 `pylib.store`/"
	at := func(line int, ident, sym string) Occurrence {
		o := occAt(t, string(src), line, ident, 0, sym, true)
		o.Path = "src/pylib/store.py"
		return o
	}
	ix := &Index{
		Occurrences: []Occurrence{at(13, "Store", pkg+"Store#"), at(25, "MemStore", pkg+"MemStore#")},
		Symbols:     map[string]SymbolInfo{pkg + "MemStore#": {Symbol: pkg + "MemStore#", Implements: []string{pkg + "Store#"}}},
	}
	row, err := s.RepoByPath(cfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := Merge(s, row, ix, "")
	if err != nil || st.Mapped != 2 || st.Implements != 0 {
		t.Fatalf("%+v %v", st, err)
	}
	mem, err := s.SymbolsByName("MemStore", "", row.ID)
	if err != nil || len(mem) != 1 {
		t.Fatalf("%+v %v", mem, err)
	}
	if es, _ := s.EdgesFrom(mem[0].ID, "implements"); len(es) != 0 {
		t.Fatalf("implements beside extends: %+v", es)
	}
	if es, _ := s.EdgesFrom(mem[0].ID, "extends"); len(es) != 1 {
		t.Fatalf("extends: %+v", es)
	}
}
