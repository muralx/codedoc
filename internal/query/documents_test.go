package query_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
)

// result pairs what a query returns, so its text can be taken in one
// expression: out(t, must(query.Outline(c, "x", query.OutlineOpts{}))).
type result struct {
	resp *output.Response
	err  error
}

func must(r *output.Response, err error) result { return result{r, err} }

// out renders a query result, failing the test on an error.
func out(t *testing.T, res result) string {
	t.Helper()
	return run(t, res.resp, res.err)
}

// docWorkspace indexes the document fixture next to go-lib.
func docWorkspace(t *testing.T) *query.Ctx {
	t.Helper()
	return workspaceOf(t, "doc-repo", "go-lib")
}

func TestOutlineDocumentIsItsHeadingTree(t *testing.T) {
	c := docWorkspace(t)
	snapshot(t, "outline_document", out(t, must(query.Outline(c, "doc-repo:docs/design.md", query.OutlineOpts{}))))
	text := out(t, must(query.Outline(c, "doc-repo:docs/notes.rst", query.OutlineOpts{})))
	mustContain(t, text, "== docs/notes.rst, 29 lines (5)", "\n    12-20  Caching  9 lines\n")
	if text := out(t, must(query.Outline(c, "doc-repo:docs/CHANGES.txt", query.OutlineOpts{}))); !strings.Contains(text, "CHANGES.txt, 6 lines (0)") {
		t.Errorf("plain text has no sections:\n%s", text)
	}
}

func TestSymbolAddressesSections(t *testing.T) {
	c := docWorkspace(t)
	snapshot(t, "symbol_section_body", out(t, must(query.Symbol(c, "doc-repo:docs/design.md#6.2", query.SymbolOpts{}))))
	for addr, want := range map[string]string{
		"doc-repo:docs/design.md#6. Delivery":                   "docs/design.md:37-53  ## 6. Delivery",
		"doc-repo:docs/design.md#6":                             "docs/design.md:37-53  ## 6. Delivery",
		"doc-repo:docs/design.md#6.":                            "docs/design.md:37-53  ## 6. Delivery",
		"doc-repo:docs/design.md#open QUESTIONS":                "docs/design.md:55-58  Open questions",
		"doc-repo:docs/design.md#open-questions":                "docs/design.md:55-58  Open questions",
		"doc-repo:docs/design.md#Append":                        "docs/design.md:60-63  Appendix",
		"doc-repo:docs/design.md#rdering":                       "docs/design.md:42-44  ### 6.1 Ordering",
		"doc-repo:docs/design.md#2. Model > 2.2 Values":         "docs/design.md:29-35  ### 2.2 Values",
		"doc-repo:docs/design.md#values > notes":                "docs/design.md:33-35  #### Notes",
		"doc-repo:docs/design.md#":                              "docs/design.md:1-63  Store design  How the store package keeps records",
		"doc-repo:docs/design.md":                               "docs/design.md:1-63  Store design",
		"doc-repo:doc:docs/design.md:6. Delivery > 6.2 Retries": "docs/design.md:46-53  ### 6.2 Retries",
		"doc-repo:docs/guide.adoc#Modes":                        "docs/guide.adoc:16-18  === Modes",
	} {
		text := out(t, must(query.Symbol(c, addr, query.SymbolOpts{NoBody: true})))
		if !strings.Contains(text, want) {
			t.Errorf("%s: missing %q in\n%s", addr, want, text)
		}
	}
	// The answer is the text of the section under one line naming it.
	text := out(t, must(query.Symbol(c, "doc-repo:docs/design.md#2.2", query.SymbolOpts{})))
	mustContain(t, text, "2.2 Values  docs/design.md:29-35\n29\t### 2.2 Values\n", "Values are opaque bytes.")
	mustContain(t, keyed(t, must(query.Symbol(c, "doc-repo:docs/design.md#2.2", query.SymbolOpts{})).resp),
		"doc-repo:doc:docs/design.md:2. Model > 2.2 Values  ", "[section] [in:2. Model]")
	if strings.Contains(text, "[doc:") || strings.Contains(text, "fan-in") {
		t.Errorf("a section has no doc state or fan-in:\n%s", text)
	}
	if text := out(t, must(query.Symbol(c, "doc-repo:docs/design.md#6", query.SymbolOpts{DocOnly: true}))); !strings.Contains(text,
		"Delivery hands records to the service.") || strings.Contains(text, "[doc:") {
		t.Errorf("--doc-only of a section:\n%s", text)
	}
}

