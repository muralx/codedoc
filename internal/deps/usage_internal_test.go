package deps

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func TestToolConfigFilesAreToldFromCode(t *testing.T) {
	for path, want := range map[string]bool{
		"eslint.config.js": true, "packages/web/vite.config.ts": true, "vitest.config.mts": true, ".eslintrc.cjs": true,
		"web/.prettierrc.mjs": true, "src/config.ts": false, "src/app.config.json": false, "config.go": false,
		"src/main.ts": false, ".github/workflows/build.ts": false,
	} {
		if toolConfig(path) != want {
			t.Errorf("toolConfig(%q) = %v", path, !want)
		}
	}
}

func TestTypedNamesThePackageATypesPackageDeclares(t *testing.T) {
	for name, want := range map[string]string{"@types/ms": "ms", "@types/scope__name": "@scope/name", "ms": "",
		"@scope/types": ""} {
		if got := typed(name); got != want {
			t.Errorf("typed(%q) = %q, want %q", name, got, want)
		}
	}
}

// Only specifiers a declaration file of the package answers to are
// built-in modules, and only below the manifest's directory.
func TestNodeModulesOfTheImports(t *testing.T) {
	root := pkgDir(t, "fs.d.ts", "fs/promises.d.ts", "path.d.ts", "events.d.ts", "test.d.ts")
	im := imports{"npm": {
		"node:fs": {"src"}, "fs": {"src/deep"}, "node:fs/promises": {"."}, "path": {"other"}, "events": {"src"},
		"node:test": {"src"}, "@scope/pkg": {"src"}, "zod": {"src"}, "node:../../etc/passwd": {"src"},
	}, "go": {"path": {"src"}}}
	declared := map[string]bool{"events": true}
	for at, want := range map[string][]string{
		".":     {"fs", "fs/promises", "path", "test"},
		"src":   {"fs", "test"},
		"other": {"path"},
		"none":  nil,
	} {
		if got := im.nodeModules(root, at, declared); !reflect.DeepEqual(got, want) {
			t.Errorf("below %s: %v, want %v", at, got, want)
		}
	}
	if got := im.nodeModules(filepath.Join(root, "absent"), ".", declared); got != nil {
		t.Errorf("a package that is gone declares nothing: %v", got)
	}
}

// partialDB returns a deps.db holding the modules fs and os of
// @types/node, with the extraction that wrote them.
func partialDB(t *testing.T) (*DB, ID, *Extraction) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "deps.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	id := ID{Eco: "npm", Name: nodeTypes, Version: "1.0.0"}
	ex := &Extraction{Modules: []Module{{Path: ".", Lang: "ts", Kind: "npm-package", Identity: nodeTypes}}, Only: []string{"fs", "os"}}
	if wrote, err := db.Write(id, ex); err != nil || !wrote {
		t.Fatalf("%v %v", wrote, err)
	}
	return db, id, ex
}

// What a partial package holds only grows.
func TestPartialPackagesGrowToTheUnion(t *testing.T) {
	db, id, _ := partialDB(t)
	name := id.String()
	for _, tc := range []struct {
		only   []string
		covers bool
		widen  []string
	}{
		{[]string{"fs"}, true, []string{"fs", "os"}},
		{[]string{"os", "path"}, false, []string{"fs", "os", "path"}},
		{nil, false, nil},
	} {
		if got := db.covers(name, tc.only); got != tc.covers {
			t.Errorf("covers(%v) = %v", tc.only, got)
		}
		if got := db.widen(name, tc.only); !reflect.DeepEqual(got, tc.widen) {
			t.Errorf("widen(%v) = %v", tc.only, got)
		}
	}
}

// A partial package is written again only for modules it lacks, and a
// whole package covers every module.
func TestAWholePackageReplacesAPartialOne(t *testing.T) {
	db, id, ex := partialDB(t)
	name := id.String()
	ex.Only = []string{"fs"}
	if wrote, err := db.Write(id, ex); err != nil || wrote {
		t.Fatalf("what is held covers fewer modules: %v %v", wrote, err)
	}
	ex.Only = nil
	if wrote, err := db.Write(id, ex); err != nil || !wrote {
		t.Fatalf("the whole package replaces a partial one: %v %v", wrote, err)
	}
	if !db.covers(name, []string{"anything"}) || !db.covers(name, nil) || !db.covers("npm/absent@1", nil) {
		t.Error("a whole package holds every module")
	}
	// A build without lazy indexing wrote the package whole and no key.
	err := db.s.Batch(func(b *store.Batch) error { return b.DeleteMeta(modulesKey + name) })
	if err != nil || db.covers(name, []string{"fs"}) || db.covers(name, nil) {
		t.Errorf("the whole rows of an older build are replaced: %v", err)
	}
}
