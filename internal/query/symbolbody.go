package query

import (
	"fmt"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// docGap is the most lines (blank ones, annotations) between the end of
// a doc comment and the declaration it is printed with.
const docGap = 2

// withDoc makes the body of e, the source of sy cut out of src, the
// answer on its own: the signature is its first line, so text leaves it
// out of the entry line, and the doc is printed as the lines of the file
// it is, never twice. A doc comment right above the declaration is put
// before the body, and a doc inside it (a Python docstring) is already
// there; only a doc written elsewhere stays on the entry line.
func withDoc(e *output.Entry, src []byte, sy store.Symbol) {
	if e.Body == "" {
		return
	}
	e.Hide |= output.HideText
	switch {
	case sy.DocStartLine == 0 || sy.DocEndLine < sy.DocStartLine:
	case sy.DocStartLine >= e.BodyStart && sy.DocEndLine <= e.BodyEnd:
		e.Hide |= output.HideDoc
	case sy.DocStartLine < e.BodyStart && e.BodyStart-sy.DocEndLine-1 <= docGap:
		lines := splitLines(string(src))
		if e.BodyStart-1 > len(lines) {
			return
		}
		e.Body = strings.Join(lines[sy.DocStartLine-1:e.BodyStart-1], "\n") + "\n" + e.Body
		e.BodyStart = sy.DocStartLine
		e.Hide |= output.HideDoc
	}
}

// fitted returns the section of a symbol answer with entry e: e itself
// when it fits the budget or sy has no members, else the outline of its
// members (see memberOutline). s is the store holding sy and lines the
// command that prints a line range of its file.
func (c *Ctx) fitted(s *store.Store, sy store.Symbol, e output.Entry, lines string) (output.Section, error) {
	sec := oneSymbol(e)
	if e.Body == "" || c.Budget == 0 || output.SectionsSize([]output.Section{sec}) <= c.Budget {
		return sec, nil
	}
	syms, err := s.SymbolsInFile(sy.FileID)
	if err != nil {
		return sec, err
	}
	var members []output.Entry
	if sy.Kind == string(extract.KindDocument) {
		members = headingTree(syms)
	} else {
		members = memberEntries(syms, sy)
	}
	if len(members) == 0 {
		return sec, nil
	}
	return c.memberOutline(sy, e, members, lines), nil
}

// memberEntries are the outline lines of the members sy holds in its
// file: name, line range, signature and first doc sentence.
func memberEntries(syms []store.Symbol, sy store.Symbol) []output.Entry {
	var out []output.Entry
	for _, m := range syms {
		if m.ContainerID == sy.ID {
			out = append(out, outlined(symbolEntry(m), m, 0))
		}
	}
	return out
}

// memberOutline answers for a symbol whose source is larger than the
// budget: its own line (signature and doc, no body) and one line per
// member with its line range, with a note saying how to read one member
// or a range of lines. The members of a document are its headings.
func (c *Ctx) memberOutline(sy store.Symbol, e output.Entry, members []output.Entry, lines string) output.Section {
	n := e.BodyEnd - e.BodyStart + 1
	e.Body, e.BodyStart, e.BodyEnd, e.Continue, e.Hide = "", 0, 0, "", 0
	if e.Extra == nil {
		e.Extra = map[string]any{}
	}
	e.Extra["body_lines"] = n
	one := "codedoc symbol " + shortName(sy) + ".<member>"
	what := "members"
	if isDocSymbol(sy) {
		one, what = "codedoc symbol "+shellQuote(sy.Path+"#<heading>"), "headings"
	}
	c.notes = append(c.notes, fmt.Sprintf("%s is %d lines, over the budget: its %s are listed; %s prints one, %s A-B a range",
		shortName(sy), n, what, one, lines))
	sec := c.section("symbol", append([]output.Entry{e}, members...))
	sec.NoTitle = true
	return sec
}
