package deps_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

// supers returns "Sub > Super confidence" of the extends edges leaving
// the symbol with a key.
func (p pkg) supers(key string) []string {
	p.t.Helper()
	edges, err := p.s.EdgesFrom(p.symbol(key).ID, "extends")
	if err != nil {
		p.t.Fatal(err)
	}
	var out []string
	for _, e := range edges {
		dst, err := p.s.SymbolByID(e.Dst)
		if err != nil {
			p.t.Fatal(err)
		}
		out = append(out, dst.StableKey+" "+e.Confidence)
	}
	return out
}

// Inherited members are not copied: a class points at the class it
// extends, and a query follows the edge.
func TestNpmClassesPointAtTheClassTheyExtend(t *testing.T) {
	sp := newSpace(t, "deps-app")
	sp.sync(sp.opts)
	p := sp.pkg("npm/@acme/sdk@1.2.0")
	const server = "npm/@acme/sdk@1.2.0:ts:@acme/sdk/server:dist/server/index.d.ts/Server"
	eq(t, "Server extends Protocol", p.supers(server), []string{
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/shared/protocol.js:dist/shared/protocol.d.ts/Protocol import"})
	eq(t, "Protocol extends nothing", p.supers(
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/shared/protocol.js:dist/shared/protocol.d.ts/Protocol"), []string(nil))
}

func TestGoStructsPointAtTheTypesTheyEmbed(t *testing.T) {
	sp := newSpace(t, "deps-go")
	sp.sync(sp.opts)
	kit := sp.pkg("go/github.com/acme/kit@v1.4.0")
	eq(t, "Client embeds Base", kit.supers("go/github.com/acme/kit@v1.4.0:go:github.com/acme/kit:Client"),
		[]string{"go/github.com/acme/kit@v1.4.0:go:github.com/acme/kit:Base local"})
}

// A package version written by an older extraction keeps answering and
// is extracted again by the next full sync, not by a query's bounded one.
func TestPackagesOfAnOlderExtractionAreWrittenAgain(t *testing.T) {
	sp := newSpace(t, "deps-app")
	sp.sync(sp.opts)
	const id = "npm/@acme/sdk@1.2.0"
	db := sp.db()
	err := db.Store().Batch(func(b *store.Batch) error { return b.DeleteMeta("dep_extraction:" + id) })
	eq(t, "error", err, nil)
	if has, _ := db.Has(id); !has {
		t.Fatal("the old rows still answer")
	}
	if current, _ := db.Current(id); current {
		t.Fatal("a package without a recorded version is not current")
	}
	query := sp.opts
	query.Full = false
	eq(t, "a query's sync leaves it", sp.sync(query).Indexed, 0)
	eq(t, "a full sync writes it again", sp.sync(sp.opts).Indexed, 1)
	if current, _ := db.Current(id); !current {
		t.Fatal("written again under the current version")
	}
	eq(t, "one repo row", len(sp.pkg(id).keys()), 18)
	eq(t, "nothing left to do", sp.sync(sp.opts).Indexed, 0)
	if current, err := db.Current("npm/absent@1.0.0"); current || err != nil {
		t.Fatalf("an absent package is not current: %v %v", current, err)
	}
}
