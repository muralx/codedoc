package deps_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/store"
)

// files returns the paths of the files indexed of a package version,
// sorted.
func (p pkg) files() []string {
	p.t.Helper()
	rows, err := p.s.FilesByRepo(p.id)
	if err != nil {
		p.t.Fatal(err)
	}
	var out []string
	for _, f := range rows {
		out = append(out, f.Path)
	}
	return out
}

// has reports whether deps.db holds a package version.
func (sp *space) has(id string) bool {
	sp.t.Helper()
	has, err := sp.db().Has(id)
	if err != nil {
		sp.t.Fatal(err)
	}
	return has
}

// visible returns the package ids queries of the workspace see.
func (sp *space) visible() []string {
	sp.t.Helper()
	ids, err := deps.PackageIDs(sp.store, 0)
	if err != nil {
		sp.t.Fatal(err)
	}
	return ids
}

// A package a manifest declares and no source file below it imports is
// recorded as unused and not indexed, whichever key of the manifest
// lists it: a dev tool, a package of dependencies (plain-js), the sdk
// the nested package declares and only the root imports, a Go module
// go.mod requires and no file imports.
func TestPackagesNoFileImportsAreUnused(t *testing.T) {
	sp := newSpace(t, "deps-app", "deps-go")
	sp.sync(sp.opts)
	states := sp.states()
	for use, want := range map[string]string{
		"package.json lint-tool":              "9.4.0 unused",
		"package.json plain-js":               "2.0.1 unused",
		"packages/web/package.json @acme/sdk": "1.2.0 unused",
		"package.json @acme/sdk":              "1.2.0 indexed",
		"go.mod github.com/BurntSushi/toml":   "v0.3.1 unused",
		"go.mod github.com/acme/kit":          "v1.4.0 indexed",
	} {
		eq(t, use, states[use], want)
	}
	for _, id := range []string{"npm/lint-tool@9.4.0", "npm/plain-js@2.0.1", "go/github.com/BurntSushi/toml@v0.3.1"} {
		eq(t, id+" in deps.db", sp.has(id), false)
		eq(t, id+" visible", slices.Contains(sp.visible(), id), false)
	}
	eq(t, "nothing unused waits", sp.sync(sp.opts), deps.Summary{})
}

// deps.include indexes a declared package the code does not import, by
// name or by pattern.
func TestIncludeIndexesAnUnusedPackage(t *testing.T) {
	sp := newSpace(t, "deps-app", "deps-go")
	sp.sync(sp.opts)
	sp.ws.Deps.Include = []string{"lint-*", "github.com/BurntSushi/toml"}
	eq(t, "indexed once included", sp.sync(deps.Options{DB: sp.opts.DB}).Indexed, 2)
	eq(t, "pattern", sp.states()["package.json lint-tool"], "9.4.0 indexed")
	eq(t, "name", sp.states()["go.mod github.com/BurntSushi/toml"], "v0.3.1 indexed")
	eq(t, "symbols", sp.pkg("npm/lint-tool@9.4.0").names(), "index.d.ts/LintRule index.d.ts/LintRule.name index.d.ts/lint")
	sp.ws.Deps.Include = nil
	sp.sync(deps.Options{DB: sp.opts.DB})
	eq(t, "unused again without the entry", sp.states()["package.json lint-tool"], "9.4.0 unused")
}

