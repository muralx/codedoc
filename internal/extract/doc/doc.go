// Package doc extracts documents (documents spec section 2): a Markdown,
// reStructuredText, AsciiDoc or plain text file becomes one symbol of
// kind document, and each of its headings a symbol of kind section that
// runs to the next heading of the same or a higher level.
package doc

import (
	"path"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// PathSep joins the headings of a section's qualified name, outermost
// first: "6. Delivery > 6.2 Retries".
const PathSep = " > "

// heading is one heading of a document. Lines are 0-based indexes into
// the document's lines; first and last cover the heading's own lines
// (two for a setext or underlined title, three with an overline).
type heading struct {
	level       int
	text        string
	first, last int
}

// scanner finds the headings of the lines from index from on.
type scanner func(lines []string, from int) []heading

// extractor extracts one document format; front tells whether it may
// start with YAML front matter, links whether its relative links are
// recorded.
type extractor struct {
	scan  scanner
	front bool
	links bool
}

func init() {
	extract.Register(parse.Markdown, extractor{scan: markdownHeadings, front: true, links: true})
	extract.Register(parse.RST, extractor{scan: rstHeadings})
	extract.Register(parse.ASCIIDoc, extractor{scan: asciidocHeadings})
	extract.Register(parse.Text, extractor{scan: func([]string, int) []heading { return nil }})
}

// Extract returns the document symbol of src, its sections in source
// order, and for Markdown its relative links as references of kind
// link. It never fails: text without headings is a document without
// sections.
func (e extractor) Extract(p string, src []byte) (*extract.FileResult, error) {
	d := newDocument(src)
	var fm frontMatter
	if e.front {
		fm = readFrontMatter(d.lines)
	}
	b := builder{doc: d, heads: e.scan(d.lines, fm.lines), from: fm.lines}
	res := &extract.FileResult{Symbols: b.symbols(path.Base(p), fm)}
	if e.links {
		res.References = links(d.lines, fm.lines, b.owners())
	}
	return res, nil
}

// document is a source split into lines, with the byte offset at which
// each line starts.
type document struct {
	src    []byte
	lines  []string // without line terminators
	starts []uint   // byte offset of each line
}

func newDocument(src []byte) document {
	d := document{src: src}
	off := uint(0)
	for _, l := range strings.SplitAfter(string(src), "\n") {
		if l == "" {
			break
		}
		d.starts = append(d.starts, off)
		d.lines = append(d.lines, strings.TrimRight(l, "\r\n"))
		off += uint(len(l))
	}
	return d
}

// span is the span of the 0-based lines first to last, inclusive.
func (d document) span(first, last int) extract.Span {
	return extract.Span{StartByte: d.starts[first], EndByte: d.starts[last] + uint(len(d.lines[last])),
		StartLine: first + 1, EndLine: last + 1}
}

// text joins the 0-based lines first to last; empty when first is past
// last.
func (d document) text(first, last int) string {
	if first > last {
		return ""
	}
	return strings.Join(d.lines[first:last+1], "\n")
}

// lastText is the last non-blank line in first..last, or first when
// the lines after it are all blank.
func (d document) lastText(first, last int) int {
	for last > first && strings.TrimSpace(d.lines[last]) == "" {
		last--
	}
	return last
}