func TestAmbiguousAndUnknownHeadings(t *testing.T) {
	c := docWorkspace(t)
	// The first of two sections with one heading is answered and the
	// other named; a heading the document lacks lists its sections.
	resp, err := query.Symbol(c, "doc-repo:docs/design.md#Notes", query.SymbolOpts{NoBody: true})
	mustContain(t, text(t, resp, err), "docs/design.md:33-35  #### Notes",
		"also: 6. Delivery > 6.2 Retries > Notes (doc-repo:docs/design.md:51)")
	resp, err = query.Symbol(c, "doc-repo:docs/design.md#No such heading", query.SymbolOpts{})
	if !errors.Is(err, query.ErrNearest) {
		t.Fatalf("err = %v", err)
	}
	mustContain(t, render(t, resp), "== no exact match; nearest: (6)\ndocs/design.md\n", "  2. Model  :15-35  ## 2. Model\n")
	c.Strict = true
	resp, err = query.Symbol(c, "doc-repo:docs/design.md#Notes", query.SymbolOpts{})
	if !errors.Is(err, query.ErrAmbiguous) {
		t.Fatalf("err = %v", err)
	}
	mustContain(t, render(t, resp), "== candidates (2)", "2. Model > 2.2 Values > Notes", "6. Delivery > 6.2 Retries > Notes")
	_, err = query.Symbol(c, "doc-repo:docs/design.md#No such heading", query.SymbolOpts{})
	if !errors.Is(err, query.ErrNotFound) || errors.Is(err, query.ErrNearest) {
		t.Fatalf("err = %v", err)
	}
	// A prefix shared by several headings is ambiguous, not the first.
	if _, err := query.Symbol(c, "doc-repo:docs/design.md#2", query.SymbolOpts{}); err != nil {
		t.Fatalf("a numbering names one section: %v", err)
	}
	if _, err := query.Symbol(c, "doc-repo:docs/guide.adoc#on", query.SymbolOpts{}); !errors.Is(err, query.ErrAmbiguous) {
		t.Fatalf("substring on: %v", err)
	}
	if _, err := query.Symbol(c, "doc-repo:docs/notes.rst#c", query.SymbolOpts{}); err != nil {
		t.Fatalf("the one heading starting with c: %v", err)
	}
}

func TestSymbolAtLine(t *testing.T) {
	c := docWorkspace(t)
	for addr, want := range map[string]string{
		"go-lib:store/store.go:28":    "MemStore.Get  store/store.go:26-32",
		"go-lib:store/store.go:12":    "Store.Get  store/store.go:12",
		"doc-repo:docs/design.md:50":  "docs/design.md:46-53  ### 6.2 Retries",
		"doc-repo:docs/design.md:52":  "docs/design.md:51-53  #### Notes",
		"doc-repo:docs/design.md:3":   "docs/design.md:1-63  Store design",
		"doc-repo:docs/CHANGES.txt:2": "docs/CHANGES.txt:1-6  CHANGES.txt",
	} {
		text := out(t, must(query.Symbol(c, addr, query.SymbolOpts{NoBody: true})))
		if !strings.Contains(text, want) {
			t.Errorf("%s: missing %q in\n%s", addr, want, text)
		}
	}
	body := out(t, must(query.Symbol(c, "go-lib:store/store.go:28", query.SymbolOpts{})))
	mustContain(t, body, "26\tfunc (m *MemStore) Get(key string) (string, error) {", "32\t}")
	// A line no symbol encloses is answered with the symbols of the file.
	resp, err := query.Symbol(c, "go-lib:store/store.go:2", query.SymbolOpts{})
	if !errors.Is(err, query.ErrNearest) {
		t.Fatalf("err = %v", err)
	}
	mustContain(t, render(t, resp), "== no exact match; nearest: (6)\nstore/store.go\n  ErrMissing  :7  ",
		"no symbol encloses line 2 of store/store.go (38 lines)")
	if resp, err = query.Symbol(c, "go-lib:store/absent.go:2", query.SymbolOpts{}); !errors.Is(err, query.ErrNotFound) || resp != nil {
		t.Fatalf("unknown file: %v, %v", err, resp)
	}
}

