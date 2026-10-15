package output_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/output"
)

func TestTextBudgetTooSmallForOneEntry(t *testing.T) {
	r := tenEntries()
	r.Sections[0].Entries[0].Text = strings.Repeat("x", 500)
	got, omitted := render(t, r, "text", 200)
	if omitted != 10 || len(got) > 200 || strings.Contains(got, "--offset") ||
		!strings.HasSuffix(got, "budget too small for one entry; raise --budget or use --out FILE\n") {
		t.Fatalf("omitted %d\n%s", omitted, got)
	}
}

func TestTextTitleCountsEntriesBeforeTheOffset(t *testing.T) {
	r := tenEntries() // ten entries after an offset of 20
	if got, _ := render(t, r, "text", 0); !strings.Contains(got, "== callers (30)\n") {
		t.Fatal(got)
	}
	r = &output.Response{Header: header(), Sections: []output.Section{{Title: "s", Offset: 4}}}
	if got, _ := render(t, r, "text", 0); !strings.Contains(got, "== s (4)\n") {
		t.Fatal(got)
	}
}

func TestPlainBodyHasNoLineNumbers(t *testing.T) {
	r := &output.Response{Header: header(), Sections: []output.Section{{Title: "diff", Entries: []output.Entry{
		{Key: "k", Body: "--- a\n+++ b\n", BodyStart: 1, Plain: true}}}}}
	if got, _ := render(t, r, "text", 0); !strings.HasSuffix(got, "k\n--- a\n+++ b\n") {
		t.Fatalf("%q", got)
	}
}
