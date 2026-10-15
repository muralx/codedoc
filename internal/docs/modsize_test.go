package docs_test

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/docs"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// TestModuleSizesCountNestedModules checks that a module counts the
// files of its language in its own directory and in the modules nested
// under it, and nothing of an unrelated module below its directory.
func TestModuleSizesCountNestedModules(t *testing.T) {
	mods := []store.Module{
		{ID: 1, Lang: "ts", Kind: "npm-package", Path: "."},
		{ID: 2, Lang: "ts", Kind: "npm-package", Path: "packages/edge"},
		{ID: 3, Lang: "ts", Kind: "npm-dir", Path: "packages/edge/src", ParentID: 2},
		{ID: 4, Lang: "ts", Kind: "npm-dir", Path: "packages/edge/src/hooks", ParentID: 3},
		{ID: 5, Lang: "py", Kind: "py-package", Path: "lib"},
		{ID: 6, Lang: "py", Kind: "py-module", Path: "lib/util.py", ParentID: 5},
		{ID: 7, Lang: "go", Kind: "go-package", Path: "packages/edge/src"},
		{ID: 8, Lang: "go", Kind: "go-package", Path: "empty"},
	}
	files := []store.File{
		{Path: "vitest.config.ts", Lang: "typescript", Lines: 5},
		{Path: "packages/edge/package.json", Lang: "json", Lines: 9},
		{Path: "packages/edge/src/a.ts", Lang: "typescript", Lines: 10},
		{Path: "packages/edge/src/b.tsx", Lang: "tsx", Lines: 20},
		{Path: "packages/edge/src/hooks/c.ts", Lang: "typescript", Lines: 30},
		{Path: "packages/edge/src/gen.go", Lang: "go", Lines: 7},
		{Path: "lib/__init__.py", Lang: "python", Lines: 1},
		{Path: "lib/util.py", Lang: "python", Lines: 4},
		{Path: "README.md", Lines: 3},
	}
	got := docs.ModuleSizes(mods, files)
	want := map[int64]docs.ModuleSize{1: {Files: 1, Lines: 5}, 2: {Files: 3, Lines: 60}, 3: {Files: 3, Lines: 60},
		4: {Files: 1, Lines: 30}, 5: {Files: 2, Lines: 5}, 6: {Files: 1, Lines: 4}, 7: {Files: 1, Lines: 7}, 8: {}}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("module %d: %+v, want %+v", id, got[id], w)
		}
	}
}

// TestModuleSizesSurviveBrokenParentLinks checks that a parent link to
// an unknown module, or a cycle of links, neither loops nor loses a
// module's own files.
func TestModuleSizesSurviveBrokenParentLinks(t *testing.T) {
	mods := []store.Module{
		{ID: 1, Lang: "ts", Path: "a", ParentID: 2},
		{ID: 2, Lang: "ts", Path: "a/b", ParentID: 1},
		{ID: 3, Lang: "ts", Path: "c", ParentID: 99},
	}
	files := []store.File{{Path: "a/x.ts", Lang: "typescript", Lines: 1}, {Path: "a/b/y.ts", Lang: "typescript", Lines: 2},
		{Path: "c/z.ts", Lang: "typescript", Lines: 3}}
	got := docs.ModuleSizes(mods, files)
	if got[3] != (docs.ModuleSize{Files: 1, Lines: 3}) || got[1].Files < 1 || got[2].Files < 1 {
		t.Fatalf("%+v", got)
	}
}

// TestRepoGapsWeighModuleByNestedFiles checks that the gaps ranking
// weighs an npm package whose sources all live in src/ by those files.
func TestRepoGapsWeighModuleByNestedFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "pkg/package.json", `{"name": "@x/pkg"}`)
	for _, f := range []string{"a", "b", "c"} {
		write(t, root, "pkg/src/"+f+".ts", "export function "+f+"(): number {\n  const x = 1;\n  return x;\n}\n")
	}
	s, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := index.Repo(s, &config.Repo{Name: "r", Path: root}, index.Options{Workspace: "w"}); err != nil {
		t.Fatal(err)
	}
	row, err := s.RepoByPath(root)
	if err != nil {
		t.Fatal(err)
	}
	gaps, err := docs.RepoGaps(s, docs.RepoRef{ID: row.ID, Name: "r", Root: root}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want, seen := 3*(1+math.Log(4)), 0
	for _, g := range gaps {
		if g.Key != "r:pkg/" && g.Key != "r:pkg/src/" {
			continue
		}
		seen++
		if g.Files != 3 || math.Abs(g.Score-want) > 1e-9 {
			t.Errorf("%s: files %d score %v, want 3 files and score %v", g.Key, g.Files, g.Score, want)
		}
	}
	if seen != 2 {
		t.Fatalf("want both r:pkg/ and r:pkg/src/ among the gaps, got %+v", gaps)
	}
}
