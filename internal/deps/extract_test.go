package deps_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/store"
)

// pkg is what deps.db holds of one package version.
type pkg struct {
	t    *testing.T
	s    *store.Store
	id   int64
	mods []store.Module
}

func (sp *space) pkg(id string) pkg {
	sp.t.Helper()
	db := sp.db()
	ids, err := db.RepoIDs([]string{id})
	if err != nil || ids[id] == 0 {
		sp.t.Fatalf("%s is not in deps.db: %v", id, err)
	}
	mods, err := db.Store().ModulesByRepo(ids[id])
	if err != nil {
		sp.t.Fatal(err)
	}
	return pkg{t: sp.t, s: db.Store(), id: ids[id], mods: mods}
}

// modules returns "path identity" of every module, sorted.
func (p pkg) modules() []string {
	var out []string
	for _, m := range p.mods {
		out = append(out, m.Path+" "+m.Identity)
	}
	sort.Strings(out)
	return out
}

func (p pkg) module(path string) store.Module {
	for _, m := range p.mods {
		if m.Path == path {
			return m
		}
	}
	p.t.Fatalf("no module %s in %v", path, p.modules())
	return store.Module{}
}

// keys returns the stable keys of the package's symbols, sorted.
func (p pkg) keys() []string {
	p.t.Helper()
	files, err := p.s.FilesByRepo(p.id)
	if err != nil {
		p.t.Fatal(err)
	}
	var out []string
	for _, f := range files {
		syms, err := p.s.SymbolsInFile(f.ID)
		if err != nil {
			p.t.Fatal(err)
		}
		for _, sy := range syms {
			out = append(out, sy.StableKey)
		}
	}
	sort.Strings(out)
	return out
}

func (p pkg) symbol(key string) store.Symbol {
	p.t.Helper()
	sy, err := p.s.SymbolByKey(key)
	if err != nil {
		p.t.Fatalf("%s: %v\nhave %s", key, err, strings.Join(p.keys(), "\n     "))
	}
	return sy
}

func TestNpmPackageWithExportsBecomesModulesPerSubpath(t *testing.T) {
	sp := newSpace(t, "deps-app")
	sp.sync(sp.opts)
	p := sp.pkg("npm/@acme/sdk@1.2.0")
	eq(t, "modules", p.modules(), []string{". @acme/sdk", "locales/en.js @acme/sdk/locales/en.js", "server @acme/sdk/server",
		"server/transport.js @acme/sdk/server/transport.js", "shared/protocol.js @acme/sdk/shared/protocol.js",
		"types.js @acme/sdk/types.js"})
	root := p.module(".")
	eq(t, "root doc is the README", strings.HasPrefix(root.DocText, "# Acme SDK\n\nClient and server building blocks"), true)
	eq(t, "root module", []string{root.DocSource, root.DocState, root.Kind}, []string{"README.md", "ok", "npm-package"})
	n := p.symbol("npm/@acme/sdk@1.2.0:ts:@acme/sdk/server:dist/server/index.d.ts/Server.connect")
	eq(t, "Server.connect", []string{n.Kind, n.Signature, n.DocText, n.DocState, n.Path}, []string{"method",
		"connect(transport: Transport): Promise<Result>", "Attaches the server to a transport.", "ok",
		"dist/server/index.d.ts"})
	eq(t, "module of Server.connect", n.ModuleID, p.module("server").ID)
	eq(t, "it has a container", n.ContainerID != 0, true)
	// dist/types.d.cts and locales/en.d.cts are twins of files read before.
	eq(t, "keys", p.keys(), []string{
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/locales/en.js:locales/en.d.ts/messages",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/server/transport.js:dist/server/transport.d.ts/Transport",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/server/transport.js:dist/server/transport.d.ts/Transport.send",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/server/transport.js:dist/server/transport.d.ts/Transport.start",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/server:dist/server/index.d.ts/Server",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/server:dist/server/index.d.ts/Server.connect",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/server:dist/server/index.d.ts/Server.constructor",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/server:dist/server/index.d.ts/Server.onClose",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/shared/protocol.js:dist/shared/protocol.d.ts/Protocol",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/shared/protocol.js:dist/shared/protocol.d.ts/Protocol.assertOpen",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/shared/protocol.js:dist/shared/protocol.d.ts/Protocol.close",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/shared/protocol.js:dist/shared/protocol.d.ts/Protocol.notification",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/shared/protocol.js:dist/shared/protocol.d.ts/Protocol.revision",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/types.js:dist/types.d.ts/Notification",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/types.js:dist/types.d.ts/Notification.method",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/types.js:dist/types.d.ts/Notification.params",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk/types.js:dist/types.d.ts/Result",
		"npm/@acme/sdk@1.2.0:ts:@acme/sdk:dist/index.d.ts/PROTOCOL_VERSION",
	})
}

