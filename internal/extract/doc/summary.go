package doc

import (
	"strings"
	"unicode/utf8"
)

// SummaryBytes is the most bytes of a section's own text kept as its
// doc, which one-line summaries and search snippets are cut from.
const SummaryBytes = 600

// Summary trims text and cuts it to SummaryBytes at a sentence end,
// else at the last white space, else at a rune boundary.
func Summary(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= SummaryBytes {
		return text
	}
	cut := text[:SummaryBytes]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	if i := lastSentenceEnd(cut); i > 0 {
		return cut[:i]
	}
	if i := strings.LastIndexAny(cut, " \t\n"); i > 0 {
		return strings.TrimSpace(cut[:i])
	}
	return cut
}

// lastSentenceEnd is the offset after the last '.', '!' or '?' of s
// that white space follows, 0 when there is none.
func lastSentenceEnd(s string) int {
	for i := len(s) - 2; i > 0; i-- {
		if strings.IndexByte(".!?", s[i]) >= 0 && strings.IndexByte(" \t\n", s[i+1]) >= 0 {
			return i + 1
		}
	}
	return 0
}

// firstParagraph is the summary of the first paragraph of text: its
// lines up to the first blank one.
func firstParagraph(text string) string {
	para, _, _ := strings.Cut(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n\n")
	return Summary(para)
}

// collapse joins the fields of s with single spaces.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }
