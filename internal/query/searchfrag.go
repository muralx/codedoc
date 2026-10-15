package query

import (
	"math/bits"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// Fragments of a search hit (efficiency spec section 3): the lines of
// the hit's file that matched, with context, printed under its entry so
// the answer needs no second call.

// searchContextLines is how many lines a search shows around a fragment
// when no -C, -A or -B says otherwise.
const searchContextLines = 2

// maxFragments is the most fragments one hit shows before they are
// joined.
const maxFragments = store.MaxHitPassages

// maxRunLines is the most consecutive matching lines taken as one
// fragment: in a text about the query's subject every line matches.
const maxRunLines = 8

// searchContext is the context of a search: what the flags ask for, else
// searchContextLines around each fragment.
func (c *Ctx) searchContext() Context {
	if c.Context.Set {
		return c.Context
	}
	return Context{Before: searchContextLines, After: searchContextLines}
}

// addFragments gives the entry of a workspace symbol hit its fragments:
// the passages of its embedded text closest to the query, then the lines
// of its doc comment and body that hold the most query terms, at most
// maxFragments in all, each widened by the context, kept inside the
// symbol with its doc comment (unless the context is the file) and
// joined where they overlap or touch. A hit whose only passage is its
// doc comment so gets the lines of the body that do what was asked. When
// the file changed since it was indexed the entry is tagged stale and
// keeps its line alone.
func (c *Ctx) addFragments(e *output.Entry, sy store.Symbol, passages []Passage, terms []string) {
	lines, ok := c.fileLines(sy.RepoID, sy.FileID, sy.Path)
	if !ok || sy.StartLine < 1 || sy.EndLine > len(lines) {
		e.Tags = append(e.Tags, staleTag)
		return
	}
	from, to := sy.StartLine, sy.EndLine
	if sy.DocStartLine > 0 && sy.DocStartLine < from {
		from = sy.DocStartLine
	}
	var cores []lineSpan
	for _, p := range passages {
		if p.StartLine >= 1 && p.EndLine <= len(lines) && p.StartLine <= p.EndLine {
			cores = append(cores, lineSpan{p.StartLine, p.EndLine})
		}
	}
	// The signature is on the entry line already.
	skip := append(cores[:len(cores):len(cores)], lineSpan{sy.SigStartLine, sy.SigEndLine})
	cores = append(cores, termLines(lines, from, to, terms, skip)...)
	if len(cores) == 0 {
		return
	}
	cores = cores[:min(len(cores), maxFragments)]
	e.Fragments = fragmentsOf(lines, cores, c.searchContext(), from, to)
}

// fragmentsOf widens the spans cores of a file by a context, inside the
// lines from..to of their symbol unless the context is the file, and
// returns them as fragments in line order, joined where they overlap or
// touch.
func fragmentsOf(lines []string, cores []lineSpan, x Context, from, to int) []output.Fragment {
	spans := make([]lineSpan, len(cores))
	for i, core := range cores {
		first, _ := x.around(core.first, from, to, len(lines))
		_, last := x.around(core.last, from, to, len(lines))
		spans[i] = lineSpan{first, last}
	}
	var out []output.Fragment
	for _, sp := range joinSpans(spans) {
		out = append(out, output.Fragment{Start: sp.first, End: sp.last,
			Text: strings.Join(lines[sp.first-1:sp.last], "\n") + "\n"})
	}
	return out
}

// termLines returns the runs of consecutive lines (at most maxRunLines)
// among from..to of a file that hold query terms, outside the spans
// taken: the runs holding more of the terms first, then in line order.
func termLines(lines []string, from, to int, terms []string, taken []lineSpan) []lineSpan {
	type run struct {
		lineSpan
		terms uint64
	}
	var runs []run
	for l := from; l <= to; l++ {
		held := termsIn(lines[l-1], terms)
		if held == 0 || inSpans(taken, l) {
			continue
		}
		if n := len(runs); n > 0 && runs[n-1].last == l-1 && l-runs[n-1].first < maxRunLines {
			runs[n-1].last, runs[n-1].terms = l, runs[n-1].terms|held
			continue
		}
		runs = append(runs, run{lineSpan{l, l}, held})
	}
	sort.SliceStable(runs, func(i, j int) bool { return bits.OnesCount64(runs[i].terms) > bits.OnesCount64(runs[j].terms) })
	out := make([]lineSpan, len(runs))
	for i, r := range runs {
		out[i] = r.lineSpan
	}
	return out
}

// termsIn returns the set of terms a line holds, one bit per term (the
// first 64), ignoring case, anywhere in a word: hub is in
// spawnHubProcess.
func termsIn(line string, terms []string) uint64 {
	line = strings.ToLower(line)
	var held uint64
	for i, t := range terms[:min(len(terms), 64)] {
		if strings.Contains(line, t) {
			held |= 1 << i
		}
	}
	return held
}

func inSpans(spans []lineSpan, line int) bool {
	for _, sp := range spans {
		if sp.first <= line && line <= sp.last {
			return true
		}
	}
	return false
}