// The code starts importing a package: the next sync, the bounded one
// of a query included, indexes it. The last import goes: the package is
// unused, no query sees it, and its rows stay in deps.db until a prune.
func TestImportsDecideAsTheCodeChanges(t *testing.T) {
	sp := newSpace(t, "deps-app", "deps-go")
	sp.sync(sp.opts)
	quick := deps.Options{DB: sp.opts.DB}
	plain, toml := filepath.Join(sp.repo("deps-app"), "src/plain.ts"), filepath.Join(sp.repo("deps-go"), "config.go")
	write(t, plain, "import { shout } from \"plain-js\";\n\nexport const loud = shout(\"x\");\n")
	write(t, toml, "package main\n\nimport \"github.com/BurntSushi/toml\"\n\nvar _ = toml.Decode\n")
	sp.index()
	eq(t, "both are indexed", sp.sync(quick).Indexed, 2)
	eq(t, "npm", sp.states()["package.json plain-js"], "2.0.1 indexed")
	eq(t, "the declarations come from @types/plain-js", sp.pkg("npm/plain-js@2.0.1").files(), []string{"extra.d.ts", "index.d.ts"})
	eq(t, "go, imported by path", sp.states()["go.mod github.com/BurntSushi/toml"], "v0.3.1 indexed")
	for _, file := range []string{plain, toml} {
		eq(t, "error", os.Remove(file), nil)
	}
	sp.index()
	eq(t, "nothing to index", sp.sync(quick), deps.Summary{})
	eq(t, "npm unused", sp.states()["package.json plain-js"], "2.0.1 unused")
	eq(t, "go unused", sp.states()["go.mod github.com/BurntSushi/toml"], "v0.3.1 unused")
	eq(t, "queries do not see it", slices.Contains(sp.visible(), "npm/plain-js@2.0.1"), false)
	eq(t, "the rows stay", sp.has("npm/plain-js@2.0.1"), true)
	out, err := sp.db().Prune(deps.PruneOptions{})
	eq(t, "error", err, nil)
	eq(t, "a prune deletes them", out.Packages, []string{"go/github.com/BurntSushi/toml@v0.3.1", "npm/plain-js@2.0.1"})
	write(t, plain, "import { shout } from \"plain-js\";\n\nexport const loud = shout(\"x\");\n")
	sp.index()
	eq(t, "imported again, indexed again", sp.sync(quick).Indexed, 1)
}

// typesRepo declares one @types package per case: shapes exists only as
// types, @scope/thing is imported under its scoped name, idle is never
// imported, and events is a real package that shadows the built-in
// module of that name.
var typesRepo = map[string]string{
	"package.json": `{"name": "app", "version": "0.0.0", "dependencies": {"events": "3.0.0"},
  "devDependencies": {"@types/shapes": "1.0.0", "@types/scope__thing": "1.0.0", "@types/idle": "1.0.0", "@types/node": "20.0.0",
  "builder": "1.0.0"}}`,
	"src/a.ts":         "import type { Shape } from \"shapes\";\nimport { thing } from \"@scope/thing/sub\";\nimport \"events\";\nexport const s: Shape = thing;\n",
	"build.config.ts":  "import { build } from \"builder\";\nexport default build;\n",
	".builderrc.cjs":   "const b = require(\"builder\");\nmodule.exports = b;\n",
	"tools/gen.ts":     "export const nothing = 1;\n",
	"tools/readme.txt": "not code\n",
}

// installTypes installs the packages typesRepo declares.
func installTypes(t *testing.T, repo string) {
	t.Helper()
	for _, name := range []string{"@types/shapes", "@types/scope__thing", "@types/idle", "events", "builder"} {
		npmPackage(t, filepath.Join(repo, "node_modules", name), name, "1.0.0")
	}
	node := filepath.Join(repo, "node_modules/@types/node")
	npmPackage(t, node, "@types/node", "20.0.0")
	write(t, filepath.Join(node, "events.d.ts"), "declare module \"events\" {\n    export class EventEmitter {}\n}\n")
}

// An @types package follows the package it declares: it is indexed when
// that one is imported, under a scope too. @types/node follows the
// built-in modules imported, and a specifier a declared package answers
// to is not one. What only a tool configuration file imports is unused.
func TestTypesPackagesFollowWhatTheyDeclare(t *testing.T) {
	sp := sourceSpace(t, "w", typesRepo, "", func(repo string) { installTypes(t, repo) })
	sp.sync(sp.opts)
	eq(t, "states", sp.states(), map[string]string{
		"package.json @types/shapes":       "1.0.0 indexed",
		"package.json @types/scope__thing": "1.0.0 indexed",
		"package.json @types/idle":         "1.0.0 unused",
		"package.json @types/node":         "20.0.0 unused",
		"package.json events":              "1.0.0 indexed",
		"package.json builder":             "1.0.0 unused",
	})
}

