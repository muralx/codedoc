package index_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

// moduleDocs returns the doc fields of a repo's modules by language and
// path.
func moduleDocs(t *testing.T, s *store.Store, root string) map[string]store.Module {
	t.Helper()
	r, err := s.RepoByPath(root)
	if err != nil {
		t.Fatal(err)
	}
	mods, err := s.ModulesByRepo(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]store.Module{}
	for _, m := range mods {
		out[m.Lang+":"+m.Path] = m
	}
	return out
}

func wantModuleDoc(t *testing.T, mods map[string]store.Module, key, text, source string) {
	t.Helper()
	m, ok := mods[key]
	state := "ok"
	if text == "" {
		state = "missing"
	}
	if !ok || m.DocText != text || m.DocSource != source || m.DocState != state {
		t.Errorf("%s: got %q from %q (%s), want %q from %q", key, m.DocText, m.DocSource, m.DocState, text, source)
	}
}

func TestModuleDocsPerLanguage(t *testing.T) {
	s := openTemp(t)
	repos := map[string]string{}
	for _, name := range []string{"go-lib", "go-app", "ts-domain", "py-lib", "java-lib"} {
		rep := repoFixture(t, name)
		indexRepo(t, s, rep)
		repos[name] = rep.Path
	}
	goLib := moduleDocs(t, s, repos["go-lib"])
	wantModuleDoc(t, goLib, "go:store", "Package store persists the fixture's key-value data.", "store/doc.go")
	wantModuleDoc(t, goLib, "go:service", "Package service exposes the fixture's use cases.", "service/service.go")
	wantModuleDoc(t, moduleDocs(t, s, repos["go-app"]), "go:.", "Command app is the fixture entry point.", "main.go")
	ts := moduleDocs(t, s, repos["ts-domain"])
	wantModuleDoc(t, ts, "ts:src", "Record types and repositories of the fixture domain.", "src/README.md")
	wantModuleDoc(t, ts, "ts:.", "", "")
	py := moduleDocs(t, s, repos["py-lib"])
	wantModuleDoc(t, py, "py:src/pylib", "Fixture library for Python.", "src/pylib/__init__.py")
	java := moduleDocs(t, s, repos["java-lib"])
	wantModuleDoc(t, java, "java:src/main/java/com/fixture/store", "Storage abstractions of the Java fixture.",
		"src/main/java/com/fixture/store/package-info.java")
	wantModuleDoc(t, java, "java:src/main/java/com/fixture/service", "", "")
}

func TestModuleDocsFollowEdits(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	writeFile(t, rep.Path, "store/doc.go", []byte("// Package store keeps values.\npackage store\n"))
	ensure(t, s, rep, 200)
	wantModuleDoc(t, moduleDocs(t, s, rep.Path), "go:store", "Package store keeps values.", "store/doc.go")
	if err := os.Remove(filepath.Join(rep.Path, "store", "doc.go")); err != nil {
		t.Fatal(err)
	}
	ensure(t, s, rep, 200)
	wantModuleDoc(t, moduleDocs(t, s, rep.Path), "go:store", "Package store holds the storage abstractions of the fixture library.",
		"store/store.go")
	writeFile(t, rep.Path, "store/a.go", []byte("// Package store comes first by name.\npackage store\n"))
	ensure(t, s, rep, 200)
	wantModuleDoc(t, moduleDocs(t, s, rep.Path), "go:store", "Package store comes first by name.", "store/a.go")
	appendFile(t, rep.Path, "store/null.go", "\nfunc Other() {}\n")
	ensure(t, s, rep, 200)
	wantModuleDoc(t, moduleDocs(t, s, rep.Path), "go:store", "Package store comes first by name.", "store/a.go")
	writeFile(t, rep.Path, "store/a.go", []byte("package store\n"))
	ensure(t, s, rep, 200)
	wantModuleDoc(t, moduleDocs(t, s, rep.Path), "go:store", "Package store holds the storage abstractions of the fixture library.",
		"store/store.go")
}

func TestModuleDocsTSIndexAndPythonFileModule(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "ts-domain")
	if err := os.Remove(filepath.Join(rep.Path, "src", "README.md")); err != nil {
		t.Fatal(err)
	}
	indexRepo(t, s, rep)
	wantModuleDoc(t, moduleDocs(t, s, rep.Path), "ts:src", "Domain records of the TypeScript fixture.", "src/index.ts")
	py := repoFixture(t, "py-lib")
	indexRepo(t, s, py)
	mods := moduleDocs(t, s, py.Path)
	if m := mods["py:src/pylib/store.py"]; m.Kind != "py-module" || m.DocState != "ok" || m.DocSource != "src/pylib/store.py" {
		t.Fatalf("file module %+v", m)
	}
}

func TestModuleDocsFollowREADME(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "ts-domain")
	indexRepo(t, s, rep)
	writeFile(t, rep.Path, "src/README.md", []byte("# Domain\n\n## Purpose\n\nDomain records.\n"))
	ensure(t, s, rep, 200)
	wantModuleDoc(t, moduleDocs(t, s, rep.Path), "ts:src", "Domain records.", "src/README.md")
	if err := os.Remove(filepath.Join(rep.Path, "src", "README.md")); err != nil {
		t.Fatal(err)
	}
	ensure(t, s, rep, 200)
	wantModuleDoc(t, moduleDocs(t, s, rep.Path), "ts:src", "Domain records of the TypeScript fixture.", "src/index.ts")
}
