package exclude_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/exclude"
)

func touch(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltinExclusions(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "web/package.json")
	touch(t, root, "svc/pom.xml")
	touch(t, root, "lib/mod/build.gradle.kts")
	m := exclude.New(root, nil)
	cases := map[string]bool{
		"main.go":                       false,
		"vendor/x/a.go":                 true,
		"a/b/node_modules/x/index.js":   true,
		"pkg/testdata/in.go":            true,
		"testdata/ws/a.go":              true,
		"build/out.js":                  true,
		"dist/app.js":                   true,
		"target/classes/A.java":         true,
		"web/dist/app.js":               true,
		"svc/target/A.class":            true,
		"lib/mod/build/x.java":          true,
		"internal/build/build.go":       false,
		"internal/dist/dist.go":         false,
		"cmd/target/main.go":            false,
		"lib/build/x.java":              false,
		"internal/builder/b.go":         false,
		"internal/vendored/v.go":        false,
		"web/src/build/index.ts":        false,
		"svc/src/main/target/Target.go": false,
	}
	for rel, want := range cases {
		if got := m.Path(rel, false); got != want {
			t.Errorf("%s: excluded %v, want %v", rel, got, want)
		}
	}
	for rel, want := range map[string]bool{"build": true, "web/dist": true, "internal/build": false, "a/testdata": true, ".": false} {
		if got := m.Path(rel, true); got != want {
			t.Errorf("dir %s: excluded %v, want %v", rel, got, want)
		}
	}
}

func TestGlobExclusions(t *testing.T) {
	m := exclude.New(t.TempDir(), []string{"gen/**", "**/*.pb.go", "docs/**/"})
	for rel, want := range map[string]bool{"gen/a.go": true, "x/y.pb.go": true, "x/y.go": false} {
		if got := m.Path(rel, false); got != want {
			t.Errorf("%s: excluded %v, want %v", rel, got, want)
		}
	}
	if !m.Path("gen", true) || !m.Path("docs/api", true) || m.Path("src", true) {
		t.Error("directory globs")
	}
}

func TestValidateGlobs(t *testing.T) {
	if err := exclude.ValidateGlobs([]string{"a/**", "*.go"}); err != nil {
		t.Fatal(err)
	}
	if err := exclude.ValidateGlobs([]string{"ok/**", "bad[", "x"}); err == nil || err.Error() != `invalid exclude glob "bad["` {
		t.Fatalf("got %v", err)
	}
}
