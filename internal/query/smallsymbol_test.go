package query_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

// symbolUnder runs codedoc symbol for addrs in repo under a budget and
// renders the answer as the CLI prints it.
func symbolUnder(t *testing.T, c *query.Ctx, repo int, budget int, command string, addrs ...string) string {
	t.Helper()
	c.Repo, c.Cwd, c.Budget, c.Command = c.Workspace.Repos[repo], c.Workspace.Repos[repo].Path, budget, command
	res := must(query.Symbols(c, addrs, query.SymbolOpts{}))
	if len(addrs) == 1 {
		res = must(query.Symbol(c, addrs[0], query.SymbolOpts{}))
	}
	text := budgeted(t, res.resp, budget)
	if len(text) > budget {
		t.Fatalf("%d bytes under --budget %d:\n%s", len(text), budget, text)
	}
	for _, old := range []string{"--out", "raise --budget", "body omitted", "more section"} {
		if strings.Contains(text, old) {
			t.Fatalf("%s: an omission that names no command (%q):\n%s", command, old, text)
		}
	}
	return text
}

// A symbol under a budget too small for it is never a line that says
// nothing: its first lines, or its members, and the exact command that
// prints the rest.
func TestSymbolUnderASmallBudgetNamesTheCommandForTheRest(t *testing.T) {
	c := workspaceOf(t, "go-lib", "py-lib", "java-lib")
	text := symbolUnder(t, c, 0, 200, "codedoc symbol Store --budget 200", "Store")
	mustContain(t, text, "  Store  :10-15  type Store interface  Store persists string values by key.\n",
		"2 more: codedoc symbol Store --budget 200 --offset 1\n")

	text = symbolUnder(t, c, 0, 150, "codedoc symbol service/service.go#Service.Lookup --budget 150", "service/service.go#Service.Lookup")
	mustContain(t, text, "Service.Lookup  service/service.go:17-23\n8 more lines: codedoc lines go-lib:service/service.go 16-23\n")

	text = symbolUnder(t, c, 1, 300, "codedoc symbol src/pylib/store.py#MemStore --budget 300", "src/pylib/store.py#MemStore")
	mustContain(t, text, "  MemStore  :25-44  class MemStore(Store)  MemStore keeps values in memory.",
		"  MemStore.__init__  :32-35  def __init__(self)\n4 more: codedoc symbol src/pylib/store.py#MemStore --budget 300 --offset 2\n")

	text = symbolUnder(t, c, 1, 260, "codedoc symbol MemStore.get MemStore.put --budget 260", "MemStore.get", "MemStore.put")
	mustContain(t, text, "1 more: codedoc symbol MemStore.get --budget 260\n1 more: codedoc symbol MemStore.put --budget 260\n")
}
