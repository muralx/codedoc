package query_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
)

// outsideFiles are files of the link repo that discovery leaves out,
// each for another reason; notIndexed maps the paths asked for to the
// reason the answer gives.
var outsideFiles = map[string]string{
	"out/gen.ts":                    "export const a = 1;\n",
	"debug.log":                     "x\n",
	"pnpm-lock.yaml":                "lockfileVersion: '9.0'\n",
	"dist/x.js":                     "var a;\n",
	"node_modules/stray/index.d.ts": "export declare const a: number;\n",
	"packages/edge/logo.png":        "\x89PNG\x00\x00",
	"packages/edge/huge.ts":         strings.Repeat("// filler line of a generated bundle\n", 70_000),
	"packages/edge/skipped.gen.ts":  "export const b = 2;\n",
	"scripts/tool.py":               "def tool():\n    pass\n",
	"empty/dir/keep.bin":            "\x00",
}

var notIndexed = map[string]string{
	"out/gen.ts":                    "git ignores it (.gitignore)",
	"debug.log":                     "git ignores it (.gitignore)",
	"pnpm-lock.yaml":                "dependency lock files are never indexed",
	"dist/x.js":                     "the directory dist/ is excluded (dependencies, fixtures and build output are)",
	"dist":                          "the directory dist/ is excluded (dependencies, fixtures and build output are)",
	"node_modules/stray/index.d.ts": "it belongs to a package no manifest of the workspace declares (dependencies of dependencies are not indexed)",
	"packages/edge/logo.png":        "it is a binary file",
	"packages/edge/huge.ts":         "it is larger than the limit of 2 MB",
	"packages/edge/skipped.gen.ts":  "it matches the exclude glob **/*.gen.ts",
	"scripts/tool.py":               "the languages of the repo in the config leave out python",
	"empty/dir":                     "no file below it is indexed",
}

