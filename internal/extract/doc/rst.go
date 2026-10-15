package doc

import "strings"

// rstHeadings returns the section titles of reStructuredText lines: a
// text line underlined, and optionally overlined, with one repeated
// punctuation character at least as long as the text. Levels follow
// the order in which the adornment styles first appear.
func rstHeadings(lines []string, from int) []heading {
	var out []heading
	styles := map[string]int{}
	for i := from; i+1 < len(lines); i++ {
		char, ok := rstTitle(lines, i)
		if !ok {
			continue
		}
		h := heading{text: collapse(lines[i]), first: i, last: i + 1}
		style := string(char)
		if i > from && adornment(lines[i-1]) == char && len(lines[i-1]) == len(lines[i+1]) {
			h.first, style = i-1, style+"^"
		}
		if _, known := styles[style]; !known {
			styles[style] = len(styles) + 1
		}
		h.level = styles[style]
		out = append(out, h)
		i++ // the underline is no title
	}
	return out
}

// rstTitle reports whether line i is a title: text that is no
// adornment itself, underlined by line i+1. It returns the underline
// character.
func rstTitle(lines []string, i int) (byte, bool) {
	text := strings.TrimRight(lines[i], " \t")
	if strings.TrimSpace(text) == "" || adornment(text) != 0 {
		return 0, false
	}
	char := adornment(lines[i+1])
	if char == 0 || len(strings.TrimRight(lines[i+1], " \t")) < len(strings.TrimSpace(text)) {
		return 0, false
	}
	// An indented line is a title only under an overline.
	if text[0] == ' ' && (i == 0 || adornment(lines[i-1]) != char) {
		return 0, false
	}
	return char, true
}

// adornment returns the character of a line made of three or more of
// one punctuation character, 0 for any other line.
func adornment(line string) byte {
	s := strings.TrimRight(line, " \t")
	if len(s) < 3 || !strings.ContainsRune("=-`:'\"~^_*+#<>.", rune(s[0])) || strings.Trim(s, s[:1]) != "" {
		return 0
	}
	return s[0]
}
