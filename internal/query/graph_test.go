package query_test

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
)

var update = flag.Bool("update", false, "rewrite the snapshot files under testdata")

// snapshot compares text with testdata/<name>.txt, or rewrites it with
// -update.
func snapshot(t *testing.T, name, text string) {
	t.Helper()
	p := filepath.Join("testdata", name+".txt")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if string(want) != text {
		t.Fatalf("snapshot %s differs:\n--- want\n%s--- got\n%s", name, want, text)
	}
}

// run runs a query and returns its text rendering.
func run(t *testing.T, r *output.Response, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return render(t, r)
}

func mustContain(t *testing.T, text string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("missing %q in\n%s", w, text)
		}
	}
}

const (
	libStore   = "go-lib:go:github.com/fixture/lib/store:"
	libService = "go-lib:go:github.com/fixture/lib/service:"
	appKey     = "go-app:go:github.com/fixture/app:"
	records    = "ts-domain:ts:@fixture/domain:src/records.ts/"
)

func TestCallersTreeSnapshot(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Callers(c, "Service.Lookup", query.GraphOpts{Depth: 2})
	text := run(t, r, err)
	snapshot(t, "callers_service_lookup_depth2", text)
}

func TestTestsSnapshot(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Tests(c, "MemStore.Put", query.GraphOpts{})
	text := run(t, r, err)
	snapshot(t, "tests_memstore_put", text)
	mustContain(t, text, "TestMemStorePutGet  store/store_test.go:7  if err := m.Put(\"k\", \"v\"); err != nil {  [test]\n")
	mustContain(t, keyed(t, r), "== tests of "+libStore+"MemStore.Put (1)", libStore+"TestMemStorePutGet  store/store_test.go:7")
}

func TestCallersDirectAndAmbiguous(t *testing.T) {
	c := ambiguousWorkspace(t)
	r, err := query.Callers(c, "fallback", query.GraphOpts{Depth: 1})
	// A caller is its name, the place of the call and the line that
	// makes it.
	mustContain(t, run(t, r, err), "go-lib@", "== callers of fallback (1)\nService.Lookup  service/service.go:20  return fallback(key)\n")
	mustContain(t, keyed(t, r), libService+"Service.Lookup  service/service.go:20  func (svc *Service) Lookup")
	r, err = query.Callers(c, "Store.Get", query.GraphOpts{Depth: 1})
	text := run(t, r, err)
	// A call whose target is one of several candidates is never listed
	// among the certain ones: it follows them under "possibly", marked
	// at the start of its line and with the number of candidates.
	mustContain(t, text, "== callers of Store.Get (1)\nService.Lookup  service/service.go:18  v, err := svc.s.Get(key)\n"+
		"== possibly callers of Store.Get (1)\n? TestRelay  store/relay_test.go:6  ", "  [test] [1 of 4]\n")
	if strings.Contains(text, "[ambiguous]") {
		t.Fatalf("the mark says it, not a tag at the end of the line:\n%s", text)
	}
	maybe := r.Sections[1].Entries[0]
	if !slices.Contains(maybe.Tags, "ambiguous") || maybe.Extra["confidence"] != "ambiguous" {
		t.Fatalf("JSON keeps the tag and the confidence: %+v", maybe)
	}
	mustContain(t, keyed(t, r), libService+"Service.Lookup  service/service.go:18", "[import]",
		"== possibly callers of "+libStore+"Store.Get (1)\n? "+libStore+"TestRelay  ")
	if strings.Contains(text, "TestMemStorePutGet") {
		t.Fatalf("m := NewMemStore(); m.Get() calls MemStore.Get alone:\n%s", text)
	}
	r, err = query.Callers(c, "Store.Get", query.GraphOpts{Depth: 1, NoTests: true})
	if text := run(t, r, err); strings.Contains(text, "TestRelay") {
		t.Fatalf("--no-tests:\n%s", text)
	}
	r, err = query.Callers(c, "isHealthy", query.GraphOpts{Depth: 1})
	mustContain(t, run(t, r, err), "\ncheck  ts-app:src/main.ts:6  ")
	mustContain(t, keyed(t, r), "ts-app:ts:@fixture/app:src/main.ts/check  ts-app:src/main.ts:6", "[import]")
}

