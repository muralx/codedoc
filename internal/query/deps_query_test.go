package query_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/testutil"
)

// Stable key prefixes of the fixture packages.
const (
	sdkServer   = "npm/@acme/sdk@1.2.0:ts:@acme/sdk/server:dist/server/index.d.ts/"
	sdkProtocol = "npm/@acme/sdk@1.2.0:ts:@acme/sdk/shared/protocol.js:dist/shared/protocol.d.ts/"
	kitKey      = "go/github.com/acme/kit@v1.4.0:go:github.com/acme/kit:"
)

// depsWorkspace indexes the fixture repos that depend on third-party
// packages, with a deps.db of its own and the fixture module cache.
func depsWorkspace(t *testing.T, names ...string) *query.Ctx {
	t.Helper()
	t.Setenv(deps.ModCacheEnv, testutil.ModCacheDir())
	if len(names) == 0 {
		names = []string{"deps-app", "deps-go"}
	}
	c := workspaceOf(t, names...)
	c.DepsDB = filepath.Join(t.TempDir(), "deps.db")
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// withDeps returns the context with --deps set.
func withDeps(c *query.Ctx) *query.Ctx {
	c.Deps = true
	return c
}

// text renders the answer of a query that must succeed.
func text(t *testing.T, r *output.Response, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return render(t, r)
}

// wants fails unless got holds every one of the lines.
func wants(t *testing.T, got string, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if !strings.Contains(got, l) {
			t.Errorf("missing %q in\n%s", l, got)
		}
	}
}

// A name the workspace has is answered from the workspace; the
// dependencies are searched second, for a name it lacks, and --deps
// lists both (efficiency spec section 5).
func TestFindIncludesDependenciesWithTheFlagOrWhenTheWorkspaceHasNone(t *testing.T) {
	c := depsWorkspace(t)
	r, err := query.Find(c, "revision", query.FindOpts{})
	if out := text(t, r, err); !strings.Contains(out, "== find revision (1)") || strings.Contains(out, "[dep ") {
		t.Fatalf("a workspace name lists no dependency without --deps:\n%s", out)
	}
	r, err = query.Find(c, "Server", query.FindOpts{})
	wants(t, text(t, r, err), "== find Server (1)", "[dep @acme/sdk@1.2.0]")
	r, err = query.Find(withDeps(c), "revision", query.FindOpts{})
	wants(t, text(t, r, err), "== find revision (2)", "\nrevision  src/main.ts:10-12  ", "Protocol.revision  ")
	r, err = query.Find(withDeps(c), "Server", query.FindOpts{})
	// A dependency is listed by name and location under its package
	// version; its doc sentence comes with --detail or --doc.
	wants(t, text(t, r, err), "== find Server (1)\nServer  dist/server/index.d.ts:8-14  export declare class Server extends Protocol"+
		"  [dep @acme/sdk@1.2.0]\n")
	wants(t, keyed(t, r), sdkServer+"Server  dist/server/index.d.ts:8-14  export declare class Server extends Protocol"+
		"  An Acme server on top of a pluggable transport.  [dep @acme/sdk@1.2.0]")
	r, err = query.Find(withDeps(c), "Server", query.FindOpts{Doc: true})
	wants(t, text(t, r, err), "extends Protocol  An Acme server on top of a pluggable transport.  [dep @acme/sdk@1.2.0]")
	c.Detail = true // --detail names the kind
	r, err = query.Find(withDeps(c), "Server", query.FindOpts{})
	if err != nil {
		t.Fatal(err)
	}
	wants(t, keyed(t, r), "An Acme server on top of a pluggable transport.  [class] [dep @acme/sdk@1.2.0]")
	c.Detail = false
	r, err = query.Find(c, "Server.notification", query.FindOpts{})
	wants(t, text(t, r, err), "\nProtocol.notification  dist/shared/protocol.d.ts:9  "+
		"notification(notification: Notification): Promise<void>  [dep @acme/sdk@1.2.0]\n")
	wants(t, keyed(t, r), sdkProtocol+"Protocol.notification  dist/shared/protocol.d.ts:9  "+
		"notification(notification: Notification): Promise<void>  Sends a notification to the other side.  "+
		"[dep @acme/sdk@1.2.0] [declared on Protocol]")
	r, err = query.Find(c, "Client.*", query.FindOpts{Lang: "go"})
	wants(t, text(t, r, err), "== find Client.* (3)\nkit.go\n  Client.Name  :14  Name string  [dep github.com/acme/kit@v1.4.0]\n",
		"  Client.Do  :31  func (c *Client) Do() error\n")
	r, err = query.Find(c, "kit.New", query.FindOpts{Kind: "function"})
	wants(t, text(t, r, err), "\nNew  kit.go:28  func New(name string) *Client  [dep github.com/acme/kit@v1.4.0]\n")
	wants(t, keyed(t, r), kitKey+"New  kit.go:28  func New(name string) *Client  New returns a client with a name.  "+
		"[dep github.com/acme/kit@v1.4.0]")
	// Nothing named so in that language: the search answers, with nothing.
	r, err = query.Find(c, "Server", query.FindOpts{Lang: "go"})
	wants(t, text(t, r, err), "== no symbol named so; search results: (0)")
}

