package query

import (
	"errors"
	"fmt"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/doc"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// docTagName tags a search or find result that is a document or one of
// its sections.
const docTagName = "doc"

// isDocSymbol reports whether a symbol is a document or a section.
func isDocSymbol(sy store.Symbol) bool { return extract.IsDocKind(sy.Kind) }

// kindTags tags a document or section doc; code gets its doc state tags
// (see docTags).
func kindTags(sy store.Symbol) []string {
	if isDocSymbol(sy) {
		return []string{docTagName}
	}
	return docTags(sy)
}

// matchHeading finds the sections of one document a heading address
// names (documents spec section 2), by the first of these steps that
// matches any: the heading or the heading path as written; the same
// ignoring case, or the heading's anchor slug; the numbering the
// heading starts with ("6.2" for "6.2 Retries"); a prefix of the
// heading; a substring of the heading; a substring of the heading
// path. An empty name is the
// document itself. Several matches of one step are all returned: the
// address is ambiguous.
func matchHeading(syms []store.Symbol, want string) []store.Symbol {
	want = strings.TrimSpace(want)
	lower := strings.ToLower(want)
	number := strings.TrimRight(want, ".")
	steps := []func(store.Symbol) bool{
		func(s store.Symbol) bool { return s.Name == want || s.Qualified == want },
		func(s store.Symbol) bool {
			return strings.EqualFold(s.Name, want) || strings.EqualFold(s.Qualified, want) || doc.Slug(s.Name) == lower
		},
		func(s store.Symbol) bool {
			return doc.Number(s.Name) != "" && strings.EqualFold(doc.Number(s.Name), number)
		},
		func(s store.Symbol) bool { return strings.HasPrefix(strings.ToLower(s.Name), lower) },
		func(s store.Symbol) bool { return strings.Contains(strings.ToLower(s.Name), lower) },
		func(s store.Symbol) bool { return strings.Contains(strings.ToLower(s.Qualified), lower) },
	}
	for _, step := range steps {
		var out []store.Symbol
		for _, s := range syms {
			isDocument := s.Kind == string(extract.KindDocument)
			if (want == "") == isDocument && (isDocument || step(s)) {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// documentAt resolves an address that is the path of an indexed
// document (repo:path, or a path as outline takes it) to its document
// symbol; none when the address names no document.
func (c *Ctx) documentAt(addr string) ([]store.Symbol, []*config.Repo, error) {
	if parse.DocLanguageForPath(addr) == "" {
		return nil, nil, nil
	}
	f, r, err := c.fileAt(addr)
	if err != nil || r == nil {
		return nil, nil, err
	}
	syms, err := c.Store.SymbolsInFile(f.ID)
	return matchHeading(syms, ""), []*config.Repo{r}, err
}

// fileAt resolves a file path (see splitTarget) to its indexed row
// after making its repo fresh; the repo is nil when the path names no
// indexed file.
func (c *Ctx) fileAt(target string) (store.File, *config.Repo, error) {
	r, p, err := c.splitTarget(target)
	if err != nil {
		return store.File{}, nil, nil
	}
	if err := c.EnsureFresh(r); err != nil {
		return store.File{}, nil, err
	}
	id, err := c.repoID(r)
	if err != nil || id == 0 {
		return store.File{}, nil, err
	}
	f, err := c.Store.FileByPath(id, p)
	if errors.Is(err, store.ErrNotFound) {
		return store.File{}, nil, nil
	}
	return f, r, err
}

// resolveLine finds the innermost symbol or section enclosing a line of
// a file (documents spec section 4): the address path:LINE.
func (c *Ctx) resolveLine(a Addr) ([]store.Symbol, []*config.Repo, error) {
	f, r, err := c.fileAt(a.Path)
	if err != nil {
		return nil, nil, err
	}
	if r == nil {
		note := fmt.Sprintf("no indexed file %s", a.Path)
		if _, _, err := c.splitTarget(a.Path); errors.Is(err, ErrOutside) {
			note = err.Error()
		}
		c.notes = append(c.notes, note)
		repos := c.scope()
		return nil, repos, c.EnsureFresh(repos...)
	}
	repos := []*config.Repo{r}
	sy, err := c.Store.SymbolByLocation(f.ID, a.Line)
	if errors.Is(err, store.ErrNotFound) {
		c.notes = append(c.notes, noSymbolAt(a.Line, f, c.fileAddr(f)))
		return nil, repos, nil
	}
	return []store.Symbol{sy}, repos, err
}

// noSymbolAt is the note of a line address in an indexed file that no
// symbol encloses; addr is the address outline takes for the file.
func noSymbolAt(line int, f store.File, addr string) string {
	return fmt.Sprintf("no symbol encloses line %d of %s (%d lines); codedoc outline %s lists its symbols",
		line, f.Path, f.Lines, shellQuote(addr))
}

// docOutline lists the heading tree of a document: one entry per
// section, nested by depth, with its line range and size. A document
// above the size limit of the index has no sections: the note says so
// and points at codedoc lines.
func (c *Ctx) docOutline(f store.File) (output.Section, error) {
	syms, err := c.Store.SymbolsInFile(f.ID)
	if err != nil {
		return output.Section{}, err
	}
	if len(syms) == 0 && index.Oversized(f.Size) {
		c.notes = append(c.notes, fmt.Sprintf("%s is too large to section (%s, over %s): read it with codedoc lines %s 1-200",
			f.Path, sizeText(f.Size), sizeText(index.SizeLimit()), c.fileAddr(f)))
	}
	return c.section(fmt.Sprintf("%s, %d %s", f.Path, f.Lines, plural(f.Lines, "line", "lines")), headingTree(syms)), nil
}

// fileAddr is the address of an indexed file that names it from any
// working directory: repo:path.
func (c *Ctx) fileAddr(f store.File) string {
	return c.repoNames()[f.RepoID] + ":" + f.Path
}

// headingTree renders the sections among syms (in line order) as a
// tree. An entry carries its line range only: the title of the answer
// names the file, and a long path repeated on every heading would use
// up the budget of a long document. path#Heading addresses a section.
func headingTree(syms []store.Symbol) []output.Entry {
	depth := map[int64]int{}
	var out []output.Entry
	for _, sy := range syms {
		if sy.Kind != string(extract.KindSection) {
			continue
		}
		d := 0
		if parent, ok := depth[sy.ContainerID]; ok {
			d = parent + 1
		}
		depth[sy.ID] = d
		n := sy.EndLine - sy.StartLine + 1
		_, span, _ := strings.Cut(spanLoc("", sy.StartLine, sy.EndLine), ":")
		out = append(out, output.Entry{Loc: span, Depth: d,
			Text:  fmt.Sprintf("%s  %d %s", sy.Signature, n, plural(n, "line", "lines")),
			Extra: map[string]any{"kind": sy.Kind, "key": sy.StableKey, "path": sy.Path, "heading": sy.Name, "lines": n}})
	}
	return out
}

// docDetail renders a document or section for the symbol query: its
// heading, span, the section it lies in, its summary, and with a body
// its text. Documents have no module, callers or doc state to report.
func (c *Ctx) docDetail(r *config.Repo, sy store.Symbol, o SymbolOpts) (e output.Entry, unavailable bool, err error) {
	e = output.Entry{Key: sy.StableKey, Name: shortName(sy), Loc: spanLoc(sy.Path, sy.StartLine, sy.EndLine),
		Text: sy.Signature, Doc: strings.Join(strings.Fields(sy.DocText), " "), Tags: []string{sy.Kind}, RepoID: sy.RepoID,
		Extra: map[string]any{"kind": sy.Kind, "doc": sy.DocText, "start_line": sy.StartLine, "end_line": sy.EndLine}}
	if sy.ContainerID != 0 {
		p, err := c.Store.SymbolByID(sy.ContainerID)
		if err != nil {
			return e, false, err
		}
		e.Tags = append(e.Tags, "in:"+p.Qualified)
		e.Extra["container"] = p.Qualified
	}
	if o.NoBody {
		return e, false, nil
	}
	src, ok, err := c.source(r, sy)
	if err != nil || !ok {
		return e, err == nil, err
	}
	e.Doc = "" // the body starts with the same text
	e.Hide |= output.HideText
	e.Body, e.BodyStart, e.BodyEnd = body(src, sy, o.Context)
	// Prose cut short is still worth reading: a section larger than the
	// budget is cut to the lines that fit and continued with lines.
	e.Continue = linesCmd(r.Name + ":" + sy.Path)
	return e, false, nil
}

// explainDocument explains a document: its title and first paragraph,
// then its heading tree.
func (c *Ctx) explainDocument(r *config.Repo, f store.File, syms []store.Symbol) *output.Response {
	var head []output.Entry
	for _, sy := range matchHeading(syms, "") {
		head = append(head, output.Entry{Key: sy.StableKey, Name: shortName(sy), Loc: spanLoc(sy.Path, sy.StartLine, sy.EndLine),
			Text: sy.Signature, Doc: strings.Join(strings.Fields(sy.DocText), " "), Tags: []string{sy.Kind},
			RepoID: sy.RepoID, Extra: map[string]any{"kind": sy.Kind, "doc": sy.DocText}})
	}
	return c.respond([]*config.Repo{r}, output.Section{Title: "explain " + r.Name + ":" + f.Path, Entries: head},
		c.section("headings", headingTree(syms)))
}

// docDeltaTags compares a current section with its version at a ref:
// documents carry no doc state, only whether their text changed.
func docDeltaTags(sy store.Symbol, s extract.Symbol, had bool) []string {
	switch {
	case !had:
		return []string{"added"}
	case s.BodyHash != sy.BodyHash:
		return []string{"text-changed"}
	}
	return nil
}

// extractorFor returns the extractor for the language of a path, code
// or document.
func extractorFor(p string) (extract.Extractor, bool) {
	lang := parse.LanguageForPath(p)
	if lang == "" {
		lang = parse.DocLanguageForPath(p)
	}
	return extract.For(lang)
}
