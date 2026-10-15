package deps_test

import (
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/store"
)

// depEdges returns "source qualified name -kind-> dependency key" of the
// edges leaving the symbols of a file of a repo, sorted.
func (sp *space) depEdges(repo, file string) []string {
	sp.t.Helper()
	row, err := sp.store.RepoByPath(sp.repo(repo))
	if err != nil {
		sp.t.Fatal(err)
	}
	f, err := sp.store.FileByPath(row.ID, file)
	if err != nil {
		sp.t.Fatal(err)
	}
	syms, err := sp.store.SymbolsInFile(f.ID)
	if err != nil {
		sp.t.Fatal(err)
	}
	var out []string
	for _, sy := range syms {
		edges, err := sp.store.EdgesFrom(sy.ID, "calls", "references")
		if err != nil {
			sp.t.Fatal(err)
		}
		for _, e := range edges {
			if e.DepKey != "" {
				out = append(out, sy.Qualified+" -"+e.Kind+"/"+e.Confidence+"-> "+e.DepKey)
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestWorkspaceCodeGetsEdgesIntoItsPackages(t *testing.T) {
	sp := newSpace(t, "deps-app", "deps-go")
	eq(t, "no edges before the packages are indexed", sp.depEdges("deps-app", "src/main.ts"), []string(nil))
	sp.sync(sp.opts)
	const sdk, kit = "npm/@acme/sdk@1.2.0:ts:@acme/sdk/", "go/github.com/acme/kit@v1.4.0:go:github.com/acme/kit:"
	eq(t, "TypeScript", sp.depEdges("deps-app", "src/main.ts"), []string{
		"src/main.ts/App.announce -calls/import-> " + sdk + "shared/protocol.js:dist/shared/protocol.d.ts/Protocol.notification",
		"src/main.ts/App.server -references/import-> " + sdk + "server:dist/server/index.d.ts/Server",
		"src/main.ts/App.server -references/import-> " + sdk + "server:dist/server/index.d.ts/Server",
		"src/main.ts/revision -calls/import-> " + sdk + "shared/protocol.js:dist/shared/protocol.d.ts/Protocol.revision",
		"src/main.ts/start -calls/import-> npm/linked-lib@0.3.0:ts:linked-lib:lib/index.d.ts/leftTrim",
		"src/main.ts/start -references/import-> " + sdk + "server:dist/server/index.d.ts/Server",
		"src/main.ts/start -references/import-> " + sdk + "server:dist/server/index.d.ts/Server",
	})
	eq(t, "Go", sp.depEdges("deps-go", "main.go"), []string{
		"app.client -references/import-> " + kit + "Client",
		"app.run -calls/import-> " + kit + "Base.Close",
		"app.run -calls/import-> " + kit + "Client.Do",
		"main -calls/import-> " + kit + "New",
	})
	eq(t, "a second sync changes nothing", sp.sync(sp.opts).Indexed, 0)
	eq(t, "the edges stay", len(sp.depEdges("deps-go", "main.go")), 4)
}

// Two manifests of one repo may resolve a package to two versions: a
// file uses the one of its nearest manifest, and follows it when that
// manifest moves to another version.
func TestEdgesFollowTheVersionOfTheNearestManifest(t *testing.T) {
	sp := newSpace(t, "deps-app")
	sp.sync(sp.opts)
	eq(t, "root", sp.depEdges("deps-app", "src/copies.ts"), []string{
		"src/copies.ts/copies -calls/import-> npm/dup@2.0.0:ts:dup:dup.d.ts/dup"})
	eq(t, "nested package", sp.depEdges("deps-app", "packages/web/src/index.ts"), []string{
		"src/index.ts/title -calls/import-> npm/dup@1.0.0:ts:dup:dup.d.ts/dup"})
	write(t, filepath.Join(sp.repo("deps-app"), "packages/web/node_modules/dup/package.json"),
		`{"name": "dup", "version": "1.1.0", "typings": "dup.d.ts"}`)
	eq(t, "the new version is indexed", sp.sync(sp.opts).Indexed, 1)
	eq(t, "nested package moved", sp.depEdges("deps-app", "packages/web/src/index.ts"), []string{
		"src/index.ts/title -calls/import-> npm/dup@1.1.0:ts:dup:dup.d.ts/dup"})
	eq(t, "root stays", sp.depEdges("deps-app", "src/copies.ts"), []string{
		"src/copies.ts/copies -calls/import-> npm/dup@2.0.0:ts:dup:dup.d.ts/dup"})
	off := false
	sp.ws.Deps.Enabled = &off
	sp.sync(sp.opts)
	eq(t, "without dependencies the edges go", sp.depEdges("deps-app", "src/copies.ts"), []string(nil))
}

// A package that waits to be indexed gets its edges once it is.
func TestEdgesAppearWhenAPendingPackageIsIndexed(t *testing.T) {
	sp := newSpace(t, "deps-go")
	late := sp.opts
	late.Full, late.Deadline = false, time.Now().Add(-time.Second)
	eq(t, "nothing indexed past the deadline", sp.sync(late).Pending, 1)
	eq(t, "no edges yet", sp.depEdges("deps-go", "main.go"), []string(nil))
	sp.sync(sp.opts)
	eq(t, "edges once indexed", len(sp.depEdges("deps-go", "main.go")), 4)
}

// A workspace indexed before dependencies had edges gets them at its
// next sync, and a package an older extraction wrote brings the edges
// its new rows allow.
func TestEdgesAreResolvedAgainAfterAnUpgrade(t *testing.T) {
	sp := newSpace(t, "deps-app")
	sp.sync(sp.opts)
	want := sp.depEdges("deps-app", "src/main.ts")
	err := sp.store.Batch(func(b *store.Batch) error {
		if err := b.DeleteHeuristicEdges(); err != nil {
			return err
		}
		return b.DeleteMeta(store.DepsDBKey)
	})
	eq(t, "error", err, nil)
	eq(t, "edges dropped", sp.depEdges("deps-app", "src/main.ts"), []string(nil))
	eq(t, "nothing to index", sp.sync(sp.opts).Indexed, 0)
	eq(t, "first sync against this deps.db", sp.depEdges("deps-app", "src/main.ts"), want)

	err = sp.store.Batch(func(b *store.Batch) error { return b.DeleteHeuristicEdges() })
	eq(t, "error", err, nil)
	db := sp.db()
	err = db.Store().Batch(func(b *store.Batch) error { return b.DeleteMeta("dep_extraction:npm/@acme/sdk@1.2.0") })
	eq(t, "error", err, nil)
	eq(t, "the package is written again", sp.sync(sp.opts).Indexed, 1)
	eq(t, "its importers are resolved again", sp.depEdges("deps-app", "src/main.ts"), want)
	eq(t, "other files are left", sp.depEdges("deps-app", "src/copies.ts"), []string(nil))
}
