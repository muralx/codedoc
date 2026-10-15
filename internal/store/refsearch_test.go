package store_test

import (
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func TestReferenceMatchesFindNamesUsedInBodies(t *testing.T) {
	s := openTemp(t)
	_, fileID := symbolFixture(t, s)
	ids, err := s.ReplaceFileSymbols(fileID, 0, sampleSymbols())
	if err != nil {
		t.Fatal(err)
	}
	typ, get := ids["r:go:p:T"], ids["r:go:p:T.Get"]
	err = s.Batch(func(b *store.Batch) error {
		return b.ReplaceFileReferences(fileID, []store.Reference{
			{SymbolID: get, Kind: "call", Name: "spawnHubProcess", Line: 13},
			{SymbolID: get, Kind: "call", Name: "Detach", Line: 13},
			{SymbolID: get, Kind: "call", Name: "spawnHubProcess", Line: 14},
			{SymbolID: typ, Kind: "type", Name: "Hubcap", Line: 4},
			{SymbolID: typ, Kind: "type", Name: "io_x", Line: 5},
			{Kind: "call", Name: "Detach", Line: 1},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ReferenceMatches([]string{"hub", "detach", "io", "100%"}, store.SearchFilter{}, 5)
	want := []store.RefMatch{
		{ID: get, Names: []string{"detach", "spawnhubprocess"}, Lines: []int{13, 14}, Terms: []string{"hub", "detach"}},
		// A term of two letters matches as a prefix only: io_x, not Detach.
		{ID: typ, Names: []string{"hubcap", "io_x"}, Lines: []int{4, 5}, Terms: []string{"hub", "io"}},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v %v", got, err)
	}
	if got, _ := s.ReferenceMatches([]string{"hub"}, store.SearchFilter{Kind: "struct"}, 1); len(got) != 1 || got[0].ID != typ {
		t.Fatalf("kind filter and limit: %+v", got)
	}
	if got, err := s.ReferenceMatches(nil, store.SearchFilter{}, 5); got != nil || err != nil {
		t.Fatalf("%+v %v", got, err)
	}
	_ = s.Close()
	if _, err := s.ReferenceMatches([]string{"hub"}, store.SearchFilter{}, 5); err == nil {
		t.Fatal("no error on a closed store")
	}
}
