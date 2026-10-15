package golang

import (
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// walker collects the records of one Go file.
type walker struct {
	src      []byte
	testFile bool
	res      *extract.FileResult
	// embedded holds, by interface, the text of its embedded types and
	// type constraints, which take part in its body hash.
	embedded map[string]string
}

// walk visits the top-level declarations of a source file. Declarations
// inside function bodies are local and not indexed.
func (w *walker) walk(root parse.Node) {
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch n.Kind() {
		case "package_clause":
			w.packageClause(n)
		case "function_declaration":
			if w.field(n, "name") != "init" { // init functions cannot be referenced
				w.add(w.funcSymbol(n))
			}
		case "method_declaration":
			w.add(w.methodSymbol(n))
		case "type_declaration":
			w.typeSymbols(n)
		case "const_declaration", "var_declaration":
			w.valueSymbols(n)
		}
	}
}

// add records a symbol; blank and unnamed declarations are skipped.
func (w *walker) add(s extract.Symbol) {
	if s.Name != "" && s.Name != "_" {
		w.res.Symbols = append(w.res.Symbols, s)
	}
}

func (w *walker) packageClause(n parse.Node) {
	w.res.ModuleDoc = w.docFor(n)
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c.Kind() == "package_identifier" {
			w.res.ModuleName = c.Text(w.src)
		}
	}
}

// docFor returns the comment block that ends on the line just above n,
// without blank lines in between, or nil.
func (w *walker) docFor(n parse.Node) *extract.Doc {
	var comments []parse.Node
	next := n
	for c := n.PrevSibling(); c != nil && c.Kind() == "comment"; c = c.PrevSibling() {
		if c.EndRow()+1 != next.StartRow() || isTrailing(c) {
			break
		}
		comments = append(comments, c)
		next = c
	}
	var lines []string
	for i := len(comments) - 1; i >= 0; i-- {
		lines = append(lines, commentLines(comments[i].Text(w.src))...)
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if text == "" {
		return nil
	}
	return &extract.Doc{Text: text, Span: extract.SpanBetween(comments[len(comments)-1], comments[0])}
}

// isTrailing reports whether comment c follows code on the same line.
func isTrailing(c parse.Node) bool {
	p := c.PrevSibling()
	return p != nil && p.Kind() != "comment" && p.EndRow() == c.StartRow()
}

// commentLines strips the markers of one // or /* */ comment and one
// leading space per line. Compiler directives (//go:...) yield no lines.
func commentLines(raw string) []string {
	if strings.HasPrefix(raw, "//go:") {
		return nil
	}
	if line, ok := strings.CutPrefix(raw, "//"); ok {
		return []string{strings.TrimPrefix(line, " ")}
	}
	body := strings.TrimSuffix(strings.TrimPrefix(raw, "/*"), "*/")
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(strings.TrimRight(l, " \t\r"), " ")
	}
	return lines
}
