package deps_test

import (
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/store"
)

// embedded returns how many vectors one pass over repos wrote and how
// many it deferred.
func (sp *space) embedded(repos []*config.Repo, limit int) (written, deferred int) {
	sp.t.Helper()
	res, err := deps.EmbedVectors(sp.store, sp.opts.DB, embed.NewFake(16), repos, limit)
	if err != nil {
		sp.t.Fatal(err)
	}
	return res.Symbols + res.Modules, res.Deferred
}

// vectors returns the embedded and the documented symbols of a package.
func (sp *space) vectors(id string) (embedded, documented int) {
	sp.t.Helper()
	embedded, documented, err := sp.db().Store().SymbolVecCounts(store.VecScope{RepoID: sp.pkg(id).id}, 16)
	if err != nil {
		sp.t.Fatal(err)
	}
	return embedded, documented
}

// The documented symbols and module docs of the packages a workspace
// uses get their vectors in deps.db once, whoever asks.
func TestPackageVectorsAreEmbeddedOncePerVersion(t *testing.T) {
	sp := newSpace(t, "deps-app", "deps-go")
	n, _ := sp.embedded(sp.ws.Repos, 0)
	eq(t, "no deps.db yet", n, 0)
	sp.sync(sp.opts)
	n, deferred := sp.embedded(sp.ws.Repos, 3)
	eq(t, "over the limit nothing is written", []bool{n == 0, deferred > 3}, []bool{true, true})
	n, _ = sp.embedded(sp.ws.Repos[:1], 0)
	eq(t, "the npm packages are embedded", n > 0, true)
	have, documented := sp.vectors("npm/@acme/sdk@1.2.0")
	eq(t, "every documented sdk symbol", []bool{have > 0, have == documented}, []bool{true, true})
	mods, err := sp.db().Store().ModuleVecs(store.VecScope{RepoID: sp.pkg("npm/@acme/sdk@1.2.0").id})
	eq(t, "error", err, nil)
	eq(t, "the README of the package", []any{len(mods), mods[0].Path, mods[0].Dims}, []any{1, ".", 16})
	have, _ = sp.vectors("go/github.com/acme/kit@v1.4.0")
	eq(t, "the Go packages of the other repo are left", have, 0)
	n, _ = sp.embedded(sp.ws.Repos, 0)
	eq(t, "the rest", n > 0, true)
	n, _ = sp.embedded(sp.ws.Repos, 0)
	eq(t, "a second pass embeds nothing", n, 0)
	n, _ = sp.embedded([]*config.Repo{{Name: "x", Path: filepath.Join(t.TempDir(), "x")}}, 0)
	eq(t, "a repo that was never indexed", n, 0)
}
