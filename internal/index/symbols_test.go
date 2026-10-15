package index_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

func indexRepo(t *testing.T, s *store.Store, rep *config.Repo) {
	t.Helper()
	if _, err := index.Repo(s, rep, index.Options{Workspace: "fixture"}); err != nil {
		t.Fatal(err)
	}
}

func symbolCount(t *testing.T, s *store.Store, root string) int {
	t.Helper()
	r, err := s.RepoByPath(root)
	if err != nil {
		t.Fatal(err)
	}
	_, n, err := s.RepoStats(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIndexStoresSymbolsWithStableKeys(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	get, err := s.SymbolByKey("go-lib:go:github.com/fixture/lib/store:MemStore.Get")
	if err != nil {
		t.Fatal(err)
	}
	if get.StartLine != 26 || get.DocStartLine != 25 || get.DocText != "Get implements Store." || get.DocState != "ok" ||
		get.Kind != "method" || get.ModuleID == 0 || get.DocHash == "" {
		t.Fatalf("%+v", get)
	}
	parent, err := s.SymbolByKey("go-lib:go:github.com/fixture/lib/store:MemStore")
	if err != nil || get.ContainerID != parent.ID {
		t.Fatalf("container %d, MemStore %+v %v", get.ContainerID, parent, err)
	}
}

func TestIndexUndocumentedSymbolHasMissingDoc(t *testing.T) {
	s := openTemp(t)
	indexRepo(t, s, repoFixture(t, "go-lib"))
	if fb, _ := s.SymbolByKey("go-lib:go:github.com/fixture/lib/service:fallback"); fb.DocState != "missing" || fb.DocText != "" {
		t.Fatalf("%+v", fb)
	}
}

func TestIndexSymbolsAreIncremental(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	get, _ := s.SymbolByKey("go-lib:go:github.com/fixture/lib/store:MemStore.Get")
	before := symbolCount(t, s, rep.Path)
	indexRepo(t, s, rep)
	again, _ := s.SymbolByKey("go-lib:go:github.com/fixture/lib/store:MemStore.Get")
	if symbolCount(t, s, rep.Path) != before || again.ID != get.ID {
		t.Fatalf("unchanged reindex touched symbols: %d->%d, id %d->%d", before, symbolCount(t, s, rep.Path), get.ID, again.ID)
	}
	appendFile(t, rep.Path, "store/store.go", "\n// Extra is new.\nfunc Extra() {}\n")
	indexRepo(t, s, rep)
	if n := symbolCount(t, s, rep.Path); n != before+1 {
		t.Fatalf("want %d symbols, got %d", before+1, n)
	}
	if _, err := s.SymbolByKey("go-lib:go:github.com/fixture/lib/store:Extra"); err != nil {
		t.Fatal(err)
	}
}

func TestIndexTypeScriptKeysUseFamily(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "ts-domain")
	indexRepo(t, s, rep)
	if _, err := s.SymbolByKey("ts-domain:ts:@fixture/domain:src/records.ts/MemoryRepo.save"); err != nil {
		t.Fatal(err)
	}
}

func TestIndexKeyCollisionAcrossFilesOfAModule(t *testing.T) {
	s := openTemp(t)
	root := t.TempDir()
	writeFile(t, root, "p/a_linux.go", []byte("package p\n\nfunc Open() {}\n"))
	indexRepo(t, s, &config.Repo{Path: root, Name: "r"})
	if _, err := s.SymbolByKey("r:go:p:Open"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "p/a_darwin.go", []byte("package p\n\nfunc Open() {}\n"))
	indexRepo(t, s, &config.Repo{Path: root, Name: "r"})
	if _, err := s.SymbolByKey("r:go:p:Open"); err == nil {
		t.Fatal("plain key kept after collision")
	}
	keys := keysNamed(t, s, "Open")
	if len(keys) != 2 || !strings.HasPrefix(keys[0], "r:go:p:Open#") || keys[0] == keys[1] {
		t.Fatalf("%v", keys)
	}
	appendFile(t, root, "p/a_darwin.go", "\n// x\n")
	indexRepo(t, s, &config.Repo{Path: root, Name: "r"})
	if again := keysNamed(t, s, "Open"); again[0] != keys[0] || again[1] != keys[1] {
		t.Fatalf("keys changed on reindex: %v -> %v", keys, again)
	}
}

func TestIndexKeyCollisionInsideOneFile(t *testing.T) {
	s := openTemp(t)
	root := t.TempDir()
	// Invalid Go, but it parses: two methods with one qualified name.
	writeFile(t, root, "p/a.go", []byte("package p\n\ntype T struct{}\n\nfunc (T) M() {}\n\nfunc (*T) M() {}\n"))
	indexRepo(t, s, &config.Repo{Path: root, Name: "r"})
	if keys := keysNamed(t, s, "M"); len(keys) != 2 || keys[0] == keys[1] {
		t.Fatalf("%v", keys)
	}
}

func TestIndexRecordsParseErrors(t *testing.T) {
	s := openTemp(t)
	root := t.TempDir()
	writeFile(t, root, "a.go", []byte("package a\n\nfunc Good() {}\n\nfunc broken( {\n"))
	indexRepo(t, s, &config.Repo{Path: root, Name: "r"})
	if f := fileRow(t, s, root, "a.go"); f.ParseErrors == 0 {
		t.Fatalf("%+v", f)
	}
	if _, err := s.SymbolByKey("r:go:.:Good"); err != nil {
		t.Fatal(err)
	}
}

func TestIndexConfigParseErrorKeepsEarlierKeys(t *testing.T) {
	s := openTemp(t)
	root := t.TempDir()
	writeFile(t, root, "good.properties", []byte("a.b=1\n"))
	writeFile(t, root, "bad.json", []byte("{\"first\": 1, \"second\": "))
	indexRepo(t, s, &config.Repo{Path: root, Name: "r"})
	if f := fileRow(t, s, root, "bad.json"); f.ParseErrors != 1 || f.Lang != "json" {
		t.Fatalf("%+v", f)
	}
	if f := fileRow(t, s, root, "good.properties"); f.ParseErrors != 0 || f.Lang != "properties" {
		t.Fatalf("%+v", f)
	}
	for _, key := range []string{"first", "a.b"} {
		if keys, err := s.ConfigKeysByName(key, 0); err != nil || len(keys) != 1 {
			t.Errorf("%s: %+v %v", key, keys, err)
		}
	}
	if err := os.Remove(filepath.Join(root, "good.properties")); err != nil {
		t.Fatal(err)
	}
	indexRepo(t, s, &config.Repo{Path: root, Name: "r"})
	if keys, err := s.ConfigKeysByName("a.b", 0); err != nil || len(keys) != 0 {
		t.Errorf("key of a deleted file kept: %+v %v", keys, err)
	}
}

func keysNamed(t *testing.T, s *store.Store, name string) []string {
	t.Helper()
	syms, err := s.SymbolsByName(name, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, len(syms))
	for i, sy := range syms {
		keys[i] = sy.StableKey
	}
	return keys
}

func appendFile(t *testing.T, root, rel, text string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(root, rel), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}
