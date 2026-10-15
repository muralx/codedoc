package output_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/output"
)

// rerunBudget matches the command an omission line names when not one
// entry fit: the command of the answer with a budget that holds it.
var rerunBudget = regexp.MustCompile(`codedoc callers X --budget (\d+)( --offset \d+)?\n`)

// An answer whose command is known never ends with a hint that names no
// command: a budget too small for one entry, or for a whole section,
// names the command with the budget that prints the answer whole.
func TestOmissionLinesNameTheCommandThatPrintsTheRest(t *testing.T) {
	r := tenEntries()
	r.Command = "codedoc callers X --budget 90"
	r.Sections = append(r.Sections, output.Section{Title: "tests", Entries: r.Sections[0].Entries[:2]})
	whole, _ := render(t, r, "text", 0)
	for budget := 40; budget < len(whole)+500; budget += 9 {
		got, omitted := render(t, r, "text", budget)
		if strings.Contains(got, "--out") || strings.Contains(got, "raise --budget") {
			t.Fatalf("budget %d: a hint without a command\n%s", budget, got)
		}
		for _, m := range rerunBudget.FindAllStringSubmatch(got, -1) {
			n, _ := strconv.Atoi(m[1])
			if n == 90 {
				continue // the command as it ran, continued at an offset
			}
			if m[2] != "" && m[2] != " --offset 20" {
				t.Fatalf("budget %d: the rerun leaves the page\n%s", budget, got)
			}
			if again, left := render(t, r, "text", n); left != 0 || again != whole || omitted == 0 {
				t.Fatalf("budget %d: --budget %d leaves %d out\n%s", budget, n, left, again)
			}
		}
	}
	got, _ := render(t, r, "text", 150)
	if !strings.HasSuffix(got, "== callers (30)\n10 more: codedoc callers X --budget 1100 --offset 20\n"+
		"... 1 more section (tests): codedoc callers X --budget 1100\n") {
		t.Fatalf("%s", got)
	}
}

// A collapsed section with a command of its own names that command.
func TestCollapsedSectionNamesItsOwnCommand(t *testing.T) {
	r := tenEntries()
	r.Sections[0].Offset = 0
	r.Command = "codedoc impact X"
	r.Sections = append(r.Sections, output.Section{Title: "tests", Command: "codedoc tests X", Entries: r.Sections[0].Entries[:2]})
	got, _ := render(t, r, "text", 300)
	if !strings.HasSuffix(got, " more: codedoc impact X --offset 3\n... 1 more section (tests): codedoc tests X\n") {
		t.Fatalf("%s", got)
	}
}

// A note longer than the budget does not cost the answer its place: the
// entries come first, and a note that does not fit after them is left
// out, never cut.
func TestNoteYieldsToTheAnswer(t *testing.T) {
	r := tenEntries()
	r.Command, r.Note = "codedoc callers X", strings.Repeat("a long note ", 30)
	got, _ := render(t, r, "text", 200)
	if len(got) > 200 || !strings.Contains(got, "\n  auth:go:pkg:F0  :10  func F0()  Does a thing.\n") ||
		!strings.Contains(got, " more: codedoc callers X --offset 22\n") {
		t.Fatalf("%d bytes\n%s", len(got), got)
	}
}

// JSON says the same as the text lines: the command that continues a
// cut section, and the one with a larger budget when no entry fit.
func TestJSONSectionNamesTheCommandThatContinuesIt(t *testing.T) {
	r := tenEntries()
	r.Command = "codedoc callers X --budget 700"
	got, omitted := render(t, r, "json", 700)
	if len(got) > 700 || omitted == 0 ||
		!regexp.MustCompile(`"next_offset":2\d,"continue":"codedoc callers X --budget 700 --offset 2\d"`).MatchString(got) {
		t.Fatalf("%d bytes, %d omitted: %s", len(got), omitted, got)
	}
	got, _ = render(t, r, "json", 300)
	m := regexp.MustCompile(`"entries":\[\],"omitted":10,"next_offset":20,"continue":"codedoc callers X --budget (\d+) --offset 20"`).
		FindStringSubmatch(got)
	if m == nil || len(got) > 300 {
		t.Fatalf("%d bytes: %s", len(got), got)
	}
	n, _ := strconv.Atoi(m[1])
	if again, left := render(t, r, "json", n); left != 0 {
		t.Fatalf("--budget %d leaves %d out: %s", n, left, again)
	}
	r.Command = ""
	if got, _ = render(t, r, "json", 700); strings.Contains(got, `"continue"`) {
		t.Fatalf("a command nobody knows: %s", got)
	}
}
