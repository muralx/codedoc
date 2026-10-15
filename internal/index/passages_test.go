package index_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// topics are words no fixture file holds; block i of a generated text
// repeats topics[i], so a query for it matches that block alone.
var topics = []string{"alpaca", "bison", "condor", "dingo", "egret", "ferret", "gibbon", "heron"}

// longDoc returns a Go doc comment of one paragraph of about 400 bytes
// per topic, and the file line (counted from 0 at its first line) each
// paragraph starts at.
func longDoc(name string) (text string, starts []int) {
	var b strings.Builder
	line := 0
	for i, topic := range topics {
		if i > 0 {
			b.WriteString("//\n")
			line++
		}
		starts = append(starts, line)
		fmt.Fprintf(&b, "// %s paragraph %d is about the %s.\n", name, i, topic)
		for range 5 {
			fmt.Fprintf(&b, "// The %s does what a %s does, and nothing else is said here at all.\n", topic, topic)
		}
		line += 6
	}
	return b.String(), starts
}

// bodyTopics are the words of the blocks of longBody.
var bodyTopics = []string{"iguana", "jackal", "koala", "lemur", "marmot", "narwhal", "ocelot", "puffin"}

// longBody returns the source of an undocumented Go function with one
// loop of about 330 bytes per body topic, and the line (from 0 at the
// func line) each loop starts at.
func longBody(name string) (text string, starts []int) {
	var b strings.Builder
	fmt.Fprintf(&b, "func %s(items []string) int {\n\ttotal := 0\n", name)
	line := 2
	for _, topic := range bodyTopics {
		starts = append(starts, line)
		fmt.Fprintf(&b, "\tfor _, %s := range items {\n", topic)
		for j := range 4 {
			fmt.Fprintf(&b, "\t\tif len(%s) > %d { // the %s grows the total when it is long\n\t\t\ttotal += len(%s)\n\t\t}\n",
				topic, j, topic, topic)
		}
		b.WriteString("\t}\n")
		line += 14
	}
	b.WriteString("\treturn total\n}\n")
	return b.String(), starts
}

// fakeDims is the size of the fake vectors of these tests: large enough
// that two of the few words of a text rarely share a dimension.
const fakeDims = 4096

// nearest returns the scanned row of the symbol named name for a query,
// failing when the scan does not rank it first.
func nearest(t *testing.T, s *store.Store, query, name string) store.Scored {
	t.Helper()
	q, _ := embed.Query(embed.NewFake(fakeDims), query)
	got, err := s.NearestSymbols(q, store.SearchFilter{}, 3)
	if err != nil || len(got) == 0 {
		t.Fatalf("%+v %v", got, err)
	}
	sy, err := s.SymbolByID(got[0].ID)
	if err != nil || sy.Name != name {
		t.Fatalf("%q: best is %s (%+v), want %s: %v", query, sy.Name, got[0], name, err)
	}
	return got[0]
}

