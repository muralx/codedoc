package index_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// countingEmbedder is a fake embedder that records the texts it embeds.
type countingEmbedder struct {
	*embed.Fake
	texts []string
}

func (c *countingEmbedder) EmbedBatch(texts []string) ([][]float32, error) {
	c.texts = append(c.texts, texts...)
	return c.Fake.EmbedBatch(texts)
}

func newCounting() *countingEmbedder { return &countingEmbedder{Fake: embed.NewFake(16)} }

func embedAll(t *testing.T, s *store.Store, e embed.Embedder) index.VecResult {
	t.Helper()
	res, err := index.EmbedVectors(s, e, store.VecScope{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestEmbedVectorsIncremental(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	e := newCounting()
	res := embedAll(t, s, e)
	n, documented, err := s.SymbolVecCounts(store.VecScope{Bodies: true}, 16)
	if err != nil || n != documented || n != res.Symbols || n == 0 || res.Modules == 0 {
		t.Fatalf("%+v %d/%d %v", res, n, documented, err)
	}
	checkTexts(t, e.texts)
	e.texts = nil
	indexRepo(t, s, rep)
	if res := embedAll(t, s, e); res.Symbols+res.Modules != 0 || len(e.texts) != 0 {
		t.Fatalf("re-index without change embedded %+v", res)
	}
	editFile(t, rep.Path, "store/store.go", "// Put implements Store.", "// Put stores a value under a key.")
	indexRepo(t, s, rep)
	if res := embedAll(t, s, e); res.Symbols != 1 || res.Modules != 0 {
		t.Fatalf("doc edit: %+v %q", res, e.texts)
	}
}

func TestEmbedVectorsCascadeOnDelete(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	embedAll(t, s, embed.NewFake(16))
	before, _, _ := s.SymbolVecCounts(store.VecScope{Bodies: true}, 16)
	q, _ := embed.Query(embed.NewFake(16), "Mode returns the library mode")
	if err := os.Remove(filepath.Join(rep.Path, "service/service.go")); err != nil {
		t.Fatal(err)
	}
	indexRepo(t, s, rep)
	after, documented, _ := s.SymbolVecCounts(store.VecScope{Bodies: true}, 16)
	got, err := s.NearestSymbols(q, store.SearchFilter{}, 100)
	if err != nil || after >= before || after != documented || len(got) != after {
		t.Fatalf("before %d after %d/%d scanned %d %v", before, after, documented, len(got), err)
	}
}

func TestEmbedVectorsLimitAndUnavailable(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	res, err := index.EmbedVectors(s, embed.NewFake(16), store.VecScope{}, 2)
	if err != nil || res.Deferred <= 2 || res.Symbols != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	broken := embed.NewLazy(16, func() (embed.Embedder, error) { return nil, errors.New("no model") }, nil)
	if res := embedAll(t, s, broken); !res.Unavailable || res.Symbols != 0 {
		t.Fatalf("%+v", res)
	}
	if _, err := index.EmbedVectors(s, failing{embed.NewFake(16)}, store.VecScope{}, 0); err == nil {
		t.Fatal("embed error swallowed")
	}
}

func TestEmbedVectorsSkipsFilesChangedOnDisk(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	appendFile(t, rep.Path, "store/store.go", "\n// Later is new.\nfunc Later() {}\n")
	res := embedAll(t, s, embed.NewFake(16))
	n, documented, _ := s.SymbolVecCounts(store.VecScope{Bodies: true}, 16)
	if res.Symbols == 0 || n == documented {
		t.Fatalf("%+v %d/%d", res, n, documented)
	}
}

func TestEnsureEmbedsTouchedSymbols(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	e := newCounting()
	embedAll(t, s, e)
	e.texts = nil
	appendFile(t, rep.Path, "store/null.go", "\n// Fresh is new.\nfunc Fresh() {}\n")
	ensureWith(t, s, rep, e)
	if len(e.texts) != 1 || !strings.Contains(e.texts[0], "Fresh is new.") {
		t.Fatalf("%q", e.texts)
	}
	cold := embed.NewLazy(16, func() (embed.Embedder, error) { return embed.NewFake(16), nil }, nil)
	appendFile(t, rep.Path, "store/null.go", "\n// Fresher is new.\nfunc Fresher() {}\n")
	ensureWith(t, s, rep, cold)
	if cold.Loaded() {
		t.Fatal("Ensure loaded a cold model")
	}
	if n, documented, _ := s.SymbolVecCounts(store.VecScope{Bodies: true}, 16); n != documented-1 {
		t.Fatalf("%d/%d", n, documented)
	}
}

func TestEnsureDefersManyStaleVectors(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	var b strings.Builder
	for i := range index.EnsureEmbedMax + 1 {
		fmt.Fprintf(&b, "\n// F%d is new.\nfunc F%d() {}\n", i, i)
	}
	appendFile(t, rep.Path, "store/null.go", b.String())
	e := newCounting()
	ensureWith(t, s, rep, e)
	if len(e.texts) != 0 {
		t.Fatalf("embedded %d", len(e.texts))
	}
	if err := index.Files(s, rep, index.Options{Workspace: "fixture", Embedder: e}, []string{"store/doc.go"}); err != nil {
		t.Fatal(err)
	}
}

func ensureWith(t *testing.T, s *store.Store, rep *config.Repo, e embed.Embedder) {
	t.Helper()
	if _, err := index.Ensure(s, rep, index.Options{Workspace: "fixture", MaxFiles: 200, Embedder: e}); err != nil {
		t.Fatal(err)
	}
}

// failing is an embedder whose batches fail.
type failing struct{ *embed.Fake }

func (failing) EmbedBatch([]string) ([][]float32, error) { return nil, errors.New("boom") }

// checkTexts checks that the embedded texts carry the document prefix,
// compose MemStore.Get as spec 7.7 says and leave out the undocumented
// fallback, a function of one line.
func checkTexts(t *testing.T, texts []string) {
	t.Helper()
	for _, text := range texts {
		if !strings.HasPrefix(text, embed.DocumentPrefix) {
			t.Fatalf("no document prefix: %q", text)
		}
	}
	if !containsText(texts, "store/store.go\nMemStore\nfunc (m *MemStore) Get(key string) (string, error)\nGet implements Store.\nfunc (m *MemStore) Get") {
		t.Fatalf("MemStore.Get text missing from %q", texts)
	}
	if containsText(texts, "fallback(key string)") {
		t.Fatal("a trivial undocumented symbol was embedded")
	}
}

func containsText(texts []string, sub string) bool {
	for _, t := range texts {
		if strings.Contains(t, sub) {
			return true
		}
	}
	return false
}

func editFile(t *testing.T, root, rel, old, repl string) {
	t.Helper()
	p := filepath.Join(root, rel)
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), old) {
		t.Fatalf("%s has no %q", rel, old)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(src), old, repl, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVectorCounts(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	n, documented, err := index.VectorCounts(s, 0, 16)
	if err != nil || n != 0 || documented == 0 {
		t.Fatalf("%d/%d %v", n, documented, err)
	}
	embedAll(t, s, embed.NewFake(16))
	if n, d, _ := index.VectorCounts(s, 0, 16); n != d || d != documented {
		t.Fatalf("%d/%d", n, d)
	}
	if n, _, _ := index.VectorCounts(s, 0, 32); n != 0 {
		t.Fatalf("size mismatch counted: %d", n)
	}
	if n, d, _ := index.VectorCounts(s, 0, 0); n != d {
		t.Fatalf("any size: %d/%d", n, d)
	}
}

func TestSymbolLosingItsDocIsEmbeddedFromItsBody(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	embedAll(t, s, embed.NewFake(16))
	q, _ := embed.Query(embed.NewFake(16), "Put implements Store")
	before, _ := s.NearestSymbols(q, store.SearchFilter{}, 1000)
	editFile(t, rep.Path, "store/store.go", "// Put implements Store.\n", "")
	indexRepo(t, s, rep)
	e := newCounting()
	if res := embedAll(t, s, e); res.Removed != 0 || res.Symbols != 1 || containsText(e.texts, "Put implements Store.") {
		t.Fatalf("%+v %q", res, e.texts)
	}
	if got, _ := s.NearestSymbols(q, store.SearchFilter{}, 1000); len(got) != len(before) {
		t.Fatalf("the symbol left the scan: %d -> %d", len(before), len(got))
	}
}

func TestEmbedVectorsShortestTextsFirst(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	e := newCounting()
	embedAll(t, s, e)
	for i := 1; i < len(e.texts); i++ {
		if len(e.texts[i]) < len(e.texts[i-1]) {
			t.Fatalf("text %d is shorter than the one before: %q", i, e.texts[i])
		}
	}
}

// Package versions of a dependency database are embedded from signature
// and doc alone: no file is read, so a package whose directory is gone
// still gets its vectors, and gets them once.
func TestEmbedAPIVectorsNeedNoSourceFiles(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	row, err := s.RepoByPath(rep.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(rep.Path); err != nil {
		t.Fatal(err)
	}
	e := newCounting()
	ids := []int64{row.ID, row.ID + 9}
	res, err := index.EmbedAPIVectors(s, e, ids, 0)
	if err != nil || res.Symbols == 0 || res.Modules == 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if n, documented, err := s.SymbolVecCounts(store.VecScope{RepoID: row.ID}, 16); err != nil || n != documented || n != res.Symbols {
		t.Fatalf("%+v %d/%d %v", res, n, documented, err)
	}
	if body := strings.Join(e.texts, "\n"); strings.Contains(body, "return ") {
		t.Fatalf("a body was embedded: %q", body)
	}
	if res, err := index.EmbedAPIVectors(s, e, ids, 0); err != nil || res.Symbols+res.Modules != 0 {
		t.Fatalf("a second pass embeds nothing: %+v %v", res, err)
	}
}

// More missing vectors than the limit are deferred, and an embedder that
// cannot load or fails ends the pass as for workspace vectors.
func TestEmbedAPIVectorsDeferredUnavailableAndFailing(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	row, err := s.RepoByPath(rep.Path)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := index.EmbedAPIVectors(s, embed.NewFake(16), []int64{row.ID}, 2); err != nil || res.Deferred <= 2 {
		t.Fatalf("over the limit nothing is embedded: %+v %v", res, err)
	}
	broken := embed.NewLazy(8, func() (embed.Embedder, error) { return nil, errors.New("no model") }, nil)
	if res, err := index.EmbedAPIVectors(s, broken, []int64{row.ID}, 0); err != nil || !res.Unavailable {
		t.Fatalf("a model that cannot load: %+v %v", res, err)
	}
	if _, err := index.EmbedAPIVectors(s, failing{embed.NewFake(8)}, []int64{row.ID}, 0); err == nil {
		t.Fatal("embed error swallowed")
	}
}
