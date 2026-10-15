package index_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// callsFrom returns the stable keys of the plain call targets of a symbol.
func callsFrom(t *testing.T, s *store.Store, key string) []string {
	t.Helper()
	sy, err := s.SymbolByKey(key)
	if err != nil {
		t.Fatal(err)
	}
	es, err := s.EdgesFrom(sy.ID, "calls")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		if e.Dst != 0 {
			d, err := s.SymbolByID(e.Dst)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, d.StableKey)
		}
	}
	return out
}

func TestResolvePendingThenEnsureKeepsCrossRepoEdges(t *testing.T) {
	s := openTemp(t)
	lib, app := repoFixture(t, "go-lib"), repoFixture(t, "go-app")
	opts := index.Options{Workspace: "fixture", MaxFiles: 100, RepoOrder: []string{"go-lib", "go-app"}}
	if _, err := index.Repo(s, lib, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Repo(s, app, opts); err != nil {
		t.Fatal(err)
	}
	if err := index.ResolvePending(s, opts); err != nil {
		t.Fatal(err)
	}
	const mainKey = "go-app:go:github.com/fixture/app:main"
	if got := callsFrom(t, s, mainKey); len(got) != 3 {
		t.Fatalf("main calls New, NewMemStore and run: %v", got)
	}
	// A new package changes the repo's modules: everything is resolved again.
	dir := filepath.Join(lib.Path, "extra")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extra.go"), []byte("package extra\n\nfunc New() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Ensure(s, lib, opts); err != nil {
		t.Fatal(err)
	}
	if got := callsFrom(t, s, mainKey); len(got) != 3 {
		t.Fatalf("service.New still resolves through the import: %v", got)
	}
}
