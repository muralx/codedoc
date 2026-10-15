package query_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func TestSymbolBodyUnavailableWhenFileChanged(t *testing.T) {
	c := indexedWorkspace(t)
	t.Setenv("CODEDOC_FRESH_MAX_FILES", "0")
	writeAppend(t, filepath.Join(c.Workspace.Repos[0].Path, "store", "store.go"), "\nfunc Later() {}\n")
	r, err := query.Symbol(c, "MemStore.Get", query.SymbolOpts{Context: 2})
	if err != nil {
		t.Fatal(err)
	}
	e := r.Sections[0].Entries[0]
	text := render(t, r)
	if e.Body != "" || !strings.Contains(r.Note, "body unavailable: file changed since indexing; run codedoc index") ||
		!strings.Contains(text, "store/store.go:26-32  func (m *MemStore) Get(key string) (string, error)") {
		t.Fatal(text)
	}
}

func TestSymbolDocOnly(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Symbol(c, "MemStore.Get", query.SymbolOpts{DocOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	text := render(t, r)
	want := "MemStore.Get  store/store.go:26-32  func (m *MemStore) Get(key string) (string, error)  Get implements Store.\n"
	if !strings.Contains(text, want) || strings.Contains(text, "26\t") {
		t.Fatal(text)
	}
	if text = keyed(t, r); !strings.Contains(text, "Get implements Store.  [doc:ok]\n") || strings.Contains(text, "module:") {
		t.Fatal(text)
	}
}

func TestSymbolContextLines(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Symbol(c, "MemStore.Get", query.SymbolOpts{Context: 2})
	if err != nil {
		t.Fatal(err)
	}
	e := r.Sections[0].Entries[0]
	text := render(t, r)
	if e.BodyStart != 24 || e.BodyEnd != 34 || !strings.Contains(text, "25\t// Get implements Store.\n") ||
		!strings.Contains(text, "34\t// Put implements Store.\n") || strings.Contains(text, "35\t") {
		t.Fatal(text)
	}
	r, err = query.Symbol(c, "go-lib:store/null.go#NullStore", query.SymbolOpts{Context: 50})
	if err != nil {
		t.Fatal(err)
	}
	if e := r.Sections[0].Entries[0]; e.BodyStart != 1 || e.BodyEnd != 8 || !strings.HasPrefix(e.Body, "package store\n") {
		t.Fatalf("%+v", e)
	}
}

func TestOffsetPastTheEndIsNoted(t *testing.T) {
	c := indexedWorkspace(t)
	c.Offset = 1000
	r, err := query.Find(c, "Get", query.FindOpts{})
	if err != nil {
		t.Fatal(err)
	}
	text := render(t, r)
	if !strings.Contains(r.Note, "offset 1000 is past the end (total 4)") || !strings.Contains(text, "== find Get (4)\n") {
		t.Fatal(text)
	}
	c.Offset = 1
	if r, _ = query.Find(c, "Get", query.FindOpts{}); r.Note != "" || !strings.Contains(render(t, r), "== find Get (4)\n") {
		t.Fatal(render(t, r))
	}
}
