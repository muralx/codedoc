package query_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

// Each address of a query for several symbols is resolved as a query
// for it alone is: answered for its only plausible or its best
// candidate, with the note saying so after the address, or one line
// with the nearest candidates.
func TestSymbolsAnswerNearMissesPerAddress(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Symbols(c, []string{"MemStore.Get", "errmissing", "Lookupp", "Zzyzxq"}, query.SymbolOpts{NoBody: true})
	got := run(t, r, err)
	mustContain(t, got, "MemStore.Get  store/store.go:26-32  ", "\nErrMissing  store/store.go:7  ",
		"\nLookupp  not found; nearest: Service.Lookup", "\nZzyzxq  not found\n", "\nerrmissing: answered for ErrMissing\n")
	// Candidates are an answer: the query succeeds on them alone.
	if _, err := query.Symbols(c, []string{"Lookupp", "Zzyzxq"}, query.SymbolOpts{}); err != nil {
		t.Fatalf("nearest candidates: %v", err)
	}
	// Under --strict an ambiguous address is one line and the others are
	// answered; alone it fails as ambiguous.
	c.Strict = true
	r, err = query.Symbols(c, []string{"MemStore.Get", "Get"}, query.SymbolOpts{NoBody: true})
	mustContain(t, run(t, r, err), "\nGet  ambiguous (4 candidates); codedoc symbol Get --strict lists them\n")
	if _, err := query.Symbols(c, []string{"Get", "Zzyzxq"}, query.SymbolOpts{}); !errors.Is(err, query.ErrAmbiguous) {
		t.Fatalf("strict: %v", err)
	}
}

// The commands an impact or a how answer names ask about the symbol
// that was answered, not about the address that only came near it.
func TestCompositeFollowUpsNameTheResolvedSymbol(t *testing.T) {
	c := busyWorkspace(t)
	got := impact(t, c, "data", query.ImpactOpts{}, 450)
	mustContain(t, got, "Data  m.go:4  ", " more: codedoc callers r:go:x/m:Data --offset ", "answered for Data")
	c.Budget = 300
	r, err := query.How(c, "twice")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, budgeted(t, r, 300), "== explain Twice (", "answered for Twice")
	// Nothing near: not found; several near: they are the answer.
	if _, err := query.Impact(c, "Zzyzxq", query.ImpactOpts{}); !errors.Is(err, query.ErrNotFound) || errors.Is(err, query.ErrNearest) {
		t.Fatalf("nothing near: %v", err)
	}
}

// A file named without its directory is answered when only one indexed
// file can be meant.
func TestHowFindsTheOneFileOfThatName(t *testing.T) {
	c := workspaceOf(t, "go-lib", "go-app")
	r, err := query.How(c, "main.go")
	got := run(t, r, err)
	if !strings.Contains(got, "== explain go-app:main.go (3)\n") || !strings.Contains(got, "== flow from main (5)\n") ||
		!strings.HasSuffix(got, "answered for go-app:main.go\n") {
		t.Fatalf("got\n%s", got)
	}
	// Under --strict it is not looked for.
	c.Strict = true
	if _, err := query.How(c, "main.go"); !errors.Is(err, query.ErrNotFound) {
		t.Fatalf("strict: %v", err)
	}
}
