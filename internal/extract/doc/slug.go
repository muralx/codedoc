package doc

import (
	"regexp"
	"strings"
	"unicode"
)

// Slug is the anchor a Markdown renderer gives a heading: lower case,
// spaces as dashes, everything but letters, digits, dashes and
// underscores dropped ("6.2 Retries" is "62-retries").
func Slug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(heading)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// numbering matches the numbering a heading may start with: digits or
// one letter, then dotted digits, closed by an optional dot, parenthesis
// or colon.
var numbering = regexp.MustCompile(`^((?:[0-9]+|[A-Za-z])(?:\.[0-9]+)*)([.):]?)$`)

// Number is the numbering a heading starts with, without its closing
// punctuation: "6" for "6. Delivery", "6.2" for "6.2 Retries", "A.1"
// for "A.1 Notes"; "" for a heading that starts with none. A single
// letter counts only when punctuation closes it ("A. Appendix"), so a
// heading that starts with the word "A" has no numbering.
func Number(heading string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(heading), " ")
	m := numbering.FindStringSubmatch(first)
	if m == nil || (len(m[1]) == 1 && unicode.IsLetter(rune(m[1][0])) && m[2] == "") {
		return ""
	}
	return m[1]
}
