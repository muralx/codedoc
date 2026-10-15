package store_test

import (
	"math"
	"math/rand/v2"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

// vecFixture stores sampleSymbols (T documented) and one documented
// module and returns the ids of T, T.Get and the module.
func vecFixture(t *testing.T, s *store.Store) (typ, get, mod int64) {
	t.Helper()
	repoID, fileID := symbolFixture(t, s)
	ids, err := s.ReplaceFileSymbols(fileID, 0, sampleSymbols())
	if err != nil {
		t.Fatal(err)
	}
	err = s.Batch(func(b *store.Batch) error {
		var err error
		if mod, err = b.UpsertModule(store.Module{RepoID: repoID, Path: "p", Lang: "go", Kind: "package", Identity: "x/p"}); err != nil {
			return err
		}
		if _, err = b.UpsertModule(store.Module{RepoID: repoID, Path: "q", Lang: "go", Kind: "package", Identity: "x/q"}); err != nil {
			return err
		}
		return b.SetModuleDoc(mod, "Package p does things.", "p/doc.go", "ok")
	})
	if err != nil {
		t.Fatal(err)
	}
	return ids["r:go:p:T"], ids["r:go:p:T.Get"], mod
}

func TestStaleVectorsListsMissing(t *testing.T) {
	s := openTemp(t)
	typ, _, mod := vecFixture(t, s)
	stale, err := s.StaleSymbolVecs(store.VecScope{}, 2)
	if err != nil || len(stale) != 1 {
		t.Fatalf("%+v %v", stale, err)
	}
	want := store.StaleSymbol{ID: typ, RepoID: stale[0].RepoID, FileID: stale[0].FileID, Kind: "struct", Qualified: "T",
		Path: "p/a.go", Signature: "sig T", Doc: "T is a thing.", StartLine: 3, EndLine: 10, SigStartLine: 3, SigEndLine: 3,
		DocStartLine: 2, DocEndLine: 2, OwnEndLine: 10, Members: "x Get", FileHash: "h", VectorHash: vecHashDHB}
	if stale[0] != want {
		t.Fatalf("%+v", stale[0])
	}
	mods, err := s.ModuleVecs(store.VecScope{})
	if err != nil || len(mods) != 1 || mods[0].ID != mod || mods[0].Hash != "" {
		t.Fatalf("%+v %v", mods, err)
	}
}

// With bodies the undocumented symbols of more than one line are stale
// too; the field T.x, a trivial one, is not embedded.
func TestStaleVectorsWithBodies(t *testing.T) {
	s := openTemp(t)
	_, get, _ := vecFixture(t, s)
	all, err := s.StaleSymbolVecs(store.VecScope{Bodies: true}, 2)
	if err != nil || len(all) != 2 {
		t.Fatalf("%+v %v", all, err)
	}
	for _, st := range all {
		wantGet := store.StaleSymbol{ID: get, RepoID: st.RepoID, FileID: st.FileID, Kind: "method", Qualified: "T.Get",
			Path: "p/a.go", Container: "T", Signature: "sig T.Get", StartLine: 12, EndLine: 14, SigStartLine: 12,
			SigEndLine: 12, OwnEndLine: 14, FileHash: "h", VectorHash: "p1::b"}
		if st.ID == get && st != wantGet {
			t.Fatalf("%+v", st)
		}
	}
}

func TestUpsertVectorsClearsStaleness(t *testing.T) {
	s := openTemp(t)
	typ, get, mod := vecFixture(t, s)
	upsert(t, s, map[int64][]float32{typ: {1, 0}, get + 1000: {1, 0}}, vecHashDHB)
	if err := s.Batch(func(b *store.Batch) error { return b.UpsertModuleVec(mod, "mh", []float32{0, 1}) }); err != nil {
		t.Fatal(err)
	}
	for dims, want := range map[int]int{2: 0, 3: 1} {
		if stale, _ := s.StaleSymbolVecs(store.VecScope{}, dims); len(stale) != want {
			t.Fatalf("dims %d: %+v", dims, stale)
		}
	}
	if mods, _ := s.ModuleVecs(store.VecScope{}); mods[0].Hash != "mh" || mods[0].Dims != 2 {
		t.Fatalf("%+v", mods)
	}
	checkVecCounts(t, s, store.VecScope{}, 2, 1, 1)
	checkVecCounts(t, s, store.VecScope{}, 3, 0, 1)
	checkVecCounts(t, s, store.VecScope{Bodies: true}, 2, 1, 2)
}

// checkVecCounts checks the symbols of a scope with current vectors of a
// size over all its embedded symbols.
func checkVecCounts(t *testing.T, s *store.Store, sc store.VecScope, dims, embedded, total int) {
	t.Helper()
	n, d, err := s.SymbolVecCounts(sc, dims)
	if err != nil || n != embedded || d != total {
		t.Fatalf("%+v dims %d: %d/%d, want %d/%d: %v", sc, dims, n, d, embedded, total, err)
	}
}

// A symbol with several passage vectors is one row of a scan, scored as
// its best passage, with its best passages first and their file lines;
// it counts once, and replacing its vectors drops the passages it no
// longer has.
func TestPassagesOfOneSymbolAreOneScannedRow(t *testing.T) {
	s := openTemp(t)
	typ, get, _ := vecFixture(t, s)
	replace(t, s, typ, vecHashDHB, []store.PassageVec{
		{FromDoc: true, Start: 0, End: 0, Vec: []float32{1, 0}},
		{Start: 1, End: 2, Vec: []float32{0, 1}},
		{Start: 3, End: 4, Vec: []float32{0.5, 0.5}},
		{Start: 5, End: 7, Vec: []float32{-1, 0}},
	})
	replace(t, s, get, "p1::b", []store.PassageVec{{Start: 0, End: 2, Vec: []float32{0.25, 0.25}}})
	got, err := s.NearestSymbols([]float32{0, 1}, store.SearchFilter{}, 5)
	want := []store.Scored{
		{ID: typ, Score: 1, Passages: []store.ScoredPassage{
			{StartLine: 4, EndLine: 5, Score: 1}, {StartLine: 6, EndLine: 7, Score: 0.5}, {StartLine: 2, EndLine: 2}}},
		{ID: get, Score: 0.25, Passages: []store.ScoredPassage{{StartLine: 12, EndLine: 14, Score: 0.25}}},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v %v", got, err)
	}
	if top, _ := s.NearestSymbols([]float32{0, 1}, store.SearchFilter{}, 1); len(top) != 1 || top[0].ID != typ {
		t.Fatalf("top 1: %+v", top)
	}
	checkVecCounts(t, s, store.VecScope{Bodies: true}, 2, 2, 2)
	if n, err := s.PassageCount(0); n != 5 || err != nil {
		t.Fatalf("%d passages %v", n, err)
	}
	replace(t, s, typ, vecHashDHB, []store.PassageVec{{Vec: []float32{1, 0}}})
	if n, _ := s.PassageCount(0); n != 2 {
		t.Fatalf("%d passages after replacing", n)
	}
}

// replace makes passages the vectors of a symbol.
func replace(t *testing.T, s *store.Store, id int64, hash string, passages []store.PassageVec) {
	t.Helper()
	if err := s.Batch(func(b *store.Batch) error { return b.ReplaceSymbolVecs(id, hash, passages) }); err != nil {
		t.Fatal(err)
	}
}

// upsert stores symbol vectors with one text hash; ids of missing
// symbols are skipped by the store.
func upsert(t *testing.T, s *store.Store, vecs map[int64][]float32, hash string) {
	t.Helper()
	err := s.Batch(func(b *store.Batch) error {
		for id, v := range vecs {
			if err := b.ReplaceSymbolVecs(id, hash, []store.PassageVec{{Vec: v}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestVectorScopesAndCascade(t *testing.T) {
	s := openTemp(t)
	typ, _, mod := vecFixture(t, s)
	sym, _ := s.SymbolByID(typ)
	if stale, _ := s.StaleSymbolVecs(store.VecScope{Touched: true}, 0); len(stale) != 0 {
		t.Fatalf("empty touched scope: %+v", stale)
	}
	if stale, _ := s.StaleSymbolVecs(store.VecScope{Touched: true, FileIDs: []int64{sym.FileID}}, 0); len(stale) != 1 {
		t.Fatalf("touched file: %+v", stale)
	}
	if stale, _ := s.StaleSymbolVecs(store.VecScope{RepoID: sym.RepoID + 1}, 0); len(stale) != 0 {
		t.Fatalf("other repo: %+v", stale)
	}
	if mods, _ := s.ModuleVecs(store.VecScope{RepoID: sym.RepoID, Touched: true}); len(mods) != 1 {
		t.Fatalf("touched module: %+v", mods)
	}
	err := s.Batch(func(b *store.Batch) error {
		if err := b.ReplaceSymbolVecs(typ, "h", []store.PassageVec{{Vec: []float32{1}}}); err != nil {
			return err
		}
		if err := b.UpsertModuleVec(mod, "h", []float32{1}); err != nil {
			return err
		}
		if _, err := b.DeleteFiles([]int64{sym.FileID}); err != nil {
			return err
		}
		return b.DeleteModules([]int64{mod})
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.NearestSymbols([]float32{1}, store.SearchFilter{}, 5); len(got) != 0 {
		t.Fatalf("symbol vector survived: %+v", got)
	}
	if got, _ := s.NearestModules([]float32{1}, store.SearchFilter{}, 5); len(got) != 0 {
		t.Fatalf("module vector survived: %+v", got)
	}
}

func TestNearestRanksAndFilters(t *testing.T) {
	s := openTemp(t)
	typ, get, mod := vecFixture(t, s)
	upsert(t, s, map[int64][]float32{typ: {1, 0}, get: {0.6, 0.8}}, "h")
	if err := s.Batch(func(b *store.Batch) error { return b.UpsertModuleVec(mod, "h", []float32{0, 1}) }); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		q    []float32
		f    store.SearchFilter
		k    int
		want []int64
	}{
		"best first":    {[]float32{0, 1}, store.SearchFilter{}, 5, []int64{get, typ}},
		"top 1":         {[]float32{0, 1}, store.SearchFilter{}, 1, []int64{get}},
		"kind and lang": {[]float32{0, 1}, store.SearchFilter{Kind: "struct", Langs: []string{"go"}}, 5, []int64{typ}},
		"other repo":    {[]float32{0, 1}, store.SearchFilter{RepoIDs: []int64{}}, 5, nil},
		"size mismatch": {[]float32{0, 1, 0}, store.SearchFilter{}, 5, nil},
	} {
		got, err := s.NearestSymbols(tc.q, tc.f, tc.k)
		if err != nil || len(got) != len(tc.want) {
			t.Fatalf("%s: %+v %v", name, got, err)
		}
		for i, id := range tc.want {
			if got[i].ID != id {
				t.Fatalf("%s: %+v", name, got)
			}
		}
	}
	if got, _ := s.NearestModules([]float32{0, 1}, store.SearchFilter{Family: "ts"}, 5); len(got) != 0 {
		t.Fatalf("family filter: %+v", got)
	}
	if got, _ := s.NearestModules([]float32{0, 1}, store.SearchFilter{Family: "go", RepoID: 1}, 0); got != nil {
		t.Fatalf("k=0: %+v", got)
	}
}

func TestVectorsNoLongerEmbeddedAreDeleted(t *testing.T) {
	s := openTemp(t)
	typ, get, mod := vecFixture(t, s)
	upsert(t, s, map[int64][]float32{typ: {1, 0}, get: {0, 1}}, "h")
	err := s.Batch(func(b *store.Batch) error {
		if err := b.UpsertModuleVec(mod, "h", []float32{0, 1}); err != nil {
			return err
		}
		return b.SetModuleDoc(mod, "", "", "missing")
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.NearestModules([]float32{0, 1}, store.SearchFilter{}, 5); len(got) != 0 {
		t.Fatalf("undocumented module scanned: %+v", got)
	}
	sy, _ := s.SymbolByID(typ)
	checkDeleted(t, s, sy.RepoID+1, 0)
	checkDeleted(t, s, sy.RepoID, 1) // the module's vector
	// The file becomes generated: its undocumented symbols are not
	// embedded any more, the documented one still is.
	if _, err := s.UpsertFile(store.File{RepoID: sy.RepoID, Path: "p/a.go", Lang: "go", Hash: "h", Generated: true}); err != nil {
		t.Fatal(err)
	}
	checkVecCounts(t, s, store.VecScope{Bodies: true}, 0, 0, 1)
	checkDeleted(t, s, 0, 1)
	checkDeleted(t, s, 0, 0)
	if got, _ := s.NearestSymbols([]float32{0, 1}, store.SearchFilter{}, 5); len(got) != 1 || got[0].ID != typ {
		t.Fatalf("symbol scan: %+v", got)
	}
}

// checkDeleted checks how many symbols and modules of a repo lose their
// vectors for not being embedded any more.
func checkDeleted(t *testing.T, s *store.Store, repoID int64, want int) {
	t.Helper()
	if n, err := s.DeleteUnembeddedVecs(repoID); n != want || err != nil {
		t.Fatalf("repo %d: deleted %d, want %d: %v", repoID, n, want, err)
	}
}

func TestEncodeDecodeVec(t *testing.T) {
	v := []float32{1.5, -2, 0}
	got := store.DecodeVec(store.EncodeVec(v))
	if len(got) != 3 || got[0] != 1.5 || got[1] != -2 || got[2] != 0 {
		t.Fatal(got)
	}
}

// BenchmarkNearestSymbols scans 100k unit vectors of 768 dimensions, the
// size of a large workspace's documented symbols under the real model.
func BenchmarkNearestSymbols(b *testing.B) {
	const n, dims = 100_000, 768
	s, err := store.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	seedVectors(b, s, n, dims)
	if err := s.Checkpoint(); err != nil {
		b.Fatal(err)
	}
	q := randomUnit(rand.New(rand.NewPCG(9, 9)), dims)
	b.ResetTimer()
	for b.Loop() {
		got, err := s.NearestSymbols(q, store.SearchFilter{}, 50)
		if err != nil || len(got) != 50 {
			b.Fatalf("%d %v", len(got), err)
		}
	}
}

// seedVectors stores n symbols with random unit vectors in one file.
func seedVectors(b *testing.B, s *store.Store, n, dims int) {
	b.Helper()
	repoID, err := s.UpsertRepo(store.Repo{Workspace: "w", Name: "r", Path: "/r"})
	if err != nil {
		b.Fatal(err)
	}
	fileID, err := s.UpsertFile(store.File{RepoID: repoID, Path: "a.go", Lang: "go", Hash: "h"})
	if err != nil {
		b.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	syms := make([]store.Symbol, n)
	for i := range syms {
		syms[i] = sym("function", "F"+strconv.Itoa(i), "", i+1, i+1)
		syms[i].DocText = "F does one thing."
	}
	ids, err := s.ReplaceFileSymbols(fileID, 0, syms)
	if err != nil {
		b.Fatal(err)
	}
	err = s.Batch(func(bt *store.Batch) error {
		for _, id := range ids {
			if err := bt.ReplaceSymbolVecs(id, "h", []store.PassageVec{{Vec: randomUnit(rng, dims)}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
}

func randomUnit(rng *rand.Rand, dims int) []float32 {
	v := make([]float32, dims)
	var norm float32
	for i := range v {
		v[i] = float32(rng.NormFloat64())
		norm += v[i] * v[i]
	}
	for i := range v {
		v[i] /= float32(math.Sqrt(float64(norm)))
	}
	return v
}

func TestVectorReadsFailOnClosedStore(t *testing.T) {
	s := openTemp(t)
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	for name, call := range map[string]func() error{
		"StaleSymbolVecs":  func() error { _, err := s.StaleSymbolVecs(store.VecScope{}, 0); return err },
		"ModuleVecs":       func() error { _, err := s.ModuleVecs(store.VecScope{}); return err },
		"SymbolVecCounts":  func() error { _, _, err := s.SymbolVecCounts(store.VecScope{}, 0); return err },
		"PassageCount":     func() error { _, err := s.PassageCount(0); return err },
		"NearestSymbols":   func() error { _, err := s.NearestSymbols([]float32{1}, store.SearchFilter{}, 1); return err },
		"DeleteUnembedded": func() error { _, err := s.DeleteUnembeddedVecs(0); return err },
		"Checkpoint":       s.Checkpoint,
	} {
		if call() == nil {
			t.Errorf("%s on a closed store returned no error", name)
		}
	}
}

// vecHashDHB is the vector hash of the vecFixture type: the passage
// scheme, its doc hash dh and body hash b, joined as the store's SQL
// joins them.
const vecHashDHB = "p1:dh:b"
