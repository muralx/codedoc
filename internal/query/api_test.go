package query_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/testutil"
)

// api renders the api answer for a package and a name, with the
// directories of the fixture (repo copies, module cache) replaced by
// their names so that the text is the same on every run.
func api(t *testing.T, c *query.Ctx, pkg, name string, o query.APIOpts) string {
	t.Helper()
	r, err := query.API(c, pkg, name, o)
	if err != nil {
		t.Fatalf("api %s %s: %v", pkg, name, err)
	}
	out := budgeted(t, r, c.Budget)
	for _, repo := range c.Workspace.Repos {
		out = strings.ReplaceAll(out, repo.Path, "<"+repo.Name+">")
	}
	return strings.ReplaceAll(out, testutil.ModCacheDir(), "<modcache>")
}

func TestAPISnapshots(t *testing.T) {
	c := depsWorkspace(t)
	// A package: its line, the names of its main module and of the
	// modules the workspace uses, the list of its modules.
	snapshot(t, "api_sdk", api(t, c, "@acme/sdk", "", query.APIOpts{}))
	// A type: its doc, its members and the inherited ones, each under the
	// file that declares it.
	snapshot(t, "api_sdk_server", api(t, c, "@acme/sdk", "Server", query.APIOpts{Doc: true}))
	// A Go module, by its module path and version.
	snapshot(t, "api_kit", api(t, c, "github.com/acme/kit@v1.4.0", "", query.APIOpts{}))
	snapshot(t, "api_kit_client", api(t, c, "github.com/acme/kit", "Client", query.APIOpts{}))
}

func TestAPIOfANameOrAModule(t *testing.T) {
	c := depsWorkspace(t)
	// A member, also one the type inherits, is answered alone and says
	// where it is declared.
	got := api(t, c, "@acme/sdk", "Server.notification", query.APIOpts{})
	wants(t, got, "Protocol.notification  <deps-app>/node_modules/@acme/sdk/dist/shared/protocol.d.ts:9  "+
		"notification(notification: Notification): Promise<void>  Sends a notification to the other side.  "+
		"[dep @acme/sdk@1.2.0] [declared on Protocol] [from @acme/sdk/shared/protocol.js]\n")
	if strings.Contains(got, "== members") {
		t.Fatalf("a member has no members:\n%s", got)
	}
	// A function is its signature and doc.
	wants(t, api(t, c, "github.com/acme/kit", "New", query.APIOpts{}), "New  <modcache>/github.com/acme/kit@v1.4.0/kit.go:",
		"func New(name string) *Client", "[dep github.com/acme/kit@v1.4.0] [from github.com/acme/kit]\n")
	// An import specifier names one module: its names alone, and the
	// offset pages them.
	if got := api(t, c, "@acme/sdk/server", "", query.APIOpts{}); !strings.HasSuffix(got, "== @acme/sdk/server (1)\nServer  class\n") ||
		strings.Contains(got, "== modules of") {
		t.Fatalf("got\n%s", got)
	}
	// Without the doc flag the names carry no doc sentence; --detail
	// prints it as --doc does.
	c.Detail = true
	wants(t, api(t, c, "@acme/sdk/server", "", query.APIOpts{}), "Server  class  An Acme server on top of a pluggable transport.\n")
	c.Detail = false
	// An offset continues the names of the main module alone.
	c.Offset = 1
	if got := api(t, c, "@acme/sdk", "", query.APIOpts{}); !strings.Contains(got, "== @acme/sdk (1)\noffset 1 is past the end (total 1)\n") ||
		strings.Contains(got, "== @acme/sdk/server") {
		t.Fatalf("got\n%s", got)
	}
	c.Offset = 0
	// Two versions of one name: the first is answered, the other noted.
	wants(t, api(t, c, "dup", "", query.APIOpts{}), "dup@1.0.0  <deps-app>/packages/web/node_modules/dup\n== dup (1)\n"+
		"dup  function<T>(value: T): [T, T]\n", "the workspace also uses dup@2.0.0: codedoc api dup@2.0.0\n")
	wants(t, api(t, c, "dup@2.0.0", "", query.APIOpts{}), "dup@2.0.0  <deps-app>/node_modules/dup\n")
}

