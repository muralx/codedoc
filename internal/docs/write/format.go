package write

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// lineWidth is the column doc comments are wrapped at.
const lineWidth = 80

// minWrap is the narrowest wrap width used for deeply indented code.
const minWrap = 40

// cleanText normalizes input doc text: CRLF becomes LF, trailing
// whitespace is trimmed from every line, and leading and trailing blank
// lines are dropped.
func cleanText(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRightFunc(l, unicode.IsSpace)
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// paragraphs splits text into runs of non-blank lines.
func paragraphs(text string) [][]string {
	var out [][]string
	var cur []string
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == "" {
			if cur != nil {
				out = append(out, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, strings.TrimRightFunc(l, unicode.IsSpace))
	}
	if cur != nil {
		out = append(out, cur)
	}
	return out
}

// normalize reduces doc text to its words per paragraph, so text compares
// equal however it was wrapped or indented.
func normalize(text string) string {
	paras := paragraphs(text)
	out := make([]string, len(paras))
	for i, p := range paras {
		out[i] = strings.Join(strings.Fields(strings.Join(p, " ")), " ")
	}
	return strings.Join(out, "\n\n")
}

// wrap fills every paragraph of text to width columns, separating
// paragraphs with one empty line. Paragraphs with an indented line (code
// blocks) are kept as written; list items and Javadoc block tags start
// new lines.
func wrap(text string, width int) []string {
	width = max(width, minWrap)
	var out []string
	for i, p := range paragraphs(text) {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, wrapParagraph(p, width)...)
	}
	return out
}

func wrapParagraph(lines []string, width int) []string {
	for _, l := range lines {
		if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") {
			return lines
		}
	}
	var out []string
	cur := ""
	for _, l := range lines {
		if isItemStart(l) && cur != "" {
			out, cur = append(out, cur), ""
		}
		for _, w := range strings.Fields(l) {
			switch {
			case cur == "":
				cur = w
			case utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(w) <= width:
				cur += " " + w
			default:
				out, cur = append(out, cur), w
			}
		}
	}
	return append(out, cur)
}

// isItemStart reports whether a line starts a list item or a Javadoc block
// tag, which wrapping must not join to the previous line.
func isItemStart(l string) bool {
	if strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") || strings.HasPrefix(l, "+ ") || isBlockTag(l) {
		return true
	}
	digits := strings.TrimLeft(l, "0123456789")
	return len(digits) < len(l) && (strings.HasPrefix(digits, ". ") || strings.HasPrefix(digits, ") "))
}

// isBlockTag reports whether a line starts with a Javadoc block tag such
// as @param.
func isBlockTag(l string) bool {
	r, _ := utf8.DecodeRuneInString(strings.TrimPrefix(l, "@"))
	return strings.HasPrefix(l, "@") && unicode.IsLetter(r)
}

