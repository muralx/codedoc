// Package python extracts symbols, docs, references and imports from
// Python source files.
package python

import (
	"path"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

func init() { extract.Register(parse.Python, New()) }

// Extractor is the Python extractor.
type Extractor struct{}

// New returns a Python extractor.
func New() *Extractor { return &Extractor{} }

// Extract parses a Python file and returns its declarations, references
// and imports. Qualified names are dotted inside the file (MemStore.get);
// the module identity comes from the file's dotted module path, which the
// pipeline computes (spec 5.1).
func (*Extractor) Extract(filePath string, src []byte) (*extract.FileResult, error) {
	tree, err := parse.Parse(parse.Python, src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.Root()
	w := &walker{src: src, testFile: isTestFile(filePath), res: &extract.FileResult{}, instance: map[int]bool{}}
	if d := docstring(root); d != nil {
		w.res.ModuleDoc = w.doc(d)
	}
	w.block(root, scope{fields: map[string]bool{}})
	extract.HashContainers(w.res.Symbols, nil)
	extract.UniqueQualified(w.res.Symbols)
	w.refs(root)
	w.res.ParseErrors = extract.CountErrors(root)
	return w.res, nil
}

// isTestFile matches test_*.py, *_test.py and files under a tests/
// directory.
func isTestFile(p string) bool {
	base := path.Base(p)
	return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") ||
		strings.HasPrefix(p, "tests/") || strings.Contains(p, "/tests/")
}

// visibility applies the Python naming convention: __name (not a dunder)
// is private, _name is module-internal, anything else is public.
func visibility(name string) extract.Visibility {
	switch {
	case strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__"):
		return extract.Public
	case strings.HasPrefix(name, "__"):
		return extract.Private
	case strings.HasPrefix(name, "_"):
		return extract.Package
	}
	return extract.Public
}

// isConstName reports whether a module-level name is ALL_CAPS.
func isConstName(name string) bool {
	return strings.ToUpper(name) == name && strings.ContainsFunc(name, func(r rune) bool { return r >= 'A' && r <= 'Z' })
}

// docstring returns the expression statement holding the docstring of a
// module or block: its first statement, comments aside, when that is a
// plain string literal (not an f-string). It returns nil otherwise.
func docstring(body parse.Node) parse.Node {
	for i := uint(0); i < body.NamedChildCount(); i++ {
		c := body.NamedChild(i)
		if c.Kind() == "comment" {
			continue
		}
		if c.Kind() != "expression_statement" || c.NamedChildCount() != 1 || c.NamedChild(0).Kind() != "string" {
			return nil
		}
		if start := c.NamedChild(0).NamedChild(0); start == nil || start.Kind() != "string_start" {
			return nil
		}
		return c
	}
	return nil
}

// doc returns the cleaned text of a docstring statement found by
// docstring, or nil for an f-string or an empty docstring.
func (w *walker) doc(stmt parse.Node) *extract.Doc {
	str := stmt.NamedChild(0)
	start, end := str.NamedChild(0), str.NamedChild(str.NamedChildCount()-1)
	if strings.ContainsAny(start.Text(w.src), "fF") || end.Kind() != "string_end" {
		return nil
	}
	text := cleandoc(string(w.src[start.EndByte():end.StartByte()]))
	if text == "" {
		return nil
	}
	return &extract.Doc{Text: text, Span: extract.SpanOf(stmt)}
}

// cleandoc follows inspect.cleandoc: tabs expand to 8 columns, the first
// line loses its leading whitespace, the common indentation of the other
// lines is removed, and leading and trailing blank lines are dropped.
// Trailing whitespace is dropped from every line.
func cleandoc(s string) string {
	lines := strings.Split(s, "\n")
	margin := -1
	for i, l := range lines {
		lines[i] = strings.TrimRight(expandTabs(l), " \r")
		if trimmed := strings.TrimLeft(lines[i], " "); i > 0 && trimmed != "" {
			if indent := len(lines[i]) - len(trimmed); margin < 0 || indent < margin {
				margin = indent
			}
		}
	}
	lines[0] = strings.TrimLeft(lines[0], " ")
	for i := 1; i < len(lines); i++ {
		if margin > 0 && len(lines[i]) >= margin {
			lines[i] = lines[i][margin:]
		}
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// expandTabs replaces every tab with spaces up to the next multiple of 8
// columns.
func expandTabs(l string) string {
	if !strings.Contains(l, "\t") {
		return l
	}
	var b strings.Builder
	for _, r := range l {
		if r == '\t' {
			b.WriteString(strings.Repeat(" ", 8-b.Len()%8))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
