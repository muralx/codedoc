package doc

import "strings"

// Section is one heading of a Markdown text with the text below it up
// to the next heading of any level.
type Section struct {
	// Level is 1 to 6; 0 for the text before the first heading.
	Level int
	// Title is the heading text; Heading the heading as "## Title".
	Title, Heading string
	// Body is the text below the heading, line ends kept.
	Body string
}

// MarkdownSections splits Markdown at its headings, as the document
// extractor finds them (fenced code and front matter skipped). The
// first section has level 0 and holds the text before any heading.
func MarkdownSections(src string) []Section {
	d := newDocument([]byte(src))
	from := readFrontMatter(d.lines).lines
	heads := markdownHeadings(d.lines, from)
	out := make([]Section, 0, len(heads)+1)
	start := from
	sec := Section{}
	for _, h := range heads {
		sec.Body = d.raw(start, h.first)
		out = append(out, sec)
		sec = Section{Level: h.level, Title: h.text, Heading: strings.Repeat("#", h.level) + " " + h.text}
		start = h.last + 1
	}
	sec.Body = d.raw(start, len(d.lines))
	return append(out, sec)
}

// raw is the source of the 0-based lines first up to, not including,
// end, with their line ends.
func (d document) raw(first, end int) string {
	if first >= end || first >= len(d.lines) {
		return ""
	}
	stop := uint(len(d.src))
	if end < len(d.lines) {
		stop = d.starts[end]
	}
	return string(d.src[d.starts[first]:stop])
}
