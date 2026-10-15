package doc_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/parse"
)

func TestMarkdownHeadingsInsideHTMLCommentsAreNoSections(t *testing.T) {
	src := "# Top\n\n<!--\n## hidden\n\nHidden title\n============\n-->\n\n## Shown\n\n" +
		"text <!-- inline --> more\n\n## After inline\n\n<!-- one line ## not -->\n" +
		"para <!-- opens here\n## still hidden\n```\n## hidden in a fence in a comment\n```\ncloses --> tail\n\n" +
		"## Code span\n\nThe marker `<!--` in a code span opens nothing.\n\n### Kept\n\n" +
		"```html\n<!--\n```\n\n## After fence\n\n    <!-- indented code\n\n## After code\n\n<!-- never closed\n\n## Lost\n"
	_, got := outline(t, parse.Markdown, "c.md", []byte(src))
	equal(t, got, []string{
		`document "" 1-42 "Top" in ""`,
		`section "Top" 1-42 "# Top" in ""`,
		`section "Top > Shown" 10-12 "## Shown" in "Top"`,
		`section "Top > After inline" 14-22 "## After inline" in "Top"`,
		`section "Top > Code span" 24-32 "## Code span" in "Top"`,
		`section "Top > Code span > Kept" 28-32 "### Kept" in "Top > Code span"`,
		`section "Top > After fence" 34-36 "## After fence" in "Top"`,
		`section "Top > After code" 38-42 "## After code" in "Top"`,
	})
}

func TestMarkdownCommentClosedOnItsLineHidesNothingAfterIt(t *testing.T) {
	src := "<!-- a --> <!-- b\n# Hidden\n--> <!-- c -->\n# One\n\n<!-- d -->\n## Two\n"
	_, got := outline(t, parse.Markdown, "c.md", []byte(src))
	equal(t, got, []string{
		`document "" 1-7 "One" in ""`,
		`section "One" 4-7 "# One" in ""`,
		`section "One > Two" 7-7 "## Two" in "One"`,
	})
}
