package index

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/passage"
	"github.com/muralx/codedoc/internal/store"
)

// symbolItems composes the embedding texts of stale workspace symbols
// from their rows and their files on disk (efficiency spec section 3):
//
//   - a document or section, from its own lines, heading to first
//     sub-section, under its path and heading path;
//   - a documented symbol, from its doc: one text with the signature and
//     the first lines of the body when the doc is short, else one per
//     passage of the doc comment's lines;
//   - an undocumented container, from its signature and the names of its
//     members, which are embedded on their own;
//   - any other undocumented symbol, from the lines of its body.
//
// A text longer than passage.SplitOver is one item with several
// passages (see passage.Split). A symbol whose file is gone or changed
// since it was indexed gets no item and stays stale.
func symbolItems(s *store.Store, syms []store.StaleSymbol) ([]vecItem, error) {
	src := sourceReader{s: s, roots: map[int64]string{}}
	out := make([]vecItem, 0, len(syms))
	for _, sy := range syms {
		lines, ok, err := src.lines(sy)
		if err != nil {
			return nil, err
		}
		if !ok || sy.StartLine < 1 || sy.EndLine > len(lines) || sy.StartLine > sy.EndLine {
			continue
		}
		out = append(out, vecItem{id: sy.ID, hash: sy.VectorHash, passages: symbolPassages(sy, lines)})
	}
	return out, nil
}

// symbolPassages composes the texts of one symbol from the lines of its
// file.
func symbolPassages(sy store.StaleSymbol, lines []string) []vecPassage {
	switch {
	case extract.IsDocKind(sy.Kind):
		return sectionPassages(sy, lines)
	case sy.Doc != "":
		var raw []string
		if sy.DocStartLine >= 1 && sy.DocStartLine <= sy.DocEndLine && sy.DocEndLine <= len(lines) {
			raw = lines[sy.DocStartLine-1 : sy.DocEndLine]
		}
		return docPassages(sy, raw, strings.Join(lines[sy.StartLine-1:sy.EndLine], "\n"))
	case extract.IsContainer(extract.Kind(sy.Kind)):
		return []vecPassage{{start: sy.SigStartLine - sy.StartLine, end: sy.SigEndLine - sy.StartLine,
			text: embed.PassageText(sy.Path, sy.Container, sy.Signature, sy.Members)}}
	}
	return bodyPassages(sy, lines[sy.StartLine-1:sy.EndLine])
}

// sectionPassages composes the texts of a document or section from its
// own lines: one, covering heading and text, when they are short or
// empty, else one per passage of the text.
func sectionPassages(sy store.StaleSymbol, lines []string) []vecPassage {
	first := sy.StartLine
	if sy.Kind == string(extract.KindSection) {
		first = sy.SigEndLine + 1
	}
	last := max(min(sy.OwnEndLine, sy.EndLine), first-1)
	own := lines[first-1 : last]
	spans := passage.Split(own, false)
	if len(spans) <= 1 {
		return []vecPassage{{end: max(last, sy.StartLine) - sy.StartLine,
			text: embed.SectionText(sy.Path, sy.Qualified, sy.Signature, strings.TrimSpace(strings.Join(own, "\n")))}}
	}
	out := make([]vecPassage, len(spans))
	for i, sp := range spans {
		out[i] = vecPassage{start: first - sy.StartLine + sp.First, end: first - sy.StartLine + sp.Last,
			text: embed.SectionText(sy.Path, sy.Qualified, sy.Signature, passage.Text(own, sp))}
	}
	return out
}

// docPassages composes the texts of a documented symbol. A doc of at
// most passage.SplitOver bytes is one text, with the signature and the
// first lines of body (none for a dependency), covering the doc's lines,
// or the symbol's when they are not known. A longer doc is split by the
// lines raw of its comment in the file; without them (a dependency, whose
// files are not read) by the lines of the doc text, which are the
// comment's lines when there are as many, else every passage covers the
// whole comment.
func docPassages(sy store.StaleSymbol, raw []string, body string) []vecPassage {
	whole := vecPassage{end: sy.EndLine - sy.StartLine}
	if sy.DocStartLine > 0 {
		whole = vecPassage{fromDoc: true, end: sy.DocEndLine - sy.DocStartLine}
	}
	if len(sy.Doc) <= passage.SplitOver {
		whole.text = embed.SymbolText(sy.Path, sy.Container, sy.Signature, sy.Doc, body)
		return []vecPassage{whole}
	}
	lines, mapped := raw, true
	if lines == nil {
		lines = strings.Split(sy.Doc, "\n")
		mapped = sy.DocStartLine > 0 && len(lines) == sy.DocEndLine-sy.DocStartLine+1
	}
	spans := passage.Split(lines, false)
	out := make([]vecPassage, len(spans))
	for i, sp := range spans {
		out[i] = whole
		if mapped {
			out[i].start, out[i].end = sp.First, sp.Last
		}
		out[i].text = embed.PassageText(sy.Path, sy.Container, sy.Signature, passage.Text(lines, sp))
	}
	return out
}

// bodyPassages composes the texts of an undocumented symbol from the
// lines of its declaration: one per passage, each under the symbol's
// path and container, and under its signature when the passage does not
// start with it.
func bodyPassages(sy store.StaleSymbol, body []string) []vecPassage {
	spans := passage.Split(body, true)
	if len(spans) == 0 {
		return []vecPassage{{end: sy.EndLine - sy.StartLine, text: embed.PassageText(sy.Path, sy.Container, sy.Signature, "")}}
	}
	out := make([]vecPassage, len(spans))
	for i, sp := range spans {
		sig := sy.Signature
		if sp.First <= sy.SigStartLine-sy.StartLine {
			sig = ""
		}
		out[i] = vecPassage{start: sp.First, end: sp.Last,
			text: embed.PassageText(sy.Path, sy.Container, sig, passage.Text(body, sp))}
	}
	return out
}

// sourceReader reads the lines of the files of symbols, keeping the last
// file read.
type sourceReader struct {
	s      *store.Store
	roots  map[int64]string
	fileID int64
	src    []string
	ok     bool
}

// lines returns the lines of a symbol's file; ok is false when the file
// is gone or no longer matches the index.
func (r *sourceReader) lines(sy store.StaleSymbol) ([]string, bool, error) {
	if sy.FileID != r.fileID {
		root, err := r.root(sy.RepoID)
		if err != nil {
			return nil, false, err
		}
		r.fileID = sy.FileID
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(sy.Path)))
		r.ok = err == nil && ContentHash(src) == sy.FileHash
		r.src = strings.Split(string(src), "\n")
	}
	return r.src, r.ok, nil
}

// root returns the root directory of a repo row.
func (r *sourceReader) root(repoID int64) (string, error) {
	if p, ok := r.roots[repoID]; ok {
		return p, nil
	}
	row, err := r.s.RepoByID(repoID)
	if err != nil {
		return "", err
	}
	r.roots[repoID] = row.Path
	return row.Path, nil
}
