package store_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func TestSymbolCountsByFileFiltersByPrefix(t *testing.T) {
	s := openTemp(t)
	repoID, fileID := symbolFixture(t, s)
	if _, err := s.ReplaceFileSymbols(fileID, 0, sampleSymbols()); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"p/b.go", "p_x/c.go", "q/d.go"} {
		if _, err := s.UpsertFile(store.File{RepoID: repoID, Path: p, Lang: "go", Hash: "h"}); err != nil {
			t.Fatal(err)
		}
	}
	for prefix, want := range map[string]string{"p/": "p/a.go=3,p/b.go=0", "": "p/a.go=3,p/b.go=0,p_x/c.go=0,q/d.go=0", "%/": ""} {
		got, err := s.SymbolCountsByFile(repoID, prefix)
		if err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(got))
		for i, fc := range got {
			parts[i] = fmt.Sprintf("%s=%d", fc.Path, fc.Symbols)
		}
		if strings.Join(parts, ",") != want {
			t.Errorf("%q: %v", prefix, parts)
		}
	}
}

func TestParseErrorsSumsTheRepoFiles(t *testing.T) {
	s := openTemp(t)
	a, err := s.UpsertRepo(store.Repo{Workspace: "w", Name: "a", Path: "/a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.UpsertRepo(store.Repo{Workspace: "w", Name: "b", Path: "/b"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []store.File{{RepoID: a, Path: "x.go", ParseErrors: 2}, {RepoID: a, Path: "y.go", ParseErrors: 1},
		{RepoID: b, Path: "z.go", ParseErrors: 5}} {
		if _, err := s.UpsertFile(f); err != nil {
			t.Fatal(err)
		}
	}
	for repo, want := range map[int64]int{a: 3, b: 5, 99: 0} {
		if n, err := s.ParseErrors(repo); err != nil || n != want {
			t.Fatalf("repo %d: %d %v, want %d", repo, n, err, want)
		}
	}
}
