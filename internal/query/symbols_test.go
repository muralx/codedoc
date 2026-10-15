package query_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func TestSymbolsPrintsEachSymbolAsSymbolDoes(t *testing.T) {
	c := indexedWorkspace(t)
	c.Command = "codedoc symbol MemStore.Get MemStore.Put Nope Get"
	r, err := query.Symbols(c, []string{"MemStore.Get", "MemStore.Put", "Nope", "Get"}, query.SymbolOpts{})
	got := run(t, r, err)
	// An address that names nothing, or several symbols, is one line; the
	// others are answered.
	snapshot(t, "symbols_memstore", got)
	var single []string
	for _, addr := range []string{"MemStore.Get", "MemStore.Put"} {
		r, err := query.Symbol(c, addr, query.SymbolOpts{})
		single = append(single, strings.TrimPrefix(run(t, r, err), "go-lib@0b29eeb\n"))
	}
	if !strings.Contains(got, single[0]+single[1]) {
		t.Fatalf("the layout of a single answer is not kept:\n%s", got)
	}
	// The options apply to every symbol.
	r, err = query.Symbols(c, []string{"MemStore.Get", "MemStore.Put"}, query.SymbolOpts{NoBody: true})
	if got := run(t, r, err); got != "go-lib@0b29eeb\nMemStore.Get  store/store.go:26-32  func (m *MemStore) Get(key string) (string, error)"+
		"  Get implements Store.\nMemStore.Put  store/store.go:35-38  func (m *MemStore) Put(key, value string) error  Put implements Store.\n" {
		t.Fatalf("--no-body:\n%s", got)
	}
	r, err = query.Symbols(c, []string{"MemStore.Get", "Service.Lookup"}, query.SymbolOpts{DocOnly: true})
	mustContain(t, run(t, r, err), "go-lib@0b29eeb\n", "Service.Lookup  service/service.go:17-23  func (svc *Service) Lookup(key string) string"+
		"  Lookup returns the value for key, or \"\" when missing.\n")
	r, err = query.Symbols(c, []string{"MemStore.Get", "MemStore.Put"}, query.SymbolOpts{Context: 1})
	mustContain(t, run(t, r, err), "32\t}\n33\t\nMemStore.Put  store/store.go:35-38\n34\t// Put implements Store.\n")
	if _, err := query.Symbols(c, []string{"MemStore.Get"}, query.SymbolOpts{Context: -1}); err == nil {
		t.Fatal("negative context accepted")
	}
	// Symbols of several repos get a header naming them and their repo
	// before each location.
	r, err = query.Symbols(c, []string{"MemStore.Get", "go-app:main.go#main"}, query.SymbolOpts{NoBody: true})
	mustContain(t, run(t, r, err), "go-lib@0b29eeb go-app@f29d590\n", "MemStore.Get  go-lib:store/store.go:26-32  ",
		"main  go-app:main.go:11-14  func main()\n")
	// Only when no address names a symbol the query fails, after the
	// line of each.
	r, err = query.Symbols(c, []string{"Nope", "Never"}, query.SymbolOpts{})
	if !errors.Is(err, query.ErrNotFound) || !strings.HasSuffix(render(t, r), "Nope  not found\nNever  not found\n") {
		t.Fatalf("%v\n%s", err, render(t, r))
	}
}

func TestSymbolsOfADependencyWithTheFlag(t *testing.T) {
	c := depsWorkspace(t)
	r, err := query.Symbols(withDeps(c), []string{"Server.notification", "run"}, query.SymbolOpts{NoBody: true})
	wants(t, text(t, r, err), "Protocol.notification  ", "[dep @acme/sdk@1.2.0]", "run  ")
}

// bigWorkspace has two small functions, one long function and a class
// of many methods.
func bigWorkspace(t *testing.T) *query.Ctx {
	t.Helper()
	long := "// Long does a lot.\nfunc Long() int {\n\tn := 0\n" + strings.Repeat("\tn++ // one more step of the long function\n", 60) + "\treturn n\n}\n"
	class := "export class Big {\n"
	for i := range 30 {
		class += fmt.Sprintf("  /** m%d does step %d. */\n  m%d(): number {\n    return %d;\n  }\n", i, i, i, i)
	}
	return tempWorkspace(t, map[string]string{
		"go.mod": "module x/m\n",
		"m.go":   "package m\n\n// One is small.\nfunc One() int { return 1 }\n\n// Two is small.\nfunc Two() int { return 2 }\n\n" + long,
		"big.ts": class + "}\n",
	})
}

func TestSymbolsShareTheBudget(t *testing.T) {
	c := bigWorkspace(t)
	c.Command = "codedoc symbol One Long Big Two --budget 4500"
	c.Budget = 4500
	r, err := query.Symbols(c, []string{"One", "Long", "Big", "Two"}, query.SymbolOpts{})
	if err != nil {
		t.Fatal(err)
	}
	got := budgeted(t, r, 4500)
	if len(got) > 4500 {
		t.Fatalf("%d bytes over the budget:\n%s", len(got), got)
	}
	// The small ones are whole, wherever they stand in the list.
	mustContain(t, got, "One  m.go:4\n3\t// One is small.\n4\tfunc One() int { return 1 }\n",
		"Two  m.go:7\n6\t// Two is small.\n7\tfunc Two() int { return 2 }\n")
	// The long function is cut and continued with the exact lines left.
	mustContain(t, got, "Long  m.go:10-73\n9\t// Long does a lot.\n10\tfunc Long() int {\n", " more lines: codedoc lines r:m.go ")
	// The class is answered by the outline of its members.
	mustContain(t, got, "  Big  :1-122  export class Big\n  Big.m0  :3-5  m0(): number  m0 does step 0.\n",
		"  Big.m29  :119-121  m29(): number  m29 does step 29.\n",
		"Big is 122 lines, over the budget: its members are listed; codedoc symbol Big.<member> prints one")
	if strings.Contains(got, "more sections") {
		t.Fatalf("a symbol was dropped:\n%s", got)
	}
	// When the outline does not fit its share either, the class is cut
	// and continued like any other source.
	c.Budget = 1200
	r, err = query.Symbols(c, []string{"One", "Long", "Big", "Two"}, query.SymbolOpts{})
	if err != nil {
		t.Fatal(err)
	}
	got = budgeted(t, r, 1200)
	mustContain(t, got, "Big  big.ts:1-122\n1\texport class Big {\n", " more lines: codedoc lines r:big.ts ", "7\tfunc Two() int { return 2 }\n")
	if len(got) > 1200 || strings.Contains(got, "its members are listed") {
		t.Fatalf("%d bytes:\n%s", len(got), got)
	}
	// Without a budget everything is whole.
	c.Budget = 0
	r, err = query.Symbols(c, []string{"One", "Long", "Big", "Two"}, query.SymbolOpts{})
	if got := run(t, r, err); !strings.Contains(got, "73\t}\n") || !strings.Contains(got, "\n122\t}\n") {
		t.Fatalf("got\n%s", got)
	}
}
