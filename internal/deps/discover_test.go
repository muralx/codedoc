package deps_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/deps"
)

func TestParseID(t *testing.T) {
	for in, want := range map[string]deps.ID{
		"npm/@modelcontextprotocol/sdk@1.20.0": {Eco: "npm", Name: "@modelcontextprotocol/sdk", Version: "1.20.0"},
		"go/github.com/spf13/cobra@v1.9.1":     {Eco: "go", Name: "github.com/spf13/cobra", Version: "v1.9.1"},
		"npm/zod@4.0.0":                        {Eco: "npm", Name: "zod", Version: "4.0.0"},
	} {
		got, ok := deps.ParseID(in)
		if !ok || got != want || got.String() != in {
			t.Errorf("ParseID(%q) = %+v, %v", in, got, ok)
		}
	}
	for _, bad := range []string{"zod", "npm/zod", "npm/@scope/pkg", "npm/zod@", "/zod@1", "npm/zod@1:ts:zod:x"} {
		if _, ok := deps.ParseID(bad); ok {
			t.Errorf("ParseID(%q) must fail", bad)
		}
	}
}

func TestIsTrigger(t *testing.T) {
	for _, p := range []string{"package.json", "web/package.json", "go.mod", "svc/go.sum", "pnpm-lock.yaml",
		"yarn.lock", "package-lock.json"} {
		if !deps.IsTrigger(p) {
			t.Errorf("%s must trigger discovery", p)
		}
	}
	for _, p := range []string{"src/main.ts", "go.mod.bak", ".modules.yaml", "tsconfig.json"} {
		if deps.IsTrigger(p) {
			t.Errorf("%s must not trigger discovery", p)
		}
	}
}

// The npm fixture holds every layout of deps spec section 4: a scoped
// package, a pnpm symlink, an @types pair, one package at two versions,
// a workspace package, a declared package that is not installed, and
// packages no source file below their manifest imports (a dev tool, the
// @types pair, the sdk in the nested package), which are unused.
func TestDiscoverNpmLayouts(t *testing.T) {
	sp := newSpace(t, "deps-app")
	sp.sync(deps.Options{DB: sp.opts.DB, Deadline: timePast})
	root, _ := filepath.EvalSymlinks(sp.repo("deps-app"))
	eq(t, "states", sp.states(), map[string]string{
		"package.json @acme/sdk":              "1.2.0 pending",
		"package.json dup":                    "2.0.0 pending",
		"package.json linked-lib":             "0.3.0 pending",
		"package.json plain-js":               "2.0.1 unused",
		"package.json lint-tool":              "9.4.0 unused",
		"package.json @types/node":            "22.1.0 pending",
		"package.json left-pad":               "missing",
		"package.json @fixture/web":           "skipped",
		"packages/web/package.json @acme/sdk": "1.2.0 unused",
		"packages/web/package.json dup":       "1.0.0 pending",
	})
	eq(t, "directories", sp.dirs(root), map[string]string{
		"package.json @acme/sdk":              "node_modules/@acme/sdk",
		"package.json dup":                    "node_modules/dup",
		"package.json linked-lib":             "node_modules/.pnpm/linked-lib@0.3.0/node_modules/linked-lib",
		"package.json plain-js":               "node_modules/plain-js",
		"package.json lint-tool":              "node_modules/lint-tool",
		"package.json @types/node":            "node_modules/@types/node",
		"package.json left-pad":               "",
		"package.json @fixture/web":           "packages/web",
		"packages/web/package.json @acme/sdk": "node_modules/@acme/sdk",
		"packages/web/package.json dup":       "packages/web/node_modules/dup",
	})
	uses := sp.uses()
	plain := uses["package.json plain-js"]
	eq(t, "plain-js takes its declarations from @types", plain.TypesDir, filepath.Join(root, "node_modules/@types/plain-js"))
	eq(t, "root of plain-js", plain.Root(), plain.TypesDir)
	eq(t, "declared range", uses["package.json left-pad"].Declared, "^1.3.0")
	eq(t, "declared workspace range", uses["package.json @fixture/web"].Declared, "workspace:*")
	eq(t, "ecosystem", uses["package.json dup"].Eco, "npm")
}

// timePast is a deadline that already passed: discovery runs, nothing is
// indexed.
var timePast = time.Unix(1, 0)

