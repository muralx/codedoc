package query_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/query"
)

// impact renders the impact answer for an address under a budget (0 for
// none).
func impact(t *testing.T, c *query.Ctx, addr string, o query.ImpactOpts, budget int) string {
	t.Helper()
	c.Budget = budget
	r, err := query.Impact(c, addr, o)
	if err != nil {
		t.Fatal(err)
	}
	return budgeted(t, r, budget)
}

func TestImpactSnapshots(t *testing.T) {
	c := indexedWorkspace(t)
	// A method: its caller, what it implements, the test that reaches it.
	snapshot(t, "impact_memstore_get", impact(t, c, "MemStore.Get", query.ImpactOpts{}, 0))
	// An interface method: what implements it, and the doc comment that
	// names it.
	snapshot(t, "impact_store_get", impact(t, c, "go-lib:store/store.go#Store.Get", query.ImpactOpts{}, 0))
	// A type: the references to it and its implementations.
	snapshot(t, "impact_store", impact(t, c, "go-lib:store/store.go#Store", query.ImpactOpts{}, 0))
	// A symbol another repo calls says so on its last line.
	snapshot(t, "impact_service_lookup", impact(t, c, "Service.Lookup", query.ImpactOpts{}, 0))
}

func TestImpactListsTheConfigKeysASymbolReads(t *testing.T) {
	c := workspaceOf(t, "go-lib", "cfg-repo")
	got := impact(t, c, "Mode", query.ImpactOpts{}, 0)
	mustContain(t, got, "== config keys read (1)\nLIB_MODE  service/service.go:28  "+
		"func Mode() string { return os.Getenv(\"LIB_MODE\") }\n", "exported  yes  no other repo of the workspace uses it\n")
	// A key no indexed config file defines is tagged so; each key is
	// listed once, at its first read.
	c = tempWorkspace(t, map[string]string{"go.mod": "module x/m\n",
		"m.go": "package m\n\nimport \"os\"\n\nfunc env() string {\n\ta := os.Getenv(\"NOPE_URL\")\n\treturn a + os.Getenv(\"NOPE_URL\")\n}\n"})
	if got := impact(t, c, "env", query.ImpactOpts{}, 0); got != "env  m.go:5-8  func env() string\n"+
		"== config keys read (1)\nNOPE_URL  m.go:6  a := os.Getenv(\"NOPE_URL\")  [undefined]\nexported  no (package)\n" {
		t.Fatalf("got\n%s", got)
	}
}

