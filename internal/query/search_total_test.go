package query_test

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
)

// sectionTotal matches the count in a section title.
var sectionTotal = regexp.MustCompile(`(?m)^== search .* \((\d+)\)$`)

// searchPage renders one page of a search and returns its text, the
// total its title reports and the keys it lists.
func searchPage(t *testing.T, c *query.Ctx, text string, offset, limit int) (string, string, []string) {
	t.Helper()
	c.Offset = offset
	r := search(t, c, text, query.SearchOpts{Limit: limit})
	var b bytes.Buffer
	if _, err := output.Render(&b, r, "text", 0); err != nil {
		t.Fatal(err)
	}
	m := sectionTotal.FindStringSubmatch(b.String())
	if m == nil {
		t.Fatalf("no search title in\n%s", b.String())
	}
	return b.String(), m[1], keys(r)
}

// pagesOf follows the --offset of each more line of a search to its
// end and returns the keys of every page in order; every page must
// report the same total.
func pagesOf(t *testing.T, c *query.Ctx, text string, limit int) (string, []string) {
	t.Helper()
	next := regexp.MustCompile(`(?m)^\d+ more: rerun with --offset (\d+)$`)
	var all []string
	total, offset := "", 0
	for range 100 {
		page, n, ks := searchPage(t, c, text, offset, limit)
		if total == "" {
			total = n
		}
		if n != total {
			t.Fatalf("offset %d reports total %s, the first page %s\n%s", offset, n, total, page)
		}
		all = append(all, ks...)
		m := next.FindStringSubmatch(page)
		if m == nil {
			return total, all
		}
		if offset, _ = strconv.Atoi(m[1]); offset == 0 {
			t.Fatalf("a more line that continues nowhere:\n%s", page)
		}
	}
	t.Fatal("search never ends")
	return "", nil
}

func TestSearchTotalIsTheSameOnEveryPage(t *testing.T) {
	c := workspaceOf(t, "go-lib", "go-app")
	_, total, whole := searchPage(t, c, "store value key get", 0, 500)
	if len(whole) < 7 {
		t.Fatalf("too few hits to page through: %v", whole)
	}
	page, first, ks := searchPage(t, c, "store value key get", 0, 3)
	if first != total || len(ks) != 3 || !strings.Contains(page, " more: rerun with --offset 3\n") {
		t.Fatalf("first page of 3: total %s, want %s\n%s", first, total, page)
	}
	got, all := pagesOf(t, c, "store value key get", 3)
	if got != total || strings.Join(all, "\n") != strings.Join(whole, "\n") {
		t.Fatalf("pages of 3 (total %s, want %s):\n%s\nwant:\n%s", got, total, strings.Join(all, "\n"), strings.Join(whole, "\n"))
	}
}

func TestSearchTotalWithDependenciesIsTheSameOnEveryPage(t *testing.T) {
	c := withDeps(depsWorkspace(t))
	_, total, whole := searchPage(t, c, "server client request", 0, 500)
	got, all := pagesOf(t, c, "server client request", 2)
	if len(whole) < 5 || got != total || strings.Join(all, "\n") != strings.Join(whole, "\n") {
		t.Fatalf("pages of 2 (total %s, want %s):\n%s\nwant:\n%s", got, total, strings.Join(all, "\n"), strings.Join(whole, "\n"))
	}
	if !strings.Contains(strings.Join(whole, "\n"), "npm/@acme/sdk@1.2.0") {
		t.Fatalf("no dependency hit among\n%s", strings.Join(whole, "\n"))
	}
}