// Every import specifier a package answers to names its module, so an
// import of the workspace can be matched to it.
func TestNpmImportSpecifiersNameModules(t *testing.T) {
	sp := newSpace(t, "deps-app")
	sp.sync(sp.opts)
	p := sp.pkg("npm/@acme/sdk@1.2.0")
	ids, err := p.s.RepoIdentities(p.id)
	eq(t, "error", err, nil)
	specs := map[string]string{}
	for _, id := range ids {
		specs[id.Identity] = id.Path
	}
	eq(t, "specifiers", specs, map[string]string{
		"@acme/sdk": ".", "@acme/sdk/index.js": ".", "@acme/sdk/index": ".",
		"@acme/sdk/server": "server", "@acme/sdk/server/index.js": "server", "@acme/sdk/server/index": "server",
		"@acme/sdk/server/transport.js": "server/transport.js", "@acme/sdk/server/transport": "server/transport.js",
		"@acme/sdk/shared/protocol.js": "shared/protocol.js", "@acme/sdk/shared/protocol": "shared/protocol.js",
		"@acme/sdk/types.js": "types.js", "@acme/sdk/types": "types.js", "@acme/sdk/types.cjs": "types.js",
		"@acme/sdk/locales/en.cjs": "locales/en.js", "@acme/sdk/locales/en.js": "locales/en.js",
	})
}

func TestNpmSymlinkedTypesFallbackAndTwoVersions(t *testing.T) {
	sp := newSpace(t, "deps-app")
	sp.ws.Deps.Include = []string{"plain-js"}
	sp.sync(sp.opts)
	linked := sp.pkg("npm/linked-lib@0.3.0")
	want := []string{"npm/linked-lib@0.3.0:ts:linked-lib:lib/index.d.ts/leftTrim", "npm/linked-lib@0.3.0:ts:linked-lib:lib/util.d.ts/pad"}
	if got := linked.keys(); !reflect.DeepEqual(got, want) {
		t.Errorf("linked-lib (export clause, followed import): %v", got)
	}
	plain := sp.pkg("npm/plain-js@2.0.1")
	want = []string{"npm/plain-js@2.0.1:ts:plain-js:extra.d.ts/PlainOptions", "npm/plain-js@2.0.1:ts:plain-js:extra.d.ts/PlainOptions.locale",
		"npm/plain-js@2.0.1:ts:plain-js:index.d.ts/shout"}
	if got := plain.keys(); !reflect.DeepEqual(got, want) {
		t.Errorf("plain-js (declarations of @types, reference directive): %v", got)
	}
	if doc := plain.module(".").DocText; !strings.HasPrefix(doc, "# plain-js") {
		t.Errorf("the README comes from the real package: %q", doc)
	}
	v1, v2 := sp.pkg("npm/dup@1.0.0"), sp.pkg("npm/dup@2.0.0")
	if len(v1.keys()) != 1 || len(v2.keys()) != 2 {
		t.Errorf("dup versions: %v %v", v1.keys(), v2.keys())
	}
	if _, ok := sp.uses()["package.json @types/plain-js"]; ok {
		t.Error("@types/plain-js only supplies plain-js and is no use of its own")
	}
	ids, err := deps.PackageIDs(sp.store, 0)
	if err != nil || !reflect.DeepEqual(ids, []string{"npm/@acme/sdk@1.2.0", "npm/@types/node@22.1.0", "npm/dup@1.0.0",
		"npm/dup@2.0.0", "npm/linked-lib@0.3.0", "npm/plain-js@2.0.1"}) {
		t.Errorf("package ids of the workspace: %v %v", ids, err)
	}
}

