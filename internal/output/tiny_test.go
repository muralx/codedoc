package output_test

import (
	"bytes"
	"testing"

	"github.com/muralx/codedoc/internal/output"
)

func TestTinyBudgetNamesTheSectionItCouldNotPrint(t *testing.T) {
	r := tenEntries()
	header := output.HeaderLine(r.Header)
	// The summary line with the title does not fit, the title with the
	// too-small line does.
	got, omitted := render(t, r, "text", len(header)+95)
	want := "== callers (30)\n... 10 more; budget too small for one entry; raise --budget or use --out FILE\n"
	if omitted != 10 || got != header+want {
		t.Fatalf("omitted %d\n%s", omitted, got)
	}
	if got, _ = render(t, r, "text", len(header)+50); got != header+"== callers (30)\n... 10 more; raise --budget\n" {
		t.Fatalf("%s", got)
	}
	r.Sections = append(r.Sections, output.Section{Title: "a second section with a long title", Entries: r.Sections[0].Entries[:1]})
	got, omitted = render(t, r, "text", len(header)+80)
	if omitted != 11 || got != header+"== callers (30)\n... 10 more; raise --budget\n... 1 more section; use --out FILE\n" {
		t.Fatalf("omitted %d\n%s", omitted, got)
	}
	// Too small for any of them: the count alone.
	if got, _ = render(t, r, "text", len(header)+30); got != header+"... 11 more\n" {
		t.Fatalf("%s", got)
	}
}

func TestTinyBudgetOfASummaryPointsAtTheFullResult(t *testing.T) {
	r := tenEntries()
	var b bytes.Buffer
	if _, err := output.RenderSummary(&b, r, len(output.HeaderLine(r.Header))+50); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != output.HeaderLine(r.Header)+"== callers (30)\n... 10 more in the full result\n" {
		t.Fatalf("%s", got)
	}
}
