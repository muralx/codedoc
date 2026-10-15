package query_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/stablekey"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
)

// indexedWorkspace indexes copies of the four Go and TypeScript fixture
// repos into one temp database and returns a query context over them.
func indexedWorkspace(t *testing.T) *query.Ctx {
	t.Helper()
	return workspaceOf(t, "go-lib", "go-app", "ts-domain", "ts-app")
}

// relayTest is a Go test that calls Get on the result of a call: no
// declaration makes the type of that receiver evident, so the call is
// ambiguous among the Get methods of the workspace.
const relayTest = `package store

import "testing"

func TestRelay(t *testing.T) {
	if _, err := pick().Get("k"); err != nil {
		t.Fatal(err)
	}
}

func pick() Store { return NewMemStore() }
`

// ambiguousWorkspace is indexedWorkspace with relayTest added to go-lib
// as store/relay_test.go.
func ambiguousWorkspace(t *testing.T) *query.Ctx {
	t.Helper()
	var repos []*config.Repo
	for _, name := range []string{"go-lib", "go-app", "ts-domain", "ts-app"} {
		repos = append(repos, &config.Repo{Name: name, Path: testutil.CopyFixture(t, name)})
	}
	if err := os.WriteFile(filepath.Join(repos[0].Path, "store", "relay_test.go"), []byte(relayTest), 0o644); err != nil {
		t.Fatal(err)
	}
	return indexRepos(t, repos...)
}

// workspaceOf indexes copies of the named fixture repos into one temp
// database and returns a query context over them.
func workspaceOf(t *testing.T, names ...string) *query.Ctx {
	t.Helper()
	var repos []*config.Repo
	for _, name := range names {
		repos = append(repos, &config.Repo{Name: name, Path: testutil.CopyFixture(t, name)})
	}
	return indexRepos(t, repos...)
}

