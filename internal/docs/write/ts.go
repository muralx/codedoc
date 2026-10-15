package write

import (
	"fmt"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
)

// tsLang writes /** */ comments for TypeScript, TSX and JavaScript and
// the ## Purpose section of a module's README.md.
type tsLang struct{ lang string }

// tsKeptTags are the JSDoc block tags an existing doc keeps when the new
// text has no block tags of its own.
var tsKeptTags = []string{"@param", "@returns", "@return", "@throws", "@deprecated", "@see", "@example"}

func (tsLang) symbolDoc(src []byte, sym extract.Symbol, text string) ([]byte, string, error) {
	return blockComment(src, sym, withKeptTags(text, sym.Doc, tsKeptTags))
}

// blockComment writes text as the /** */ comment of sym: in place of its
// doc, or above the declaration (export keyword, decorators and
// annotations included, as the symbol span starts there).
func blockComment(src []byte, sym extract.Symbol, text string) ([]byte, string, error) {
	if strings.Contains(text, "*/") {
		return nil, "", fmt.Errorf("write: doc text for %s contains */", sym.Qualified)
	}
	indent, ok := lineIndent(src, sym.Span.StartByte)
	if !ok {
		return nil, "", fmt.Errorf("write: %s does not start its line", sym.Qualified)
	}
	lines := blockLines(text, indent)
	if sym.Doc == nil {
		return insertAbove(src, sym.Span.StartByte, lines, indent), text, nil
	}
	return replaceSpan(src, sym.Doc.Span.StartByte, sym.Doc.Span.EndByte, lines, indent), text, nil
}

// purposeHeading is the README section a module doc lives in.
const purposeHeading = "## Purpose"

// moduleDoc replaces the body of the README's ## Purpose section, appends
// the section when absent, or creates README.md holding only the section
// (so it is also the text under the README's first heading).
func (tsLang) moduleDoc(t Target, text string) (Edit, error) {
	path := moduleFile(t.ModuleDir, "README.md")
	src, existed, err := readExisting(t.Root, path)
	if err != nil {
		return Edit{}, err
	}
	section := purposeHeading + "\n\n" + text + "\n"
	if !existed {
		return Edit{Path: path, After: []byte(section), Want: text}, nil
	}
	e := Edit{Path: path, Existed: true, Before: src, Want: text}
	if body, end, ok := purposeSection(src); ok {
		lead, tail := "\n", ""
		if src[body-1] != '\n' {
			lead = "\n\n"
		}
		if end < len(src) {
			tail = "\n"
		}
		e.After = splice(src, uint(body), uint(end), lead+text+"\n"+tail)
		return e, nil
	}
	sep := "\n"
	if len(src) > 0 && src[len(src)-1] != '\n' {
		sep = "\n\n"
	}
	if len(src) == 0 {
		sep = ""
	}
	e.After = append(append([]byte{}, src...), sep+section...)
	return e, nil
}

// readModuleDoc returns the body of the README's ## Purpose section.
func (tsLang) readModuleDoc(_ string, src []byte) (*extract.Doc, int, error) {
	body, end, ok := purposeSection(src)
	if !ok {
		return nil, 0, nil
	}
	text := strings.TrimSpace(string(src[body:end]))
	start := strings.Count(string(src[:body]), "\n") + 1
	span := extract.Span{StartByte: uint(body), EndByte: uint(end), StartLine: start,
		EndLine: start + strings.Count(strings.TrimRight(string(src[body:end]), "\n"), "\n")}
	return &extract.Doc{Text: text, Span: span}, 0, nil
}

// purposeSection finds the ## Purpose heading outside code fences and
// returns the offsets of its body: from the line after the heading to the
// next level one or two heading, or the end of src.
func purposeSection(src []byte) (body, end int, ok bool) {
	fenced := false
	pos := 0
	for _, l := range strings.SplitAfter(string(src), "\n") {
		line := strings.TrimRight(l, " \t\r\n")
		switch {
		case strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~"):
			fenced = !fenced
		case fenced:
		case ok && isSectionHeading(line):
			return body, pos, true
		case !ok && strings.EqualFold(line, purposeHeading):
			body, ok = pos+len(l), true
		}
		pos += len(l)
	}
	return body, len(src), ok
}

// isSectionHeading reports whether a Markdown line is a level one or two
// ATX heading.
func isSectionHeading(line string) bool {
	for _, h := range []string{"#", "##"} {
		if line == h || strings.HasPrefix(line, h+" ") {
			return true
		}
	}
	return false
}
