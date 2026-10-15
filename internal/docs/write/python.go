package write

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// pyLang writes docstrings.
type pyLang struct{}

// pyIndentUnit is the indentation added for a body written from scratch.
const pyIndentUnit = "    "

func (pyLang) symbolDoc(src []byte, sym extract.Symbol, text string) ([]byte, string, error) {
	switch sym.Kind {
	case extract.KindFunction, extract.KindMethod, extract.KindClass:
	default:
		return nil, "", fmt.Errorf("write: python %s %s cannot carry a docstring", sym.Kind, sym.Qualified)
	}
	if sym.Doc != nil {
		indent, ok := lineIndent(src, sym.Doc.Span.StartByte)
		if !ok { // def f(): """doc"""
			def, _ := lineIndent(src, sym.Signature.StartByte)
			indent = def + indentUnit(def)
		}
		lines, err := docstring(text, indent)
		if err != nil {
			return nil, "", err
		}
		return replaceSpan(src, sym.Doc.Span.StartByte, sym.Doc.Span.EndByte, lines, indent), text, nil
	}
	out, err := insertDocstring(src, sym, text)
	return out, text, err
}

// insertDocstring adds a docstring as the first statement of sym's body.
// The colon that opens the body follows the signature span.
func insertDocstring(src []byte, sym extract.Symbol, text string) ([]byte, error) {
	colon := sym.Signature.EndByte
	for colon < uint(len(src)) && (src[colon] == ' ' || src[colon] == '\t') {
		colon++
	}
	if colon >= uint(len(src)) || src[colon] != ':' {
		return nil, fmt.Errorf("write: no body colon after the signature of %s", sym.Qualified)
	}
	defIndent, _ := lineIndent(src, sym.Signature.StartByte)
	rest := string(src[colon+1:])
	nl := strings.IndexByte(rest, '\n')
	if inline := strings.Trim(rest[:max(nl, 0)], " \t\r"); nl < 0 || (inline != "" && !strings.HasPrefix(inline, "#")) {
		return inlineBody(src, colon, sym, text, defIndent)
	}
	at := colon + 1 + uint(nl) + 1
	indent := bodyIndent(string(src[at:]), defIndent)
	lines, err := docstring(text, indent)
	if err != nil {
		return nil, err
	}
	return splice(src, at, at, indentBlock(lines, indent)+"\n"), nil
}

// inlineBody handles "def f(): return x": the docstring goes on its own
// line and the inline statement moves below it.
func inlineBody(src []byte, colon uint, sym extract.Symbol, text, defIndent string) ([]byte, error) {
	start := colon + 1
	for start < uint(len(src)) && (src[start] == ' ' || src[start] == '\t') {
		start++
	}
	if start >= uint(len(src)) || src[start] == '\n' {
		return nil, fmt.Errorf("write: %s has no body", sym.Qualified)
	}
	indent := defIndent + indentUnit(defIndent)
	lines, err := docstring(text, indent)
	if err != nil {
		return nil, err
	}
	return splice(src, colon+1, start, "\n"+indentBlock(lines, indent)+"\n"+indent), nil
}

// bodyIndent returns the indentation of the first non-blank line of body
// when it is deeper than the def, else the def's plus one unit.
func bodyIndent(body, defIndent string) string {
	for _, l := range strings.Split(body, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		if len(indent) > len(defIndent) && strings.HasPrefix(indent, defIndent) {
			return indent
		}
		break
	}
	return defIndent + indentUnit(defIndent)
}

func indentUnit(defIndent string) string {
	if strings.Contains(defIndent, "\t") {
		return "\t"
	}
	return pyIndentUnit
}

// docstring formats text as a triple double quoted docstring: on one line
// when it fits, else the summary on the opening line and the closing
// quotes on their own line. Text with a backslash becomes a raw string.
func docstring(text, indent string) ([]string, error) {
	if strings.Contains(text, `"""`) {
		return nil, fmt.Errorf(`write: doc text contains """`)
	}
	open := `"""`
	if strings.Contains(text, `\`) {
		open = `r"""`
	}
	width := widthFor(indent, len(open))
	lines := wrap(text, width)
	if len(lines) == 1 && !strings.HasSuffix(lines[0], `"`) && utf8.RuneCountInString(lines[0])+3 <= width {
		return []string{open + lines[0] + `"""`}, nil
	}
	lines[0] = open + lines[0]
	return append(lines, `"""`), nil
}

// moduleDoc writes the docstring of a package's __init__.py (created when
// absent) or, when ModuleDir names a .py file, of that file module.
func (pyLang) moduleDoc(t Target, text string) (Edit, error) {
	path := t.ModuleDir
	if !strings.HasSuffix(path, ".py") {
		path = moduleFile(t.ModuleDir, "__init__.py")
	}
	src, existed, err := readExisting(t.Root, path)
	if err != nil {
		return Edit{}, err
	}
	lines, err := docstring(text, "")
	if err != nil {
		return Edit{}, err
	}
	block := strings.Join(lines, "\n") + "\n"
	e := Edit{Path: path, Existed: existed, Before: src, After: []byte(block), Want: text}
	if !existed {
		return e, nil
	}
	res, err := extractSource(parse.Python, path, src)
	if err != nil {
		return Edit{}, err
	}
	if d := res.ModuleDoc; d != nil {
		e.After = replaceSpan(src, d.Span.StartByte, d.Span.EndByte, lines, "")
		return e, nil
	}
	e.After = insertModuleDocstring(src, block)
	return e, nil
}

// insertModuleDocstring puts block after the leading comment lines
// (shebang, encoding, license) and before the first statement, separated
// from it by a blank line.
func insertModuleDocstring(src []byte, block string) []byte {
	pos := 0
	for _, l := range strings.SplitAfter(string(src), "\n") {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			return splice(src, uint(pos), uint(pos), block+"\n")
		}
		pos += len(l)
	}
	if len(src) > 0 && src[len(src)-1] != '\n' {
		block = "\n" + block
	}
	return append(append([]byte{}, src...), block...)
}

func (pyLang) readModuleDoc(path string, src []byte) (*extract.Doc, int, error) {
	return moduleFileDoc(parse.Python, path, src)
}