// embedFake embeds everything stale with a fake embedder that records
// its texts.
func embedFake(t *testing.T, s *store.Store) (index.VecResult, []string) {
	t.Helper()
	e := &countingEmbedder{Fake: embed.NewFake(fakeDims)}
	res, err := index.EmbedVectors(s, e, store.VecScope{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return res, e.texts
}

// passageFixture indexes go-lib with a function Wide under a long doc
// comment and an undocumented long function sprawl, and returns the
// repo, the file line of Wide's doc and of sprawl, and the offsets of
// their blocks.
func passageFixture(t *testing.T, s *store.Store) (rep *config.Repo, docLine, bodyLine int, docStarts, bodyStarts []int) {
	t.Helper()
	rep = repoFixture(t, "go-lib")
	doc, docStarts := longDoc("Wide")
	body, bodyStarts := longBody("sprawl")
	// null.go has 8 lines; a blank line follows them.
	appendFile(t, rep.Path, "store/null.go", "\n"+doc+"func Wide() {}\n\n"+body)
	docLine = 10
	bodyLine = docLine + strings.Count(doc, "\n") + 2
	indexRepo(t, s, rep)
	return rep, docLine, bodyLine, docStarts, bodyStarts
}

func TestLongDocIsEmbeddedByPassage(t *testing.T) {
	s := openTemp(t)
	_, docLine, _, starts, _ := passageFixture(t, s)
	res, texts := embedFake(t, s)
	if res.Passages <= res.Symbols {
		t.Fatalf("%+v", res)
	}
	var mine []string
	for _, text := range texts {
		if strings.Contains(text, "func Wide()") {
			mine = append(mine, text)
			if !strings.HasPrefix(text, embed.DocumentPrefix+"store/null.go\nfunc Wide()\n// ") {
				t.Errorf("a doc passage is the comment's lines under path and signature: %q", text)
			}
		}
	}
	if len(mine) < 4 {
		t.Fatalf("%d passages of Wide's doc", len(mine))
	}
	// A query for the fifth paragraph finds the symbol by the passage
	// that holds it, not by the first lines of the doc.
	got := nearest(t, s, "egret", "Wide")
	want := docLine + starts[4]
	if p := got.Passages[0]; p.StartLine > want || p.EndLine < want+5 || p.StartLine <= docLine {
		t.Fatalf("passage %d-%d does not hold paragraph 4 at %d-%d", p.StartLine, p.EndLine, want, want+5)
	}
}

func TestUndocumentedBodyIsEmbeddedByPassage(t *testing.T) {
	s := openTemp(t)
	_, _, bodyLine, _, starts := passageFixture(t, s)
	_, texts := embedFake(t, s)
	if containsText(texts, "func fallback(key string)") {
		t.Fatalf("an undocumented function of one line is trivial and not embedded: %q", texts)
	}
	first, later := 0, 0
	for _, text := range texts {
		switch {
		case strings.HasPrefix(text, embed.DocumentPrefix+"store/null.go\nfunc sprawl(items []string) int {\n\ttotal := 0\n"):
			first++
		case strings.HasPrefix(text, embed.DocumentPrefix+"store/null.go\nfunc sprawl(items []string) int\n"):
			later++
		}
	}
	if first != 1 || later < 3 {
		t.Fatalf("sprawl: %d first passage, %d later ones under the signature", first, later)
	}
	got := nearest(t, s, "ocelot", "sprawl")
	want := bodyLine + starts[6]
	if p := got.Passages[0]; p.StartLine > want || p.EndLine < want+13 || p.StartLine <= bodyLine {
		t.Fatalf("passage %d-%d does not hold the loop at %d-%d", p.StartLine, p.EndLine, want, want+13)
	}
}

func TestOnlyChangedTextsAreEmbeddedAgain(t *testing.T) {
	s := openTemp(t)
	rep, _, bodyLine, _, starts := passageFixture(t, s)
	embedFake(t, s)
	before := nearest(t, s, "ocelot", "sprawl").Passages[0]
	// Lines added above move sprawl: its vectors stay, and their lines
	// follow it.
	editFile(t, rep.Path, "store/null.go", "// NullStore is", "// A new line.\n// Another one.\n// NullStore is")
	indexRepo(t, s, rep)
	if res, texts := embedFake(t, s); res.Symbols != 1 || !containsText(texts, "A new line.") {
		t.Fatalf("only the edited doc is embedded again: %+v %q", res, texts)
	}
	after := nearest(t, s, "ocelot", "sprawl").Passages[0]
	if after.StartLine != before.StartLine+2 || after.EndLine != before.EndLine+2 || after.StartLine > bodyLine+2+starts[6] {
		t.Fatalf("passage %+v did not follow the symbol from %+v", after, before)
	}
	// A changed body is embedded again, every passage of it.
	editFile(t, rep.Path, "store/null.go", "\ttotal := 0\n", "\ttotal := 0 // the alpaca count\n")
	indexRepo(t, s, rep)
	res, texts := embedFake(t, s)
	if res.Symbols != 1 || res.Passages < 4 || !containsText(texts, "the alpaca count") {
		t.Fatalf("the edited body is embedded again: %+v", res)
	}
	for _, text := range texts {
		if !strings.Contains(text, "sprawl") {
			t.Fatalf("an unchanged text was embedded again: %q", text)
		}
	}
	if res, _ := embedFake(t, s); res.Symbols+res.Modules+res.Passages != 0 {
		t.Fatalf("nothing changed: %+v", res)
	}
}

func TestUndocumentedContainerIsEmbeddedByItsMembers(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	appendFile(t, rep.Path, "store/null.go", "\ntype ledger struct {\n\tentries []string\n\ttotal   int\n}\n")
	indexRepo(t, s, rep)
	_, texts := embedFake(t, s)
	if !containsText(texts, "store/null.go\ntype ledger struct\nentries total") {
		t.Fatalf("the container is embedded as its signature and member names: %q", texts)
	}
	if containsText(texts, "type ledger struct {") {
		t.Fatal("the container's body was embedded")
	}
	if containsText(texts, "store/null.go\nledger\nentries []string") {
		t.Fatalf("an undocumented field is trivial and not embedded: %q", texts)
	}
	if p := nearest(t, s, "ledger entries total struct type", "ledger").Passages; len(p) != 1 || p[0].StartLine != 10 || p[0].EndLine != 10 {
		t.Fatalf("the container's passage is its signature line: %+v", p)
	}
}

func TestLongSectionIsEmbeddedByPassage(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "doc-repo")
	var b strings.Builder
	b.WriteString("# Animals\n\nIntro.\n\n## Field notes\n\n")
	for i, topic := range topics {
		fmt.Fprintf(&b, "Note %d is about the %s.\n", i, topic)
		for range 5 {
			fmt.Fprintf(&b, "The %s does what a %s does, and nothing else is said here at all.\n", topic, topic)
		}
		b.WriteString("\n")
	}
	b.WriteString("### Later\n\nShort.\n")
	writeFile(t, rep.Path, "docs/animals.md", []byte(b.String()))
	indexRepo(t, s, rep)
	_, texts := embedFake(t, s)
	if !containsText(texts, "docs/animals.md\nAnimals > Field notes\nNote 0 is about the alpaca.") {
		t.Fatalf("a section passage is its lines under path and heading path: %q", texts)
	}
	// Paragraph i starts at line 7 + 7i; "### Later" is line 63.
	got := nearest(t, s, "ferret", "Field notes")
	if p := got.Passages[0]; p.StartLine > 42 || p.EndLine < 47 || p.EndLine >= 63 || p.StartLine <= 7 {
		t.Fatalf("passage %d-%d does not hold the note at 42-47", p.StartLine, p.EndLine)
	}
	// A short section is one passage: heading and own text, up to its
	// first sub-section.
	if p := nearest(t, s, "Animals Intro", "Animals").Passages; len(p) != 1 || p[0].StartLine != 1 || p[0].EndLine != 4 {
		t.Fatalf("%+v", p)
	}
}

func TestGeneratedFilesKeepUndocumentedBodiesOut(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	writeFile(t, rep.Path, "store/zz_gen.go", []byte("// Code generated by a tool. DO NOT EDIT.\n\npackage store\n\n"+
		"func genUndocumented() int { return 1 }\n\n// GenDocumented is documented.\nfunc GenDocumented() int { return 2 }\n"))
	indexRepo(t, s, rep)
	_, texts := embedFake(t, s)
	if containsText(texts, "genUndocumented") || !containsText(texts, "GenDocumented is documented.") {
		t.Fatalf("%q", texts)
	}
	if n, total, _ := index.VectorCounts(s, 0, fakeDims); n != total {
		t.Fatalf("the generated body is not counted as missing: %d/%d", n, total)
	}
}

func TestLimitedPassDefersManyPassages(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	embedFake(t, s)
	body, _ := longBody("sprawl")
	appendFile(t, rep.Path, "store/null.go", "\n"+body)
	indexRepo(t, s, rep)
	res, err := index.EmbedVectors(s, embed.NewFake(fakeDims), store.VecScope{}, 1)
	if err != nil || res.Deferred != 1 || res.Symbols != 0 {
		t.Fatalf("one stale symbol of many passages is over a limit of one: %+v %v", res, err)
	}
	if res, err := index.EmbedVectors(s, embed.NewFake(fakeDims), store.VecScope{}, 8); err != nil || res.Symbols != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}

// A dependency's long doc is split by the lines of its doc text, read
// from the row: they are the comment's lines when there are as many.
func TestEmbedAPIVectorsSplitLongDocs(t *testing.T) {
	s := openTemp(t)
	_, docLine, _, starts, _ := passageFixture(t, s)
	e := &countingEmbedder{Fake: embed.NewFake(fakeDims)}
	row, err := s.Repos()
	if err != nil || len(row) != 1 {
		t.Fatalf("%+v %v", row, err)
	}
	res, err := index.EmbedAPIVectors(s, e, []int64{row[0].ID}, 0)
	if err != nil || res.Passages <= res.Symbols {
		t.Fatalf("%+v %v", res, err)
	}
	if containsText(e.texts, "sprawl") {
		t.Fatal("an undocumented body was embedded for a dependency")
	}
	if !containsText(e.texts, "store/null.go\nfunc Wide()\nWide paragraph 0 is about the alpaca.") {
		t.Fatalf("a passage of the doc text under path and signature: %q", e.texts)
	}
	got := nearest(t, s, "egret", "Wide")
	want := docLine + starts[4]
	if p := got.Passages[0]; p.StartLine > want || p.EndLine < want+5 || p.StartLine <= docLine {
		t.Fatalf("passage %d-%d does not hold paragraph 4 at %d", p.StartLine, p.EndLine, want)
	}
}
