package query_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/store"
)

const (
	depsAppKy = "deps-app:ts:@fixture/deps-app:src/main.ts/"
	depsGoKy  = "deps-go:go:github.com/fixture/depsgo:"
)

// synced returns the dependency workspace after one query synced its
// dependencies and their edges.
func synced(t *testing.T, names ...string) *query.Ctx {
	t.Helper()
	c := depsWorkspace(t, names...)
	if _, err := query.Overview(c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCallersAndRefsOfADependencySymbol(t *testing.T) {
	c := synced(t)
	r, err := query.Callers(c, "Server.revision", query.GraphOpts{Depth: 2})
	out := text(t, r, err)
	wants(t, out, "== callers of Protocol.revision [dep @acme/sdk@1.2.0] (1)\nrevision  src/main.ts:11  return Server.revision();\n",
		"Server.revision is declared on Protocol", "--depth applies to workspace symbols")
	wants(t, keyed(t, r), "== callers of "+sdkProtocol+"Protocol.revision [dep @acme/sdk@1.2.0] (1)",
		depsAppKy+"revision  src/main.ts:11  export function revision(): string", "[import]")
	r, err = query.Callers(c, kitKey+"Base.Close", query.GraphOpts{Depth: 1, GroupByFile: true})
	wants(t, text(t, r, err), "[dep github.com/acme/kit@v1.4.0] in deps-go:main.go (1)",
		"\napp.run  main.go:16  return a.client.Close()\n")
	r, err = query.Refs(c, "Server", query.GraphOpts{Kind: "instantiate"})
	wants(t, text(t, r, err), "== references to Server [dep @acme/sdk@1.2.0] (2)\nsrc/main.ts\n",
		"  App.server  :16  private server: Server = new Server(\"app\");\n")
	wants(t, keyed(t, r), "== references to "+sdkServer+"Server [dep @acme/sdk@1.2.0] (2)", depsAppKy+"App.server  :16",
		"[instantiate] [import]")
	// The line that names the type and instantiates it is one entry.
	r, err = query.Refs(c, "Server", query.GraphOpts{})
	if n := len(r.Sections[0].Entries); err != nil || n != 3 {
		t.Errorf("every reference kind: %d %v", n, err)
	}
	if _, err := query.Callers(c, "Nowhere.at", query.GraphOpts{Depth: 1}); !errors.Is(err, query.ErrNotFound) {
		t.Errorf("unknown symbol: %v", err)
	}
}

func TestCalleesListDependencyTargets(t *testing.T) {
	c := synced(t)
	r, err := query.Callees(c, "app.run", query.GraphOpts{Depth: 1})
	out := text(t, r, err)
	wants(t, out, "== callees of app.run (2)\nmain.go\n  Client.Do  :13  if err := a.client.Do(); err != nil {  [dep]\n",
		"  Base.Close  :16  return a.client.Close()  [dep]\n")
	wants(t, keyed(t, r), "== callees of "+depsGoKy+"app.run (2)",
		kitKey+"Client.Do  :13  func (c *Client) Do() error  Do sends one request and waits for its answer.  [import] [dep]",
		kitKey+"Base.Close  :16  func (Base) Close() error  Close releases the client.  [import] [dep]")
	e := r.Sections[0].Entries[0]
	if e.Extra["dep"] != true || e.Extra["kind"] != "method" {
		t.Errorf("extra: %+v", e.Extra)
	}
	// Without deps.db the key still names the target.
	bare := &query.Ctx{Store: c.Store, Workspace: c.Workspace}
	r, err = query.Callees(bare, "app.run", query.GraphOpts{Depth: 1})
	wants(t, text(t, r, err), "  Client.Do  :13  if err := a.client.Do(); err != nil {  [dep]")
	wants(t, keyed(t, r), kitKey+"Client.Do  :13  [import] [dep]")
}

func TestOutlineOfPackagesModulesFilesAndTypes(t *testing.T) {
	c := synced(t)
	c.Cwd = c.Workspace.Repos[0].Path
	c.Repo = c.Workspace.Repos[0]
	r, err := query.Outline(c, "@acme/sdk", query.OutlineOpts{})
	wants(t, text(t, r, err), "== npm/@acme/sdk@1.2.0 modules (6)",
		"@acme/sdk  .  ts npm-package, 1 symbol  Client and server building blocks for the Acme protocol.",
		"@acme/sdk/server  server  ts npm-export, 4 symbols")
	r, err = query.Outline(c, "dup", query.OutlineOpts{})
	wants(t, text(t, r, err), "== npm/dup@1.0.0 modules (1)", "== npm/dup@2.0.0 modules (1)")
	r, err = query.Outline(c, "npm/dup@2.0.0", query.OutlineOpts{})
	if out := text(t, r, err); strings.Contains(out, "dup@1.0.0") || !strings.Contains(out, "dup  .  ts npm-package, 2 symbols") {
		t.Errorf("a package id names one version:\n%s", out)
	}
	r, err = query.Outline(c, "@acme/sdk/server", query.OutlineOpts{})
	wants(t, text(t, r, err), "== @acme/sdk/server [dep @acme/sdk@1.2.0] (1)",
		"\nServer  dist/server/index.d.ts:8-14  export declare class Server extends Protocol  [+3 members]\n")
	wants(t, keyed(t, r), sdkServer+"Server  dist/server/index.d.ts:8-14  export declare class Server extends Protocol  "+
		"An Acme server on top of a pluggable transport.  [+3 members]")
	r, err = query.Outline(c, "github.com/acme/kit/transport", query.OutlineOpts{})
	wants(t, text(t, r, err), "== github.com/acme/kit/transport [dep github.com/acme/kit@v1.4.0] (1)", "[+2 members]")
	r, err = query.Outline(c, "node_modules/@acme/sdk/dist/types.d.ts", query.OutlineOpts{})
	wants(t, text(t, r, err), "== dist/types.d.ts [dep @acme/sdk@1.2.0], 9 lines (2)", "export interface Notification")
	r, err = query.Outline(c, filepath.Join(c.Cwd, "node_modules/dup/dup.d.ts"), query.OutlineOpts{})
	wants(t, text(t, r, err), "== dup.d.ts [dep dup@2.0.0], 4 lines (2)")
}

func TestOutlineOfADependencyTypeListsInheritedMembers(t *testing.T) {
	c := synced(t)
	c.Repo = c.Workspace.Repos[0]
	// No workspace symbol is named Server: the name needs no --deps.
	r, err := query.Outline(c, "Server", query.OutlineOpts{})
	wants(t, text(t, r, err), "== members of Server (4)")
	r, err = query.Outline(withDeps(c), "Server", query.OutlineOpts{})
	out := text(t, r, err)
	wants(t, out, "== members of Server (4)", "  Server.connect  dist/server/index.d.ts:12  connect(",
		"== inherited members (3)\ndist/shared/protocol.d.ts\n"+
			"  Protocol.notification  :9  notification(notification: Notification): Promise<void>\n")
	wants(t, keyed(t, r), "== members of "+sdkServer+"Server (4)", sdkProtocol+"Protocol.notification  :9  "+
		"notification(notification: Notification): Promise<void>  Sends a notification to the other side.  [declared on Protocol]")
	if strings.Contains(out, "assertOpen") {
		t.Errorf("a protected member is not inherited API:\n%s", out)
	}
	r, err = query.Outline(c, kitKey+"Client", query.OutlineOpts{})
	wants(t, text(t, r, err), "== members of Client (4)", "\nBase.Close  kit.go:23  func (Base) Close() error\n")
	wants(t, keyed(t, r), "== members of "+kitKey+"Client (4)", kitKey+"Base.Close  kit.go:23  func (Base) Close() error",
		"[declared on Base]")
	for _, target := range []string{"@acme/nope", "node_modules/@acme/sdk/missing.d.ts", "kit.New"} {
		if _, err := query.Outline(c, target, query.OutlineOpts{}); !errors.Is(err, query.ErrNotFound) {
			t.Errorf("%s: want not found, got %v", target, err)
		}
	}
}

func TestExplainPackagesModulesAndDependencySymbols(t *testing.T) {
	c := synced(t)
	r, err := query.Explain(c, "@acme/sdk")
	wants(t, text(t, r, err), "== explain @acme/sdk [dep @acme/sdk@1.2.0] (1)",
		"npm/@acme/sdk@1.2.0  package  Client and server building blocks for the Acme protocol.  [dep @acme/sdk@1.2.0]",
		"== used by the workspace (3)", "\nServer  dist/server/index.d.ts:8", "[fan-in:4]")
	r, err = query.Explain(c, "github.com/acme/kit/transport")
	wants(t, text(t, r, err), "  github.com/acme/kit/transport  transport  module go go-package  "+
		"Package transport carries kit requests over HTTP.", "== main symbols (1)\nHTTP  ")
	r, err = query.Explain(c, "dup@1.0.0")
	wants(t, text(t, r, err), "== explain dup [dep dup@1.0.0] (1)", "== used by the workspace (1)")
	// No workspace symbol has the name: it needs no --deps.
	r, err = query.Explain(c, "Server.notification")
	wants(t, text(t, r, err), "== explain Protocol.notification (4)")
	r, err = query.Explain(withDeps(c), "Server.notification")
	wants(t, text(t, r, err), "== explain Protocol.notification (4)",
		"  @acme/sdk/shared/protocol.js  shared/protocol.js  module ts npm-export",
		"    Protocol  dist/shared/protocol.d.ts:5-15  export declare abstract class Protocol  "+
			"The message layer every Acme endpoint builds on.",
		"== top callers (1)", "\nApp.announce  src/main.ts:19")
	wants(t, keyed(t, r), "== explain "+sdkProtocol+"Protocol.notification (4)", "[declared on Protocol]")
	r, err = query.Explain(c, "PROTOCOL_VERSION")
	wants(t, text(t, r, err), "== explain PROTOCOL_VERSION (2)", "== top callers (0)")
	wants(t, keyed(t, r), "== explain npm/@acme/sdk@1.2.0:ts:@acme/sdk:dist/index.d.ts/PROTOCOL_VERSION (2)")
}

func TestSearchFusesWorkspaceAndDependencyLists(t *testing.T) {
	c := synced(t)
	c.Embedder = embed.NewFake(64)
	r := search(t, c, "notification", query.SearchOpts{})
	for _, k := range keys(r) {
		if strings.HasPrefix(k, "npm/") || strings.HasPrefix(k, "go/") {
			t.Fatalf("without --deps no dependency is searched: %v", keys(r))
		}
	}
	r = search(t, withDeps(c), "notification", query.SearchOpts{})
	got := keys(r)
	if len(got) < 3 || got[0] != sdkProtocol+"Protocol.notification" {
		t.Fatalf("%v\n%s", got, render(t, r))
	}
	wants(t, render(t, r), "Sends a notification to the other side.  [dep @acme/sdk@1.2.0]", "\nApp.announce  src/main.ts:19-21")
	db, err := deps.Open(c.DepsDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if n, total, err := db.Store().SymbolVecCounts(store.VecScope{}, 64); err != nil || n == 0 || n != total {
		t.Errorf("the documented dependency symbols are embedded: %d of %d %v", n, total, err)
	}
}

func TestSearchFiltersApplyToDependencies(t *testing.T) {
	c := withDeps(synced(t))
	r := search(t, c, "building blocks acme protocol", query.SearchOpts{Kind: "module"})
	wants(t, render(t, r), "@acme/sdk  .  module ts npm-package", "[dep @acme/sdk@1.2.0]")
	r = search(t, c, "request", query.SearchOpts{Lang: "go", Kind: "method"})
	for _, e := range r.Sections[0].Entries {
		if e.Extra["kind"] != "method" || !strings.Contains(e.Key, ":go:") {
			t.Errorf("filters apply to dependencies: %+v", e)
		}
	}
	if len(r.Sections[0].Entries) == 0 {
		t.Error("kit methods match request")
	}
}

func TestOverviewListsDependenciesAndCountsNoneOfThem(t *testing.T) {
	plain := workspaceOf(t, "deps-go")
	before, err := query.Overview(plain)
	if err != nil {
		t.Fatal(err)
	}
	c := synced(t, "deps-go")
	r, err := query.Overview(c)
	out := text(t, r, err)
	wants(t, out, "== dependencies (3)", "go/github.com/acme/kit@v1.4.0  deps-go indexed symbols:16",
		"go/github.com/acme/absent  deps-go missing", "go/github.com/fixture/lib  deps-go missing")
	if strings.Contains(out, "BurntSushi") {
		t.Errorf("a module no file imports is no dependency of the overview:\n%s", out)
	}
	for _, title := range []string{"repos", "doc coverage"} {
		a, b := sectionText(t, before, title), sectionText(t, r, title)
		if a != b {
			t.Errorf("%s changed with dependencies:\n%s\n%s", title, a, b)
		}
	}
	gaps, err := query.Gaps(c, "", query.GapsOpts{})
	if out := text(t, gaps, err); strings.Contains(out, "acme/kit") {
		t.Errorf("gaps list no dependency:\n%s", out)
	}
}

// sectionText renders the entries of the section with a title.
func sectionText(t *testing.T, r *output.Response, title string) string {
	t.Helper()
	for _, s := range r.Sections {
		if s.Title == title {
			var b strings.Builder
			for _, e := range s.Entries {
				b.WriteString(e.Key + " " + strings.SplitN(e.Text, " ", 2)[1] + "\n")
			}
			return b.String()
		}
	}
	t.Fatalf("no section %s", title)
	return ""
}

func TestAnswersUsingDependenciesNoteWhatIsPending(t *testing.T) {
	c := synced(t, "deps-go")
	// Without a deps.db path nothing is synced, so the count stays.
	late := &query.Ctx{Store: c.Store, Workspace: c.Workspace, DepsPending: 2}
	r, err := query.Find(late, "main", query.FindOpts{})
	if out := text(t, r, err); strings.Contains(out, "pending") {
		t.Errorf("an answer without dependencies says nothing:\n%s", out)
	}
	r, err = query.Find(withDeps(late), "main", query.FindOpts{})
	wants(t, text(t, r, err), "deps: 2 pending")
	if err := late.Close(); err != nil {
		t.Errorf("closing a context without a view: %v", err)
	}
}
