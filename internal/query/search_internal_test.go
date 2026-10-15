package query

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

func TestFTSQuery(t *testing.T) {
	if got := ftsQuery(searchTerms(`Token "refresh"-retry a token`)); got != `"token"* OR "refresh"* OR "retry"*` {
		t.Fatal(got)
	}
	if got := ftsQuery(searchTerms("*()")); got != "" {
		t.Fatal(got)
	}
	// A query of stop words alone keeps them.
	if got := ftsQuery(searchTerms("How is a")); got != `"how"* OR "is"* OR "a"` {
		t.Fatal(got)
	}
}

func TestSnippetPicksBestSentence(t *testing.T) {
	doc := "Service answers lookups. It falls back to defaults when a key is missing!\n\nSee Mode for the environment."
	if got := snippet(doc, queryWords("missing keys")); got != "It falls back to defaults when a key is missing!" {
		t.Fatal(got)
	}
	if got := snippet(doc, queryWords("zebra")); got != "Service answers lookups." {
		t.Fatal(got)
	}
	if got := snippet("", []string{"x"}); got != "" {
		t.Fatal(got)
	}
	if got := sentences("One. Two\nlines.\n\nThree"); !reflect.DeepEqual(got, []string{"One.", "Two lines.", "Three"}) {
		t.Fatal(got)
	}
}

func TestFuseReciprocalRank(t *testing.T) {
	a, b, m := hit{id: 1}, hit{id: 2}, hit{module: true, id: 1}
	none := func(hit) bool { return false }
	l := func(hits ...hit) rankedList { return ordinal(hits) }
	fuse := func(isTest func(hit) bool, lists ...rankedList) []hit {
		order, scores := fuse(isTest, lists...)
		if len(scores) != len(order) {
			t.Fatalf("%d scores for %d hits", len(scores), len(order))
		}
		return order
	}
	got := fuse(none, l(a, b), l(b, a), l(m))
	// a and b tie on score and best rank; the module ranks first only in
	// one list and falls behind both.
	if !reflect.DeepEqual(got, []hit{a, b, m}) {
		t.Fatal(got)
	}
	if got := fuse(none, l(a), l(b, m), l(b)); !reflect.DeepEqual(got, []hit{b, a, m}) {
		t.Fatal(got)
	}
	if got := fuse(none, l(m), l(b)); !reflect.DeepEqual(got, []hit{b, m}) {
		t.Fatal(got)
	}
	// a and b tie on score; a test hit falls behind production code.
	testA := func(h hit) bool { return h == a }
	if got := fuse(testA, l(a, b), l(b, a)); !reflect.DeepEqual(got, []hit{b, a}) {
		t.Fatal(got)
	}
}

func TestSearchValuesCoverKindsAndLanguages(t *testing.T) {
	for _, k := range []extract.Kind{extract.KindFunction, extract.KindMethod, extract.KindClass, extract.KindStruct,
		extract.KindInterface, extract.KindTrait, extract.KindEnum, extract.KindTypeAlias, extract.KindConst,
		extract.KindVar, extract.KindField, extract.KindDocument, extract.KindSection} {
		if !slices.Contains(searchKinds, string(k)) {
			t.Errorf("kind %s missing", k)
		}
	}
	for _, l := range []string{parse.Go, parse.TypeScript, parse.TSX, parse.JavaScript, parse.Python, parse.Java, parse.Markdown, parse.RST,
		parse.ASCIIDoc, parse.Text} {
		if !slices.Contains(searchLangs, l) || !slices.Contains(searchLangs, parse.Family(l)) {
			t.Errorf("language %s missing", l)
		}
	}
}

func TestLangHelpNamesEveryValue(t *testing.T) {
	values := LangValues()
	if len(values) != 13 {
		t.Fatalf("%v", values)
	}
	for _, v := range values {
		if !strings.Contains(LangHelp, v) {
			t.Errorf("LangHelp lacks %s", v)
		}
	}
}

func TestReadmeLeadIsTheFirstProseParagraph(t *testing.T) {
	doc := "# kit\n\n<div align=\"center\">\n  <img src=\"logo.png\" />\n</div>\n\n[![ci](x)](y)\n![logo](z)\n\n" +
		"> **Note.** Small toolkit\n> for Acme services.\n\nMore text.\n"
	if got := readmeLead(doc); got != "**Note.** Small toolkit for Acme services." {
		t.Errorf("lead: %q", got)
	}
	long := strings.Repeat("word ", 100)
	got := readmeLead(long)
	if len(got) > leadMax+4 || !strings.HasSuffix(got, "word ...") {
		t.Errorf("a long paragraph is cut at a word: %q", got)
	}
	if got := readmeLead(strings.Repeat("x", 400)); got != strings.Repeat("x", leadMax)+" ..." {
		t.Errorf("a paragraph without spaces is cut at the limit: %q", got)
	}
	if readmeLead("") != "" {
		t.Error("no README, no lead")
	}
}
