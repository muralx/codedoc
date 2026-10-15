package query_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func TestDocumentOutlineContinuationNamesTheHeadingPath(t *testing.T) {
	c := docWorkspace(t)
	c.Offset = 4
	text := out(t, must(query.Outline(c, "doc-repo:docs/design.md", query.OutlineOpts{})))
	mustContain(t, text, "\n    33-35  #### Notes  3 lines\n", "docs/design.md, 63 lines continues under 2. Model > 2.2 Values\n")
	c.Offset = 2
	text = out(t, must(query.Outline(c, "doc-repo:docs/design.md", query.OutlineOpts{})))
	mustContain(t, text, "continues under 2. Model\n")
	c.Offset = 1
	if text = out(t, must(query.Outline(c, "doc-repo:docs/design.md", query.OutlineOpts{}))); strings.Contains(text, "continues under") {
		t.Errorf("a page that starts at a top-level heading continues under nothing:\n%s", text)
	}
}

// offsetHint matches the more line of a cut section.
var offsetHint = regexp.MustCompile(`(?m)^\d+ more: rerun with --offset (\d+)$`)

func TestOffsetHintsLeadToTheEndOfAList(t *testing.T) {
	c := indexedWorkspace(t)
	whole := out(t, must(query.Outline(c, "go-lib:store/store.go", query.OutlineOpts{})))
	want := strings.Split(strings.TrimSuffix(whole, "\n"), "\n")[2:]
	var got []string
	for c.Offset = 0; ; {
		page := budgeted(t, must(query.Outline(c, "go-lib:store/store.go", query.OutlineOpts{})).resp, 420)
		lines := strings.Split(strings.TrimSuffix(page, "\n"), "\n")
		m := offsetHint.FindStringSubmatch(page)
		if m == nil {
			got = append(got, lines[2:]...)
			break
		}
		got = append(got, lines[2:len(lines)-1]...)
		next, _ := strconv.Atoi(m[1])
		if next <= c.Offset {
			t.Fatalf("offset %d continues at %d:\n%s", c.Offset, next, page)
		}
		c.Offset = next
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(want) < 5 {
		t.Fatalf("pages:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
