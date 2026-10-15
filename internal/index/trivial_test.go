package index_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

// Test cases are not embedded from their bodies: they match prose
// queries as well as the code they test and double what is embedded.
// A vector an earlier build wrote for a trivial symbol is deleted.
func TestTestsAndTrivialSymbolsKeepTheirBodiesOut(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	writeFile(t, rep.Path, "store/extra_test.go", []byte("package store\n\nimport \"testing\"\n\n"+
		"func TestRoundTrip(t *testing.T) {\n\tm := NullStore{}\n\t_ = m.Put(\"k\", \"v\")\n}\n"))
	indexRepo(t, s, rep)
	_, texts := embedFake(t, s)
	if containsText(texts, "TestRoundTrip") {
		t.Fatalf("a test body was embedded: %q", texts)
	}
	syms, err := s.SymbolsByName("fallback", "", 0)
	if err != nil || len(syms) != 1 {
		t.Fatalf("%+v %v", syms, err)
	}
	err = s.Batch(func(b *store.Batch) error {
		return b.ReplaceSymbolVecs(syms[0].ID, "old", []store.PassageVec{{Vec: make([]float32, fakeDims)}})
	})
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := embedFake(t, s); res.Removed != 1 || res.Symbols != 0 {
		t.Fatalf("%+v", res)
	}
}