func TestGoModuleExportedAPI(t *testing.T) {
	sp := newSpace(t, "deps-go")
	sp.ws.Deps.Include = []string{"github.com/BurntSushi/toml"}
	sp.sync(sp.opts)
	kit := sp.pkg("go/github.com/acme/kit@v1.4.0")
	// internal, testdata, vendor, main, hidden and nested modules are left out.
	eq(t, "modules", kit.modules(), []string{". github.com/acme/kit", "transport github.com/acme/kit/transport"})
	root, tr := kit.module("."), kit.module("transport")
	eq(t, "root doc is the README", strings.HasPrefix(root.DocText, "# kit"), true)
	eq(t, "root doc source", root.DocSource, "README.md")
	eq(t, "transport module", []any{tr.DocText, tr.DocSource, tr.ParentID}, []any{
		"Package transport carries kit requests over HTTP.", "transport/http.go", root.ID})
	var plain, platforms []string
	for _, k := range kit.keys() {
		name := strings.TrimPrefix(k, "go/github.com/acme/kit@v1.4.0:go:github.com/acme/kit")
		if strings.HasPrefix(name, ":Platform#") {
			platforms = append(platforms, name)
			continue
		}
		plain = append(plain, name)
	}
	eq(t, "keys", plain, []string{"/transport:HTTP", "/transport:HTTP.BaseURL", "/transport:HTTP.RoundTrip", ":Base",
		":Base.Close", ":Client", ":Client.Base", ":Client.Do", ":Client.Name", ":DefaultTimeout", ":Doer", ":Doer.Do",
		":ErrClosed", ":New"})
	eq(t, "Platform symbols told apart by a path hash", len(platforms), 2)
	do := kit.symbol("go/github.com/acme/kit@v1.4.0:go:github.com/acme/kit:Client.Do")
	eq(t, "Client.Do", []string{do.DocText, do.Signature}, []string{"Do sends one request and waits for its answer.",
		"func (c *Client) Do() error"})
	toml := sp.pkg("go/github.com/BurntSushi/toml@v0.3.1")
	eq(t, "without a README the root doc is the package doc", toml.module(".").DocText, "Package toml decodes TOML documents.")
}

func TestLimitsTruncateAPackage(t *testing.T) {
	sp := newSpace(t, "deps-app", "deps-go")
	opts := sp.opts
	opts.Limits = deps.Limits{Files: 1, Bytes: 1 << 20}
	sp.sync(opts)
	infos, err := sp.db().Packages()
	if err != nil {
		t.Fatal(err)
	}
	truncated := map[string]bool{}
	for _, p := range infos {
		truncated[p.ID] = p.Truncated
		if p.IndexedAt == 0 || p.RepoID == 0 {
			t.Errorf("%+v", p)
		}
	}
	if !truncated["npm/@acme/sdk@1.2.0"] || !truncated["go/github.com/acme/kit@v1.4.0"] || truncated["npm/dup@2.0.0"] {
		t.Errorf("truncated flags: %v", truncated)
	}
	if got := len(sp.pkg("npm/@acme/sdk@1.2.0").keys()); got != 1 {
		t.Errorf("one file of the sdk is indexed, got %d symbols", got)
	}
	opts.Limits = deps.Limits{Files: 100, Bytes: 10}
	sp2 := newSpace(t, "deps-app")
	opts.DB = sp2.opts.DB
	sp2.sync(opts)
	if got := sp2.pkg("npm/dup@2.0.0").keys(); len(got) != 0 {
		t.Errorf("the byte limit keeps every file out: %v", got)
	}
}
