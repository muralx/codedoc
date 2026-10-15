package doc

import (
	"fmt"
	"strconv"

	"github.com/cespare/xxhash/v2"

	"github.com/muralx/codedoc/internal/extract"
)

// builder turns the headings of a document into symbols.
type builder struct {
	doc   document
	heads []heading
	from  int // first line after the front matter
	// qualified holds the qualified name of each heading once symbols
	// ran.
	qualified []string
}

// symbols returns the document symbol named name, then one section per
// heading. A document without lines has a zero span.
func (b *builder) symbols(name string, fm frontMatter) []extract.Symbol {
	out := []extract.Symbol{b.document(name, fm)}
	b.qualified = make([]string, len(b.heads))
	seen := map[string]bool{}
	var stack []int // indexes of the open headings, outermost first
	for i, h := range b.heads {
		for len(stack) > 0 && b.heads[stack[len(stack)-1]].level >= h.level {
			stack = stack[:len(stack)-1]
		}
		parent := ""
		if len(stack) > 0 {
			parent = b.qualified[stack[len(stack)-1]]
		}
		b.qualified[i] = unique(seen, join(parent, h.text))
		out = append(out, b.section(i, parent))
		stack = append(stack, i)
	}
	return out
}

// join appends a heading to the qualified name of its parent.
func join(parent, text string) string {
	if parent == "" {
		return text
	}
	return parent + PathSep + text
}

// unique returns q, or q with the first free suffix of ~2, ~3 and so on
// when an earlier section took it, and marks the result taken.
func unique(seen map[string]bool, q string) string {
	out := q
	for n := 2; seen[out]; n++ {
		out = q + "~" + strconv.Itoa(n)
	}
	seen[out] = true
	return out
}

// document is the symbol of the whole file: its signature is the front
// matter title, else the first heading, else the file name; its doc the
// front matter description, else the first paragraph of prose.
func (b *builder) document(name string, fm frontMatter) extract.Symbol {
	sy := extract.Symbol{Kind: extract.KindDocument, Name: name, SigText: name, Visibility: extract.Public,
		BodyHash: hash(b.doc.src)}
	if n := len(b.doc.lines); n > 0 {
		sy.Span = b.doc.span(0, n-1)
		sy.Signature = b.doc.span(0, 0)
	}
	switch {
	case fm.title != "":
		sy.SigText = fm.title
	case len(b.heads) > 0:
		sy.SigText = b.heads[0].text
	}
	text := fm.description
	if text == "" {
		text = b.firstProse()
	}
	sy.Doc = docOf(text, sy.Signature)
	return sy
}

// firstProse is the first paragraph of the document: of the text
// before the first heading, else of the first section that has text of
// its own.
func (b *builder) firstProse() string {
	end := len(b.doc.lines) - 1
	if len(b.heads) > 0 {
		end = b.heads[0].first - 1
	}
	if t := firstParagraph(b.doc.text(b.from, end)); t != "" {
		return t
	}
	for i := range b.heads {
		if t := firstParagraph(b.ownText(i)); t != "" {
			return t
		}
	}
	return ""
}

// section is the symbol of heading i, whose parent section is named
// parent.
func (b *builder) section(i int, parent string) extract.Symbol {
	h := b.heads[i]
	span := b.doc.span(h.first, b.doc.lastText(h.last, b.end(i)))
	sig := b.doc.span(h.first, h.last)
	return extract.Symbol{Kind: extract.KindSection, Name: h.text, Qualified: b.qualified[i], Container: parent,
		Span: span, Signature: sig, SigText: sigText(b.doc.lines[h.first], h), Visibility: extract.Public,
		BodyHash: hash(b.doc.src[span.StartByte:span.EndByte]), Doc: docOf(Summary(b.ownText(i)), sig)}
}

// sigText is the heading line as written; for a title marked by the
// lines around it (setext, reStructuredText) the title text.
func sigText(line string, h heading) string {
	if h.first == h.last {
		return collapse(line)
	}
	return h.text
}

// end is the last line of section i: the line before the next heading
// of the same or a higher level, else the last line of the document.
func (b *builder) end(i int) int {
	for _, n := range b.heads[i+1:] {
		if n.level <= b.heads[i].level {
			return n.first - 1
		}
	}
	return len(b.doc.lines) - 1
}

// ownText is the text of section i up to its first sub-heading.
func (b *builder) ownText(i int) string {
	last := len(b.doc.lines) - 1
	if i+1 < len(b.heads) {
		last = b.heads[i+1].first - 1
	}
	return b.doc.text(b.heads[i].last+1, last)
}

// owners maps each 0-based line to the qualified name of the innermost
// section holding it, "" before the first heading.
func (b *builder) owners() []string {
	out := make([]string, len(b.doc.lines))
	cur, next := "", 0
	for l := range out {
		for next < len(b.heads) && b.heads[next].first <= l {
			cur = b.qualified[next]
			next++
		}
		out[l] = cur
	}
	return out
}

// docOf wraps a summary as the doc of a symbol; nil when it is empty.
// A section's text has no span of its own that a writer could replace,
// so the doc carries the heading's.
func docOf(text string, sig extract.Span) *extract.Doc {
	if text == "" {
		return nil
	}
	return &extract.Doc{Text: text, Span: sig}
}

func hash(b []byte) string { return fmt.Sprintf("%016x", xxhash.Sum64(b)) }