// writeIn writes a file below the root of the current repo.
func writeIn(t *testing.T, c *query.Ctx, rel, content string) {
	t.Helper()
	full := filepath.Join(c.Repo.Path, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A path outside the index says which, in one line.
func TestPathOutsideTheIndexSaysWhy(t *testing.T) {
	c := linkWorkspace(t)
	for rel, content := range outsideFiles {
		writeIn(t, c, rel, content)
	}
	c.Workspace.Exclude = []string{"**/*.gen.ts"}
	c.Repo.Languages = []string{"ts", "doc", "json"}
	for target, why := range notIndexed {
		want := target + " is not indexed: " + why
		for name, call := range map[string]func() (*output.Response, error){
			"outline": func() (*output.Response, error) { return query.Outline(c, target, query.OutlineOpts{}) },
			"symbol":  func() (*output.Response, error) { return query.Symbol(c, target+"#a", query.SymbolOpts{}) },
			"line":    func() (*output.Response, error) { return query.Symbol(c, target+":1", query.SymbolOpts{}) },
			"callers": func() (*output.Response, error) { return query.Callers(c, target+"#a", query.GraphOpts{Depth: 1}) },
			"ls":      func() (*output.Response, error) { return query.Ls(c, []string{target}, query.LsOpts{}) },
		} {
			r, err := call()
			if !errors.Is(err, query.ErrNotIndexed) || err.Error() != want || (r != nil && len(r.Sections) > 0) {
				t.Errorf("%s %s: %v", name, target, err)
			}
		}
	}
}

func TestPathOutsideTheIndexInOtherAnswers(t *testing.T) {
	c := linkWorkspace(t)
	writeIn(t, c, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	// lines prints what is on disk and says why the index has no symbols.
	r, err := query.Lines(c, "pnpm-lock.yaml", "1")
	wants(t, text(t, r, err), "pnpm-lock.yaml:1  outside any indexed symbol\n1\tlockfileVersion: '9.0'\n",
		"pnpm-lock.yaml is not indexed: dependency lock files are never indexed\n")
	// A file written since the index was read.
	t.Setenv("CODEDOC_FRESH_MAX_FILES", "0")
	writeIn(t, c, "packages/edge/src/late.ts", "export const late = 1;\n")
	if _, err = query.Outline(c, "packages/edge/src/late.ts", query.OutlineOpts{}); err == nil ||
		err.Error() != "packages/edge/src/late.ts is not indexed: the index has not read it yet; run codedoc index" {
		t.Errorf("a new file: %v", err)
	}
	// Outside every repo.
	if _, err = query.Outline(c, "/etc/hosts", query.OutlineOpts{}); !errors.Is(err, query.ErrOutside) {
		t.Errorf("/etc/hosts: %v", err)
	}
	if _, err = query.Symbol(c, "../elsewhere.ts#a", query.SymbolOpts{}); !errors.Is(err, query.ErrOutside) {
		t.Errorf("../elsewhere.ts: %v", err)
	}
}

// A file of a dependency that is not indexed says what the dependency
// lacks; a name nothing was found for names the dependencies that were
// not searched.
func TestDependencyOutsideTheIndexSaysWhy(t *testing.T) {
	c := synced(t, "deps-app")
	c.Repo, c.Cwd = c.Workspace.Repos[0], c.Workspace.Repos[0].Path
	for target, want := range map[string]string{
		"node_modules/lint-tool/index.d.ts": "node_modules/lint-tool/index.d.ts is not indexed: no source file imports the dependency " +
			"lint-tool; name it under deps.include in the config to index it",
		"node_modules/@acme/sdk/package.json": "node_modules/@acme/sdk/package.json is not indexed: of the dependency @acme/sdk " +
			"only the declarations its entry points export are indexed",
	} {
		if _, err := query.Outline(c, target, query.OutlineOpts{}); !errors.Is(err, query.ErrNotIndexed) || err.Error() != want {
			t.Errorf("outline %s: %v", target, err)
		}
		if _, err := query.Symbol(c, target+"#lint", query.SymbolOpts{}); !errors.Is(err, query.ErrNotIndexed) || err.Error() != want {
			t.Errorf("symbol %s: %v", target, err)
		}
	}
	// The packages no repo gets indexed are named; @acme/sdk, which one
	// manifest declares without importing it, is indexed for another.
	_, err := query.Symbol(c, "zzyzxq", query.SymbolOpts{})
	if err == nil || err.Error() != "symbol zzyzxq: not found; not searched: lint-tool, plain-js "+
		"(dependencies no source file imports; deps.include in the config indexes them)" {
		t.Errorf("a name nothing has: %v", err)
	}
	// A workspace-only command searched everything it could.
	if _, err = query.Callees(c, "zzyzxq", query.GraphOpts{Depth: 1}); err == nil || err.Error() != "symbol zzyzxq: not found" {
		t.Errorf("callees: %v", err)
	}
}

// Dependency names need no --deps when no workspace symbol has them.
func TestDependencyNamesNeedNoFlag(t *testing.T) {
	c := synced(t, "deps-app")
	r, err := query.Symbol(c, "Server", query.SymbolOpts{})
	wants(t, text(t, r, err), "Server  ", "[dep @acme/sdk@1.2.0]\n", "export declare class Server extends Protocol {")
	r, err = query.Callers(c, "Server.notification", query.GraphOpts{Depth: 1})
	wants(t, text(t, r, err), "== callers of Protocol.notification [dep @acme/sdk@1.2.0] (1)\nApp.announce  src/main.ts:20  ")
	r, err = query.Refs(c, "Server", query.GraphOpts{})
	wants(t, text(t, r, err), "== references to Server [dep @acme/sdk@1.2.0] (")
	r, err = query.Outline(c, "Server", query.OutlineOpts{})
	wants(t, text(t, r, err), "== members of Server (4)")
	r, err = query.Find(c, "Server.connect", query.FindOpts{})
	wants(t, text(t, r, err), "== find Server.connect (1)", "[dep @acme/sdk@1.2.0]")
	// A workspace symbol with the name answers alone; --deps forces both.
	r, err = query.Symbol(c, "revision", query.SymbolOpts{NoBody: true})
	if got := text(t, r, err); !strings.Contains(got, "revision  src/main.ts:10-12  ") || strings.Contains(got, "also:") {
		t.Errorf("a workspace name:\n%s", got)
	}
	r, err = query.Symbol(withDeps(c), "revision", query.SymbolOpts{NoBody: true})
	wants(t, text(t, r, err), "revision  src/main.ts:10-12  ", "also: Protocol.revision (")
	// The nearest names are a dependency's when the workspace has none.
	c.Deps = false
	r, err = query.Symbol(c, "protocol_version", query.SymbolOpts{NoBody: true})
	wants(t, text(t, r, err), "PROTOCOL_VERSION  ", "answered for PROTOCOL_VERSION")
	// A command on workspace symbols does not look there.
	if _, err = query.Callees(c, "PROTOCOL_VERSION", query.GraphOpts{Depth: 1}); !errors.Is(err, query.ErrNotFound) {
		t.Errorf("callees of a dependency name: %v", err)
	}
}
