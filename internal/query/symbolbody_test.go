package query_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func TestSymbolNeverPrintsTheDocTwice(t *testing.T) {
	c := workspaceOf(t, "py-lib", "ts-domain")
	// A Python docstring is inside the body: the entry line carries no
	// copy of it.
	text := out(t, must(query.Symbol(c, "MemStore.get", query.SymbolOpts{})))
	if !strings.Contains(text, "MemStore.get  src/pylib/store.py:37-41\n37\t    def get(self, key):\n"+
		"38\t        \"\"\"Return the value for key or raise KeyError.\"\"\"\n") ||
		strings.Count(text, "Return the value for key or raise KeyError.") != 1 {
		t.Fatalf("docstring:\n%s", text)
	}
	// A doc comment above the declaration is printed as the lines it is.
	text = out(t, must(query.Symbol(c, "MemoryRepo.save", query.SymbolOpts{})))
	first := strings.SplitN(text, "\n", 3)
	if len(first) < 3 || !strings.HasPrefix(first[1], "MemoryRepo.save  src/records.ts:") ||
		strings.Contains(first[1], "save(env") || !strings.Contains(text, "\t  save(env: Environment): void {\n") {
		t.Fatalf("the signature is the first line of the body, not of the header:\n%s", text)
	}
	// With extra context lines around the body the doc is among them.
	text = out(t, must(query.Symbol(c, "MemStore.get", query.SymbolOpts{Context: 1})))
	if strings.Count(text, "Return the value for key or raise KeyError.") != 1 || !strings.Contains(text, "\n42\t\n") {
		t.Fatalf("--context 1:\n%s", text)
	}
}

// wideType is a Go file with a struct of n documented methods.
func wideType(n int) string {
	var b strings.Builder
	b.WriteString("package m\n\n// Box holds things.\ntype Box struct {\n")
	for i := range n {
		fmt.Fprintf(&b, "\t// F%d is field %d of the box, with a comment long enough to take room.\n\tF%d int\n", i, i, i)
	}
	b.WriteString("}\n\n// Long is a function that is long.\nfunc Long() int {\n")
	for i := range n {
		fmt.Fprintf(&b, "\t_ = %d // a line of the long function\n", i)
	}
	b.WriteString("\treturn 0\n}\n")
	return b.String()
}

func TestSymbolOverTheBudgetIsNeverWithoutItsSource(t *testing.T) {
	c := tempWorkspace(t, map[string]string{"go.mod": "module x/m\n", "m.go": wideType(60)})
	// A type larger than the budget answers with its members and how to
	// read one of them or a range.
	text := budgetedSymbol(t, c, "Box", 1500)
	mustContain(t, text, "m.go\n  Box  :4-125  type Box struct  Box holds things.\n",
		"  Box.F0  :6  F0 int  F0 is field 0 of the box, with a comment long enough to take room.\n",
		" more: rerun with --offset ", "Box is 123 lines, over the budget: its members are listed; "+
			"codedoc symbol Box.<member> prints one, codedoc lines r:m.go A-B a range\n")
	if strings.Contains(text, "body omitted") || strings.Contains(text, "\n5\t") {
		t.Fatalf("the outline replaces the body:\n%s", text)
	}
	// A function is cut to the lines that fit and continued.
	text = budgetedSymbol(t, c, "Long", 600)
	mustContain(t, text, "Long  m.go:128-190\n127\t// Long is a function that is long.\n128\tfunc Long() int {\n",
		" more lines: codedoc lines r:m.go ", "-190\n")
	if strings.Contains(text, "body omitted") {
		t.Fatalf("cut, not dropped:\n%s", text)
	}
	// Without a budget (--out) both are whole.
	c.Budget = 0
	if text = out(t, must(query.Symbol(c, "Box", query.SymbolOpts{}))); !strings.Contains(text, "\n125\t}\n") ||
		strings.Contains(text, "over the budget") {
		t.Fatalf("unlimited:\n%s", text)
	}
}