func TestCallersRepoFilterAndGroupBy(t *testing.T) {
	c := ambiguousWorkspace(t)
	c.Repo, c.Filter = c.Workspace.Repos[1], true // go-app
	r, err := query.Callers(c, "Service.Lookup", query.GraphOpts{Depth: 1})
	mustContain(t, run(t, r, err), "\nrun  go-app:main.go:16  ")
	c.Repo = c.Workspace.Repos[0] // go-lib: the caller in go-app is filtered out
	r, err = query.Callers(c, "Service.Lookup", query.GraphOpts{Depth: 2})
	if text := run(t, r, err); strings.Contains(text, "go-app:main.go") {
		t.Fatal(text)
	}
	c.Filter = false
	r, err = query.Callers(c, "Store.Get", query.GraphOpts{Depth: 1, GroupByFile: true})
	if r == nil || err != nil || len(r.Sections) != 2 || !strings.HasSuffix(r.Sections[1].Title, "go-lib:store/relay_test.go") {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestCallersErrors(t *testing.T) {
	c := indexedWorkspace(t)
	if _, err := query.Callers(c, "Nope", query.GraphOpts{Depth: 1}); !errors.Is(err, query.ErrNotFound) {
		t.Fatal(err)
	}
	c.Strict = true
	if _, err := query.Callers(c, "Get", query.GraphOpts{Depth: 1}); !errors.Is(err, query.ErrAmbiguous) {
		t.Fatal(err)
	}
	if _, err := query.Callers(c, "fallback", query.GraphOpts{}); err == nil {
		t.Fatal("depth 0 accepted")
	}
}

func TestCallees(t *testing.T) {
	c := ambiguousWorkspace(t)
	r, err := query.Callees(c, "Service.Lookup", query.GraphOpts{Depth: 1})
	text := run(t, r, err)
	mustContain(t, text, "== callees of Service.Lookup (2)\nservice/service.go\n  Store.Get  :18  v, err := svc.s.Get(key)\n"+
		"  fallback  :20  return fallback(key)\n")
	mustContain(t, keyed(t, r), libStore+"Store.Get  :18", "[import]", libService+"fallback  :20")
	// An ambiguous call names its candidates, apart from the certain
	// callees.
	r, err = query.Callees(c, "TestRelay", query.GraphOpts{Depth: 1})
	mustContain(t, run(t, r, err), "== possibly callees of TestRelay (1)\n? Cache.Get | NullStore.Get | Store.Get | MemStore.Get  "+
		"store/relay_test.go:6  ")
	mustContain(t, keyed(t, r), "\n?  store/relay_test.go:6  one of ", libStore+"MemStore.Get")
	r, err = query.Callees(c, "TestMemStorePutGet", query.GraphOpts{Depth: 1})
	run(t, r, err)
	mustContain(t, keyed(t, r), libStore+"MemStore.Get  :10  func (m *MemStore) Get(key string) (string, error)"+
		"  Get implements Store.  [local]")
	r, err = query.Callees(c, "main", query.GraphOpts{Depth: 3})
	mustContain(t, run(t, r, err), "\n  Service.Lookup  go-app:main.go:16  ", "\nNewMemStore  go-app:main.go:12  ")
	mustContain(t, keyed(t, r), "\n  "+libService+"Service.Lookup  go-app:main.go:16",
		libStore+"NewMemStore  go-app:main.go:12  func NewMemStore() *MemStore  NewMemStore creates an empty MemStore.  [import]")
}

func TestRefsImplsSupertypes(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Refs(c, "MemStore", query.GraphOpts{Kind: "instantiate"})
	text := run(t, r, err)
	mustContain(t, text, "== references to MemStore (1)\nNewMemStore  store/store.go:23  func NewMemStore() *MemStore {")
	if text = keyed(t, r); strings.Count(text, "[instantiate]") != 1 || strings.Contains(text, "[type]") {
		t.Fatal(text)
	}
	if _, err := query.Refs(c, "MemStore", query.GraphOpts{Kind: "call"}); err == nil {
		t.Fatal("refs --kind call accepted")
	}
	r, err = query.Impls(c, "go-lib:store/store.go#Store", query.GraphOpts{})
	mustContain(t, run(t, r, err), "\nMemStore  store/store.go:18  type MemStore struct {\n", "\nNullStore  store/null.go:4  ")
	mustContain(t, keyed(t, r), libStore+"MemStore  store/store.go:18", libStore+"NullStore  store/null.go:4")
	r, err = query.Impls(c, "EnvironmentRepo", query.GraphOpts{})
	mustContain(t, run(t, r, err), "ts-domain@", "\nMemoryRepo  src/records.ts:17  ")
	mustContain(t, keyed(t, r), records+"MemoryRepo  src/records.ts:17")
	r, err = query.Supertypes(c, "MemStore", query.GraphOpts{})
	mustContain(t, run(t, r, err), "== supertypes of MemStore (1)\nStore  store/store.go:10  type Store interface {\n")
}

func TestTestsTransitiveAndModule(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Tests(c, "Environment", query.GraphOpts{Transitive: true})
	mustContain(t, run(t, r, err), "\nit:stores and checks an environment  src/records.test.ts:6  ", "  via MemoryRepo.load  [test]")
	mustContain(t, keyed(t, r), "records.test.ts/it:stores and checks an environment")
	r, err = query.Tests(c, "go-lib:store", query.GraphOpts{})
	mustContain(t, run(t, r, err), "tests of module github.com/fixture/lib/store", "TestMemStorePutGet  ")
	if _, err := query.Tests(c, "nothing-here", query.GraphOpts{}); !errors.Is(err, query.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSiblings(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Siblings(c, "MemStore.Get")
	mustContain(t, run(t, r, err), "== other implementations of Store (1)\nNullStore.Get  store/null.go:6  ")
	mustContain(t, keyed(t, r), "other implementations of "+libStore+"Store", libStore+"NullStore.Get")
	r, err = query.Siblings(c, "MemStore")
	mustContain(t, run(t, r, err), "\nNullStore  store/null.go:4  ")
	r, err = query.Siblings(c, "Store.Get")
	mustContain(t, run(t, r, err), "other method symbols of Store", "\nStore.Put  store/store.go:14  ")
	r, err = query.Siblings(c, "fallback")
	mustContain(t, run(t, r, err), "other function symbols in github.com/fixture/lib/service")
	mustContain(t, keyed(t, r), libService+"New  ")
	r, err = query.Siblings(c, "ts-domain:src/records.test.ts")
	mustContain(t, run(t, r, err), "*.test.ts in src (0)")
}

func TestDepsAndDependents(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Deps(c, "github.com/fixture/app", query.GraphOpts{Depth: 1})
	mustContain(t, run(t, r, err), "(0)", "2 cross-repo modules hidden; rerun with --cross-repo")
	r, err = query.Deps(c, "go-app:.", query.GraphOpts{Depth: 1, CrossRepo: true})
	mustContain(t, run(t, r, err), "github.com/fixture/lib/store  go-lib:store  go go-package  [cross-repo]")
	r, err = query.Dependents(c, "github.com/fixture/lib/store", query.GraphOpts{Depth: 2, CrossRepo: true})
	mustContain(t, run(t, r, err), "github.com/fixture/lib/service  go-lib:service", "github.com/fixture/app  go-app:.")
	r, err = query.Dependents(c, "@fixture/domain", query.GraphOpts{Depth: 1, CrossRepo: true})
	mustContain(t, run(t, r, err), "ts-app:src  ts npm-dir  [cross-repo]")
	if _, err := query.Deps(c, "github.com/none", query.GraphOpts{Depth: 1}); !errors.Is(err, query.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := query.Deps(c, "go-lib:store", query.GraphOpts{}); err == nil {
		t.Fatal("depth 0 accepted")
	}
}

func TestOutlineModuleIdentity(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Outline(c, "github.com/fixture/lib/store", query.OutlineOpts{})
	mustContain(t, run(t, r, err), "== github.com/fixture/lib/store (go-lib:store)", "store/store.go  ")
	r, err = query.Outline(c, "@fixture/domain", query.OutlineOpts{})
	mustContain(t, run(t, r, err), "src/  4 files")
}

func TestPathSiblings(t *testing.T) {
	c := indexedWorkspace(t)
	root := c.Workspace.Repos[0].Path
	for _, f := range []string{"providers/a/server.go", "providers/b/server.go", "providers/a/util.go",
		"providers/c/util.go", "providers/a/x_store.go", "providers/a/y_store.go"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		pkg := filepath.Base(filepath.Dir(p))
		if err := os.WriteFile(p, []byte("package "+pkg+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := query.Siblings(c, "go-lib:providers/a/server.go")
	mustContain(t, run(t, r, err), "== server.go in sibling directories (1)\nproviders/b/server.go  1 lines")
	r, err = query.Siblings(c, "go-lib:providers/a/x_store.go")
	mustContain(t, run(t, r, err), "== *_store.go in providers/a (1)\nproviders/a/y_store.go")
	r, err = query.Siblings(c, "go-lib:providers/a")
	mustContain(t, run(t, r, err), "providers/b/  1 shared: server.go", "providers/c/  1 shared: util.go")
}

func TestGoStyleAddressAndOffsetAncestry(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Symbol(c, "store.NewMemStore", query.SymbolOpts{})
	mustContain(t, run(t, r, err), "NewMemStore  store/store.go:23\n")
	mustContain(t, keyed(t, r), libStore+"NewMemStore")
	r, err = query.Symbol(c, "service.Service.Lookup", query.SymbolOpts{})
	mustContain(t, run(t, r, err), "Service.Lookup  service/service.go:17-23\n")
	c.Offset = 1
	r, err = query.Callers(c, "Service.Lookup", query.GraphOpts{Depth: 2})
	if err != nil || !strings.Contains(r.Note, "callers of Service.Lookup continues under run") {
		t.Fatalf("%+v %v", r, err)
	}
}
