package query_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/testutil"
)

// bigDoc is a document whose one long section has no sentence end: 300
// lines of filler under "## Huge", then a short section.
func bigDoc() string {
	var b strings.Builder
	b.WriteString("# Big\n\n## Huge\n\n")
	for i := range 300 {
		fmt.Fprintf(&b, "line %d of the huge section with some filler text to take bytes\n", i)
	}
	b.WriteString("\n## After\n\ntail\n")
	return b.String()
}

// bigDocWorkspace indexes a repo "big" holding bigDoc as docs/big.md.
func bigDocWorkspace(t *testing.T, more map[string]string) *query.Ctx {
	t.Helper()
	files := map[string]string{"docs/big.md": bigDoc()}
	for p, text := range more {
		files[p] = text
	}
	return indexRepos(t, &config.Repo{Name: "big", Path: testutil.SourceRepo(t, "big", files)})
}

// budgeted renders a response under a byte budget.
func budgeted(t *testing.T, r *output.Response, budget int) string {
	t.Helper()
	var b bytes.Buffer
	if _, err := output.Render(&b, r, "text", budget); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestOutlineUnderATinyBudgetStillNamesItsSection(t *testing.T) {
	c := bigDocWorkspace(t, nil)
	c.Budget = 70
	text := budgeted(t, must(query.Outline(c, "big:docs/big.md", query.OutlineOpts{})).resp, 70)
	if len(text) > 70 || text != "== docs/big.md, 308 lines (3)\n... 3 more; raise --budget\n" {
		t.Fatalf("%d bytes:\n%s", len(text), text)
	}
}

func TestCandidatesOfAnAmbiguousHeadingFitASmallBudget(t *testing.T) {
	c := bigDocWorkspace(t, nil)
	c.Strict = true
	resp, err := query.Symbol(c, "big:docs/big.md#e", query.SymbolOpts{})
	if !errors.Is(err, query.ErrAmbiguous) {
		t.Fatalf("err = %v", err)
	}
	text := budgeted(t, resp, 400)
	// A candidate is its heading path, its place and its heading: the
	// paragraph under it, however long, stays out.
	if text != "== candidates (2)\ndocs/big.md\n  Big > Huge  :3-304  ## Huge\n  Big > After  :306-308  ## After\n" {
		t.Fatalf("%d bytes:\n%s", len(text), text)
	}
}

func TestOutlineOfADocumentTooLargeToSectionSaysSo(t *testing.T) {
	huge := "# Huge\n\n" + strings.Repeat("a line of a document that is too large to section\n", 45000)
	c := bigDocWorkspace(t, map[string]string{"docs/huge.md": huge})
	text := out(t, must(query.Outline(c, "big:docs/huge.md", query.OutlineOpts{})))
	mustContain(t, text, "== docs/huge.md, 45002 lines (0)\n",
		"docs/huge.md is too large to section (2.1M, over 2.0M): read it with codedoc lines big:docs/huge.md 1-200")
	if text = out(t, must(query.Outline(c, "big:docs/big.md", query.OutlineOpts{}))); strings.Contains(text, "too large") {
		t.Errorf("a sectioned document is not too large:\n%s", text)
	}
}