// indexRepos indexes repos into one temp database as the workspace
// "fixture" and returns a query context over them.
func indexRepos(t *testing.T, repos ...*config.Repo) *query.Ctx {
	t.Helper()
	ws := &config.Workspace{Name: "fixture", Repos: repos}
	s, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	opts := index.Options{Workspace: ws.Name, RepoOrder: ws.RepoNames()}
	for _, r := range ws.Repos {
		if _, err := index.Repo(s, r, opts); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.ResolvePending(s, opts); err != nil {
		t.Fatal(err)
	}
	return &query.Ctx{Store: s, Workspace: ws}
}

func render(t *testing.T, r *output.Response) string {
	t.Helper()
	var b bytes.Buffer
	if _, err := output.Render(&b, r, "text", 0); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// keyed renders a response as --keys --detail prints it: stable keys in
// place of names, every tag, signature and doc.
func keyed(t *testing.T, r *output.Response) string {
	t.Helper()
	r.Keys, r.Detail = true, true
	return render(t, r)
}

// names returns the qualified names (last key segment) of a section.
func names(sec output.Section) []string {
	out := make([]string, len(sec.Entries))
	for i, e := range sec.Entries {
		k, _ := stablekey.Parse(e.Key)
		out[i] = k.Qualified
	}
	return out
}

func TestOutlineFile(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Outline(c, "go-lib:store/store.go", query.OutlineOpts{})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(names(r.Sections[0]), ",")
	if got != "ErrMissing,Store,MemStore,NewMemStore,MemStore.Get,MemStore.Put" {
		t.Fatal(got)
	}
	text := render(t, r)
	for _, want := range []string{
		"== store/store.go, 38 lines (6)\nErrMissing  :7  var ErrMissing = errors.New(\"missing\")  ErrMissing",
		"\nStore  :10-15  type Store interface  Store persists string values by key.  [+2 members]\n",
		"\nMemStore.Get  :26-32  func (m *MemStore) Get(key string) (string, error)  Get implements Store.\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	// The header names the repo of a workspace of several, and says
	// nothing about a fresh, clean index.
	if !strings.HasPrefix(text, "go-lib@") || strings.Contains(text, "fresh") || strings.Contains(text, "dirty") {
		t.Fatal(text)
	}
	if text = keyed(t, r); !strings.Contains(text, "\ngo-lib:go:github.com/fixture/lib/store:ErrMissing  :7  var ErrMissing") {
		t.Fatal(text)
	}
	r, err = query.Outline(c, "go-lib:store/store.go", query.OutlineOpts{Doc: true})
	if text = run(t, r, err); !strings.Contains(text, "MemStore  :18-20  type MemStore struct  MemStore is an in-memory Store.") {
		t.Fatal(text)
	}
}

func TestOutlineTagsUndocumentedSymbols(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Outline(c, "go-lib:store/null.go", query.OutlineOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if text := render(t, r); !strings.Contains(text, "\nNullStore.Get  :6  func (NullStore) Get") ||
		strings.Contains(text, "[doc:missing]") {
		t.Fatal(text)
	}
	if text := keyed(t, r); !strings.Contains(text, "NullStore.Get  :6  func (NullStore) Get") ||
		!strings.Contains(text, "[doc:missing]") {
		t.Fatal(text)
	}
}

func TestOutlineDirectory(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Outline(c, "go-lib:store", query.OutlineOpts{})
	if err != nil {
		t.Fatal(err)
	}
	text := render(t, r)
	for _, want := range []string{"store/null.go  3 symbols, 8 lines", "store/store.go  9 symbols, 38 lines", "store/store_test.go  1 symbol, 13 lines"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	if len(r.Sections[0].Entries) != 4 {
		t.Fatal(text)
	}
}

func TestOutlineRepoRootListsSubdirectories(t *testing.T) {
	c := indexedWorkspace(t)
	c.Repo = c.Workspace.Repos[0]
	r, err := query.Outline(c, ".", query.OutlineOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if text := render(t, r); !strings.Contains(text, "store/  4 files") || !strings.Contains(text, "go.mod") {
		t.Fatal(text)
	}
}

func TestOutlineMissingPathIsNotFound(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Outline(c, "go-lib:nope", query.OutlineOpts{})
	if !errors.Is(err, query.ErrNotFound) || r != nil || !strings.Contains(err.Error(), "no such file or directory in go-lib") {
		t.Fatalf("%+v %v", r, err)
	}
	c.Strict = true
	r, err = query.Outline(c, "go-lib:nope", query.OutlineOpts{})
	if !errors.Is(err, query.ErrNotFound) || len(r.Sections) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := query.Outline(c, "unknown:x", query.OutlineOpts{}); err == nil {
		t.Fatal("unknown repo should fail")
	}
}

func TestFind(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Find(c, "Get", query.FindOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(r.Sections[0]), ","); got != "Cache.Get,NullStore.Get,Store.Get,MemStore.Get" {
		t.Fatal(got)
	}
	if text := render(t, r); !strings.HasPrefix(text, "go-lib@") ||
		!strings.Contains(text, "\nCache.Get  service/cache.go:9-12  func (c *Cache) Get(key string) (string, bool)  "+
			"Get returns the cached value for key.\n") ||
		!strings.Contains(text, "store/store.go\n  Store.Get  :12  Get(key string) (string, error)  Get returns the value for key.\n") {
		t.Fatal("an answer drawing on one repo has its header: " + text)
	}
	c.Repo, c.Filter = c.Workspace.Repos[0], true
	r, _ = query.Find(c, "Get", query.FindOpts{Kind: "method"})
	if len(r.Sections[0].Entries) != 4 {
		t.Fatal(render(t, r))
	}
	c.Repo, c.Filter = nil, false
	r, _ = query.Find(c, "*Store", query.FindOpts{Kind: "struct"})
	if got := strings.Join(names(r.Sections[0]), ","); got != "NullStore,MemStore" {
		t.Fatal(got)
	}
}

func TestFindFiltersLanguageAndPages(t *testing.T) {
	c := indexedWorkspace(t)
	r, _ := query.Find(c, "*", query.FindOpts{Lang: "ts"})
	for _, e := range r.Sections[0].Entries {
		if k := mustKey(t, e.Key); k.Lang != "ts" {
			t.Fatal(e.Key)
		}
	}
	all := len(r.Sections[0].Entries)
	c.Offset = 2
	r, _ = query.Find(c, "*", query.FindOpts{Lang: "ts"})
	if len(r.Sections[0].Entries) != all-2 || r.Sections[0].Offset != 2 {
		t.Fatalf("%d of %d", len(r.Sections[0].Entries), all)
	}
	r, _ = query.Find(c, "Nothing", query.FindOpts{})
	if len(r.Sections[0].Entries) != 0 {
		t.Fatal(render(t, r))
	}
}

func TestSymbolDetailAndBody(t *testing.T) {
	c := ambiguousWorkspace(t)
	r, err := query.Symbol(c, "MemStore.Get", query.SymbolOpts{})
	if err != nil {
		t.Fatal(err)
	}
	text := render(t, r)
	for _, want := range []string{
		// One line names the symbol and its place; the lines of the file
		// follow from the doc comment on, which is so never said twice.
		"MemStore.Get  store/store.go:26-32\n25\t// Get implements Store.\n26\tfunc (m *MemStore) Get(key string) (string, error) {\n",
		"32\t}\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	if !strings.HasPrefix(text, "go-lib@") || strings.Count(text, "Get implements Store.") != 1 {
		t.Fatal(text)
	}
	e := r.Sections[0].Entries[0]
	if e.Continue != "codedoc lines go-lib:store/store.go" || e.Text == "" || e.Doc == "" || e.BodyStart != 25 {
		t.Fatalf("JSON keeps signature and doc, and a body can be continued: %+v", e)
	}
	want := "go-lib:go:github.com/fixture/lib/store:MemStore.Get  store/store.go:26-32  func (m *MemStore) Get(key string) (string, error)  Get implements Store.  [method] [container:MemStore] [module:github.com/fixture/lib/store] [fan-in:2 (1 ambiguous)] [fan-out:2] [doc:ok]\n"
	if text = keyed(t, r); !strings.Contains(text, want) {
		t.Fatalf("--detail: missing %q in\n%s", want, text)
	}
	r, err = query.Symbol(c, "MemStore.Get", query.SymbolOpts{NoBody: true})
	if text = run(t, r, err); !strings.HasSuffix(text, "MemStore.Get  store/store.go:26-32  "+
		"func (m *MemStore) Get(key string) (string, error)  Get implements Store.\n") {
		t.Fatalf("--no-body:\n%s", text)
	}
}

func TestSymbolBodyKeepsIndentation(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Symbol(c, "MemoryRepo.save", query.SymbolOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if text := render(t, r); !strings.Contains(text, "23\t  save(env: Environment): void {\n") {
		t.Fatal(text)
	}
}

func TestSymbolAddressForms(t *testing.T) {
	c := indexedWorkspace(t)
	for _, addr := range []string{
		"go-lib:go:github.com/fixture/lib/store:MemStore.Get", "go-lib:store/store.go#MemStore.Get", "ts-domain:src/records.ts#MemoryRepo.save",
	} {
		if _, err := query.Symbol(c, addr, query.SymbolOpts{}); err != nil {
			t.Errorf("%s: %v", addr, err)
		}
	}
	c.Repo = c.Workspace.Repos[0]
	if _, err := query.Symbol(c, "store/null.go#NullStore.Put", query.SymbolOpts{}); err != nil {
		t.Fatal(err)
	}
}

func TestSymbolAmbiguousAndNotFound(t *testing.T) {
	c := indexedWorkspace(t)
	c.Strict = true
	r, err := query.Symbol(c, "Get", query.SymbolOpts{})
	if !errors.Is(err, query.ErrAmbiguous) || r == nil || len(r.Sections[0].Entries) != 4 {
		t.Fatalf("%v %+v", err, r)
	}
	for _, addr := range []string{"Nope", "go-lib:go:github.com/fixture/lib/store:Nope", "go-lib:go:store:",
		"go-lib:go:store:#abcd", "nope:go:store:X", "go-lib:store/store.go#Nope", "go-lib:missing.go#X"} {
		if _, err := query.Symbol(c, addr, query.SymbolOpts{}); !errors.Is(err, query.ErrNotFound) || errors.Is(err, query.ErrNearest) {
			t.Errorf("%s: %v", addr, err)
		}
	}
}

func TestStaleAnswerSaysFreshNo(t *testing.T) {
	c := indexedWorkspace(t)
	t.Setenv("CODEDOC_FRESH_MAX_FILES", "0")
	writeAppend(t, filepath.Join(c.Workspace.Repos[0].Path, "store", "null.go"), "\nfunc Later() {}\n")
	r, err := query.Outline(c, "go-lib:store/null.go", query.OutlineOpts{})
	if err != nil {
		t.Fatal(err)
	}
	text := render(t, r)
	if !strings.HasPrefix(text, "go-lib@0b29eeb dirty fresh:no\n") || !strings.Contains(text, "codedoc index") || strings.Contains(text, "Later") {
		t.Fatal(text)
	}
	t.Setenv("CODEDOC_FRESH_MAX_FILES", "x")
	c.Fresh = nil
	if _, err := query.Outline(c, "go-lib:store/null.go", query.OutlineOpts{}); err == nil {
		t.Fatal("bad CODEDOC_FRESH_MAX_FILES accepted")
	}
}

func TestParseAddr(t *testing.T) {
	cases := map[string]query.Addr{
		"r:go:m:A.b":       {Kind: query.AddrKey, Key: "r:go:m:A.b"},
		"r:go:m:A.b#7f3a":  {Kind: query.AddrKey, Key: "r:go:m:A.b#7f3a"},
		"r:a/b.go#X.y":     {Kind: query.AddrPath, Repo: "r", Path: "a/b.go", Name: "X.y"},
		"a/b.go#X.y":       {Kind: query.AddrPath, Path: "a/b.go", Name: "X.y"},
		"Type::method":     {Kind: query.AddrBare, Name: "Type::method"},
		"a:b:c:d":          {Kind: query.AddrBare, Name: "a:b:c:d"},
		"MemStore.Get":     {Kind: query.AddrBare, Name: "MemStore.Get"},
		"t.ts#it:x~2":      {Kind: query.AddrPath, Path: "t.ts", Name: "it:x~2"},
		"Base.#secret":     {Kind: query.AddrBare, Name: "Base.#secret"},
		"a.ts#Base.#x":     {Kind: query.AddrPath, Path: "a.ts", Name: "Base.#x"},
		"r:t.ts#it:case 1": {Kind: query.AddrPath, Repo: "r", Path: "t.ts", Name: "it:case 1"},
		"r:go:m:":          {Kind: query.AddrBare, Name: "r:go:m:"},
	}
	for in, want := range cases {
		if got := query.ParseAddr(in); got != want {
			t.Errorf("%s: got %+v want %+v", in, got, want)
		}
	}
}

func writeAppend(t *testing.T, p, text string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// mustKey parses a stable key the test knows to be valid.
func mustKey(t testing.TB, s string) stablekey.Key {
	t.Helper()
	k, err := stablekey.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
