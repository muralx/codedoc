package resolve_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
)

// ws is an indexed workspace of fixture copies.
type ws struct {
	t     *testing.T
	s     *store.Store
	repos []*config.Repo
}

func (w *ws) opts() index.Options {
	cw := config.Workspace{Repos: w.repos}
	return index.Options{Workspace: "fixture", RepoOrder: cw.RepoNames(), MaxFiles: 100}
}

// indexWorkspace indexes every repo, then resolves imports workspace-wide.
func (w *ws) indexAll() {
	w.t.Helper()
	for _, r := range w.repos {
		if _, err := index.Repo(w.s, r, w.opts()); err != nil {
			w.t.Fatal(err)
		}
	}
	if err := index.ResolvePending(w.s, w.opts()); err != nil {
		w.t.Fatal(err)
	}
}

// newWS copies fixtures; each entry is "name=fixture" or "fixture".
func newWS(t *testing.T, repos ...[2]string) *ws {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	w := &ws{t: t, s: s}
	for _, r := range repos {
		w.repos = append(w.repos, &config.Repo{Name: r[0], Path: testutil.CopyFixture(t, r[1])})
	}
	return w
}

func fixtureWS(t *testing.T) *ws {
	return newWS(t, [2]string{"go-lib", "go-lib"}, [2]string{"go-app", "go-app"},
		[2]string{"ts-domain", "ts-domain"}, [2]string{"ts-app", "ts-app"})
}

func (w *ws) repo(name string) int64 {
	w.t.Helper()
	for _, r := range w.repos {
		if r.Name == name {
			row, err := w.s.RepoByPath(r.Path)
			if err != nil {
				w.t.Fatal(err)
			}
			return row.ID
		}
	}
	w.t.Fatalf("no repo %s", name)
	return 0
}

func (w *ws) module(repo, lang, path string) int64 {
	w.t.Helper()
	m, err := w.s.ModuleByPath(w.repo(repo), lang, path)
	if err != nil {
		w.t.Fatalf("module %s %s: %v", repo, path, err)
	}
	return m.ID
}

