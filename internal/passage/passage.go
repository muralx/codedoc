// Package passage splits a long text into overlapping runs of whole
// lines, the passages that are embedded one vector each so a search can
// point at the lines that matched (efficiency spec section 3). It is
// pure: lines in, line ranges out.
package passage

import "strings"

// A text of at most SplitOver bytes stays one passage. A longer one is
// cut into passages of about Size bytes, each starting with the last
// lines of the one before it, at most Overlap bytes of them (15% of
// Size). Max bounds the passages of one text: what follows them is not
// covered.
const (
	SplitOver = 1200
	Size      = 800
	Overlap   = Size * 15 / 100
	Max       = 40
)

// Span is a run of whole lines of a text: the 0-based indexes of its
// first and last line.
type Span struct {
	First, Last int
}

// Lines is the number of lines of the span.
func (s Span) Lines() int { return s.Last - s.First + 1 }

// Text returns the lines of a span joined with newlines.
func Text(lines []string, s Span) string {
	return strings.Join(lines[s.First:s.Last+1], "\n")
}

// Split returns the passages of a text given as lines, in order. Blank
// lines at either end of the text and of a passage belong to none, so a
// text of blank lines has no passage. A line is never divided: one
// longer than Size is a passage of its own. Cuts fall where the text
// pauses: after or before a blank line (a paragraph, or a group of
// statements), and with code otherwise before the least indented line
// that does not close a block, so a statement is kept whole where the
// size allows; without such a place, at the last line that fits.
func Split(lines []string, code bool) []Span {
	t := newText(lines)
	whole, ok := t.trim(0, len(lines))
	if !ok {
		return nil
	}
	end := whole.Last + 1
	var out []Span
	// fresh is the first line no passage holds yet.
	for start, fresh := whole.First, whole.First; len(out) < Max; {
		if end-start == 1 || t.size(start, end) <= SplitOver {
			return appendSpan(out, t, start, end)
		}
		cut := t.cut(start, end, code)
		if cut <= fresh {
			// The repeated lines leave no room for the next one.
			start = fresh
			cut = t.cut(start, end, code)
		}
		out = appendSpan(out, t, start, cut)
		start, fresh = t.overlap(start, cut), cut
	}
	return out
}

// appendSpan appends the lines [from, to) without the blank lines at
// their ends, nothing when all are blank.
func appendSpan(out []Span, t text, from, to int) []Span {
	if s, ok := t.trim(from, to); ok {
		return append(out, s)
	}
	return out
}

// text is the lines of a text with the byte offset each would start at
// in the text joined with newlines.
type text struct {
	lines []string
	offs  []int
}

func newText(lines []string) text {
	offs := make([]int, len(lines)+1)
	for i, l := range lines {
		offs[i+1] = offs[i] + len(l) + 1
	}
	return text{lines: lines, offs: offs}
}

// size is the number of bytes of the lines [from, to) joined with
// newlines.
func (t text) size(from, to int) int {
	if to <= from {
		return 0
	}
	return t.offs[to] - t.offs[from] - 1
}

func (t text) blank(i int) bool { return strings.TrimSpace(t.lines[i]) == "" }

// trim drops the blank lines at the ends of [from, to); ok is false
// when every line is blank.
func (t text) trim(from, to int) (Span, bool) {
	for from < to && t.blank(from) {
		from++
	}
	for to > from && t.blank(to-1) {
		to--
	}
	return Span{First: from, Last: to - 1}, from < to
}

// cut returns the line a passage starting at line start ends before:
// the best place (see rank) among those that leave it between half of
// Size and Size, the last of equally good ones. It is always after
// start and before end.
func (t text) cut(start, end int, code bool) int {
	limit := start + 1
	for limit+1 < end && t.size(start, limit+1) <= Size {
		limit++
	}
	low := limit
	for low-1 > start && t.size(start, low-1) >= Size/2 {
		low--
	}
	best, bestRank := limit, t.rank(limit, code)
	for c := limit - 1; c >= low; c-- {
		if r := t.rank(c, code); r < bestRank {
			best, bestRank = c, r
		}
	}
	return best
}

// closerRank is the rank of a cut before a line that closes a block.
const closerRank = 1 << 20

// rank says how bad a cut before line c is, lower is better: 0 next to
// a blank line; else 1 for prose, and for code 1 plus the indentation
// of the line, or closerRank when it closes a block.
func (t text) rank(c int, code bool) int {
	switch {
	case t.blank(c) || t.blank(c-1):
		return 0
	case !code:
		return 1
	}
	line := t.lines[c]
	rest := strings.TrimLeft(line, " \t")
	if strings.ContainsRune("})]", rune(rest[0])) {
		return closerRank
	}
	return 1 + len(line) - len(rest)
}

// overlap returns the line the passage after one of [start, cut)
// starts at: as many of its last lines as fit in Overlap bytes are
// repeated, never all of them.
func (t text) overlap(start, cut int) int {
	next := cut
	for next-1 > start && t.size(next-1, cut) <= Overlap {
		next--
	}
	return next
}
