package store_test

import (
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func TestSymbolNamesAreDistinctAndScopedToRepos(t *testing.T) {
	s := openTemp(t)
	repoID, fileID := symbolFixture(t, s)
	if _, err := s.ReplaceFileSymbols(fileID, 0, sampleSymbols()); err != nil {
		t.Fatal(err)
	}
	other, err := s.UpsertRepo(store.Repo{Workspace: "w", Name: "other", Path: "/other"})
	if err != nil {
		t.Fatal(err)
	}
	otherFile, err := s.UpsertFile(store.File{RepoID: other, Path: "q/q.go", Hash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceFileSymbols(otherFile, 0, []store.Symbol{sym("func", "Get", "", 1, 2), sym("func", "Zed", "", 3, 4)}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		repos []int64
		want  []string
	}{
		{nil, []string{"Get", "T", "Zed", "x"}},
		{[]int64{repoID}, []string{"Get", "T", "x"}},
		{[]int64{other}, []string{"Get", "Zed"}},
		{[]int64{repoID, other}, []string{"Get", "T", "Zed", "x"}},
		{[]int64{-1}, nil},
	} {
		got, err := s.SymbolNames(c.repos...)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: %v, %v; want %v", c.repos, got, err, c.want)
		}
	}
}
