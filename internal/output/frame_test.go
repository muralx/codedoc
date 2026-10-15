package output_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/output"
)

func header() output.Header {
	return output.Header{Repo: "auth", Branch: "main", Commit: "3f2a1c9d0e", Dirty: 2, Fresh: true}
}

func tenEntries() *output.Response {
	var es []output.Entry
	for i := 0; i < 10; i++ {
		es = append(es, output.Entry{
			Key: fmt.Sprintf("auth:go:pkg:F%d", i), Loc: fmt.Sprintf("a.go:%d", 10+i),
			Text: fmt.Sprintf("func F%d()", i), Doc: "Does a thing.",
		})
	}
	return &output.Response{Header: header(), Sections: []output.Section{
		{Title: "callers", Entries: es, Offset: 20},
	}}
}

func render(t *testing.T, r *output.Response, format string, budget int) (string, int) {
	t.Helper()
	var buf bytes.Buffer
	n, err := output.Render(&buf, r, format, budget)
	if err != nil {
		t.Fatal(err)
	}
	return buf.String(), n
}

func TestTextLayout(t *testing.T) {
	r := &output.Response{Header: header(), Note: "index is stale", Sections: []output.Section{{
		Title: "callers",
		Entries: []output.Entry{{Key: "k", Loc: "a.go:3", Text: "func A()", Doc: "Does A.", Tags: []string{"ambiguous", "test"},
			Body: "func A() {\n}\n", BodyStart: 3}},
	}}}
	got, omitted := render(t, r, "text", 0)
	want := "dirty\n== callers (1)\n" +
		"k  a.go:3  func A()  Does A.  [ambiguous] [test]\n3\tfunc A() {\n4\t}\nindex is stale\n"
	if got != want || omitted != 0 {
		t.Fatalf("got %q", got)
	}
}

func TestTreeDepthIndentsTextAndShowsInJSON(t *testing.T) {
	r := &output.Response{Header: header(), Sections: []output.Section{{Title: "callers", Entries: []output.Entry{
		{Key: "a", Loc: "a.go:1"}, {Key: "b", Loc: "b.go:2", Depth: 1}, {Key: "c", Loc: "c.go:3", Depth: 2}}}}}
	got, _ := render(t, r, "text", 0)
	if !strings.HasSuffix(got, "a  a.go:1\n  b  b.go:2\n    c  c.go:3\n") {
		t.Fatalf("%q", got)
	}
	js, _ := render(t, r, "json", 0)
	if !strings.Contains(js, `"key":"c","loc":"c.go:3","depth":2`) || strings.Contains(js, `"key":"a","loc":"a.go:1","depth"`) {
		t.Fatal(js)
	}
}

func TestTextHeaderWithoutGit(t *testing.T) {
	r := &output.Response{Header: output.Header{Repo: "plain"}}
	got, _ := render(t, r, "", 0)
	if got != "fresh:no\n" {
		t.Fatalf("got %q", got)
	}
}

func TestTextBudgetStopsAtFittingEntries(t *testing.T) {
	r := tenEntries()
	full, _ := render(t, r, "text", 0)
	lines := strings.Split(full, "\n")
	budget := 0 // header, section title, the path of the file group and four entries
	for _, l := range lines[:7] {
		budget += len(l) + 1
	}
	next := r.Sections[0].Offset + 4
	more := fmt.Sprintf("6 more: rerun with --offset %d", next)
	budget += len(more) + 1 + 2 // the more line, plus slack for its worst-case reservation
	got, omitted := render(t, r, "text", budget)
	if omitted != 6 || len(got) > budget {
		t.Fatalf("omitted %d len %d budget %d\n%s", omitted, len(got), budget, got)
	}
	gotLines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(gotLines) != 8 || gotLines[2] != "a.go" || gotLines[6] != "  auth:go:pkg:F3  :13  func F3()  Does a thing." {
		t.Fatalf("got\n%s", got)
	}
	if gotLines[7] != more {
		t.Fatalf("more line %q, want %q", gotLines[7], more)
	}
}

func TestTextMoreLineCountsFromSectionOffset(t *testing.T) {
	for _, offset := range []int{0, 7, 120} {
		r := tenEntries()
		r.Sections[0].Offset = offset
		got, omitted := render(t, r, "text", 300)
		written := 10 - omitted
		want := fmt.Sprintf("%d more: rerun with --offset %d\n", omitted, offset+written)
		if omitted == 0 || !strings.HasSuffix(got, want) || len(got) > 300 {
			t.Fatalf("offset %d: got %q, want suffix %q", offset, got, want)
		}
	}
}

