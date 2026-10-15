package doc

import "strings"

// fence is an open fenced code block: its marker and length.
type fence struct {
	char byte
	n    int
}

// markdownHeadings returns the ATX ("## Title") and setext (a text line
// underlined with = or -) headings of Markdown or MDX lines, ignoring
// fenced code blocks, lines indented as code and HTML comments (a line
// that starts inside one, see commentAfter).
func markdownHeadings(lines []string, from int) []heading {
	var out []heading
	var open fence
	comment := false
	prev := -1 // the line a setext underline may title, -1 when none
	for i := from; i < len(lines); i++ {
		line := lines[i]
		title := prev
		prev = -1
		if open.n == 0 {
			hidden := comment
			if comment = commentAfter(line, comment); hidden {
				continue
			}
		}
		if f, ok := fenceLine(line); ok || open.n > 0 {
			open = open.next(f, ok, line)
			continue
		}
		if level, text, ok := atxHeading(line); ok {
			out = append(out, heading{level: level, text: text, first: i, last: i})
			continue
		}
		if level := setextLevel(line); level > 0 && title >= 0 {
			out = append(out, heading{level: level, text: collapse(lines[title]), first: title, last: i})
			continue
		}
		if titleCandidate(line) {
			prev = i
		}
	}
	return out
}

// next is the fence state after a line: f is the line's own fence
// marker when ok. A marker opens a block, and closes the open one when
// it is of the same character, at least as long, and alone on its line.
func (o fence) next(f fence, ok bool, line string) fence {
	switch {
	case o.n == 0:
		return f
	case ok && f.char == o.char && f.n >= o.n && strings.Trim(line, " \t"+string(f.char)) == "":
		return fence{}
	}
	return o
}

// fenceLine reports whether a line starts, after at most three spaces,
// with three or more backticks or tildes.
func fenceLine(line string) (fence, bool) {
	s, ok := unindented(line)
	if !ok || s == "" || (s[0] != '`' && s[0] != '~') {
		return fence{}, false
	}
	n := len(s) - len(strings.TrimLeft(s, s[:1]))
	if n < 3 {
		return fence{}, false
	}
	return fence{char: s[0], n: n}, true
}

// unindented strips up to three leading spaces; ok is false for a line
// indented further, which Markdown reads as code.
func unindented(line string) (string, bool) {
	s := strings.TrimLeft(line, " ")
	if len(line)-len(s) > 3 || strings.HasPrefix(s, "\t") {
		return "", false
	}
	return s, true
}

// atxHeading parses "# Title" to "###### Title", with an optional
// closing run of #; a heading without text is none.
func atxHeading(line string) (level int, text string, ok bool) {
	s, ok := unindented(line)
	if !ok {
		return 0, "", false
	}
	level = len(s) - len(strings.TrimLeft(s, "#"))
	rest := s[level:]
	if level < 1 || level > 6 || (rest != "" && rest[0] != ' ' && rest[0] != '\t') {
		return 0, "", false
	}
	text = collapse(rest)
	if closed := strings.TrimRight(text, "#"); closed == "" || strings.HasSuffix(closed, " ") {
		text = strings.TrimSpace(closed)
	}
	return level, text, text != ""
}

// setextLevel is 1 for a line of = and 2 for a line of two or more -,
// 0 for any other line.
func setextLevel(line string) int {
	s, ok := unindented(line)
	s = strings.TrimRight(s, " \t")
	switch {
	case !ok || s == "":
		return 0
	case strings.Trim(s, "=") == "":
		return 1
	case strings.Trim(s, "-") == "" && len(s) >= 2:
		return 2
	}
	return 0
}

// titleCandidate reports whether a line may be the text of a setext
// heading: a paragraph line, so not blank, not a quote, a list item, a
// table row or an HTML line.
func titleCandidate(line string) bool {
	s, ok := unindented(line)
	if !ok || strings.TrimSpace(s) == "" || strings.ContainsRune(">|<", rune(s[0])) {
		return false
	}
	return !listItem(s)
}

// listItem reports whether s starts with a bullet or an ordered list
// marker.
func listItem(s string) bool {
	if len(s) > 1 && strings.IndexByte("-*+", s[0]) >= 0 && s[1] == ' ' {
		return true
	}
	digits := len(s) - len(strings.TrimLeft(s, "0123456789"))
	return digits > 0 && digits+1 < len(s) && (s[digits] == '.' || s[digits] == ')') && s[digits+1] == ' '
}
