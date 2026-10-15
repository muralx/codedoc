package deps

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// pkgDir writes files (path: content) into a temp package directory.
func pkgDir(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("export {};\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// entriesOf returns "subpath file" of the entries of a package.json.
func entriesOf(t *testing.T, dir, manifest string) []string {
	t.Helper()
	var pj packageJSON
	if err := json.Unmarshal([]byte(manifest), &pj); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range npmEntries(dir, pj) {
		out = append(out, e.Subpath+" "+e.File)
	}
	return out
}

func TestNpmEntriesFromFieldsWithoutExports(t *testing.T) {
	dir := pkgDir(t, "index.d.ts", "lib/main.js", "lib/main.d.ts", "types/api.d.ts", "src/index.ts", "esm/a.mjs", "esm/a.d.mts")
	for manifest, want := range map[string]string{
		`{}`:                              ". index.d.ts",
		`{"types": "types/api"}`:          ". types/api.d.ts",
		`{"typings": "./types/api.d.ts"}`: ". types/api.d.ts",
		`{"main": "lib/main.js"}`:         ". lib/main.d.ts",
		`{"main": "./esm/a.mjs"}`:         ". esm/a.d.mts",
		`{"types": "src/index.ts"}`:       ". src/index.ts",
		`{"types": "../outside.d.ts", "main": "lib/main"}`: ". lib/main.d.ts",
		`{"types": "missing.d.ts", "main": "missing.js"}`:  ". index.d.ts",
	} {
		if got := entriesOf(t, dir, manifest); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("%s: got %v, want %v", manifest, got, want)
		}
	}
	if got := entriesOf(t, pkgDir(t, "main.js"), `{"main": "main.js"}`); len(got) != 0 {
		t.Errorf("a package without declarations has no entries: %v", got)
	}
}

func TestNpmEntriesFromExportsShapes(t *testing.T) {
	dir := pkgDir(t, "dist/index.d.ts", "dist/index.js", "dist/node.d.ts", "dist/sub/index.d.ts", "dist/sub/index.js",
		"dist/feat/a.js", "dist/feat/a.d.ts", "dist/feat/node_modules/x/b.js", "dist/feat/internal.txt")
	for manifest, want := range map[string][]string{
		`{"exports": "./dist/index.js"}`:                                   {". dist/index.d.ts"},
		`{"exports": {"import": "./dist/index.js", "require": "./x.cjs"}}`: {". dist/index.d.ts"},
		`{"exports": ["./nope.js", {"node": "./dist/node.d.ts"}]}`:         {". dist/node.d.ts"},
		`{"exports": {".": {"browser": null, "node": {"import": "./dist/node.d.ts"}}, "./sub": "./dist/sub"}}`: {
			". dist/node.d.ts", "./sub dist/sub/index.d.ts"},
		`{"exports": {"./features/*": ["./dist/feat/*.js"], "./multi/*/*": "./dist/*/*.js"}, "types": "dist/index.d.ts"}`: {
			". dist/index.d.ts", "./features/a dist/feat/a.d.ts"},
		`{"exports": {"./f/*.js": {"types": "./dist/feat/*.d.ts", "default": "./dist/feat/*.js"}}}`: {
			"./f/a.js dist/feat/a.d.ts"},
		`{"exports": 42}`: nil,
	} {
		if got := entriesOf(t, dir, manifest); !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got  %v\n want %v", manifest, got, want)
		}
	}
}

func TestTypesNameAndStem(t *testing.T) {
	for in, want := range map[string]string{"left-pad": "@types/left-pad", "@babel/core": "@types/babel__core"} {
		if got := typesName(in); got != want {
			t.Errorf("typesName(%q) = %q", in, got)
		}
	}
	for in, want := range map[string]string{"a/b.d.ts": "a/b", "a/b.d.cts": "a/b", "a/b.d.mts": "a/b", "src/x.ts": "src/x",
		"data.json": "data.json"} {
		if got := stem(in); got != want {
			t.Errorf("stem(%q) = %q", in, got)
		}
	}
	if got := []int{countLines(nil), countLines([]byte("a\nb")), countLines([]byte("a\n"))}; !reflect.DeepEqual(got, []int{0, 2, 1}) {
		t.Errorf("countLines: %v", got)
	}
}

func TestReadmeIsClippedAtALineEnd(t *testing.T) {
	long := strings.Repeat("a line of the readme\n", maxReadme/10)
	if got := clip(long); len(got) > maxReadme || !strings.HasSuffix(got, "readme") {
		t.Errorf("clip keeps whole lines within the cap: %d bytes", len(got))
	}
	if got := clip("short"); got != "short" {
		t.Errorf("a short text is kept: %q", got)
	}
	for _, dir := range []string{t.TempDir(), filepath.Join(t.TempDir(), "absent")} {
		if text, name := readme(dir); text+name != "" {
			t.Errorf("no README in %s: %q %q", dir, text, name)
		}
	}
}

func TestModCacheOrder(t *testing.T) {
	t.Setenv(ModCacheEnv, "/override")
	t.Setenv("GOMODCACHE", "/gomodcache")
	if got := modCache(); got != "/override" {
		t.Errorf("CODEDOC_GOMODCACHE wins: %q", got)
	}
	t.Setenv(ModCacheEnv, "")
	if got := modCache(); got != "/gomodcache" {
		t.Errorf("then GOMODCACHE: %q", got)
	}
	t.Setenv("GOMODCACHE", "")
	if got := modCache(); !filepath.IsAbs(got) {
		t.Errorf("then go env or ~/go/pkg/mod: %q", got)
	}
}
