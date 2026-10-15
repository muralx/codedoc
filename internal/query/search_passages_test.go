package query_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/testutil"
)

// animals are words no fixture holds: block i of a generated text
// repeats one of them, so a query for it matches that block alone.
var animals = []string{"alpaca", "bison", "condor", "dingo", "egret", "ferret", "gibbon", "heron"}

// passageWorkspace indexes go-lib with, appended to store/null.go from
// line 10, a function Wide under a doc comment of one six-line paragraph
// per animal (a "//" line between them) and, in docs/animals.md, a
// section "Field notes" of one six-line paragraph per animal from line 7.
func passageWorkspace(t *testing.T) *query.Ctx {
	t.Helper()
	root := testutil.CopyFixture(t, "go-lib")
	var doc, notes strings.Builder
	notes.WriteString("# Animals\n\nIntro.\n\n## Field notes\n\n")
	for i, a := range animals {
		if i > 0 {
			doc.WriteString("//\n")
		}
		fmt.Fprintf(&doc, "// Wide paragraph %d is about the %s.\n", i, a)
		fmt.Fprintf(&notes, "Note %d is about the %s.\n", i, a)
		for range 5 {
			fmt.Fprintf(&doc, "// The %s does what a %s does, and nothing else is said here at all.\n", a, a)
			fmt.Fprintf(&notes, "The %s does what a %s does, and nothing else is said here at all.\n", a, a)
		}
		notes.WriteString("\n")
	}
	f, err := os.OpenFile(filepath.Join(root, "store/null.go"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n" + doc.String() + "func Wide() {}\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs/animals.md"), []byte(notes.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	c := indexRepos(t, &config.Repo{Name: "go-lib", Path: root})
	c.Embedder = embed.NewFake(4096)
	return c
}

// passagesOf returns the passages listed in the entry whose key ends
// with suffix, failing when the answer has not exactly one such entry.
func passagesOf(t *testing.T, r *output.Response, suffix string) []query.Passage {
	t.Helper()
	var found []output.Entry
	for _, e := range r.Sections[0].Entries {
		if strings.HasSuffix(e.Key, suffix) {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d entries for %s in\n%s", len(found), suffix, render(t, r))
	}
	p, _ := found[0].Extra["passages"].([]query.Passage)
	return p
}

func TestSearchReportsTheMatchingPassageOfALongDoc(t *testing.T) {
	c := passageWorkspace(t)
	r := search(t, c, "egret", query.SearchOpts{Kind: "function"})
	if got := keys(r); len(got) == 0 || !strings.HasSuffix(got[0], ":Wide") {
		t.Fatalf("%v", got)
	}
	// Paragraph 4 of the doc is lines 38-43: 10 + 4*7.
	p := passagesOf(t, r, ":Wide")
	if len(p) == 0 || len(p) > 3 || p[0].StartLine > 38 || p[0].EndLine < 43 || p[0].StartLine <= 10 || p[0].Score <= 0 {
		t.Fatalf("%+v", p)
	}
	for i := 1; i < len(p); i++ {
		if p[i].Score > p[i-1].Score {
			t.Fatalf("passages are not best first: %+v", p)
		}
	}
}

func TestSearchReportsASymbolOnceWithItsBestPassages(t *testing.T) {
	c := passageWorkspace(t)
	// Two paragraphs far apart match equally: one entry, both passages.
	// Paragraph 2 is lines 24-29 and paragraph 5 lines 45-50.
	r := search(t, c, "condor ferret", query.SearchOpts{})
	p := passagesOf(t, r, ":Wide")
	if len(p) != 2 {
		t.Fatalf("%+v", p)
	}
	condor, ferret := false, false
	for _, x := range p {
		condor = condor || (x.StartLine <= 24 && x.EndLine >= 29)
		ferret = ferret || (x.StartLine <= 45 && x.EndLine >= 50)
	}
	if !condor || !ferret {
		t.Fatalf("the passages of both paragraphs (24-29, 45-50): %+v", p)
	}
	// A passage far from the query is left out of a hit's passages.
	if p := passagesOf(t, search(t, c, "condor", query.SearchOpts{}), ":Wide"); len(p) != 1 {
		t.Fatalf("only the matching passage: %+v", p)
	}
}

func TestSearchReportsTheMatchingPassageOfASection(t *testing.T) {
	c := passageWorkspace(t)
	r := search(t, c, "ferret", query.SearchOpts{Kind: "section"})
	// Note 5 is lines 42-47: 7 + 5*7.
	p := passagesOf(t, r, "Field notes")
	if len(p) == 0 || p[0].StartLine > 42 || p[0].EndLine < 47 || p[0].StartLine <= 7 {
		t.Fatalf("%+v\n%s", p, render(t, r))
	}
}

func TestSearchPassagesAreInJSONAndNotInText(t *testing.T) {
	c := passageWorkspace(t)
	r := search(t, c, "egret", query.SearchOpts{Limit: 1, Kind: "function"})
	var b bytes.Buffer
	if _, err := output.Render(&b, r, "json", 0); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Sections []struct {
			Entries []struct {
				Extra struct {
					Passages []map[string]float64 `json:"passages"`
				} `json:"extra"`
			} `json:"entries"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, b.String())
	}
	ps := got.Sections[0].Entries[0].Extra.Passages
	if len(ps) == 0 || len(ps[0]) != 3 || ps[0]["start"] > 38 || ps[0]["end"] < 43 || ps[0]["score"] <= 0 {
		t.Fatalf("%+v\n%s", ps, b.String())
	}
	if text := render(t, r); strings.Contains(text, "passages") {
		t.Fatalf("text output changed:\n%s", text)
	}
}

func TestKeywordOnlyHitsCarryNoPassages(t *testing.T) {
	c := passageWorkspace(t)
	c.Embedder = nil
	r := search(t, c, "egret", query.SearchOpts{})
	if p := passagesOf(t, r, ":Wide"); p != nil {
		t.Fatalf("%+v", p)
	}
}