func TestSymbolAtLineStrictlyAndLineAddresses(t *testing.T) {
	c := docWorkspace(t)
	c.Strict = true
	resp, err := query.Symbol(c, "go-lib:store/store.go:2", query.SymbolOpts{})
	if !errors.Is(err, query.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	mustContain(t, render(t, resp), "no symbol encloses line 2 of store/store.go (38 lines); codedoc outline go-lib:store/store.go")
	resp, err = query.Symbol(c, "go-lib:store/nope.go:2", query.SymbolOpts{})
	if !errors.Is(err, query.ErrNotFound) || !strings.Contains(render(t, resp), "no indexed file go-lib:store/nope.go") {
		t.Fatalf("unknown file: %v", err)
	}
	if a := query.ParseAddr("a/b.go:12"); a.Kind != query.AddrLine || a.Path != "a/b.go" || a.Line != 12 {
		t.Errorf("ParseAddr = %+v", a)
	}
	if a := query.ParseAddr("r:a/b.md#Step 2:30"); a.Kind != query.AddrPath || a.Name != "Step 2:30" {
		t.Errorf("a heading ending in :N is a heading: %+v", a)
	}
}

func TestFindAndSearchCoverSections(t *testing.T) {
	c := docWorkspace(t)
	text := out(t, must(query.Find(c, "retries", query.FindOpts{Kind: "section"})))
	mustContain(t, text, "== find retries (1)\n6.2 Retries  docs/design.md:46-53  "+
		"### 6.2 Retries  A failed delivery is retried three times.\n")
	mustContain(t, keyed(t, must(query.Find(c, "retries", query.FindOpts{Kind: "section"})).resp),
		"doc-repo:doc:docs/design.md:6. Delivery > 6.2 Retries  docs/design.md:46-53  ", "three times.  [doc]")
	text = out(t, must(query.Find(c, "store", query.FindOpts{Kind: "document"})))
	mustContain(t, text, "== find store (1)\ndesign.md  docs/design.md:1-63  Store design")
	text = out(t, must(query.Find(c, "*Notes", query.FindOpts{Kind: "section", Lang: "markdown"})))
	mustContain(t, text, "(2)")
	if text := out(t, must(query.Find(c, "Notes", query.FindOpts{Lang: "doc"}))); !strings.Contains(text, "(2)") {
		t.Errorf("find by exact heading:\n%s", text)
	}

	c.Embedder = embed.NewFake(16)
	text = render(t, search(t, c, "exponentially", query.SearchOpts{}))
	mustContain(t, text, "  Notes  :51-53  #### Notes  Retries back off exponentially.\n")
	text = keyed(t, search(t, c, "glossary", query.SearchOpts{Kind: "section", Lang: "doc"}))
	mustContain(t, text, ":60-63  Appendix  Glossary of terms.  [doc]")
	if text := keyed(t, search(t, c, "glossary", query.SearchOpts{Kind: "function"})); strings.Contains(text, "[doc]") {
		t.Errorf("--kind function keeps sections out:\n%s", text)
	}
	if _, err := query.Search(c, "x", query.SearchOpts{Lang: "markdown"}); err != nil {
		t.Errorf("--lang markdown: %v", err)
	}
}

func TestExplainDocument(t *testing.T) {
	c := docWorkspace(t)
	snapshot(t, "explain_document", out(t, must(query.Explain(c, "doc-repo:docs/design.md"))))
}

func TestDocumentsStayOutOfDocUpkeep(t *testing.T) {
	c := workspaceOf(t, "doc-repo")
	text := out(t, must(query.Overview(c)))
	mustContain(t, text, "doc-repo  main@", "files:6 symbols:0", "doc-repo  symbols ok:0 stale:0 missing:0  modules ok:0 missing:0")
	if text := out(t, must(query.Gaps(c, "", query.GapsOpts{}))); strings.Contains(text, "docs/") {
		t.Errorf("gaps lists sections:\n%s", text)
	}
	if text := out(t, must(query.DocCheck(c))); strings.Contains(text, "docs/") || !strings.Contains(text, "symbols ok:0 stale:0 missing:0") {
		t.Errorf("doc check counts sections:\n%s", text)
	}
	_, err := query.DocSet(c, "doc-repo:docs/design.md#1. Goals", "New text.")
	if err == nil || !strings.Contains(err.Error(), "is a section of docs/design.md, which has no doc to set") {
		t.Errorf("doc set on a section: %v", err)
	}
}

func TestRefsListsLinkingSections(t *testing.T) {
	c := docWorkspace(t)
	text := out(t, must(query.Refs(c, "doc-repo:docs/design.md#6", query.GraphOpts{})))
	// The linking section is listed with the line that holds the link.
	mustContain(t, text, "== references to 6. Delivery (1)\n"+
		"Retry runbook  docs/runbooks/retries.md:4  [design](../design.md#6-delivery).\n")
	mustContain(t, keyed(t, must(query.Refs(c, "doc-repo:docs/design.md#6", query.GraphOpts{})).resp),
		"== references to doc-repo:doc:docs/design.md:6. Delivery (1)",
		"doc-repo:doc:docs/runbooks/retries.md:Retry runbook  docs/runbooks/retries.md:4  # Retry runbook", "[link]")
	text = out(t, must(query.Refs(c, "doc-repo:docs/guide.adoc", query.GraphOpts{Kind: "link"})))
	mustContain(t, text, "(1)", "design.md  docs/design.md:7  ")
	if _, err := query.Refs(c, "doc-repo:docs/guide.adoc", query.GraphOpts{Kind: "nope"}); err == nil ||
		!strings.Contains(err.Error(), "field or link") {
		t.Errorf("--kind nope: %v", err)
	}
}

func TestChangesListsSections(t *testing.T) {
	c := workspaceOf(t, "doc-repo")
	root := c.Workspace.Repos[0].Path
	editRepo(t, root, "docs/design.md", "Records are delivered in key order.", "Records are delivered in insertion order.")
	editRepo(t, root, "docs/design.md", "### 2.2 Values", "### 2.3 Payloads")
	editRepo(t, root, "docs/guide.adoc", "== Upgrade", "== Migrate")
	text := out(t, must(query.Changes(c, "HEAD")))
	mustContain(t, text,
		"doc-repo:docs/design.md  modified, ",
		"docs/design.md\n", "  6.1 Ordering  :42-44  ### 6.1 Ordering  Records are delivered in insertion order.  [text-changed]\n",
		"  2.3 Payloads  :29-35  ### 2.3 Payloads  Values are opaque bytes.  [added]\n",
		"### 2.2 Values  [removed]",
		"  Migrate  :20-22  == Migrate  Read the changelog first.  [added]\n")
	if strings.Contains(text, "[doc:") {
		t.Errorf("sections carry no doc state:\n%s", text)
	}
}

// budgetedSymbol renders symbol addr under a budget.
func budgetedSymbol(t *testing.T, c *query.Ctx, addr string, budget int) string {
	t.Helper()
	c.Budget = budget
	resp, err := query.Symbol(c, addr, query.SymbolOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if _, err := output.Render(&b, resp, "text", budget); err != nil {
		t.Fatal(err)
	}
	if b.Len() > budget {
		t.Errorf("%d bytes under a budget of %d:\n%s", b.Len(), budget, b.String())
	}
	return b.String()
}

func TestSectionBodyIsCutToTheBudget(t *testing.T) {
	c := docWorkspace(t)
	// A section without subsections is cut to the lines that fit and
	// continued with codedoc lines.
	text := budgetedSymbol(t, c, "doc-repo:docs/design.md#2.1", 200)
	mustContain(t, text, "2.1 Keys  docs/design.md:19-27\n19\t### 2.1 Keys\n",
		" more lines: codedoc lines doc-repo:docs/design.md ", "-27\n")
	if strings.Contains(text, "body omitted") || strings.Contains(text, "\n27\t") {
		t.Errorf("not cut:\n%s", text)
	}
	// A body that fits is not cut.
	c.Budget = 0
	if text := out(t, must(query.Symbol(c, "doc-repo:docs/design.md", query.SymbolOpts{}))); strings.Contains(text,
		"more lines:") || !strings.Contains(text, "63\tGlossary of terms.") {
		t.Errorf("unlimited:\n%s", text)
	}
}

func TestDocumentOverTheBudgetAnswersWithItsHeadings(t *testing.T) {
	c := docWorkspace(t)
	text := budgetedSymbol(t, c, "doc-repo:docs/design.md", 1000)
	mustContain(t, text, "design.md  docs/design.md:1-63  Store design\n10-13  ## 1. Goals  4 lines\n",
		"    51-53  #### Notes  3 lines\n", "design.md is 63 lines, over the budget: its headings are listed; "+
			"codedoc symbol 'docs/design.md#<heading>' prints one, codedoc lines doc-repo:docs/design.md A-B a range\n")
	if strings.Contains(text, "1\t---") {
		t.Errorf("the text of the document is left out:\n%s", text)
	}
}
