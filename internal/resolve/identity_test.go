package resolve_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/testutil"
)

func fixtureFiles(t *testing.T, name string) (string, []string) {
	t.Helper()
	root := filepath.Join(testutil.WorkspaceDir(), name)
	var files []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		files = append(files, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return root, files
}

func TestIdentitiesGo(t *testing.T) {
	m, err := resolve.Identities(fixtureFiles(t, "go-lib"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []resolve.Identity{{Lang: "go", Identity: "github.com/fixture/lib", Path: "go.mod"}}; !reflect.DeepEqual(m.Repo, want) {
		t.Errorf("repo identities %+v", m.Repo)
	}
	want := []resolve.Module{
		{Path: "service", Lang: "go", Kind: "go-package", Identity: "github.com/fixture/lib/service", Root: ".",
			Package: "github.com/fixture/lib/service"},
		{Path: "store", Lang: "go", Kind: "go-package", Identity: "github.com/fixture/lib/store", Root: ".",
			Package: "github.com/fixture/lib/store"},
	}
	if !reflect.DeepEqual(m.Modules, want) {
		t.Errorf("modules %+v", m.Modules)
	}
	mod, ok := m.ModuleFor("go", "store/store_test.go")
	if !ok || mod.Identity != "github.com/fixture/lib/store" {
		t.Errorf("ModuleFor %+v %v", mod, ok)
	}
}

func TestIdentitiesTS(t *testing.T) {
	m, err := resolve.Identities(fixtureFiles(t, "ts-domain"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []resolve.Identity{{Lang: "ts", Identity: "@fixture/domain", Path: "package.json"}}; !reflect.DeepEqual(m.Repo, want) {
		t.Errorf("repo identities %+v", m.Repo)
	}
	want := []resolve.Module{
		{Path: ".", Lang: "ts", Kind: "npm-package", Identity: "@fixture/domain", Root: ".", Package: "@fixture/domain"},
		{Path: "src", Lang: "ts", Kind: "npm-dir", Identity: "@fixture/domain/src", Parent: ".", Root: ".",
			Package: "@fixture/domain"},
	}
	if !reflect.DeepEqual(m.Modules, want) {
		t.Errorf("modules %+v", m.Modules)
	}
}

func write(t *testing.T, root, rel, text string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

type moduleCase struct{ lang, file, identity, kind, parent, rel, pkg string }

func (c moduleCase) check(t *testing.T, m *resolve.Map) {
	t.Helper()
	mod, ok := m.ModuleFor(c.lang, c.file)
	if !ok || mod.Identity != c.identity || mod.Kind != c.kind || mod.Parent != c.parent ||
		mod.RelPath(c.file) != c.rel || mod.Package != c.pkg {
		t.Errorf("%s: %+v %v rel %q", c.file, mod, ok, mod.RelPath(c.file))
	}
}

// nestedRepo writes a repo with nested Go modules, an npm workspace
// package, a broken and a nameless package.json and loose scripts.
func nestedRepo(t *testing.T) (string, []string) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                       "// comment\nmodule \"example.com/m\" // trailing\n",
		"main.go":                      "package main",
		"sub/go.mod":                   "module example.com/m/sub\n",
		"sub/a/a.go":                   "package a",
		"packages/web/package.json":    `{"name": "@x/web"}`,
		"packages/web/src/ui/b.tsx":    "",
		"packages/broken/package.json": `{not json`,
		"packages/broken/c.ts":         "",
		"scripts/d.js":                 "",
		"nameless/package.json":        `{"version": "1"}`,
		"nameless/e.ts":                "",
	}
	var paths []string
	for p, text := range files {
		write(t, root, p, text)
		paths = append(paths, p)
	}
	return root, paths
}

func TestIdentitiesNestedAndFallback(t *testing.T) {
	m, err := resolve.Identities(nestedRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Repo) != 3 {
		t.Errorf("repo identities %+v", m.Repo)
	}
	for _, c := range []moduleCase{
		{"go", "main.go", "example.com/m", "go-package", "", "main.go", "example.com/m"},
		{"go", "sub/a/a.go", "example.com/m/sub/a", "go-package", "", "sub/a/a.go", "example.com/m/sub/a"},
		{"ts", "packages/web/src/ui/b.tsx", "@x/web/src/ui", "npm-dir", "packages/web/src", "src/ui/b.tsx", "@x/web"},
		{"ts", "packages/broken/c.ts", "packages/broken", "dir", "", "packages/broken/c.ts", "packages/broken"},
		{"ts", "scripts/d.js", "scripts", "dir", "", "scripts/d.js", "scripts"},
		{"ts", "nameless/e.ts", "nameless", "dir", "", "nameless/e.ts", "nameless"},
	} {
		c.check(t, m)
	}
	if mod, ok := m.ModuleFor("ts", "packages/web/src/x.ts"); !ok || mod.Identity != "@x/web/src" || mod.Parent != "packages/web" {
		t.Errorf("intermediate module %+v %v", mod, ok)
	}
	if _, ok := m.ModuleFor("go", "packages/web/src/ui/b.tsx"); ok {
		t.Error("no go module for a ts directory")
	}
}

func TestIdentitiesMissingManifest(t *testing.T) {
	m, err := resolve.Identities(t.TempDir(), []string{"go.mod", "a.go"})
	if err != nil || len(m.Repo) != 0 {
		t.Fatalf("a manifest gone since discovery is skipped: %+v %v", m, err)
	}
	if mod, ok := m.ModuleFor("go", "a.go"); !ok || mod.Identity != "." {
		t.Errorf("fallback %+v", mod)
	}
	if _, err := resolve.Identities(t.TempDir()+"/nope\x00", []string{"go.mod"}); err == nil {
		t.Error("a manifest that cannot be read is an error")
	}
}

func TestDuplicateIdentityKeepsShallowestManifest(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n")
	write(t, root, "a.go", "package m\n")
	write(t, root, "testdata/copy/go.mod", "module example.com/m\n")
	write(t, root, "testdata/copy/b.go", "package m\n")
	write(t, root, "aa/go.mod", "module example.com/other\n")
	m, err := resolve.Identities(root, []string{"testdata/copy/go.mod", "go.mod", "aa/go.mod", "a.go", "testdata/copy/b.go"})
	if err != nil {
		t.Fatal(err)
	}
	want := []resolve.Identity{
		{Lang: "go", Identity: "example.com/other", Path: "aa/go.mod"},
		{Lang: "go", Identity: "example.com/m", Path: "go.mod"},
	}
	if !reflect.DeepEqual(m.Repo, want) {
		t.Errorf("repo identities %+v", m.Repo)
	}
	if mod, ok := m.ModuleFor("go", "testdata/copy/b.go"); !ok || mod.Identity != "example.com/m" {
		t.Errorf("the nested copy still names its own packages: %+v", mod)
	}
}