func TestSymbolOfADependencyReadsItsDeclarationFromDisk(t *testing.T) {
	c := withDeps(depsWorkspace(t))
	r, err := query.Symbol(c, "Server.notification", query.SymbolOpts{})
	out := text(t, r, err)
	root := filepath.Join(c.Workspace.Repos[0].Path, "node_modules/@acme/sdk")
	// The answer is the lines of the file from the doc comment on, under
	// one line naming the symbol, where it is and its package.
	wants(t, out, "Protocol.notification  "+root+"/dist/shared/protocol.d.ts:9  [dep @acme/sdk@1.2.0]\n6\t    /**\n"+
		"7\t     * Sends a notification to the other side.\n8\t     */\n"+
		"9\t    notification(notification: Notification): Promise<void>\n")
	wants(t, keyed(t, r), sdkProtocol+"Protocol.notification  "+root+"/dist/shared/protocol.d.ts:9  ",
		"[method] [dep @acme/sdk@1.2.0] [container:Protocol] [module:@acme/sdk/shared/protocol.js] [declared on Protocol] [fan-in:1]")
	e := r.Sections[0].Entries[0]
	if e.Extra["declared_on"] != "Protocol" || e.Extra["inherited_by"] != "Server" || e.Extra["dep"] != "npm/@acme/sdk@1.2.0" {
		t.Errorf("extra: %+v", e.Extra)
	}
	r, err = query.Symbol(c, "kit.Client.Do", query.SymbolOpts{Context: 1})
	wants(t, text(t, r, err), "30\t// Do sends one request and waits for its answer.",
		"31\tfunc (c *Client) Do() error { return c.dial() }")
	r, err = query.Symbol(c, "Protocol.close", query.SymbolOpts{DocOnly: true})
	wants(t, text(t, r, err), "Protocol.close  dist/shared/protocol.d.ts:11  close(): Promise<void>  "+
		"Closes the connection.  [dep @acme/sdk@1.2.0]")
}

