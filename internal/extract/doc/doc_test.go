package doc_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/doc"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/testutil"
)

// outline extracts a document and renders one line per symbol:
// qualified name, line span, signature and container.
func outline(t *testing.T, lang, name string, src []byte) (*extract.FileResult, []string) {
	t.Helper()
	ex, ok := extract.For(lang)
	if !ok {
		t.Fatalf("no extractor for %s", lang)
	}
	res, err := ex.Extract(name, src)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, sy := range res.Symbols {
		out = append(out, fmt.Sprintf("%s %q %d-%d %q in %q", sy.Kind, sy.Qualified, sy.Span.StartLine, sy.Span.EndLine,
			sy.SigText, sy.Container))
	}
	return res, out
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(testutil.WorkspaceDir(), "doc-repo", "docs", name))
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func equal(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestMarkdownSections(t *testing.T) {
	res, got := outline(t, parse.Markdown, "docs/design.md", fixture(t, "design.md"))
	equal(t, got, []string{
		`document "" 1-63 "Store design" in ""`,
		`section "1. Goals" 10-13 "## 1. Goals" in ""`,
		`section "2. Model" 15-35 "## 2. Model" in ""`,
		`section "2. Model > 2.1 Keys" 19-27 "### 2.1 Keys" in "2. Model"`,
		`section "2. Model > 2.2 Values" 29-35 "### 2.2 Values" in "2. Model"`,
		`section "2. Model > 2.2 Values > Notes" 33-35 "#### Notes" in "2. Model > 2.2 Values"`,
		`section "6. Delivery" 37-53 "## 6. Delivery" in ""`,
		`section "6. Delivery > 6.1 Ordering" 42-44 "### 6.1 Ordering" in "6. Delivery"`,
		`section "6. Delivery > 6.2 Retries" 46-53 "### 6.2 Retries" in "6. Delivery"`,
		`section "6. Delivery > 6.2 Retries > Notes" 51-53 "#### Notes" in "6. Delivery > 6.2 Retries"`,
		`section "Open questions" 55-58 "Open questions" in ""`,
		`section "Appendix" 60-63 "Appendix" in ""`,
	})
	d := res.Symbols[0]
	if d.Name != "design.md" || d.Doc == nil || !strings.HasPrefix(d.Doc.Text, "How the store package keeps") {
		t.Errorf("document = %q doc %+v", d.Name, d.Doc)
	}
	if goals := res.Symbols[1]; goals.Doc == nil || !strings.HasPrefix(goals.Doc.Text, "The store keeps records") {
		t.Errorf("1. Goals doc = %+v", goals.Doc)
	}
	if model := res.Symbols[2]; model.Doc.Text != "A record has a key and a value." {
		t.Errorf("2. Model doc = %q: want its own text only", model.Doc.Text)
	}
}

func TestMarkdownSpansCutTheSection(t *testing.T) {
	src := fixture(t, "design.md")
	res, _ := outline(t, parse.Markdown, "design.md", src)
	keys := res.Symbols[3]
	body := string(src[keys.Span.StartByte:keys.Span.EndByte])
	if !strings.HasPrefix(body, "### 2.1 Keys\n") || !strings.HasSuffix(body, "codedoc find Get\n```") {
		t.Errorf("2.1 Keys body = %q", body)
	}
	if keys.BodyHash == "" || keys.BodyHash == res.Symbols[4].BodyHash {
		t.Errorf("body hashes = %q, %q", keys.BodyHash, res.Symbols[4].BodyHash)
	}
}

func TestDuplicateHeadingPaths(t *testing.T) {
	_, got := outline(t, parse.Markdown, "d.md", []byte("# A\n## Notes\n### X\n## Notes\n### X\n# A\n"))
	equal(t, got, []string{
		`document "" 1-6 "A" in ""`,
		`section "A" 1-5 "# A" in ""`,
		`section "A > Notes" 2-3 "## Notes" in "A"`,
		`section "A > Notes > X" 3-3 "### X" in "A > Notes"`,
		`section "A > Notes~2" 4-5 "## Notes" in "A"`,
		`section "A > Notes~2 > X" 5-5 "### X" in "A > Notes~2"`,
		`section "A~2" 6-6 "# A" in ""`,
	})
}

func TestMarkdownLinks(t *testing.T) {
	res, _ := outline(t, parse.Markdown, "docs/design.md", fixture(t, "design.md"))
	var got []string
	for _, r := range res.References {
		got = append(got, fmt.Sprintf("%s %s#%s in %q line %d", r.Kind, r.Name, r.Qualifier, r.Enclosing, r.Line))
	}
	equal(t, got, []string{
		`link guide.adoc# in "" line 7`,
		`link notes.rst# in "" line 8`,
		`link ../settings.json# in "6. Delivery" line 40`,
		`link runbooks/retries.md#backoff in "6. Delivery > 6.2 Retries" line 49`,
	})
}

func TestLinkForms(t *testing.T) {
	src := "# T\n[a](https://x.io/a.md) [b](mailto:x@y.z) ![img](img/a.png \"t\") [c](<b.md>)\n" +
		"[ref]: other.md#part\n```\n[code](in/fence.md)\n```\n"
	res, _ := outline(t, parse.Markdown, "t.md", []byte(src))
	var got []string
	for _, r := range res.References {
		got = append(got, r.Name+"#"+r.Qualifier)
	}
	equal(t, got, []string{"img/a.png#", "b.md#", "other.md#part"})
}

func TestMarkdownEdges(t *testing.T) {
	src := "Intro text.\n\n    # indented code\n#hashtag\n####### seven\n# Closed #\n#\n- item\n---\n> quote\n===\n" +
		"~~~~\n# in tilde fence\n~~~\n~~~~\n| a |\n---\nText\n- - -\n"
	res, got := outline(t, parse.Markdown, "e.md", []byte(src))
	equal(t, got, []string{
		`document "" 1-19 "Closed" in ""`,
		`section "Closed" 6-19 "# Closed #" in ""`,
	})
	if res.Symbols[0].Doc.Text != "Intro text." {
		t.Errorf("doc = %q", res.Symbols[0].Doc.Text)
	}
}

func TestFrontMatter(t *testing.T) {
	for name, tc := range map[string]struct{ src, sig, doc string }{
		"title":    {"---\ntitle:  My  doc\n---\n# H\nBody.\n", "My doc", "Body."},
		"dots":     {"---\ndescription: D.\n...\n# H\n", "H", "D."},
		"unclosed": {"---\ntitle: x\n# H\n", "H", "---\ntitle: x"},
		"not yaml": {"---\nhint: [a] [b]\ntitle: \"Join\"\ndescription: Joins.\n---\ntext\n", "Join", "Joins."},
		"no field": {"---\nhint: [a] [b]\n---\ntext\n", "n.md", "text"},
		"none":     {"plain\n", "n.md", "plain"},
	} {
		res, _ := outline(t, parse.Markdown, "n.md", []byte(tc.src))
		d := res.Symbols[0]
		if d.SigText != tc.sig || d.Doc == nil || d.Doc.Text != tc.doc {
			t.Errorf("%s: sig %q doc %+v, want %q %q", name, d.SigText, d.Doc, tc.sig, tc.doc)
		}
	}
}

func TestRSTSections(t *testing.T) {
	_, got := outline(t, parse.RST, "notes.rst", fixture(t, "notes.rst"))
	equal(t, got, []string{
		`document "" 1-29 "Service notes" in ""`,
		`section "Service notes" 1-29 "Service notes" in ""`,
		`section "Service notes > Lookup" 7-20 "Lookup" in "Service notes"`,
		`section "Service notes > Lookup > Caching" 12-20 "Caching" in "Service notes > Lookup"`,
		`section "Service notes > Lookup > Caching > Eviction" 17-20 "Eviction" in "Service notes > Lookup > Caching"`,
		`section "Service notes > Writes" 22-29 "Writes" in "Service notes"`,
	})
}

func TestRSTShortUnderlineIsNoTitle(t *testing.T) {
	_, got := outline(t, parse.RST, "s.rst", []byte("A long title\n===\n\n----\n\nOk\n==\nX\n===\n"))
	equal(t, got, []string{`document "" 1-9 "X" in ""`, `section "X" 8-9 "X" in ""`})
}

func TestAsciiDocSections(t *testing.T) {
	_, got := outline(t, parse.ASCIIDoc, "guide.adoc", fixture(t, "guide.adoc"))
	equal(t, got, []string{
		`document "" 1-22 "Operations guide" in ""`,
		`section "Operations guide" 1-22 "= Operations guide" in ""`,
		`section "Operations guide > Install" 5-12 "== Install" in "Operations guide"`,
		`section "Operations guide > Configure" 14-18 "== Configure" in "Operations guide"`,
		`section "Operations guide > Configure > Modes" 16-18 "=== Modes" in "Operations guide > Configure"`,
		`section "Operations guide > Upgrade" 20-22 "== Upgrade" in "Operations guide"`,
	})
	_, got = outline(t, parse.ASCIIDoc, "m.adoc", []byte("# Md style\n```\n= no\n```\n=======  seven\n=nospace\n"))
	equal(t, got, []string{`document "" 1-6 "Md style" in ""`, `section "Md style" 1-6 "# Md style" in ""`})
}

func TestPlainTextIsOneDocument(t *testing.T) {
	res, got := outline(t, parse.Text, "CHANGES.txt", fixture(t, "CHANGES.txt"))
	equal(t, got, []string{`document "" 1-6 "CHANGES.txt" in ""`})
	if res.Symbols[0].Doc.Text != "Changes" {
		t.Errorf("doc = %q", res.Symbols[0].Doc.Text)
	}
	if res, _ := outline(t, parse.Text, "empty.txt", nil); len(res.Symbols) != 1 || res.Symbols[0].Span.EndLine != 0 {
		t.Errorf("empty document = %+v", res.Symbols)
	}
}

func TestSummary(t *testing.T) {
	long := strings.Repeat("word ", 100) + "end. " + strings.Repeat("tail ", 40)
	if got := doc.Summary(long); !strings.HasSuffix(got, "end.") || len(got) > doc.SummaryBytes {
		t.Errorf("sentence cut = %q", got)
	}
	if got := doc.Summary(strings.Repeat("word ", 200)); len(got) > doc.SummaryBytes || strings.HasSuffix(got, " ") {
		t.Errorf("space cut = %q", got)
	}
	if got := doc.Summary(strings.Repeat("é", 400)); len(got) != doc.SummaryBytes {
		t.Errorf("rune cut length = %d", len(got))
	}
	if got := doc.Summary("  short \n"); got != "short" {
		t.Errorf("short = %q", got)
	}
}

func TestSlugAndNumber(t *testing.T) {
	for in, want := range map[string]string{"6.2 Retries": "62-retries", " Open questions ": "open-questions",
		"What's new_here?": "whats-new_here"} {
		if got := doc.Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"6. Delivery": "6", "6.2 Retries": "6.2", "A.1 Notes": "A.1",
		"A. Appendix": "A", "A note": "", "Goals": "", "10) Ten": "10", "v1.2 release": "", "": ""} {
		if got := doc.Number(in); got != want {
			t.Errorf("Number(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMarkdownSectionsKeepsBodies(t *testing.T) {
	src := "---\ntitle: T\n---\nintro\n# A\na1\n\nSub\n---\ns1\n```\n# no\n```\n## Purpose\nlast"
	var got []string
	for _, s := range doc.MarkdownSections(src) {
		got = append(got, fmt.Sprintf("%d %q %q %q", s.Level, s.Title, s.Heading, s.Body))
	}
	equal(t, got, []string{
		`0 "" "" "intro\n"`,
		`1 "A" "# A" "a1\n\n"`,
		`2 "Sub" "## Sub" "s1\n` + "```" + `\n# no\n` + "```" + `\n"`,
		`2 "Purpose" "## Purpose" "last"`,
	})
	if secs := doc.MarkdownSections(""); len(secs) != 1 || secs[0].Body != "" {
		t.Errorf("empty text = %+v", secs)
	}
}
