package output_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/output"
)

// longBody is an answer of one entry whose body is the lines first to
// last of a file, continued by `codedoc lines r:f.md`.
func longBody(first, last int) *output.Response {
	var b strings.Builder
	for i := first; i <= last; i++ {
		fmt.Fprintf(&b, "line %d of the file\n", i)
	}
	return &output.Response{Header: header(), Sections: []output.Section{{Title: "lines f.md", Entries: []output.Entry{{
		Loc: fmt.Sprintf("f.md:%d-%d", first, last), Text: "in Intro", Body: b.String(), BodyStart: first, BodyEnd: last,
		Continue: "codedoc lines r:f.md"}}}}, Note: "a note"}
}

func TestTextCutsAContinuableBodyToTheLinesThatFit(t *testing.T) {
	r := longBody(10, 200)
	got, omitted := render(t, r, "text", 600)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if omitted != 0 || len(got) > 600 || lines[2] != "f.md:10-200  in Intro" || lines[3] != "10\tline 10 of the file" ||
		lines[len(lines)-1] != "a note" {
		t.Fatalf("omitted %d, %d bytes\n%s", omitted, len(got), got)
	}
	shown := len(lines) - 5 // header, title, entry, continue line, note
	want := fmt.Sprintf("%d more lines: codedoc lines r:f.md %d-200", 191-shown, 10+shown)
	if shown < 5 || lines[len(lines)-2] != want || lines[len(lines)-3] != fmt.Sprintf("%d\tline %d of the file", 9+shown, 9+shown) {
		t.Fatalf("shown %d, want %q\n%s", shown, want, got)
	}
	// One more line would not have fit.
	if next := fmt.Sprintf("%d\tline %d of the file\n", 10+shown, 10+shown); len(got)+len(next) <= 600-2 {
		t.Fatalf("%d bytes of 600 used, a line of %d would fit\n%s", len(got), len(next), got)
	}
	// A body that fits is printed whole, without a continue line.
	if got, _ = render(t, r, "text", 0); strings.Contains(got, "more lines:") || !strings.Contains(got, "\n200\tline 200 of the file\n") {
		t.Fatal(got)
	}
}

func TestTextNeverExceedsBudgetCuttingABody(t *testing.T) {
	r := longBody(1, 400)
	full, _ := render(t, r, "text", 0)
	for budget := 60; budget <= len(full)+5; budget += 7 {
		got, _ := render(t, r, "text", budget)
		if len(got) > budget {
			t.Fatalf("budget %d: %d bytes\n%s", budget, len(got), got)
		}
		cut := strings.Contains(got, "more lines: codedoc lines r:f.md ")
		if whole := strings.Contains(got, "\n400\tline 400"); budget > 250 && cut == whole {
			t.Fatalf("budget %d: cut %v, whole %v\n%s", budget, cut, whole, got)
		}
	}
	// Too small for one line and its continue line: the entry line and
	// the command that prints the whole body.
	r.Sections[0].Entries[0].Body = strings.Repeat("x", 200) + "\n" + r.Sections[0].Entries[0].Body
	r.Sections[0].Entries[0].BodyStart = 0
	got, omitted := render(t, r, "text", 200)
	if omitted != 0 || strings.Contains(got, "body omitted") ||
		!strings.Contains(got, "f.md:1-400  in Intro\n401 more lines: codedoc lines r:f.md 0-400\n") {
		t.Fatal(got)
	}
}

func TestSummaryDoesNotCutABody(t *testing.T) {
	var b bytes.Buffer
	if _, err := output.RenderSummary(&b, longBody(1, 400), 500); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); strings.Contains(got, "more lines:") || !strings.Contains(got, "[body omitted: lines 1-400]") {
		t.Fatal(got)
	}
}

func TestJSONCutsAContinuableBody(t *testing.T) {
	r := longBody(10, 200)
	got, omitted := render(t, r, "json", 900)
	d := decode(t, got)
	e := d.Sections[0].Entries[0]
	body, _ := e["body"].(string)
	end, _ := e["body_end"].(float64)
	n := strings.Count(body, "\n")
	if omitted != 0 || len(got) > 900 || n < 5 || !strings.HasPrefix(body, "line 10 of the file\n") || int(end) != 9+n ||
		e["continue"] != fmt.Sprintf("codedoc lines r:f.md %d-200", 10+n) || e["body_omitted"] != nil {
		t.Fatalf("%d bytes, %d lines to %v: %s", len(got), n, end, got)
	}
	got, _ = render(t, r, "json", 0)
	if e = decode(t, got).Sections[0].Entries[0]; e["continue"] != nil || e["body_end"].(float64) != 200 {
		t.Fatalf("a body that fits is whole: %s", got)
	}
	// Not one line fits: the body is omitted and the command prints all
	// of it.
	got, _ = render(t, r, "json", 330)
	if e = decode(t, got).Sections[0].Entries[0]; e["body_omitted"] != true || e["continue"] != "codedoc lines r:f.md 10-200" {
		t.Fatalf("%s", got)
	}
}