// imports maps raw import to resolved module id for one file.
func (w *ws) imports(repo, path string) map[string]int64 {
	w.t.Helper()
	f, err := w.s.FileByPath(w.repo(repo), path)
	if err != nil {
		w.t.Fatal(err)
	}
	imps, err := w.s.FileImports(f.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	out := map[string]int64{}
	for _, imp := range imps {
		out[imp.Raw] = imp.ResolvedModuleID
	}
	return out
}

func (w *ws) edges(src int64) map[int64]bool {
	w.t.Helper()
	edges, err := w.s.ModuleEdgesFrom(src)
	if err != nil {
		w.t.Fatal(err)
	}
	out := map[int64]bool{}
	for _, e := range edges {
		if e.Kind != "imports" || e.Confidence != "import" {
			w.t.Errorf("edge %+v", e)
		}
		out[e.Dst] = true
	}
	return out
}

func TestGoImportsResolveAcrossRepos(t *testing.T) {
	w := fixtureWS(t)
	w.indexAll()
	service, st := w.module("go-lib", "go", "service"), w.module("go-lib", "go", "store")
	got := w.imports("go-app", "main.go")
	if got["fmt"] != 0 || got["github.com/fixture/lib/service"] != service || got["github.com/fixture/lib/store"] != st {
		t.Fatalf("imports %v (service %d store %d)", got, service, st)
	}
	app := w.module("go-app", "go", ".")
	if e := w.edges(app); len(e) != 2 || !e[service] || !e[st] {
		t.Fatalf("edges %v", e)
	}
	if e := w.edges(service); len(e) != 1 || !e[st] {
		t.Fatalf("service edges %v", e)
	}
}

func TestTSImportsResolve(t *testing.T) {
	w := fixtureWS(t)
	w.indexAll()
	root, src := w.module("ts-domain", "ts", "."), w.module("ts-domain", "ts", "src")
	if got := w.imports("ts-app", "src/main.ts"); got["@fixture/domain"] != root {
		t.Fatalf("bare import %v, want %d", got, root)
	}
	if got := w.imports("ts-domain", "src/index.ts"); got["./records.js"] != src {
		t.Fatalf("relative import %v, want %d", got, src)
	}
	if e := w.edges(w.module("ts-app", "ts", "src")); len(e) != 1 || !e[root] {
		t.Fatalf("ts-app edges %v", e)
	}
	if e := w.edges(src); len(e) != 0 {
		t.Fatalf("a module importing itself has no edge: %v", e)
	}
}

func TestDuplicateIdentityPrefersImportingRepoThenConfigOrder(t *testing.T) {
	w := newWS(t, [2]string{"lib2", "go-lib"}, [2]string{"app", "go-app"}, [2]string{"lib1", "go-lib"})
	w.indexAll()
	if got := w.imports("app", "main.go"); got["github.com/fixture/lib/store"] != w.module("lib2", "go", "store") {
		t.Fatalf("app resolves to the first repo in config order: %v", got)
	}
	if got := w.imports("lib1", "service/service.go"); got["github.com/fixture/lib/store"] != w.module("lib1", "go", "store") {
		t.Fatalf("a repo's own import resolves to itself: %v", got)
	}
	w.repos[0], w.repos[2] = w.repos[2], w.repos[0]
	if err := index.ResolvePending(w.s, w.opts()); err != nil {
		t.Fatal(err)
	}
	if got := w.imports("app", "main.go"); got["github.com/fixture/lib/store"] != w.module("lib1", "go", "store") {
		t.Fatalf("config order changed: %v", got)
	}
}

func TestEnsureResolvesChangedFiles(t *testing.T) {
	w := fixtureWS(t)
	w.indexAll()
	app := w.repos[1]
	src := "package main\n\nimport \"github.com/fixture/lib/store\"\n\nfunc main() { store.NewMemStore() }\n"
	if err := os.WriteFile(filepath.Join(app.Path, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Ensure(w.s, app, w.opts()); err != nil {
		t.Fatal(err)
	}
	st := w.module("go-lib", "go", "store")
	if got := w.imports("go-app", "main.go"); len(got) != 1 || got["github.com/fixture/lib/store"] != st {
		t.Fatalf("imports %v", got)
	}
	if e := w.edges(w.module("go-app", "go", ".")); len(e) != 1 || !e[st] {
		t.Fatalf("edges %v", e)
	}
}

func TestEnsureReresolvesWhenAProviderDisappears(t *testing.T) {
	w := fixtureWS(t)
	w.indexAll()
	lib := w.repos[0]
	if err := os.RemoveAll(filepath.Join(lib.Path, "service")); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Ensure(w.s, lib, w.opts()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.s.ModuleByPath(w.repo("go-lib"), "go", "service"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("service module kept: %v", err)
	}
	if got := w.imports("go-app", "main.go"); got["github.com/fixture/lib/service"] != 0 {
		t.Fatalf("imports %v", got)
	}
}

func TestEnsureDropsEdgesOfDeletedFiles(t *testing.T) {
	w := fixtureWS(t)
	w.indexAll()
	lib := w.repos[0]
	service := w.module("go-lib", "go", "service")
	if e := w.edges(service); len(e) != 1 {
		t.Fatalf("service edges before %v", e)
	}
	if err := os.Remove(filepath.Join(lib.Path, "service", "service.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Ensure(w.s, lib, w.opts()); err != nil {
		t.Fatal(err)
	}
	if e := w.edges(service); len(e) != 0 {
		t.Fatalf("service edges after its importing file is gone: %v", e)
	}
}

// An import of a repo that is not indexed yet stays unresolved until the
// provider is synced; Ensure on the provider then resolves it.
func TestEnsureResolvesWhenAProviderAppears(t *testing.T) {
	w := fixtureWS(t)
	lib, app := w.repos[0], w.repos[1]
	if _, err := index.Repo(w.s, app, w.opts()); err != nil {
		t.Fatal(err)
	}
	if err := index.ResolvePending(w.s, w.opts()); err != nil {
		t.Fatal(err)
	}
	if got := w.imports("go-app", "main.go"); got["github.com/fixture/lib/store"] != 0 {
		t.Fatalf("resolved before the provider exists: %v", got)
	}
	if _, err := index.Ensure(w.s, lib, w.opts()); err != nil {
		t.Fatal(err)
	}
	st := w.module("go-lib", "go", "store")
	if got := w.imports("go-app", "main.go"); got["github.com/fixture/lib/store"] != st {
		t.Fatalf("imports %v, want store %d", got, st)
	}
	if e := w.edges(w.module("go-app", "go", ".")); !e[st] {
		t.Fatalf("edges %v", e)
	}
}

// Two go.mod files with one module path in a repo (a copy under
// testdata/): imports resolve to the shallowest copy, and a later
// one-file edit leaves the identities untouched.
func TestDuplicateIdentityWithinOneRepo(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n")
	write(t, root, "sub/s.go", "package sub\n\nfunc S() {}\n")
	write(t, root, "testdata/copy/go.mod", "module example.com/m\n")
	write(t, root, "testdata/copy/sub/s.go", "package sub\n\nfunc S() {}\n")
	write(t, root, "main.go", "package main\n\nimport \"example.com/m/sub\"\n\nfunc main() { sub.S() }\n")
	s, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	w := &ws{t: t, s: s, repos: []*config.Repo{{Name: "r", Path: root}}}
	w.indexAll()
	sub := w.module("r", "go", "sub")
	if got := w.imports("r", "main.go"); got["example.com/m/sub"] != sub {
		t.Fatalf("imports %v, want %d", got, sub)
	}
	ids, err := s.RepoIdentities(w.repo("r"))
	if err != nil || len(ids) != 1 || ids[0].Path != "go.mod" {
		t.Fatalf("identities %+v %v", ids, err)
	}
	write(t, root, "main.go", "package main\n\nimport \"example.com/m/sub\"\n\nfunc main() { sub.S(); sub.S() }\n")
	if _, err := index.Ensure(s, w.repos[0], w.opts()); err != nil {
		t.Fatal(err)
	}
	if got := w.imports("r", "main.go"); got["example.com/m/sub"] != sub {
		t.Fatalf("after edit %v", got)
	}
}
