package query_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/query"
)

func TestHowSnapshots(t *testing.T) {
	c := indexedWorkspace(t)
	// A symbol: its doc stack, its top callers, the tree below it.
	r, err := query.How(c, "Service.Lookup")
	snapshot(t, "how_service_lookup", run(t, r, err))
	// A file that holds an entry function: the file, then the tree below
	// main.
	r, err = query.How(c, "go-app:main.go")
	snapshot(t, "how_main_file", run(t, r, err))
}

func TestHowAnswersAsExplainWhereFlowHasNothingToSay(t *testing.T) {
	c := workspaceOf(t, "go-lib", "go-app", "doc-repo")
	for _, target := range []string{
		"go-lib:store",                 // a directory
		"github.com/fixture/lib/store", // a module
		"doc-repo:docs/design.md",      // a document
		"go-lib:store/store.go",        // a file whose functions all have callers or call nothing
		"go-lib:store/store_test.go",   // a test file: its tests are no flow of the program
	} {
		r, err := query.How(c, target)
		got := run(t, r, err)
		r, err = query.Explain(c, target)
		if want := run(t, r, err); got != want || strings.Contains(got, "== flow") {
			t.Errorf("how %s:\n%s\nexplain:\n%s", target, got, want)
		}
	}
	// A symbol that calls nothing has no tree either.
	r, err := query.How(c, "MemStore.Put")
	if got := run(t, r, err); strings.Contains(got, "== flow") || strings.Contains(got, "== top callees") ||
		!strings.Contains(got, "== top callers (1)\n") {
		t.Fatalf("got\n%s", got)
	}
	if _, err := query.How(c, "Nope"); !errors.Is(err, query.ErrNotFound) {
		t.Fatalf("unknown target: %v", err)
	}
}

func TestHowOfADependencyIsItsExplanation(t *testing.T) {
	c := depsWorkspace(t)
	if err := c.EnsureFresh(c.Workspace.Repos...); err != nil {
		t.Fatal(err)
	}
	r, err := query.How(c, "@acme/sdk")
	got := text(t, r, err)
	r, err = query.Explain(c, "@acme/sdk")
	if want := text(t, r, err); got != want || !strings.Contains(got, "== used by the workspace (") {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	r, err = query.How(withDeps(c), "Server.notification")
	wants(t, text(t, r, err), "== explain Protocol.notification (")
}

func TestHowSharesItsBudgetAndCapsTheEntryFunctions(t *testing.T) {
	c := tempWorkspace(t, map[string]string{
		"go.mod": "module x/m\n",
		"m.go": "package m\n\nfunc leaf() {}\n\nfunc mid() { leaf() }\n\n// A starts a.\nfunc A() { mid() }\n\nfunc B() { mid() }\n\n" +
			"func C() { mid() }\n\nfunc D() { leaf() }\n\nfunc E() { leaf() }\n",
	})
	if err := index.ResolvePending(c.Store, index.Options{Workspace: "w"}); err != nil {
		t.Fatal(err)
	}
	c.Scope = " --workspace w"
	r, err := query.How(c, "r:m.go")
	got := run(t, r, err)
	mustContain(t, got, "== flow from A (3)\nA  m.go:8\n  mid  m.go:5  func mid()\n    leaf  m.go:3  func leaf()\n",
		"== flow from B (3)\n", "== flow from C (3)\n",
		"2 more entry functions without their call tree: D, E; codedoc flow <name> prints one\n")
	if strings.Contains(got, "== flow from D") || strings.Contains(got, "== flow from mid") {
		t.Fatalf("got\n%s", got)
	}
	// Under a budget no section costs another its place, and a cut one
	// names the command that prints it alone.
	c.Budget = 520
	if r, err = query.How(c, "r:m.go"); err != nil {
		t.Fatal(err)
	}
	got = budgeted(t, r, 520)
	if len(got) > 520 {
		t.Fatalf("%d bytes over the budget:\n%s", len(got), got)
	}
	mustContain(t, got, "== explain r:m.go (", "== symbols (7)\n", "== flow from A (3)\n", "== flow from C (3)\n",
		"7 more: codedoc explain r:m.go --workspace w\n")
	c.Budget = 300
	if r, err = query.How(c, "A"); err != nil {
		t.Fatal(err)
	}
	mustContain(t, budgeted(t, r, 300), "== explain A (", "== flow from A (3)\nA  m.go:8\n")
}

// A call into a dependency is a node of the tree like any other: the
// dependency symbol by its name, tagged with its package.
func TestFlowNamesTheDependencySymbolsItReaches(t *testing.T) {
	c := depsWorkspace(t)
	r, err := query.How(c, "App.announce")
	got := text(t, r, err)
	wants(t, got, "== flow from App.announce (2)\nApp.announce  src/main.ts:19-21\n"+
		"  Protocol.notification  dist/shared/protocol.d.ts:9  notification(notification: Notification): Promise<void>"+
		" — Sends a notification to the other side.  [dep @acme/sdk@1.2.0]\n")
	if strings.Contains(got, "?  src/main.ts:20") {
		t.Fatalf("a callee without a name:\n%s", got)
	}
	r, err = query.Flow(c, "App.announce", query.FlowOpts{Depth: 2, DocumentedOnly: true})
	wants(t, text(t, r, err), "  Protocol.notification  dist/shared/protocol.d.ts:9  ")
}
