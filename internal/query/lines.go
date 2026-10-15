package query

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// lineRange matches A, A-B and A+N.
var lineRange = regexp.MustCompile(`^([0-9]+)(?:([-+])([0-9]+))?$`)

// Lines prints line ranges of a file on disk with their numbers
// (documents spec section 4.1): the cross-check of what the index says
// about a file. A range is A-B, A+N (A and the N lines after it, as
// sed's A,+N) or A alone; ranges that overlap or touch are printed as
// one. Each range is one entry: a header naming the innermost symbol or
// section that encloses its lines (see lineOwners), then the lines. A file that
// changed since it was indexed is re-indexed first, and said so; a
// symbol whose recorded range still does not start at its declaration is
// reported as an index mismatch. A range larger than the budget is
// printed up to the lines that fit and ends with the lines call for the
// rest (output.Entry.Continue). A file of a dependency (addressed as
// outline and symbol take it) is printed under the symbols deps.db holds
// for it.
func Lines(c *Ctx, target string, rngs ...string) (*output.Response, error) {
	spans, err := parseLineRanges(rngs)
	if err != nil {
		return nil, err
	}
	f, resp, err := c.lineFile(target)
	if err != nil {
		return resp, err
	}
	lines := splitLines(string(f.src))
	entries := make([]output.Entry, len(spans))
	for i, sp := range spans {
		if sp.first > len(lines) {
			return nil, fmt.Errorf("lines %s: line %d is past the end (%d lines)", target, sp.first, len(lines))
		}
		first, last := sp.first, min(sp.last, len(lines))
		entries[i] = output.Entry{Loc: spanLoc(f.loc, first, last), Text: c.lineOwners(f.syms, lines, first, last),
			Tags: f.tags, Body: strings.Join(lines[first-1:last], "\n") + "\n", BodyStart: first, BodyEnd: last,
			Continue: linesCmd(f.addr), Extra: map[string]any{"start_line": first, "end_line": last, "lines": len(lines)}}
	}
	return c.respond(f.repos, output.Section{Title: "lines " + f.title + " " + strings.Join(rngs, " "), NoTitle: true,
		Entries: entries}), nil
}

// lineSpan is a range of lines, both ends included.
type lineSpan struct{ first, last int }

// parseLineRanges reads the range arguments of lines into spans in line
// order, joining those that overlap or touch.
func parseLineRanges(rngs []string) ([]lineSpan, error) {
	if len(rngs) == 0 {
		return nil, usagef("lines needs a range: A-B, A+N or A")
	}
	var spans []lineSpan
	for _, rng := range rngs {
		first, last, err := parseLineRange(rng)
		if err != nil {
			return nil, err
		}
		spans = append(spans, lineSpan{first, last})
	}
	return joinSpans(spans), nil
}

// joinSpans returns spans in line order, those that overlap or touch
// joined into one.
func joinSpans(spans []lineSpan) []lineSpan {
	slices.SortFunc(spans, func(a, b lineSpan) int { return cmp.Compare(a.first, b.first) })
	out := spans[:1]
	for _, sp := range spans[1:] {
		if prev := &out[len(out)-1]; sp.first <= prev.last+1 {
			prev.last = max(prev.last, sp.last)
			continue
		}
		out = append(out, sp)
	}
	return out
}

// parseLineRange reads A, A-B or A+N into a first and last line.
func parseLineRange(rng string) (first, last int, err error) {
	m := lineRange.FindStringSubmatch(rng)
	if m == nil {
		return 0, 0, usagef("line range %q: want A-B, A+N or A", rng)
	}
	first, _ = strconv.Atoi(m[1]) // lineRange guarantees digits
	last = first
	if n, err := strconv.Atoi(m[3]); err == nil {
		last = n
		if m[2] == "+" {
			last = first + n
		}
	}
	if first < 1 || last < first {
		return 0, 0, usagef("line range %q: lines start at 1 and A must not be after B", rng)
	}
	return first, last, nil
}