func TestSymbolSaysWhenTheDependencyDirectoryIsGone(t *testing.T) {
	c := withDeps(depsWorkspace(t, "deps-app"))
	if _, err := query.Find(c, "Server", query.FindOpts{}); err != nil {
		t.Fatal(err)
	}
	sdk := filepath.Join(c.Workspace.Repos[0].Path, "node_modules/@acme/sdk")
	if err := os.WriteFile(filepath.Join(sdk, "dist/shared/protocol.d.ts"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := query.Symbol(c, "Protocol.close", query.SymbolOpts{})
	if out := text(t, r, err); !strings.Contains(out, "body unavailable: "+sdk+"/dist/shared/protocol.d.ts differs from the "+
		"npm/@acme/sdk@1.2.0 that was indexed") || r.Sections[0].Entries[0].Body != "" {
		t.Errorf("a changed file:\n%s", out)
	}
	if err := os.RemoveAll(sdk); err != nil {
		t.Fatal(err)
	}
	r, err = query.Symbol(c, sdkProtocol+"Protocol.close", query.SymbolOpts{})
	out := text(t, r, err)
	wants(t, out, "body unavailable: npm/@acme/sdk@1.2.0 is no longer installed ("+sdk+"); install the dependencies again",
		"Protocol.close  dist/shared/protocol.d.ts:11  close(): Promise<void>  Closes the connection.")
}

func TestDependencyAddressesNeedNoFlag(t *testing.T) {
	c := depsWorkspace(t)
	r, err := query.Symbol(c, kitKey+"Client.Do", query.SymbolOpts{})
	wants(t, text(t, r, err), "Client.Do  ", "[dep github.com/acme/kit@v1.4.0]")
	c.Cwd = c.Workspace.Repos[0].Path
	for _, addr := range []string{"node_modules/@acme/sdk/dist/server/index.d.ts#Server.connect",
		"deps-app:node_modules/@acme/sdk/dist/server/index.d.ts#Server.connect",
		filepath.Join(c.Cwd, "node_modules/linked-lib/lib/util.d.ts") + "#pad"} {
		r, err = query.Symbol(c, addr, query.SymbolOpts{})
		wants(t, text(t, r, err), "[dep ")
	}
	// A name no workspace symbol has is a dependency's without --deps,
	// and so is the name of a key or path that names nothing.
	for _, addr := range []string{"Server.connect", "npm/@acme/sdk@9.9.9:ts:@acme/sdk:dist/index.d.ts/PROTOCOL_VERSION",
		"src/nope.ts#Server"} {
		r, err = query.Symbol(c, addr, query.SymbolOpts{})
		wants(t, text(t, r, err), "[dep @acme/sdk@1.2.0]")
	}
	r, err = query.Symbol(c, "node_modules/@acme/sdk/dist/server/index.d.ts#Nope", query.SymbolOpts{})
	if !errors.Is(err, query.ErrNearest) {
		t.Fatalf("a name the dependency file lacks: %v", err)
	}
	wants(t, render(t, r), "== no exact match; nearest: (1)\nServer  dist/server/index.d.ts:8-14  ")
	c.Strict = true
	for _, addr := range []string{"node_modules/@acme/sdk/dist/server/index.d.ts#Nope",
		"npm/@acme/sdk@9.9.9:ts:@acme/sdk:dist/index.d.ts/PROTOCOL_VERSION", "src/nope.ts#Server"} {
		if _, err := query.Symbol(c, addr, query.SymbolOpts{}); !errors.Is(err, query.ErrNotFound) || errors.Is(err, query.ErrNearest) {
			t.Errorf("%s: want not found, got %v", addr, err)
		}
	}
}

func TestAmbiguousAddressListsWorkspaceAndDependencyCandidates(t *testing.T) {
	c := withDeps(depsWorkspace(t))
	// The best candidate is answered and the note names the others: the
	// first package version, and a workspace symbol before a dependency's.
	r, err := query.Symbol(c, "dup", query.SymbolOpts{NoBody: true})
	wants(t, text(t, r, err), "dup  ", "dup.d.ts:2  ", "[dep dup@1.0.0]", "also: dup (dup.d.ts:2 dep dup@2.0.0)")
	r, err = query.Symbol(c, "revision", query.SymbolOpts{NoBody: true})
	wants(t, text(t, r, err), "revision  src/main.ts:10-12  ", "also: Protocol.revision (dist/shared/protocol.d.ts:13 dep @acme/sdk@1.2.0)")
	c.Strict = true
	r, err = query.Symbol(c, "dup", query.SymbolOpts{})
	if !errors.Is(err, query.ErrAmbiguous) {
		t.Fatalf("two versions of dup: %v", err)
	}
	wants(t, render(t, r), "== candidates (2)", "\ndup  dup.d.ts:2  "+
		"export declare function dup<T>(value: T): [T, T];  Duplicates a value (version 1 API).  [dep dup@1.0.0]",
		"[dep dup@2.0.0]")
	wants(t, keyed(t, r), "npm/dup@1.0.0:ts:dup:dup.d.ts/dup  dup.d.ts:2  ", "npm/dup@2.0.0:ts:dup:dup.d.ts/dup  dup.d.ts:2  ")
	goOnly := &query.Ctx{Store: c.Store, Workspace: c.Workspace, DepsDB: c.DepsDB, Deps: true, Strict: true,
		Repo: c.Workspace.Repos[1], Filter: true}
	defer func() { _ = goOnly.Close() }()
	if _, err := query.Symbol(goOnly, "dup", query.SymbolOpts{}); !errors.Is(err, query.ErrNotFound) {
		t.Errorf("--repo narrows to the packages of that repo: %v", err)
	}
	r, err = query.Symbol(goOnly, "kit.New", query.SymbolOpts{})
	wants(t, text(t, r, err), "New  ")
	wants(t, keyed(t, r), kitKey+"New  ")
	r, err = query.Symbol(c, "revision", query.SymbolOpts{})
	if !errors.Is(err, query.ErrAmbiguous) {
		t.Fatalf("a workspace and a dependency symbol: %v", err)
	}
	wants(t, render(t, r), "\nrevision  src/main.ts:10-12  ", "\nProtocol.revision  dist/shared/protocol.d.ts:13  ")
	wants(t, keyed(t, r), "deps-app:ts:@fixture/deps-app:src/main.ts/revision", sdkProtocol+"Protocol.revision")
}

func TestCommandsOnWorkspaceSymbolsRefuseADependency(t *testing.T) {
	c := depsWorkspace(t)
	for name, run := range map[string]func(string) error{
		"callees": func(a string) error { _, err := query.Callees(c, a, query.GraphOpts{Depth: 1}); return err },
		"impls":   func(a string) error { _, err := query.Impls(c, a, query.GraphOpts{}); return err },
		"doc set": func(a string) error { _, err := query.DocSet(c, a, "A doc."); return err },
	} {
		if err := run(kitKey + "Client.Do"); !errors.Is(err, query.ErrDependency) {
			t.Errorf("%s of a dependency key: %v", name, err)
		}
		if err := run("Client.Do"); !errors.Is(err, query.ErrNotFound) {
			t.Errorf("%s of a dependency name: %v", name, err)
		}
	}
}

// A member asked through a type that inherits it says where it is
// declared, in the note of every answer about it, not only as a tag of
// --detail.
func TestSymbolOfAnInheritedMemberSaysWhereItIsDeclared(t *testing.T) {
	c := depsWorkspace(t)
	const note = "Server.notification is declared on Protocol"
	r, err := query.Symbol(c, "Server.notification", query.SymbolOpts{})
	if got := text(t, r, err); !strings.HasSuffix(got, "9\t    notification(notification: Notification): Promise<void>\n"+note+"\n") {
		t.Fatalf("got\n%s", got)
	}
	r, err = query.Symbol(c, "Server.notification", query.SymbolOpts{NoBody: true})
	wants(t, text(t, r, err), "\n"+note+"\n")
	r, err = query.Symbols(c, []string{"Server.notification", "Server.revision"}, query.SymbolOpts{})
	wants(t, text(t, r, err), note+". Server.revision is declared on Protocol\n")
	r, err = query.Refs(c, "Server.notification", query.GraphOpts{})
	wants(t, text(t, r, err), "\n"+note+"\n")
	// The member asked through the type that declares it has no note.
	r, err = query.Symbol(c, "Protocol.notification", query.SymbolOpts{})
	if got := text(t, r, err); strings.Contains(got, "declared on") {
		t.Fatalf("got\n%s", got)
	}
}