func TestAPIUnderABudgetSharesItAmongTheModules(t *testing.T) {
	c := depsWorkspace(t)
	c.Budget, c.Scope = 420, " --workspace fixture"
	got := api(t, c, "@acme/sdk", "", query.APIOpts{})
	if len(got) > 420 {
		t.Fatalf("%d bytes over the budget:\n%s", len(got), got)
	}
	// The list of modules is cut; codedoc outline continues it.
	wants(t, got, "== @acme/sdk (1)\nPROTOCOL_VERSION  const\n", "== @acme/sdk/server (1)\nServer  class\n",
		"== modules of @acme/sdk (6)\n", " more: codedoc outline @acme/sdk@1.2.0 --workspace fixture --offset ")
}

func TestAPINearestNamesAndNotFound(t *testing.T) {
	c := depsWorkspace(t)
	// A name the package does not export lists the nearest ones with the
	// module to import each from.
	if got := api(t, c, "@acme/sdk", "Servr", query.APIOpts{}); !strings.HasSuffix(got, "== nearest names in @acme/sdk (1)\n"+
		"Server  @acme/sdk/server  class\n@acme/sdk exports no Servr; the nearest names are listed\n") {
		t.Fatalf("got\n%s", got)
	}
	wants(t, api(t, c, "@acme/sdk", "protocol", query.APIOpts{}), "Protocol  @acme/sdk/shared/protocol.js  class\n",
		"PROTOCOL_VERSION  @acme/sdk  const\n")
	r, err := query.API(c, "github.com/acme/kit", "Zzzzzz", query.APIOpts{})
	if !errors.Is(err, query.ErrNotFound) || r != nil || err.Error() != "Zzzzzz in github.com/acme/kit: not found: "+
		"it exports no name like that; codedoc api github.com/acme/kit lists what it exports" {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestAPIOfAPackageThatIsNotIndexedSaysWhyInOneLine(t *testing.T) {
	c := depsWorkspace(t)
	for pkg, want := range map[string]string{
		"lint-tool": "lint-tool@9.4.0 is declared in package.json but no source file imports it, so it is not indexed; " +
			"name it under deps.include in the codedoc config and run codedoc deps index",
		"left-pad":               "left-pad is declared in package.json but not installed; install the dependencies and run codedoc deps index",
		"github.com/acme/absent": "github.com/acme/absent is declared in go.mod but not installed; install the dependencies and run codedoc deps index",
		"@fixture/web":           "@fixture/web is a package of the workspace, indexed as a repo: codedoc outline and codedoc find answer for it",
	} {
		r, err := query.API(c, pkg, "", query.APIOpts{})
		if err != nil || len(r.Sections) != 0 || r.Note != want {
			t.Errorf("%s: %v %+v", pkg, err, r)
		}
	}
	// A name no manifest declares is not found; the nearest names help.
	r, err := query.API(c, "lint-tol", "", query.APIOpts{})
	if !errors.Is(err, query.ErrNotFound) || r != nil ||
		err.Error() != "package lint-tol: not found: no manifest of the workspace declares it; nearest: lint-tool" {
		t.Fatalf("%v %+v", err, r)
	}
	r, err = query.API(c, "zzzzzzzz", "", query.APIOpts{})
	if !errors.Is(err, query.ErrNotFound) || r != nil ||
		err.Error() != "package zzzzzzzz: not found: no manifest of the workspace declares it; codedoc deps lists the packages" {
		t.Fatalf("%v %+v", err, r)
	}
	// A workspace without any dependency answers the same way.
	plain := indexedWorkspace(t)
	if _, err := query.API(plain, "zod", "", query.APIOpts{}); !errors.Is(err, query.ErrNotFound) {
		t.Fatalf("no dependencies: %v", err)
	}
}