func manySections(n, per int) *output.Response {
	r := &output.Response{Header: header(), Note: "index is stale; run codedoc index"}
	for i := 0; i < n; i++ {
		s := output.Section{Title: fmt.Sprintf("sec%d", i)}
		for j := 0; j < per; j++ {
			s.Entries = append(s.Entries, output.Entry{Key: fmt.Sprintf("k%d_%d", i, j), Loc: "a.go:1", Text: "func X()"})
		}
		r.Sections = append(r.Sections, s)
	}
	return r
}

func TestTextMultiSectionBudgetIsHard(t *testing.T) {
	got, omitted := render(t, manySections(6, 20), "text", 400)
	if len(got) > 400 {
		t.Fatalf("len %d > 400\n%s", len(got), got)
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	last := lines[len(lines)-1]
	if last != "index is stale; run codedoc index" || omitted == 0 {
		t.Fatalf("note missing or nothing omitted (%d)\n%s", omitted, got)
	}
	summary := lines[len(lines)-2]
	if !strings.HasPrefix(summary, "... ") || !strings.Contains(summary, "more sections (titles: ") ||
		!strings.HasSuffix(summary, "sec5); rerun with --out FILE") {
		t.Fatalf("summary line %q\n%s", summary, got)
	}
	if !strings.Contains(got, "\n... ") || !strings.Contains(got, " more: rerun with --offset ") {
		t.Fatalf("cut section has no more line\n%s", got)
	}
}

func TestTextNeverExceedsBudget(t *testing.T) {
	for budget := 60; budget < 3000; budget += 13 {
		r := manySections(6, 20)
		r.Sections[2].Entries[3].Body = strings.Repeat("x := 1\n", 30)
		r.Sections[2].Entries[3].BodyStart = 4
		got, _ := render(t, r, "text", budget)
		if len(got) > budget {
			t.Fatalf("budget %d: len %d\n%s", budget, len(got), got)
		}
	}
}

func TestTextNoteThatDoesNotFitIsLeftOutWhole(t *testing.T) {
	r := &output.Response{Header: header(), Note: strings.Repeat("stale ", 100)}
	if got, _ := render(t, r, "text", 100); got != output.HeaderLine(r.Header) {
		t.Fatalf("len %d\n%q", len(got), got)
	}
}

func TestTextBodyDroppedWhenItDoesNotFit(t *testing.T) {
	body := strings.Repeat("\treturn nil // padding padding padding\n", 100)
	r := &output.Response{Header: header(), Sections: []output.Section{{
		Title:   "symbol",
		Entries: []output.Entry{{Key: "k", Loc: "a.go:1", Text: "func A() error", Body: body, BodyStart: 1, BodyEnd: 100}},
	}}}
	got, omitted := render(t, r, "text", 500)
	if omitted != 0 || strings.Contains(got, "padding") {
		t.Fatalf("omitted %d\n%s", omitted, got)
	}
	if !strings.Contains(got, "k  a.go:1  func A() error  [body omitted: lines 1-100; use --out]\n") {
		t.Fatalf("got %q", got)
	}
}

func TestTextTinyBudgetKeepsHeaderAndCount(t *testing.T) {
	got, omitted := render(t, tenEntries(), "text", 1)
	if omitted != 10 || got != output.HeaderLine(header())+"... 10 more\n" {
		t.Fatalf("omitted %d got %q", omitted, got)
	}
}

func TestSummaryPointsAtTheFullResult(t *testing.T) {
	var buf strings.Builder
	r := tenEntries()
	r.Sections = append(r.Sections, output.Section{Title: "second", Entries: r.Sections[0].Entries[:1]})
	r.Sections[0].Entries[0].Body, r.Sections[0].Entries[0].BodyStart = strings.Repeat("x\n", 100), 1
	if _, err := output.RenderSummary(&buf, r, 200); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "--out") || strings.Contains(got, "--offset") || !strings.Contains(got, "in the full result") {
		t.Fatalf("%q", got)
	}
}

func TestHeadRef(t *testing.T) {
	if got := output.HeadRef("main", "3f2a1c9d0e"); got != "main@3f2a1c9" {
		t.Fatal(got)
	}
	if got := output.HeadRef("", ""); got != "-@-" {
		t.Fatal(got)
	}
}

