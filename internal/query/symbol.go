package query

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// bodyUnavailable is the note printed instead of a body read from a file
// that no longer matches the index.
const bodyUnavailable = "body unavailable: file changed since indexing; run codedoc index"

// SymbolOpts control the symbol query.
type SymbolOpts struct {
	// NoBody leaves the source out: location, signature and doc only.
	NoBody bool
	// DocOnly is the signature and the doc alone; it overrides Context.
	DocOnly bool
	// Context adds that many source lines before and after the body.
	Context int
}

// Symbol prints the one symbol an address names: its location and its
// source read from disk, starting at its doc comment (see withDoc), so
// the answer is the lines of the file and nothing is said twice. With
// NoBody or DocOnly it is the location, signature and doc. A document or
// one of its sections (doc.md, doc.md#Heading) is its text, and
// path:LINE names the innermost symbol or section enclosing that line.
// A source larger than the budget is never dropped: a symbol with
// members answers with their outline (see memberOutline), any other is
// cut to the lines that fit and continued with codedoc lines. No match
// returns ErrNotFound; several return ErrAmbiguous together with a
// response listing the candidates. With Ctx.Deps, or an address that
// names one, the symbol may be a dependency's: its declaration is read
// from the directory the package is installed in.
func Symbol(c *Ctx, addr string, o SymbolOpts) (*output.Response, error) {
	if o.Context < 0 {
		return nil, errors.New("--context must not be negative")
	}
	t, resp, err := c.target(addr, depsOnFlag)
	if err != nil {
		return resp, err
	}
	p, err := c.symbolPart(t, o)
	if err != nil {
		return nil, err
	}
	resp = c.respond(p.repos, p.sec)
	if p.note != "" {
		resp.Note = strings.TrimSuffix(p.note+". "+resp.Note, ". ")
	}
	return resp, nil
}

// symbolPart is the answer for one symbol of a symbol query: its
// section, the repos it draws on and what the note says about a body
// that could not be read.
type symbolPart struct {
	sec   output.Section
	repos []*config.Repo
	note  string
}

// symbolPart builds the answer for the symbol t names under the budget
// of the context.
func (c *Ctx) symbolPart(t target, o SymbolOpts) (symbolPart, error) {
	if t.dep != nil {
		return c.depSymbolPart(*t.dep, o)
	}
	sy := t.sym
	r, err := c.repoOf(sy)
	if err != nil {
		return symbolPart{}, err
	}
	p := symbolPart{repos: []*config.Repo{r}}
	if o.DocOnly {
		p.sec = oneSymbol(docEntry(sy))
		return p, nil
	}
	render := c.detail
	if isDocSymbol(sy) {
		render = c.docDetail
	}
	e, unavailable, err := render(r, sy, o)
	if err != nil {
		return p, err
	}
	if unavailable {
		p.note = bodyUnavailable
	}
	p.sec, err = c.fitted(c.Store, sy, e, linesCmd(r.Name+":"+sy.Path))
	return p, err
}

// oneSymbol is the section of a symbol answer: its one entry, without a
// title line.
func oneSymbol(e output.Entry) output.Section {
	return output.Section{Title: "symbol", NoTitle: true, Entries: []output.Entry{e}}
}

// depSymbolPart is symbolPart for a dependency symbol; the note says
// where a member asked through a type that inherits it is declared.
func (c *Ctx) depSymbolPart(d depSym, o SymbolOpts) (symbolPart, error) {
	p := symbolPart{repos: c.scope(), note: d.declaredNote()}
	if o.DocOnly {
		p.sec = oneSymbol(depDocEntry(d))
		return p, nil
	}
	e, unavailable, err := c.depDetail(d, o)
	if err != nil {
		return p, err
	}
	if unavailable != "" {
		p.note = strings.TrimPrefix(p.note+". "+unavailable, ". ")
	}
	p.sec, err = c.fitted(c.view.s, d.Symbol, e, e.Continue)
	return p, err
}

// one resolves the address of a required workspace symbol argument. No
// match returns ErrNotFound and several ErrAmbiguous, each with a
// response (the candidates listed) to print before the error; an address
// that names a dependency symbol returns ErrDependency.
func (c *Ctx) one(addr string) (store.Symbol, *output.Response, error) {
	t, resp, err := c.target(addr, depsRefused)
	return t.sym, resp, err
}

// repoOf returns the workspace repo a symbol belongs to.
func (c *Ctx) repoOf(sy store.Symbol) (*config.Repo, error) {
	row, err := c.Store.RepoByID(sy.RepoID)
	if err != nil {
		return nil, err
	}
	return c.repoNamed(row.Name)
}

