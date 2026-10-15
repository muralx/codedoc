package index_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
)

func TestIndexJavaAndPythonKeys(t *testing.T) {
	s := openTemp(t)
	indexRepo(t, s, repoFixture(t, "java-lib"))
	indexRepo(t, s, repoFixture(t, "py-lib"))
	puts := regexp.MustCompile(`^java-lib:java:com\.fixture\.store:MemStore\.put#[0-9a-f]{4}$`)
	var got []string
	for _, k := range keysNamed(t, s, "put") {
		if puts.MatchString(k) {
			got = append(got, k)
		}
	}
	if len(got) != 2 || got[0] == got[1] {
		t.Fatalf("overload keys %v", keysNamed(t, s, "put"))
	}
	for _, k := range got {
		if parsed := mustKey(t, k); parsed.Qualified != "MemStore.put" || len(parsed.SigHash) != 4 {
			t.Errorf("parse %s: %+v", k, parsed)
		}
	}
	for _, key := range []string{
		"py-lib:py:pylib.store:MemStore.put", "py-lib:py:pylib.service:Service.lookup", "py-lib:py:tests.test_store:test_put_get",
	} {
		if _, err := s.SymbolByKey(key); err != nil {
			t.Errorf("%s: %v", key, err)
		}
	}
	if keys := keysNamed(t, s, "<init>"); len(keys) != 2 || !strings.HasPrefix(keys[0], "java-lib:java:com.fixture.") {
		t.Errorf("constructors %v", keys)
	}
}

const javaX = "package p;\n\nclass X {\n  int m;\n  void m(String s) {}\n}\n"

func TestIndexJavaCollisionAppendsPathHashAfterSigHash(t *testing.T) {
	s := openTemp(t)
	root := t.TempDir()
	rep := &config.Repo{Path: root, Name: "r"}
	writeFile(t, root, "src/main/java/p/X.java", []byte(javaX))
	indexRepo(t, s, rep)
	plain := keysNamed(t, s, "m")
	if len(plain) != 2 || plain[0] != "r:java:p:X.m" || !regexp.MustCompile(`^r:java:p:X\.m#[0-9a-f]{4}$`).MatchString(plain[1]) {
		t.Fatalf("field and method keep plain keys: %v", plain)
	}
	writeFile(t, root, "src/test/java/p/X.java", []byte(javaX))
	indexRepo(t, s, rep)
	checkCollidedKeys(t, keysNamed(t, s, "m"), plain[1])
	if err := os.Remove(filepath.Join(root, "src", "test", "java", "p", "X.java")); err != nil {
		t.Fatal(err)
	}
	indexRepo(t, s, rep)
	if back := keysNamed(t, s, "m"); len(back) != 2 || back[0] != plain[0] || back[1] != plain[1] {
		t.Fatalf("plain keys not restored: %v, want %v", back, plain)
	}
}

// checkCollidedKeys expects the field key with a path hash and two method
// keys whose path hash follows the signature hash of method.
func checkCollidedKeys(t *testing.T, keys []string, method string) {
	t.Helper()
	sigPath := regexp.MustCompile(`^r:java:p:X\.m#[0-9a-f]{4}#[0-9a-f]{4}$`)
	n := 0
	for _, k := range keys {
		switch {
		case sigPath.MatchString(k) && strings.HasPrefix(k, method+"#") && mustKey(t, k).Qualified == "X.m":
			n++
		case !regexp.MustCompile(`^r:java:p:X\.m#[0-9a-f]{4}$`).MatchString(k):
			t.Errorf("unexpected key %s", k)
		}
	}
	if len(keys) != 4 || n != 2 {
		t.Fatalf("collided keys %v", keys)
	}
}