func TestCheckFormat(t *testing.T) {
	for _, f := range []string{"", "text", "json"} {
		if err := output.CheckFormat(f); err != nil {
			t.Fatalf("%q: %v", f, err)
		}
	}
	if err := output.CheckFormat("yaml"); err == nil {
		t.Fatal("expected error")
	}
}

func TestBudgetZeroRendersEverything(t *testing.T) {
	for _, f := range []string{"text", "json"} {
		got, omitted := render(t, tenEntries(), f, 0)
		if omitted != 0 || !strings.Contains(got, "F9") {
			t.Fatalf("%s: omitted %d", f, omitted)
		}
	}
}

type jsonResp struct {
	Header   map[string]any
	Sections []struct {
		Title      string
		Omitted    int
		NextOffset *int `json:"next_offset"`
		Entries    []map[string]any
	}
}

func decode(t *testing.T, s string) jsonResp {
	t.Helper()
	var r jsonResp
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		t.Fatalf("%v\n%s", err, s)
	}
	return r
}

func TestJSONRoundTripsWithOmittedPerSection(t *testing.T) {
	r := tenEntries()
	r.Sections[0].Entries[0].Extra = map[string]any{"confidence": "exact"}
	got, omitted := render(t, r, "json", 900)
	if len(got) > 900 || omitted == 0 {
		t.Fatalf("len %d omitted %d", len(got), omitted)
	}
	d := decode(t, got)
	if d.Header["repo"] != "auth" || d.Sections[0].Omitted != omitted || len(d.Sections[0].Entries)+omitted != 10 {
		t.Fatalf("%+v", d)
	}
	if d.Sections[0].Entries[0]["extra"].(map[string]any)["confidence"] != "exact" {
		t.Fatalf("%+v", d.Sections[0].Entries[0])
	}
	next := d.Sections[0].NextOffset
	if want := r.Sections[0].Offset + len(d.Sections[0].Entries); next == nil || *next != want {
		t.Fatalf("next_offset %v, want %d", next, want)
	}
}

func TestJSONNeverExceedsBudget(t *testing.T) {
	for budget := 600; budget < 6000; budget += 17 {
		r := manySections(6, 20)
		r.Sections[1].Entries[0].Body = strings.Repeat("x := 1\n", 30)
		r.Sections[1].Entries[0].BodyStart = 4
		got, omitted := render(t, r, "json", budget)
		if len(got) > budget {
			t.Fatalf("budget %d: len %d", budget, len(got))
		}
		d := decode(t, got)
		kept := 0
		for _, s := range d.Sections {
			kept += len(s.Entries)
		}
		if kept+omitted != 120 || len(d.Sections) != 6 {
			t.Fatalf("budget %d: kept %d omitted %d", budget, kept, omitted)
		}
	}
}

func TestJSONMultiSectionCollapsesAfterCut(t *testing.T) {
	got, _ := render(t, manySections(6, 20), "json", 1500)
	d := decode(t, got)
	if len(got) > 1500 || d.Sections[0].Omitted == 0 && d.Sections[1].Omitted == 0 {
		t.Fatalf("len %d\n%s", len(got), got)
	}
	last := d.Sections[5]
	if len(last.Entries) != 0 || last.Omitted != 20 || last.NextOffset == nil || *last.NextOffset != 0 {
		t.Fatalf("%+v", last)
	}
}

func TestJSONDropsBodiesBeforeEntries(t *testing.T) {
	body := strings.Repeat("x := 1\n", 200)
	r := &output.Response{Header: header(), Sections: []output.Section{{Title: "s", Entries: []output.Entry{
		{Key: "a", Body: body, BodyStart: 1, BodyEnd: 200}, {Key: "b", Body: body, BodyStart: 1, BodyEnd: 200},
	}}}}
	got, omitted := render(t, r, "json", 600)
	d := decode(t, got)
	if omitted != 0 || len(d.Sections[0].Entries) != 2 {
		t.Fatalf("omitted %d\n%s", omitted, got)
	}
	for _, e := range d.Sections[0].Entries {
		if e["body"] != nil || e["body_omitted"] != true || e["body_start"] != 1.0 || e["body_end"] != 200.0 {
			t.Fatalf("%+v", e)
		}
	}
}

func TestUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	if _, err := output.Render(&buf, tenEntries(), "yaml", 0); err == nil {
		t.Fatal("expected error")
	}
}