// @types/node is indexed lazily: the declaration files of the built-in
// modules the code imports, with or without the node: scheme, and
// nothing else of the package. A module imported later is added to the
// ones held; deps.include indexes the whole package.
func TestNodeTypesHoldOnlyTheImportedModules(t *testing.T) {
	sp := newSpace(t, "deps-app")
	sp.sync(sp.opts)
	const id = "npm/@types/node@22.1.0"
	eq(t, "files", sp.pkg(id).files(), []string{"fs.d.ts", "path.d.ts"})
	eq(t, "symbols", sp.pkg(id).names(), "fs.d.ts/fs.ReadStream fs.d.ts/fs.ReadStream.bytesRead fs.d.ts/fs.readFileSync path.d.ts/path.join")
	eq(t, "the README is kept", sp.pkg(id).module(".").DocSource, "README.md")
	eq(t, "modules held", sp.info(id).Only, []string{"fs", "path"})
	quick := deps.Options{DB: sp.opts.DB}
	eq(t, "the modules are there: nothing to do", sp.sync(quick), deps.Summary{})

	server := filepath.Join(sp.repo("deps-app"), "src/server.ts")
	write(t, server, "import { createServer } from \"node:http\";\nimport { readFile } from \"node:fs/promises\";\n"+
		"import \"node:nowhere\";\nexport const s = [createServer, readFile];\n")
	sp.index()
	eq(t, "a module imported later is read", sp.sync(quick).Indexed, 1)
	eq(t, "files", sp.pkg(id).files(), []string{"fs.d.ts", "fs/promises.d.ts", "http.d.ts", "path.d.ts"})
	eq(t, "state", sp.states()["package.json @types/node"], "22.1.0 indexed")

	// Another workspace sharing deps.db imports fewer modules: what is
	// held covers it, and stays.
	other := newSpace(t, "deps-app")
	other.opts.DB = sp.opts.DB
	eq(t, "a workspace importing less writes nothing", other.sync(other.opts).Indexed, 0)
	eq(t, "modules held", sp.info(id).Only, []string{"fs", "fs/promises", "http", "path"})
}

// deps.include indexes @types/node whole, which covers every module
// from then on. The whole rows a build without the lazy rule wrote are
// replaced by the modules imported. With no built-in module imported
// the package is unused.
func TestNodeTypesWholeLegacyAndUnused(t *testing.T) {
	sp := newSpace(t, "deps-app")
	sp.sync(sp.opts)
	const id = "npm/@types/node@22.1.0"
	quick := deps.Options{DB: sp.opts.DB}
	sp.ws.Deps.Include = []string{"@types/node"}
	eq(t, "included, the whole package is read", sp.sync(quick).Indexed, 1)
	eq(t, "files", sp.pkg(id).files(), []string{"fs.d.ts", "fs/promises.d.ts", "http.d.ts", "index.d.ts", "path.d.ts", "stream.d.ts"})
	eq(t, "no module list for a whole package", sp.info(id).Only, []string(nil))
	sp.ws.Deps.Include = nil
	eq(t, "a whole package covers every module", sp.sync(sp.opts).Indexed, 0)

	err := sp.db().Store().Batch(func(b *store.Batch) error { return b.DeleteMeta("dep_modules:" + id) })
	eq(t, "error", err, nil)
	eq(t, "the whole rows of an older build are replaced", sp.sync(quick).Indexed, 1)
	eq(t, "files", sp.pkg(id).files(), []string{"fs.d.ts", "path.d.ts"})

	eq(t, "error", os.Remove(filepath.Join(sp.repo("deps-app"), "src/files.ts")), nil)
	sp.index()
	sp.sync(quick)
	eq(t, "no built-in module imported", sp.states()["package.json @types/node"], "22.1.0 unused")
}

// info returns what deps.db says of a package version.
func (sp *space) info(id string) deps.PackageInfo {
	sp.t.Helper()
	infos, err := sp.db().Packages()
	if err != nil {
		sp.t.Fatal(err)
	}
	for _, p := range infos {
		if p.ID == id {
			return p
		}
	}
	sp.t.Fatalf("%s is not in deps.db", id)
	return deps.PackageInfo{}
}
