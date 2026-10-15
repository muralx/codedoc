package query_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func where(t *testing.T, c *query.Ctx, text string, o query.WhereOpts) string {
	t.Helper()
	r, err := query.Where(c, text, o)
	if err != nil {
		t.Fatal(err)
	}
	return render(t, r)
}

// Several locations answer with the first five, each with the lines
// that match; modules are no location.
func TestWhereListsTheTopLocationsWithTheirLines(t *testing.T) {
	c := launcherWorkspace(t)
	got := where(t, c, "store value key", query.WhereOpts{})
	if !strings.Contains(got, "== where \"store value key\" (") || strings.Contains(got, "module go") {
		t.Fatalf("%s", got)
	}
	wants(t, got, "  Store.Put  :14  Put(key, value string) error  Put stores value under key.\n13\t\t// Put stores value under key.\n",
		"MemStore.Put  store/store.go:35-38", " more: rerun with --offset 5")
	two := where(t, c, "store value key", query.WhereOpts{Limit: 2})
	if len(two) >= len(got) {
		t.Fatalf("--limit 2 is no shorter:\n%s", two)
	}
	if _, err := query.Where(c, "x", query.WhereOpts{Limit: -1}); err == nil {
		t.Fatal("a negative limit is accepted")
	}
	if got := where(t, c, "!!!", query.WhereOpts{}); !strings.Contains(got, "(0)") {
		t.Fatalf("%s", got)
	}
}

// A single strong hit is shown whole, as symbol shows it: the doc
// comment and every line of the body; the line that continues the
// answer lists the weaker ones.
func TestWhereShowsASingleStrongHitWhole(t *testing.T) {
	c := launcherWorkspace(t)
	got := where(t, c, "start the worker detached", query.WhereOpts{})
	wants(t, got, "launchWorker  store/launch.go:7-16", "5\t// launchWorker starts the worker binary",
		"8\t\tcmd := exec.Command(bin)\n", "15\t\treturn cmd.Start()\n16\t}\n")
	if strings.Contains(got, "func setDetached") {
		t.Fatalf("more than the one symbol:\n%s", got)
	}
	// The next page lists the others.
	c.Offset = 1
	if next := where(t, c, "start the worker detached", query.WhereOpts{}); strings.Contains(next, "8\t\tcmd := exec.Command(bin)") {
		t.Fatalf("the second page shows the first hit whole again:\n%s", next)
	}
}

// A document and the section that is its whole text are one location:
// the section is listed, once.
func TestWhereListsADocumentAndItsOnlySectionOnce(t *testing.T) {
	c := linkWorkspace(t)
	got := where(t, c, "message delivered through the channel hook", query.WhereOpts{})
	wants(t, got, "== where \"message delivered through the channel hook\" (1)\nDelivery  docs/delivery.md:1-3\n1\t# Delivery\n")
	if strings.Count(got, "Delivery  ") != 1 || strings.Contains(got, "delivery.md  docs/") {
		t.Fatalf("the same lines twice:\n%s", got)
	}
	r, err := query.Search(c, "nags", query.SearchOpts{})
	wants(t, text(t, r, err), "== search \"nags\" (1)\nDelivery  docs/delivery.md:1-3  ")
	// Asked for by kind, the document is still found.
	r, err = query.Search(c, "nags", query.SearchOpts{Kind: "document"})
	wants(t, text(t, r, err), "== search \"nags\" (1)\n")
}
