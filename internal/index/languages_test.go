package index_test

import (
	"slices"
	"sort"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
)

// storedPaths lists the file rows of a repo, sorted.
func storedPaths(t *testing.T, s *store.Store, root string) []string {
	t.Helper()
	row, err := s.RepoByPath(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.FilesByRepo(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	sort.Strings(out)
	return out
}

// TestRepoLanguagesFilterDiscovery indexes a repo limited to yaml, then
// widens the selection to Go and narrows it again: Ensure adds and
// removes the Go files, files of no language stay.
func TestRepoLanguagesFilterDiscovery(t *testing.T) {
	dir := testutil.SourceRepo(t, "mixed", map[string]string{
		"go.mod": "module example.com/m\n", "a.go": "package m\n", "app.yaml": "a: 1\n", "run.sh": "X=1\n",
	})
	rep := &config.Repo{Path: dir, Name: "mixed", Languages: []string{"yaml"}}
	s := openTemp(t)
	opts := index.Options{Workspace: "w", MaxFiles: 100}
	if _, err := index.Repo(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	want := func(paths ...string) {
		t.Helper()
		if got := storedPaths(t, s, dir); !slices.Equal(got, paths) {
			t.Fatalf("stored %v, want %v", got, paths)
		}
	}
	want("app.yaml", "go.mod")
	rep.Languages = []string{"go", "yaml"}
	if _, err := index.Ensure(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	want("a.go", "app.yaml", "go.mod")
	rep.Languages = []string{"shell"}
	if _, err := index.Ensure(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	want("go.mod", "run.sh")
	if err := index.Files(s, rep, opts, []string{"a.go", "app.yaml"}); err != nil {
		t.Fatal(err)
	}
	want("go.mod", "run.sh")
}