// splitLines splits text into lines without their terminators; a final
// line terminator starts no further line.
func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// lineOwners names where the lines first to last are, in one line: the
// innermost symbol or section that encloses them all, its doc comment
// counted ("in Store (10-15)"); when no single one does, the ones the
// range starts and ends in ("in A (26-32) .. B (35-38)"). A document is
// named only when no section is on the lines. It notes each index
// mismatch among the symbols on those lines (see symbolsOn).
func (c *Ctx) lineOwners(syms []store.Symbol, lines []string, first, last int) string {
	on := c.symbolsOn(syms, lines, first, last)
	if len(on) == 0 {
		return "outside any indexed symbol"
	}
	if sy, ok := innermost(on, first, last); ok {
		return "in " + ownerSpan(sy)
	}
	from, _ := innermost(on, first, first)
	to, _ := innermost(on, last, last)
	if from.ID == 0 {
		from = slices.MinFunc(on, func(a, b store.Symbol) int { return cmp.Compare(ownerStart(a), ownerStart(b)) })
	}
	if to.ID == 0 {
		to = slices.MaxFunc(on, func(a, b store.Symbol) int { return cmp.Compare(a.EndLine, b.EndLine) })
	}
	if from.ID == to.ID {
		return "in " + ownerSpan(from)
	}
	return "in " + ownerSpan(from) + " .. " + ownerSpan(to)
}

// ownerStart is the first line a symbol owns: that of its doc comment
// when it has one above it, else of its declaration.
func ownerStart(sy store.Symbol) int {
	if sy.DocStartLine > 0 && sy.DocStartLine < sy.StartLine {
		return sy.DocStartLine
	}
	return sy.StartLine
}

// ownerSpan is a symbol in the header of lines: its name and its lines.
func ownerSpan(sy store.Symbol) string {
	_, span, _ := strings.Cut(spanLoc("", sy.StartLine, sy.EndLine), ":")
	return fmt.Sprintf("%s (%s)", lineOwner(sy), span)
}

// innermost returns the symbol with the fewest lines among those of on
// that own every line from first to last.
func innermost(on []store.Symbol, first, last int) (best store.Symbol, ok bool) {
	for _, sy := range on {
		if ownerStart(sy) > first || sy.EndLine < last {
			continue
		}
		if !ok || sy.EndLine-sy.StartLine < best.EndLine-best.StartLine {
			best, ok = sy, true
		}
	}
	return best, ok
}

// symbolsOn returns the symbols with a line in first..last (their doc
// comment counted), without the document when a section is among them,
// noting each symbol whose recorded range does not match the file (see
// declaredAt).
func (c *Ctx) symbolsOn(syms []store.Symbol, lines []string, first, last int) []store.Symbol {
	var on []store.Symbol
	for _, sy := range syms {
		if sy.EndLine < first || ownerStart(sy) > last {
			continue
		}
		on = append(on, sy)
		if !declaredAt(sy, lines) {
			c.notes = append(c.notes, fmt.Sprintf("index mismatch: %s recorded %d-%d; report it with codedoc feedback",
				sy.StableKey, sy.StartLine, sy.EndLine))
		}
	}
	if len(on) > 1 {
		on = slices.DeleteFunc(on, func(sy store.Symbol) bool { return sy.Kind == string(extract.KindDocument) })
	}
	return on
}

// lineOwner is the name of a symbol in the header of lines: its short
// name, for a section its heading path.
func lineOwner(sy store.Symbol) string {
	if isDocSymbol(sy) && sy.Qualified != "" {
		return sy.Qualified
	}
	return shortName(sy)
}

// declaredAt reports whether the lines on disk a symbol's signature is
// recorded on still declare it: they hold its name or its signature. A
// recorded range past the end of the file does not.
func declaredAt(sy store.Symbol, lines []string) bool {
	if sy.Kind == string(extract.KindDocument) {
		return sy.EndLine <= len(lines)
	}
	start, end := min(sy.StartLine, sy.SigStartLine), max(sy.SigEndLine, sy.SigStartLine)
	if start < 1 || end > len(lines) || sy.EndLine > len(lines) {
		return false
	}
	text := strings.Join(strings.Fields(strings.Join(lines[start-1:end], " ")), " ")
	return strings.Contains(text, sy.Name) || strings.Contains(text, strings.Join(strings.Fields(sy.Signature), " "))
}
