package doc

import "strings"

// asciidocHeadings returns the titles of AsciiDoc lines: "= Title" to
// "====== Title" (the Markdown-style # is accepted too), outside
// delimited blocks.
func asciidocHeadings(lines []string, from int) []heading {
	var out []heading
	block := ""
	for i := from; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		if d := blockDelimiter(line); d != "" || block != "" {
			switch {
			case block == "":
				block = d
			case d == block:
				block = ""
			}
			continue
		}
		if level, text, ok := asciidocTitle(line); ok {
			out = append(out, heading{level: level, text: text, first: i, last: i})
		}
	}
	return out
}

// blockDelimiter returns a line that opens or closes a delimited block
// whose content is not parsed for titles (listing, literal, passthrough,
// comment): four or more of one of - . + /, or a Markdown-style fence.
func blockDelimiter(line string) string {
	if strings.HasPrefix(line, "```") {
		return "```"
	}
	if len(line) >= 4 && strings.IndexByte("-.+/", line[0]) >= 0 && strings.Trim(line, line[:1]) == "" {
		return line
	}
	return ""
}

// asciidocTitle parses a title line: one to six = or #, a space, text.
func asciidocTitle(line string) (level int, text string, ok bool) {
	if line == "" || (line[0] != '=' && line[0] != '#') {
		return 0, "", false
	}
	level = len(line) - len(strings.TrimLeft(line, line[:1]))
	rest := line[level:]
	if level > 6 || !strings.HasPrefix(rest, " ") {
		return 0, "", false
	}
	text = collapse(rest)
	return level, text, text != ""
}
