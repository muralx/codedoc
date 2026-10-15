package index_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cespare/xxhash/v2"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/stablekey"
)

const storeModule = "go-lib:go:github.com/fixture/lib/store:"

func TestIndexKeysSurviveFileRename(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	if err := os.Rename(filepath.Join(rep.Path, "store", "null.go"), filepath.Join(rep.Path, "store", "nil.go")); err != nil {
		t.Fatal(err)
	}
	if f := ensure(t, s, rep, 200); !f.Fresh {
		t.Fatalf("%+v", f)
	}
	for _, q := range []string{"NullStore", "NullStore.Get", "NullStore.Put"} {
		sy, err := s.SymbolByKey(storeModule + q)
		if err != nil || sy.Path != "store/nil.go" {
			t.Fatalf("%s: %+v %v (keys %v)", q, sy, err, keysNamed(t, s, "NullStore"))
		}
	}
}

func TestIndexKeysSurviveMoveBetweenFiles(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	p := filepath.Join(rep.Path, "store", "store.go")
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	decl := "// NewMemStore creates an empty MemStore.\nfunc NewMemStore() *MemStore { return &MemStore{data: map[string]string{}} }\n"
	if !strings.Contains(string(src), decl) {
		t.Fatal("fixture changed")
	}
	writeFile(t, rep.Path, "store/store.go", []byte(strings.Replace(string(src), decl, "", 1)))
	writeFile(t, rep.Path, "store/aaa.go", []byte("package store\n\n"+decl))
	indexRepo(t, s, rep)
	sy, err := s.SymbolByKey(storeModule + "NewMemStore")
	if err != nil || sy.Path != "store/aaa.go" {
		t.Fatalf("%+v %v (keys %v)", sy, err, keysNamed(t, s, "NewMemStore"))
	}
}

func TestIndexKeyCollisionRestoresPlainKeyWhenPartnerGoes(t *testing.T) {
	s := openTemp(t)
	root := t.TempDir()
	rep := &config.Repo{Path: root, Name: "r"}
	writeFile(t, root, "p/a_linux.go", []byte("package p\n\nfunc Open() {}\n"))
	writeFile(t, root, "p/a_darwin.go", []byte("package p\n\nfunc Open() {}\n"))
	indexRepo(t, s, rep)
	if keys := keysNamed(t, s, "Open"); len(keys) != 2 || !strings.Contains(keys[0], "#") || !strings.Contains(keys[1], "#") {
		t.Fatalf("%v", keys)
	}
	if err := os.Remove(filepath.Join(root, "p", "a_darwin.go")); err != nil {
		t.Fatal(err)
	}
	indexRepo(t, s, rep)
	if sy, err := s.SymbolByKey("r:go:p:Open"); err != nil || sy.Path != "p/a_linux.go" {
		t.Fatalf("%+v %v (keys %v)", sy, err, keysNamed(t, s, "Open"))
	}
}

func TestIndexKeyCollisionWithEqualPathHashes(t *testing.T) {
	a, b := pathsWithEqualHashPrefix()
	s := openTemp(t)
	root := t.TempDir()
	writeFile(t, root, a, []byte("package p\n\nfunc Open() {}\n"))
	writeFile(t, root, b, []byte("package p\n\nfunc Open() {}\n"))
	indexRepo(t, s, &config.Repo{Path: root, Name: "r"})
	keys := keysNamed(t, s, "Open")
	if len(keys) != 2 || keys[0] == keys[1] {
		t.Fatalf("%v", keys)
	}
	hashes := []string{mustKey(t, keys[0]).SigHash, mustKey(t, keys[1]).SigHash}
	if len(hashes[0])+len(hashes[1]) != 12 {
		t.Fatalf("want one 4 and one 8 digit hash: %v", keys)
	}
}

// pathsWithEqualHashPrefix returns two Go file paths whose path hashes
// share their first four hex digits.
func pathsWithEqualHashPrefix() (string, string) {
	seen := map[string]string{}
	for i := 0; ; i++ {
		p := fmt.Sprintf("p/f%d.go", i)
		h := fmt.Sprintf("%016x", xxhash.Sum64String(p))[:4]
		if prev, ok := seen[h]; ok {
			return prev, p
		}
		seen[h] = p
	}
}

func TestHashedKeyFallsBackToLongerHashThenOrdinal(t *testing.T) {
	full := fmt.Sprintf("%016x", xxhash.Sum64String("p/a.go"))
	used := map[string]bool{}
	pick := func() string {
		k, err := index.PickHashedKey("r:go:p:Open", "p/a.go", func(k string) (bool, error) { return used[k], nil })
		if err != nil {
			t.Fatal(err)
		}
		used[k] = true
		return k
	}
	for _, want := range []string{"#" + full[:4], "#" + full[:8], "#" + full[:8] + "-2", "#" + full[:8] + "-3"} {
		if got := pick(); got != "r:go:p:Open"+want {
			t.Fatalf("got %s want suffix %s", got, want)
		}
		if k := mustKey(t, "r:go:p:Open"+want); k.Qualified != "Open" || "#"+k.SigHash != want {
			t.Fatalf("%+v", k)
		}
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