// docEntry is the --doc-only entry: key, span, signature and full doc.
func docEntry(sy store.Symbol) output.Entry {
	e := output.Entry{Key: sy.StableKey, Name: shortName(sy), Loc: spanLoc(sy.Path, sy.StartLine, sy.EndLine),
		Text: sy.Signature, Doc: strings.Join(strings.Fields(sy.DocText), " "), Tags: []string{docTag(sy.DocState)},
		Extra: map[string]any{"kind": sy.Kind, "doc": sy.DocText, "doc_state": sy.DocState}, RepoID: sy.RepoID}
	if isDocSymbol(sy) {
		e.Tags = []string{sy.Kind} // a section's text is no doc with a state
	}
	return e
}

// detail renders the symbol entry: the full doc on one line, tags for
// kind, container and module, and optionally the body. unavailable is
// true when a body was asked for but the file no longer matches the index.
func (c *Ctx) detail(r *config.Repo, sy store.Symbol, o SymbolOpts) (e output.Entry, unavailable bool, err error) {
	e = symbolEntry(sy)
	e.Loc = spanLoc(sy.Path, sy.StartLine, sy.EndLine)
	e.Doc = strings.Join(strings.Fields(sy.DocText), " ")
	e.Tags = append([]string{sy.Kind}, e.Tags...)
	container := ""
	if sy.ContainerID != 0 {
		p, err := c.Store.SymbolByID(sy.ContainerID)
		if err != nil {
			return e, false, err
		}
		container = p.Qualified
		e.Tags = append(e.Tags, "container:"+container)
	}
	if err := c.edgeFacts(&e, sy, container); err != nil || o.NoBody {
		return e, false, err
	}
	src, ok, err := c.source(r, sy)
	if err != nil || !ok {
		return e, err == nil, err
	}
	e.Body, e.BodyStart, e.BodyEnd = body(src, sy, o.Context)
	withDoc(&e, src, sy)
	e.Continue = linesCmd(r.Name + ":" + sy.Path)
	return e, false, nil
}

// edgeFacts adds to the entry of sy what --detail and JSON say about it
// beyond its text: module, fan-in (with the ambiguous edges that may
// end at it), fan-out and doc state.
func (c *Ctx) edgeFacts(e *output.Entry, sy store.Symbol, container string) error {
	fanIn, fanOut, err := c.Store.EdgeCounts(sy.ID)
	if err != nil {
		return err
	}
	ambiguous, err := c.Store.AmbiguousEdgesTo(sy.ID)
	if err != nil {
		return err
	}
	in := fmt.Sprintf("fan-in:%d", fanIn)
	if ambiguous > 0 {
		in += fmt.Sprintf(" (%d ambiguous)", ambiguous)
	}
	e.Tags = append(e.Tags, "module:"+moduleOf(sy), in, fmt.Sprintf("fan-out:%d", fanOut), docTag(sy.DocState))
	e.Extra = map[string]any{"kind": sy.Kind, "container": container, "module": moduleOf(sy), "doc": sy.DocText,
		"start_line": sy.StartLine, "end_line": sy.EndLine, "visibility": sy.Visibility, "fan_in": fanIn,
		"fan_in_ambiguous": ambiguous, "fan_out": fanOut, "doc_state": sy.DocState}
	return nil
}

// source reads a symbol's file. ok is false when the index is stale for
// the repo, or the file's size, mtime or content no longer match its row.
func (c *Ctx) source(r *config.Repo, sy store.Symbol) (src []byte, ok bool, err error) {
	if !c.Fresh[r.Name].Fresh {
		return nil, false, nil
	}
	f, err := c.Store.FileByID(sy.FileID)
	if err != nil {
		return nil, false, err
	}
	p := filepath.Join(r.Path, filepath.FromSlash(sy.Path))
	info, err := os.Stat(p)
	if err != nil || info.Size() != f.Size || info.ModTime().UnixNano() != f.Mtime {
		return nil, false, nil
	}
	src, err = os.ReadFile(p)
	if err != nil || index.ContentHash(src) != f.Hash {
		return nil, false, nil
	}
	return src, true, nil
}

// body cuts a symbol's source out of src: by its byte span widened to the
// start of its first line, so indentation is kept, or, with context
// lines, the whole lines from context before its start line to context
// after its end line. It returns the text and its first and last line.
func body(src []byte, sy store.Symbol, context int) (string, int, int) {
	if context == 0 {
		if sy.StartByte < 0 || sy.EndByte > int64(len(src)) || sy.StartByte > sy.EndByte {
			return "", 0, 0
		}
		start := bytes.LastIndexByte(src[:sy.StartByte], '\n') + 1
		return string(src[start:sy.EndByte]), sy.StartLine, sy.EndLine
	}
	lines := strings.SplitAfter(string(src), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	first, last := max(1, sy.StartLine-context), min(len(lines), sy.EndLine+context)
	if first > last {
		return "", 0, 0
	}
	return strings.Join(lines[first-1:last], ""), first, last
}