// startsWithWord reports whether text starts with word followed by a rune
// that cannot continue an identifier.
func startsWithWord(text, word string) bool {
	rest, ok := strings.CutPrefix(text, word)
	if !ok {
		return false
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return rest == "" || !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
}

// lowerFirst holds the capitalized words prefixWord lower-cases when
// they start the text: common verbs and articles. Any other word (a
// proper noun, an acronym, an identifier) keeps its case.
var lowerFirst = map[string]bool{
	"Returns": true, "Creates": true, "Gets": true, "Sets": true, "Is": true, "Holds": true,
	"The": true, "A": true, "An": true, "This": true, "Reports": true, "Makes": true,
	"Adds": true, "Removes": true, "Builds": true, "Finds": true, "Lists": true, "Reads": true,
	"Writes": true, "Runs": true, "Checks": true, "Handles": true, "Describes": true,
	"Does": true, "Implements": true, "Provides": true, "Defines": true, "Contains": true,
	"Manages": true, "Wraps": true, "Converts": true, "Parses": true, "Loads": true, "Stores": true,
	"Represents": true, "Computes": true, "Validates": true, "Starts": true, "Stops": true,
}

// prefixWord puts word in front of text unless text already starts with
// it, compared case-insensitively ("store holds" for Store). A first
// word from lowerFirst ("Returns") is lower-cased.
func prefixWord(text, word string) string {
	if len(text) >= len(word) && strings.EqualFold(text[:len(word)], word) && startsWithWord(word+text[len(word):], word) {
		return text
	}
	first, rest, _ := strings.Cut(text, " ")
	if lowerFirst[first] {
		text = strings.ToLower(first[:1]) + first[1:]
		if rest != "" {
			text += " " + rest
		}
	}
	return word + " " + text
}

// widthFor returns the wrap width left after indent and a line prefix,
// counting a tab as four columns.
func widthFor(indent string, prefix int) int {
	cols := len(indent) + 3*strings.Count(indent, "\t")
	return lineWidth - cols - prefix
}

// lineStart returns the offset of the start of the line holding pos.
func lineStart(src []byte, pos uint) uint {
	for pos > 0 && src[pos-1] != '\n' {
		pos--
	}
	return pos
}

// lineIndent returns the whitespace before pos on its line and whether
// only whitespace precedes pos there.
func lineIndent(src []byte, pos uint) (string, bool) {
	before := string(src[lineStart(src, pos):pos])
	return before, strings.TrimLeft(before, " \t") == ""
}

// indentBlock prefixes every non-empty line with indent and joins them.
func indentBlock(block []string, indent string) string {
	out := make([]string, len(block))
	for i, l := range block {
		if l != "" {
			out[i] = indent + l
		}
	}
	return strings.Join(out, "\n")
}

// replaceSpan replaces [start, end) with block. When only whitespace
// surrounds the range on its first and last lines, whole lines are
// replaced; otherwise the block is put on lines of its own and the code
// around it stays, without trailing whitespace.
func replaceSpan(src []byte, start, end uint, block []string, indent string) []byte {
	s, prefix := start, "\n"
	if _, ok := lineIndent(src, start); ok {
		s, prefix = lineStart(src, start), ""
	}
	for prefix != "" && s > 0 && (src[s-1] == ' ' || src[s-1] == '\t') {
		s-- // the code before the block keeps no trailing whitespace
	}
	e, suffix := end, "\n"+indent
	rest := src[end:]
	nl := strings.IndexByte(string(rest), '\n')
	if nl < 0 {
		nl = len(rest)
	}
	if strings.TrimSpace(string(rest[:nl])) == "" {
		e, suffix = end+uint(min(nl+1, len(rest))), "\n"
	}
	for suffix != "\n" && e < uint(len(src)) && (src[e] == ' ' || src[e] == '\t') {
		e++ // the code after the block starts at the indent
	}
	return splice(src, s, e, prefix+indentBlock(block, indent)+suffix)
}

// insertAbove inserts block, indented like the line, on new lines above
// the line holding pos.
func insertAbove(src []byte, pos uint, block []string, indent string) []byte {
	at := lineStart(src, pos)
	return splice(src, at, at, indentBlock(block, indent)+"\n")
}

// splice returns src with [start, end) replaced by text.
func splice(src []byte, start, end uint, text string) []byte {
	out := make([]byte, 0, len(src)+len(text))
	out = append(out, src[:start]...)
	out = append(out, text...)
	return append(out, src[end:]...)
}

// slashLines formats text as // comment lines.
func slashLines(text string, indent string) []string {
	lines := wrap(text, widthFor(indent, 3))
	for i, l := range lines {
		if l == "" {
			lines[i] = "//"
		} else {
			lines[i] = "// " + l
		}
	}
	return lines
}

// blockLines formats text as a /** */ comment, on one line when it fits.
func blockLines(text string, indent string) []string {
	lines := wrap(text, widthFor(indent, 3))
	if len(lines) == 1 && widthFor(indent, 7) >= utf8.RuneCountInString(lines[0]) {
		return []string{"/** " + lines[0] + " */"}
	}
	out := []string{"/**"}
	for _, l := range lines {
		if l == "" {
			out = append(out, " *")
		} else {
			out = append(out, " * "+l)
		}
	}
	return append(out, " */")
}