func TestHeaderLineWorkspace(t *testing.T) {
	heads := []output.RepoHead{{Name: "go-app", Branch: "main", Commit: "8490d5a1"}, {Name: "go-lib", Branch: "main",
		Commit: "f8fd258f"}}
	h := output.Header{Workspace: "fixture", Repos: heads, Fresh: true}
	if got := output.HeaderLine(h); got != "go-app@8490d5a go-lib@f8fd258\n" {
		t.Fatal(got)
	}
	h.Repos = append([]output.RepoHead{{Name: "go-lib", Branch: "main", Commit: "f8fd258"}}, heads[0], heads[0], heads[0])
	h.Fresh, h.Stale, h.Dirty = false, []string{"go-lib"}, 2
	if got := output.HeaderLine(h); got != "4 repos (go-lib@f8fd258 +3) dirty fresh:no(go-lib)\n" {
		t.Fatal(got)
	}
	h.Repos, h.Stale = nil, nil
	if got := output.HeaderLine(h); got != "dirty fresh:no\n" {
		t.Fatal(got)
	}
	var buf strings.Builder
	h.Repos = heads
	if _, err := output.Render(&buf, &output.Response{Header: h}, "json", 0); err != nil ||
		!strings.Contains(buf.String(), `"workspace":"fixture","repos":[{"name":"go-app","branch":"main","commit":"8490d5a1"`) {
		t.Fatalf("%s %v", buf.String(), err)
	}
}

func TestTextSummaryOfManySectionsIsBounded(t *testing.T) {
	r := manySections(300, 2)
	got, omitted := render(t, r, "text", 1500)
	if len(got) > 1500 || omitted == 0 {
		t.Fatalf("len %d omitted %d\n%s", len(got), omitted, got)
	}
	if !strings.Contains(got, "more sections (titles: ") || !regexp.MustCompile(`\+\d+ more\); rerun`).MatchString(got) {
		t.Fatalf("summary does not count the titles it leaves out\n%s", got)
	}
	got, _ = render(t, manySections(300, 2), "text", 120)
	if !strings.Contains(got, "more sections; use --out FILE") {
		t.Fatalf("tight budget drops the summary\n%s", got)
	}
}

func TestSectionsSize(t *testing.T) {
	r := manySections(2, 3)
	full, _ := render(t, r, "text", 0)
	if got, want := output.SectionsSize(r.Sections), len(full)-len(output.HeaderLine(r.Header))-len(r.Note)-1; got != want {
		t.Fatalf("SectionsSize %d, want %d", got, want)
	}
}

func TestHeaderLineMainAndStaleNames(t *testing.T) {
	heads := []output.RepoHead{{Name: "a", Branch: "main", Commit: "1111111"}, {Name: "b", Branch: "main", Commit: "2222222"},
		{Name: "c", Branch: "main", Commit: "3333333"}, {Name: "d", Branch: "main", Commit: "4444444"}}
	h := output.Header{Workspace: "w", Repos: heads, Main: "c", Fresh: true}
	if got := output.HeaderLine(h); got != "4 repos (c@3333333 +3)\n" {
		t.Fatal(got)
	}
	one := output.Header{Repo: "a", Branch: "main", Commit: "1111111", Stale: []string{"a"}}
	if got := output.HeaderLine(one); got != "fresh:no\n" {
		t.Fatal(got)
	}
	one.Stale = []string{"b"}
	if got := output.HeaderLine(one); got != "fresh:no(b)\n" {
		t.Fatal(got)
	}
	// A fresh, clean answer of a one-repo workspace has no header line;
	// the repo is named only where the workspace has several.
	quiet := output.Header{Repo: "a", Branch: "main", Commit: "1111111", Fresh: true}
	if got := output.HeaderLine(quiet); got != "" {
		t.Fatal(got)
	}
	quiet.Multi = true
	if got := output.HeaderLine(quiet); got != "a@1111111\n" {
		t.Fatal(got)
	}
}

func TestJSONWorkspaceHeaderHasNoRepo(t *testing.T) {
	var buf strings.Builder
	h := output.Header{Repo: "ignored", Workspace: "w", Repos: []output.RepoHead{{Name: "a"}}, Fresh: true}
	if _, err := output.Render(&buf, &output.Response{Header: h}, "json", 0); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), `{"header":{"dirty":0,"fresh":true,"workspace":"w","repos":[`) {
		t.Fatal(buf.String())
	}
	buf.Reset()
	one := output.Header{Repo: "a", Fresh: true}
	if _, err := output.Render(&buf, &output.Response{Header: one}, "json", 0); err != nil ||
		!strings.Contains(buf.String(), `"repo":"a","branch":"","commit":""`) {
		t.Fatalf("%s %v", buf.String(), err)
	}
}
