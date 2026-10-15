package store_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func TestTextMatches(t *testing.T) {
	s := openTemp(t)
	typ, get, mod := vecFixture(t, s)
	got, err := s.SymbolMatches(`"thing" OR "get"`, store.SearchFilter{}, 10)
	if err != nil || len(got) != 2 || got[0] != get || got[1] != typ {
		t.Fatalf("%v %v", got, err)
	}
	if got, _ := s.SymbolMatches(`"thing"`, store.SearchFilter{RepoID: 99}, 10); len(got) != 0 {
		t.Fatalf("repo filter: %v", got)
	}
	if got, _ := s.SymbolMatches("", store.SearchFilter{}, 10); got != nil {
		t.Fatalf("empty query: %v", got)
	}
	mods, err := s.ModuleMatches(`"things"`, store.SearchFilter{Family: "go"}, 10)
	if err != nil || len(mods) != 1 || mods[0] != mod {
		t.Fatalf("%v %v", mods, err)
	}
	if _, err := s.SymbolMatches(`"unterminated`, store.SearchFilter{}, 10); err == nil {
		t.Fatal("bad FTS query accepted")
	}
}

func TestTextMatchesFailOnClosedStore(t *testing.T) {
	s := openTemp(t)
	_ = s.Close()
	if _, err := s.SymbolMatches(`"x"`, store.SearchFilter{}, 1); err == nil {
		t.Fatal("no error on a closed store")
	}
}
