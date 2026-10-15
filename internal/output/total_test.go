package output_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/output"
)

func TestTotalCountsEntriesTheCallerHeldBack(t *testing.T) {
	r := tenEntries() // ten entries after an offset of 20
	r.Sections[0].Total = 70
	r.Sections = append(r.Sections, output.Section{Title: "next", Entries: []output.Entry{{Key: "k"}}})
	got, omitted := render(t, r, "text", 0)
	if omitted != 40 || !strings.Contains(got, "== callers (70)\n") ||
		!strings.Contains(got, "  :19  func F9()  Does a thing.\n40 more: rerun with --offset 30\n== next (1)\nk\n") {
		t.Fatalf("omitted %d\n%s", omitted, got)
	}
	// A budget that cuts the page counts both what it cut and what was
	// held back, and stays within the budget.
	got, omitted = render(t, r, "text", 400)
	written := strings.Count(got, "func F")
	more := fmt.Sprintf("%d more: rerun with --offset %d\n", 50-written, 20+written)
	if written == 0 || written == 10 || omitted != 51-written || len(got) > 400 || !strings.Contains(got, more) {
		t.Fatalf("written %d omitted %d\n%s", written, omitted, got)
	}
	// A total below what the section holds is ignored.
	r.Sections[0].Total = 3
	if got, omitted = render(t, r, "text", 0); omitted != 0 || !strings.Contains(got, "== callers (30)\n") {
		t.Fatalf("omitted %d\n%s", omitted, got)
	}
}

func TestTotalReservesTheMoreLineAfterTheLastEntry(t *testing.T) {
	r := tenEntries()
	r.Sections[0].Total = 70
	full, _ := render(t, r, "text", 0)
	for budget := len(full); budget > 120; budget-- {
		if got, _ := render(t, r, "text", budget); len(got) > budget {
			t.Fatalf("budget %d: %d bytes\n%s", budget, len(got), got)
		}
	}
}

func TestJSONTotalSetsOmittedAndNextOffset(t *testing.T) {
	r := tenEntries()
	r.Sections[0].Total = 70
	got, omitted := render(t, r, "json", 0)
	d := decode(t, got)
	if next := d.Sections[0].NextOffset; omitted != 40 || d.Sections[0].Omitted != 40 || next == nil || *next != 30 ||
		len(d.Sections[0].Entries) != 10 {
		t.Fatalf("omitted %d: %s", omitted, got)
	}
}
