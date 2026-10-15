package query_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func readRepoFile(t *testing.T, c *query.Ctx, repo, rel string) string {
	t.Helper()
	for _, r := range c.Workspace.Repos {
		if r.Name == repo {
			b, err := os.ReadFile(filepath.Join(r.Path, rel))
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
	}
	t.Fatalf("no repo %s", repo)
	return ""
}

func symbolState(t *testing.T, c *query.Ctx, key string) string {
	t.Helper()
	sy, err := c.Store.SymbolByKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return sy.DocState
}

func TestDocSetGoSymbolAndReview(t *testing.T) {
	c := workspaceOf(t, "go-lib")
	r, err := query.DocSet(c, "go-lib:store/store.go#MemStore.Get", "Get returns the value stored under key.\n\nIt returns ErrMissing when the key has no value.")
	text := run(t, r, err)
	mustContain(t, text, "== doc set (1)", libStore+"MemStore.Get  store/store.go:25-27  doc ok  Get returns the value stored under key.")
	mustContain(t, readRepoFile(t, c, "go-lib", "store/store.go"), "// Get returns the value stored under key.\n//\n// It returns ErrMissing")
	src := readRepoFile(t, c, "go-lib", "store/store.go")
	path := filepath.Join(c.Workspace.Repos[0].Path, "store", "store.go")
	if err := os.WriteFile(path, []byte(strings.Replace(src, "return v, nil", "return v + \"\", nil", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Fresh = nil
	if err := c.EnsureFresh(c.Workspace.Repos...); err != nil {
		t.Fatal(err)
	}
	if got := symbolState(t, c, libStore+"MemStore.Get"); got != "stale" {
		t.Fatalf("after a body edit: %s", got)
	}
	r, err = query.DocSet(c, libStore+"MemStore.Get", "Get returns the value stored under key.\n\nIt returns ErrMissing when the key has no value.")
	mustContain(t, run(t, r, err), "doc ok")
	if got := symbolState(t, c, libStore+"MemStore.Get"); got != "ok" {
		t.Fatalf("unchanged text set again: %s", got)
	}
}

func TestDocSetModules(t *testing.T) {
	c := workspaceOf(t, "go-lib", "ts-domain", "py-lib", "java-lib")
	r, err := query.DocSet(c, "go-lib:service/", "Package service runs the fixture use cases.")
	mustContain(t, run(t, r, err), "github.com/fixture/lib/service  service/service.go:1  doc ok  Package service runs the fixture use cases.")
	r, err = query.DocSet(c, "github.com/fixture/lib/store", "Package store keeps values by key.")
	mustContain(t, run(t, r, err), "go-lib@", "store/doc.go:1  doc ok")
	r, err = query.DocSet(c, "ts-domain:src/", "Domain records and repositories.")
	mustContain(t, run(t, r, err), "ts-domain@", "  src/README.md:", "doc ok  Domain records and repositories.")
	r, err = query.DocSet(c, "com.fixture.service", "Use cases of the Java fixture.")
	mustContain(t, run(t, r, err), "java-lib@", "  src/main/java/com/fixture/service/package-info.java:1  doc ok")
	r, err = query.DocSet(c, "pylib", "Python fixture library.")
	mustContain(t, run(t, r, err), "py-lib@", "  src/pylib/__init__.py:1  doc ok  Python fixture library.")
}

func TestDocSetOtherLanguagesAndErrors(t *testing.T) {
	c := workspaceOf(t, "ts-domain", "py-lib", "java-lib")
	r, err := query.DocSet(c, "ts-domain:ts:@fixture/domain:src/records.ts/MemoryRepo", "In-memory repository of environments.")
	mustContain(t, run(t, r, err), "doc ok  In-memory repository of environments.")
	r, err = query.DocSet(c, "java-lib:java:com.fixture.store:MemStore.put#dbc4", "Stores an int value as text.")
	mustContain(t, run(t, r, err), "MemStore.put#dbc4", "doc ok")
	r, err = query.DocSet(c, "py-lib:src/pylib/store.py#MemStore", "Dictionary-backed store.")
	mustContain(t, run(t, r, err), "doc ok  Dictionary-backed store.")
	if _, err := query.DocSet(c, "Nope", "x"); err == nil {
		t.Fatal("unknown address")
	}
	if _, err := query.DocSet(c, "py-lib:src/pylib/store.py#MemStore", "   "); err == nil {
		t.Fatal("empty text")
	}
}

func TestDocStateInOutlineFindSymbol(t *testing.T) {
	c := workspaceOf(t, "go-lib")
	root := c.Workspace.Repos[0].Path
	editRepo(t, root, "store/store.go", "return v, nil", "return v + \"\", nil")
	r, err := query.Outline(c, "go-lib:store/store.go", query.OutlineOpts{})
	text := run(t, r, err)
	// Doc states are detail: the default lines carry none.
	if strings.Contains(text, "[doc:") {
		t.Fatalf("a doc state without --detail:\n%s", text)
	}
	text = keyed(t, r)
	mustContain(t, text, "MemStore.Get  :26-32  func (m *MemStore) Get(key string) (string, error)  Get implements Store.  [doc:stale]")
	if strings.Contains(text, "MemStore.Put  :35-38  func (m *MemStore) Put(key, value string) error  Put implements Store.  [") {
		t.Fatalf("an ok doc is tagged:\n%s", text)
	}
	r, err = query.Find(c, "Get", query.FindOpts{Kind: "method"})
	run(t, r, err)
	mustContain(t, keyed(t, r), "MemStore.Get  :26", "Get implements Store.  [doc:stale]",
		"NullStore.Get  store/null.go:6  func (NullStore) Get(key string) (string, error)  [doc:missing]")
	r, err = query.Symbol(c, "MemStore.Get", query.SymbolOpts{})
	run(t, r, err)
	mustContain(t, keyed(t, r), "[fan-out:2] [doc:stale]")
	r, err = query.Symbol(c, "MemStore.Put", query.SymbolOpts{DocOnly: true})
	run(t, r, err)
	mustContain(t, keyed(t, r), "Put implements Store.  [doc:ok]")
}