func TestDiscoverGoModules(t *testing.T) {
	sp := newSpace(t, "deps-go", "go-lib")
	sp.sync(deps.Options{DB: sp.opts.DB, Deadline: timePast})
	eq(t, "states", sp.states(), map[string]string{
		"go.mod github.com/acme/kit":        "v1.4.0 pending",
		"go.mod github.com/BurntSushi/toml": "v0.3.1 unused",
		"go.mod github.com/acme/absent":     "missing",
		"go.mod github.com/fixture/lib":     "skipped",
	})
	dirs := sp.dirs(os.Getenv(deps.ModCacheEnv))
	eq(t, "kit", dirs["go.mod github.com/acme/kit"], "github.com/acme/kit@v1.4.0")
	eq(t, "upper case is escaped", dirs["go.mod github.com/BurntSushi/toml"], "github.com/!burnt!sushi/toml@v0.3.1")
	eq(t, "a missing module records where it is expected", dirs["go.mod github.com/acme/absent"], "github.com/acme/absent@v0.1.0")
	absent := sp.uses()["go.mod github.com/acme/absent"]
	eq(t, "declared", absent.Declared+" "+absent.Eco, "v0.1.0 go")
}

// A replace to a local directory outside the workspace is indexed as the
// local version of its own; a replace to another module uses that module; a
// required module a workspace repo provides is skipped even without a
// replace. No file imports the replaced modules: a deps.include pattern
// has them indexed all the same.
func TestDiscoverGoReplaces(t *testing.T) {
	sp := newSpace(t, "deps-go", "go-lib")
	sp.ws.Deps.Include = []string{"github.com/acme/*"}
	mod := filepath.Join(sp.repo("deps-go"), "go.mod")
	raw, _ := os.ReadFile(mod)
	fork := filepath.Join(filepath.Dir(os.Getenv(deps.ModCacheEnv)), "golocal", "fork")
	body := strings.Replace(string(raw), "replace github.com/fixture/lib => ../go-lib\n", "", 1) +
		"require github.com/acme/fork v0.0.0\nrequire github.com/acme/gone v0.0.0\nrequire github.com/acme/moved v1.0.0\n" +
		"replace github.com/acme/fork => " + fork + "\nreplace github.com/acme/gone => /nonexistent/nowhere\n" +
		"replace github.com/acme/moved v1.0.0 => github.com/acme/kit v1.4.0\n"
	write(t, mod, body)
	sp.index()
	sp.sync(deps.Options{DB: sp.opts.DB, Deadline: timePast})
	states := sp.states()
	eq(t, "local replace", localRE.MatchString(states["go.mod github.com/acme/fork"]), true)
	eq(t, "replace to a missing directory", states["go.mod github.com/acme/gone"], "missing")
	eq(t, "module replace", states["go.mod github.com/acme/moved"], "v1.4.0 pending")
	eq(t, "a module the workspace provides", states["go.mod github.com/fixture/lib"], "skipped")
	uses := sp.uses()
	eq(t, "local directory", uses["go.mod github.com/acme/fork"].Dir, fork)
	eq(t, "replacing module's directory", filepath.Base(uses["go.mod github.com/acme/moved"].Dir), "kit@v1.4.0")
}

func TestDiscoverIncludeAndExclude(t *testing.T) {
	sp := newSpace(t, "deps-app", "deps-go")
	sp.ws.Deps.Include = []string{"transitive-dep", "github.com/acme/indirect", "nowhere-pkg"}
	sp.ws.Deps.Exclude = []string{"@acme/*", "dup", "github.com/BurntSushi/**"}
	sp.sync(deps.Options{DB: sp.opts.DB, Deadline: timePast})
	eq(t, "states", sp.states(), map[string]string{
		"package.json transitive-dep":     "5.1.0 pending",
		"package.json linked-lib":         "0.3.0 pending",
		"package.json plain-js":           "2.0.1 unused",
		"package.json lint-tool":          "9.4.0 unused",
		"package.json @types/node":        "22.1.0 pending",
		"package.json left-pad":           "missing",
		"package.json @fixture/web":       "skipped",
		"go.mod github.com/acme/indirect": "v1.0.0 pending",
		"go.mod github.com/acme/kit":      "v1.4.0 pending",
		"go.mod github.com/acme/absent":   "missing",
		"go.mod github.com/fixture/lib":   "missing",
	})
	dir := sp.uses()["package.json transitive-dep"].Dir
	eq(t, "an included package is found in pnpm's hoisting directory", strings.HasSuffix(dir,
		"transitive-dep@5.1.0/node_modules/transitive-dep"), true)
}

func TestDiscoverReportsBrokenManifests(t *testing.T) {
	sp := newSpace(t, "deps-app", "deps-go")
	write(t, filepath.Join(sp.repo("deps-app"), "packages/web/package.json"), "{not json")
	write(t, filepath.Join(sp.repo("deps-go"), "go.mod"), "module x\nrequire (\n")
	sp.index()
	sum := sp.sync(deps.Options{DB: sp.opts.DB, Deadline: timePast})
	if len(sum.Problems) != 2 || !strings.Contains(sum.Problems[0], "packages/web/package.json") ||
		!strings.Contains(sum.Problems[1], "go.mod") {
		t.Fatalf("problems: %v", sum.Problems)
	}
	if _, ok := sp.uses()["package.json dup"]; !ok {
		t.Error("the readable manifest must still be discovered")
	}
}
