package query

import (
	"cmp"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// mentionCap is the most full-text hits an impact answer checks for a
// mention of the symbol, per kind of text (document sections, doc
// comments).
const mentionCap = 200

// mentions lists the texts that name the symbol: the sections of
// documents, then the doc comments of other symbols, production code
// before tests, each with the first line that names it. A document or
// section is also mentioned by the sections that link to it. The name
// counts as a whole word, written as it is in the code (see mention).
func (im *impact) mentions() ([]output.Section, error) {
	c, sy := im.c, im.sy
	phrase := `"` + strings.ReplaceAll(sy.Name, `"`, `""`) + `"`
	sections, err := c.Store.SectionMatches(phrase, store.SearchFilter{}, mentionCap)
	if err != nil {
		return nil, err
	}
	comments, err := c.Store.SymbolMatches("doc_text : "+phrase, store.SearchFilter{}, mentionCap)
	if err != nil {
		return nil, err
	}
	syms, err := c.Store.SymbolsByID(append(sections, comments...))
	if err != nil {
		return nil, err
	}
	keep, err := c.repoFilter()
	if err != nil {
		return nil, err
	}
	m, err := im.mention()
	if err != nil {
		return nil, err
	}
	var entries []output.Entry
	for _, o := range syms {
		if o.ID == sy.ID || !keep(o) {
			continue
		}
		if e, ok := im.mentionEntry(m, o); ok {
			entries = append(entries, e)
		}
	}
	sortMentions(entries)
	links, err := im.links()
	if err != nil {
		return nil, err
	}
	return im.page("mentions in documents and doc comments", im.own("mentions"), append(links, entries...), nil), nil
}

// links lists the sections that link to the document or section the
// answer is about, at the line of the link.
func (im *impact) links() ([]output.Entry, error) {
	if !isDocSymbol(im.sy) {
		return nil, nil
	}
	edges, err := im.c.Store.EdgesTo(im.sy.ID, "references")
	if err != nil {
		return nil, err
	}
	var kept []store.Edge
	for _, e := range edges {
		if e.RefKind == store.LinkRefKind {
			kept = append(kept, e)
		}
	}
	return im.c.sourceEntries(kept, func(store.Symbol) bool { return true })
}

// sortMentions orders mentions: documents before doc comments, tests
// after production code, then by path and line.
func sortMentions(entries []output.Entry) {
	rank := func(e output.Entry) int {
		r := 0
		if kind, _ := e.Extra["kind"].(string); !extract.IsDocKind(kind) {
			r = 2
		}
		if isTestLoc(locPath(e.Loc)) {
			r++
		}
		return r
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if ri, rj := rank(entries[i]), rank(entries[j]); ri != rj {
			return ri < rj
		}
		if pi, pj := locPath(entries[i].Loc), locPath(entries[j].Loc); pi != pj {
			return pi < pj
		}
		li, _ := entries[i].Extra["line"].(int)
		lj, _ := entries[j].Extra["line"].(int)
		return cmp.Or(cmp.Compare(li, lj), cmp.Compare(entries[i].Key, entries[j].Key)) < 0
	})
}

// mention tells whether a text names a symbol.
type mention struct {
	// name matches the name as a whole word; code the name written as
	// code (called, or between backticks), which is what a name that is
	// an everyday word needs to count; qualified the name after its
	// container (Store.Get, Store#get, Store::get).
	name, code, qualified *regexp.Regexp
	// container matches the name of the container as a whole word, nil
	// for a top-level symbol; containerID is its row.
	container   *regexp.Regexp
	containerID int64
	// plain says that the name is an everyday word: lower-case letters
	// only.
	plain bool
}

// word matches text as a whole identifier.
func word(text string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^A-Za-z0-9_$])` + text + `($|[^A-Za-z0-9_$])`)
}

// plainName matches a name that reads as an everyday word.
var plainName = regexp.MustCompile(`^[a-z]+$`)

// mentionCode matches a name written as code: between backticks, or
// called.
func mentionCode(name string) *regexp.Regexp {
	q := regexp.QuoteMeta(name)
	return regexp.MustCompile("`" + q + "`|(^|[^A-Za-z0-9_$])" + q + `\(`)
}

// mention builds the matcher of the symbol of the answer.
func (im *impact) mention() (mention, error) {
	sy := im.sy
	name := regexp.QuoteMeta(sy.Name)
	m := mention{name: word(name), code: mentionCode(sy.Name), plain: plainName.MatchString(sy.Name),
		containerID: sy.ContainerID}
	if sy.ContainerID == 0 {
		return m, nil
	}
	cont, err := im.c.Store.SymbolByID(sy.ContainerID)
	if err != nil {
		return m, err
	}
	m.container = word(regexp.QuoteMeta(cont.Name))
	m.qualified = word(regexp.QuoteMeta(cont.Name) + `(\.|#|::)` + name)
	return m, nil
}

// line reports whether a line of the text of symbol o names the symbol.
// near says that the text is about the container of the symbol: it
// names it, or o is the container or one of its members. The qualified
// name always counts. The bare name of a member counts only near its
// container, and a name that is an everyday word (get, list) only
// there or written as code, so that prose using the word is no mention.
func (m mention) line(line string, near bool) bool {
	switch {
	case m.qualified != nil && m.qualified.MatchString(line):
		return true
	case m.container != nil && !near:
		return false
	case m.plain && m.container == nil:
		return m.code.MatchString(line)
	}
	return m.name.MatchString(line)
}

// mentionEntry is the entry of the text of o when it names the symbol:
// o at the first line that does, with that line as its fragment. The
// text is the own text of a section (up to its first subsection) or the
// doc comment of a symbol, read from the file; a file that changed
// since it was indexed is skipped.
func (im *impact) mentionEntry(m mention, o store.Symbol) (output.Entry, bool) {
	c := im.c
	first, last := o.DocStartLine, o.DocEndLine
	if isDocSymbol(o) {
		first, last = o.StartLine, im.ownEnd(o)
	}
	lines, ok := c.fileLines(o.RepoID, o.FileID, o.Path)
	if !ok || first < 1 || last > len(lines) || first > last {
		return output.Entry{}, false
	}
	text := lines[first-1 : last]
	near := m.container != nil && (o.ID == m.containerID || o.ContainerID == m.containerID ||
		m.container.MatchString(strings.Join(text, "\n")))
	for i, l := range text {
		if !m.line(l, near) {
			continue
		}
		span := o
		span.StartLine, span.EndLine = first, last
		e := output.Entry{Key: o.StableKey, Name: shortName(o), Loc: fmt.Sprintf("%s:%d", o.Path, first+i), RepoID: o.RepoID,
			Extra: map[string]any{"kind": o.Kind, "line": first + i}}
		c.site(&e, span, first+i)
		return e, true
	}
	return output.Entry{}, false
}

// ownEnd is the last line of the own text of a document or section:
// the line before its first subsection, else its last line.
func (im *impact) ownEnd(o store.Symbol) int {
	syms, err := im.c.Store.SymbolsInFile(o.FileID)
	if err != nil {
		return o.EndLine
	}
	end := o.EndLine
	for _, s := range syms {
		if s.ID != o.ID && isDocSymbol(s) && s.StartLine > o.StartLine && s.StartLine <= end {
			end = s.StartLine - 1
		}
	}
	return end
}
