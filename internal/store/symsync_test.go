package store_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func TestParkThenSyncKeepsIDsByStableKey(t *testing.T) {
	s := openTemp(t)
	_, fileID := symbolFixture(t, s)
	ids, err := s.ReplaceFileSymbols(fileID, 0, sampleSymbols())
	if err != nil {
		t.Fatal(err)
	}
	next := sampleSymbols()
	next[0].Signature += " // edited"
	next = append(next[:2], sym("function", "F", "", 9, 9))
	var res store.FileSymbols
	err = s.Batch(func(b *store.Batch) error {
		parked, err := b.ParkFileSymbols(fileID)
		if err != nil || len(parked) != 3 {
			t.Fatalf("%v %v", parked, err)
		}
		res, err = b.SyncFileSymbols(fileID, 0, next)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IDs["r:go:p:T"] != ids["r:go:p:T"] || len(res.Added) != 1 || len(res.Removed) != 1 || len(res.Changed) != 1 {
		t.Fatalf("%+v (before %v)", res, ids)
	}
	if sy, err := s.SymbolByKey("r:go:p:T"); err != nil || sy.ID != ids["r:go:p:T"] {
		t.Fatalf("%+v %v", sy, err)
	}
}
