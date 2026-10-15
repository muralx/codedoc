// Package ts extracts symbols, docs, references and imports from
// TypeScript, TSX and JavaScript files.
package ts

import (
	"path"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

func init() {
	for _, lang := range []string{parse.TypeScript, parse.TSX, parse.JavaScript} {
		extract.Register(lang, New(lang))
	}
}

// Extractor is the TypeScript-family extractor for one precise language.
type Extractor struct{ lang string }

// New returns an extractor that parses with the grammar of lang
// (typescript, tsx or javascript).
func New(lang string) *Extractor { return &Extractor{lang: lang} }

// Extract parses a file and returns its declarations, references and
// imports. path is relative to
// the package root and prefixes every qualified name (spec 5.1).
func (e *Extractor) Extract(filePath string, src []byte) (*extract.FileResult, error) {
	tree, err := parse.Parse(e.lang, src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.Root()
	w := &walker{src: src, prefix: filePath + "/", testFile: isTestFile(filePath), moduleDocAt: -1,
		res: &extract.FileResult{}}
	if isIndex(filePath) {
		w.moduleDoc(root)
	}
	w.walk(root, false)
	extract.HashContainers(w.res.Symbols, w.enums)
	w.qualifyTests()
	extract.UniqueQualified(w.res.Symbols)
	w.refs(root)
	w.res.ParseErrors = extract.CountErrors(root)
	return w.res, nil
}

// isTestFile matches *.test.*, *.spec.* and files under __tests__/.
func isTestFile(p string) bool {
	base := path.Base(p)
	return strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		strings.HasPrefix(p, "__tests__/") || strings.Contains(p, "/__tests__/")
}

func isIndex(p string) bool {
	base := path.Base(p)
	return strings.TrimSuffix(base, path.Ext(base)) == "index"
}

// moduleDoc records a leading /** comment of an index file as the module
// doc, unless it directly precedes a declaration (no blank line between),
// which it then documents. A module doc is not attached to any symbol.
func (w *walker) moduleDoc(root parse.Node) {
	if root.NamedChildCount() == 0 {
		return
	}
	c := root.NamedChild(0)
	if c.Kind() != "comment" || !strings.HasPrefix(c.Text(w.src), "/**") {
		return
	}
	if next := c.NextSibling(); next != nil && isDeclaration(next) && next.StartRow() <= c.EndRow()+1 {
		return
	}
	w.res.ModuleDoc = &extract.Doc{Text: jsdocText(c.Text(w.src)), Span: extract.SpanOf(c)}
	w.moduleDocAt = int(c.StartByte())
}

// docFor returns the /** comment directly above n, or nil.
func (w *walker) docFor(n parse.Node) *extract.Doc {
	c := n.PrevSibling()
	if c == nil || c.Kind() != "comment" || int(c.StartByte()) == w.moduleDocAt {
		return nil
	}
	text := c.Text(w.src)
	if !strings.HasPrefix(text, "/**") || c.EndRow()+1 < n.StartRow() {
		return nil
	}
	if p := c.PrevSibling(); p != nil && p.EndRow() == c.StartRow() {
		return nil // trailing comment of the previous line's code
	}
	return &extract.Doc{Text: jsdocText(text), Span: extract.SpanOf(c)}
}

// jsdocText strips /**, */ and the leading " * " of every line.
func jsdocText(raw string) string {
	body := strings.TrimSuffix(strings.TrimPrefix(raw, "/**"), "*/")
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		l = strings.TrimSpace(l)
		l = strings.TrimPrefix(l, "*")
		lines[i] = strings.TrimPrefix(l, " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