// busyWorkspace has a function Data that a.go calls twice and a test
// file calls thirteen times from four tests, and a document and a doc
// comment that name it.
func busyWorkspace(t *testing.T) *query.Ctx {
	t.Helper()
	test := "package m\n\nimport \"testing\"\n\n"
	for i, n := range []int{1, 5, 4, 3} {
		test += fmt.Sprintf("func TestT%d(t *testing.T) {\n%s}\n\n", i, strings.Repeat("\t_ = Data()\n", n))
	}
	c := tempWorkspace(t, map[string]string{
		"go.mod":    "module x/m\n",
		"m.go":      "package m\n\n// Data returns the data.\nfunc Data() string { return \"\" }\n\n// Twice joins what Data returns with itself.\nfunc Twice() string {\n\treturn Data() + Data()\n}\n",
		"m_test.go": test,
		"docs/a.md": "# Notes\n\nSome prose.\n\n## Loading\n\nThe data comes from `Data()`.\nMore about Data.\n\n## Other\n\nNothing here.\n",
	})
	if err := index.ResolvePending(c.Store, index.Options{Workspace: "w"}); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestImpactSummarizesACrowdedFile(t *testing.T) {
	c := busyWorkspace(t)
	c.Scope = " --workspace w"
	got := impact(t, c, "Data", query.ImpactOpts{}, 0)
	snapshot(t, "impact_crowded", got)
	// The command of the count line continues the callers at the caller
	// that was cut: rank 2 is TestT1, whole.
	c.Offset = 2
	r, err := query.Callers(c, "Data", query.GraphOpts{Depth: 1})
	if rest := run(t, r, err); !strings.HasPrefix(rest, "== callers of Data (5)\nm_test.go\n  TestT1  :10  [test]\n10\t\t_ = Data()\n") {
		t.Fatalf("codedoc callers Data --offset 2:\n%s", rest)
	}
	c.Offset = 0
	// Wider fragments keep each caller whole.
	c.Context = query.Context{Before: 1}
	mustContain(t, impact(t, c, "Data", query.ImpactOpts{}, 0), "  TestT1  :10  [test]\n9\tfunc TestT1(t *testing.T) {\n10\t\t_ = Data()\n",
		"14\t\t_ = Data()\n  7 more call sites in this file: codedoc callers Data --workspace w --offset 3\n")
}

func TestImpactSharesItsBudgetAndPagesOneSection(t *testing.T) {
	c := busyWorkspace(t)
	// Under a small budget every section keeps its title and what was cut
	// names its command.
	got := impact(t, c, "Data", query.ImpactOpts{}, 450)
	if len(got) > 450 {
		t.Fatalf("%d bytes over the budget:\n%s", len(got), got)
	}
	mustContain(t, got, "Data  m.go:4  ", "== callers (5)\n", "== tests (4)\n", "== mentions in documents and doc comments (2)\n",
		"exported  yes\n", " more: codedoc ")
	// --only prints one section, which --offset pages.
	c.Offset = 1
	if got := impact(t, c, "Data", query.ImpactOpts{Only: "mentions"}, 0); got != "Data  m.go:4  func Data() string  Data returns the data.\n"+
		"== mentions in documents and doc comments (2)\nTwice  m.go:6  // Twice joins what Data returns with itself.\n" {
		t.Fatalf("got\n%s", got)
	}
	c.Offset = 9
	mustContain(t, impact(t, c, "Data", query.ImpactOpts{Only: "tests"}, 0), "== tests (4)\noffset 9 is past the end (total 4)\n")
	if _, err := query.Impact(c, "Data", query.ImpactOpts{}); err == nil || !strings.Contains(err.Error(), "--only") {
		t.Fatalf("an offset without --only: %v", err)
	}
	c.Offset = 0
	if _, err := query.Impact(c, "Data", query.ImpactOpts{Only: "everything"}); err == nil ||
		!strings.Contains(err.Error(), "want callers, refs, impls, tests, mentions, config") {
		t.Fatalf("unknown section: %v", err)
	}
	if _, err := query.Impact(c, "Nope", query.ImpactOpts{}); !errors.Is(err, query.ErrNotFound) {
		t.Fatalf("unknown symbol: %v", err)
	}
}

func TestImpactMentionsNeedTheNameAsCode(t *testing.T) {
	c := tempWorkspace(t, map[string]string{
		"go.mod": "module x/m\n",
		"m.go": "package m\n\n// Box holds a list.\ntype Box struct{}\n\n// list returns the items.\nfunc (Box) list() []int { return nil }\n\n" +
			"// size counts what list returns.\nfunc (Box) size() int { return 0 }\n\n// other makes a list of things.\nfunc other() {}\n",
		"docs/a.md": "# Guide\n\nThe list of things is long.\n\n## Box\n\nBox keeps items; list returns them.\n\n## Calls\n\nUse Box.list to read.\n",
		"docs/b.md": "# Links\n\nSee [the box](a.md#box).\n",
	})
	if err := index.ResolvePending(c.Store, index.Options{Workspace: "w"}); err != nil {
		t.Fatal(err)
	}
	// The word alone in prose is no mention (the Guide, the comment of
	// other); near its container (a section or a comment that names Box,
	// Box itself and its members), or qualified, it is. Documents come
	// first.
	got := impact(t, c, "Box.list", query.ImpactOpts{Only: "mentions"}, 0)
	if !strings.HasSuffix(got, "== mentions in documents and doc comments (4)\ndocs/a.md\n  Box  :7  Box keeps items; list returns them.\n"+
		"  Calls  :11  Use Box.list to read.\nm.go\n  Box  :3  // Box holds a list.\n  Box.size  :9  // size counts what list returns.\n") {
		t.Fatalf("got\n%s", got)
	}
	// A section is mentioned by the sections that link to it, then by the
	// texts that name it; it is exported from nowhere.
	if got = impact(t, c, "docs/a.md#Box", query.ImpactOpts{}, 0); got != "Box  docs/a.md:5-7  ## Box  Box keeps items; list returns them.\n"+
		"== mentions in documents and doc comments (2)\nLinks  docs/b.md:3  See [the box](a.md#box).\n"+
		"Calls  docs/a.md:11  Use Box.list to read.\n" {
		t.Fatalf("got\n%s", got)
	}
}
