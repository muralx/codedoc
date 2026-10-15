package passage_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/passage"
)

// para returns n lines of about width bytes that name their paragraph.
func para(name string, n, width int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s line %02d %s", name, i, strings.Repeat("x", width-len(name)-9))
	}
	return out
}

// prose returns paragraphs of lines separated by one blank line.
func prose(paragraphs, linesEach, width int) []string {
	var out []string
	for p := range paragraphs {
		if p > 0 {
			out = append(out, "")
		}
		out = append(out, para(fmt.Sprintf("p%d", p), linesEach, width)...)
	}
	return out
}

func TestShortTextIsOnePassage(t *testing.T) {
	lines := []string{"", "  ", "First paragraph.", "", "Second paragraph.", ""}
	got := passage.Split(lines, false)
	if len(got) != 1 || got[0] != (passage.Span{First: 2, Last: 4}) || got[0].Lines() != 3 {
		t.Fatalf("%+v", got)
	}
	if text := passage.Text(lines, got[0]); text != "First paragraph.\n\nSecond paragraph." {
		t.Fatalf("%q", text)
	}
	exact := []string{strings.Repeat("a", passage.SplitOver)}
	if got := passage.Split(exact, false); len(got) != 1 {
		t.Fatalf("a text of SplitOver bytes was split: %+v", got)
	}
}

func TestBlankTextHasNoPassage(t *testing.T) {
	for _, lines := range [][]string{nil, {""}, {" ", "\t", ""}} {
		if got := passage.Split(lines, true); got != nil {
			t.Fatalf("%q: %+v", lines, got)
		}
	}
}

func TestLongProseIsCutAtBlankLines(t *testing.T) {
	lines := prose(8, 5, 60) // 8 paragraphs of about 300 bytes
	got := passage.Split(lines, false)
	if len(got) < 3 {
		t.Fatalf("%d passages: %+v", len(got), got)
	}
	for i, s := range got {
		if strings.TrimSpace(lines[s.First]) == "" || strings.TrimSpace(lines[s.Last]) == "" {
			t.Errorf("passage %d starts or ends with a blank line: %+v", i, s)
		}
		if n := len(passage.Text(lines, s)); n > passage.SplitOver {
			t.Errorf("passage %d has %d bytes", i, n)
		}
		if i+1 < len(got) && s.Last+1 < len(lines) && lines[s.Last+1] != "" {
			t.Errorf("passage %d ends inside a paragraph, before %q", i, lines[s.Last+1])
		}
	}
	checkCoverAndOverlap(t, lines, got)
}

// checkCoverAndOverlap checks that the passages are in order, cover
// every non-blank line, and that each starts within the one before it
// or right after it, repeating at most Overlap bytes.
func checkCoverAndOverlap(t *testing.T, lines []string, got []passage.Span) {
	t.Helper()
	covered := make([]bool, len(lines))
	for i, s := range got {
		for l := s.First; l <= s.Last; l++ {
			covered[l] = true
		}
		if i == 0 {
			continue
		}
		prev := got[i-1]
		if s.First <= prev.First || s.Last <= prev.Last {
			t.Fatalf("passage %d %+v does not advance past %+v", i, s, prev)
		}
		if s.First <= prev.Last {
			if n := len(passage.Text(lines, passage.Span{First: s.First, Last: prev.Last})); n > passage.Overlap {
				t.Errorf("passage %d repeats %d bytes", i, n)
			}
		}
	}
	for l, ok := range covered {
		if !ok && strings.TrimSpace(lines[l]) != "" {
			t.Errorf("line %d %q is in no passage", l, lines[l])
		}
	}
}

func TestPassagesOverlapWithinAParagraph(t *testing.T) {
	lines := para("one", 60, 50) // one paragraph of 3,000 bytes: no blank line to cut at
	got := passage.Split(lines, false)
	if len(got) < 4 {
		t.Fatalf("%+v", got)
	}
	for i := 1; i < len(got); i++ {
		if got[i].First > got[i-1].Last {
			t.Errorf("passage %d does not repeat the end of the one before: %+v %+v", i, got[i-1], got[i])
		}
	}
	for i, s := range got[:len(got)-1] {
		if n := len(passage.Text(lines, s)); n > passage.Size || n < passage.Size/2 {
			t.Errorf("passage %d has %d bytes", i, n)
		}
	}
	checkCoverAndOverlap(t, lines, got)
}

func TestVeryLongLineIsItsOwnPassage(t *testing.T) {
	long := strings.Repeat("y", 3*passage.Size)
	lines := append(append(para("a", 4, 50), long), para("b", 4, 50)...)
	got := passage.Split(lines, false)
	var own bool
	for _, s := range got {
		own = own || (s.First == 4 && s.Last == 4)
		if s.First < 4 && s.Last > 4 {
			t.Errorf("the long line is inside a larger passage: %+v", s)
		}
	}
	if !own {
		t.Fatalf("no passage is the long line alone: %+v", got)
	}
	checkCoverAndOverlap(t, lines, got)
	if got := passage.Split([]string{long}, true); len(got) != 1 || got[0] != (passage.Span{}) {
		t.Fatalf("a single long line: %+v", got)
	}
}

func TestCodeIsCutBeforeOuterStatements(t *testing.T) {
	var lines []string
	lines = append(lines, "func run() {")
	for i := range 12 {
		lines = append(lines, fmt.Sprintf("\tfor i := range items%d {", i),
			"\t\tif err := process(items[i], withALongArgumentName, anotherArgument); err != nil {",
			"\t\t\treturn fmt.Errorf(\"processing item %d failed: %w\", i, err)",
			"\t\t}",
			"\t}")
	}
	lines = append(lines, "}")
	got := passage.Split(lines, true)
	if len(got) < 3 {
		t.Fatalf("%+v", got)
	}
	for i, s := range got[:len(got)-1] {
		next := lines[s.Last+1]
		if !strings.HasPrefix(next, "\tfor ") {
			t.Errorf("passage %d ends before %q, not before a statement of the body", i, next)
		}
	}
	checkCoverAndOverlap(t, lines, got)
}

func TestCodeIsNotCutBeforeAClosingLineWhenAvoidable(t *testing.T) {
	// The closing line is the last one that fits: the cut goes before
	// the argument above it instead.
	lines := []string{"call(", "\t" + strings.Repeat("a", 450) + ",", "\t" + strings.Repeat("b", 339) + ",", ")", "next()",
		strings.Repeat("c", 500)}
	got := passage.Split(lines, true)
	if len(got) < 2 || got[0].Last != 1 {
		t.Fatalf("the first passage should end before the last argument: %+v", got)
	}
}

func TestPassagesOfOneTextAreBounded(t *testing.T) {
	lines := para("big", 5000, 80)
	got := passage.Split(lines, true)
	if len(got) != passage.Max {
		t.Fatalf("%d passages", len(got))
	}
	if last := got[len(got)-1]; last.Last >= len(lines)-1 {
		t.Fatalf("the bounded passages reach the end: %+v", last)
	}
}
