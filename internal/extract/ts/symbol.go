package ts

import (
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// symbol fills the shared fields. The span runs from d's first sibling to
// its last and the doc is the comment above the first sibling. n is the
// declaration node; the signature runs from d.sig to the end of n's first
// line until withBody narrows it.
func (w *walker) symbol(kind extract.Kind, name, container string, d decl, n parse.Node,
	vis extract.Visibility) extract.Symbol {
	q := w.prefix + w.ns + name
	if container != "" {
		q = container + "." + name
	}
	span := extract.SpanBetween(d.first, d.last)
	s := extract.Symbol{
		Kind: kind, Name: name, Qualified: q, Container: container, Span: span, Visibility: vis,
		BodyHash: w.spanHash(d), Doc: w.docFor(d.first),
	}
	s.Signature, s.SigText = extract.SignatureSpan(d.sig, w.lineEnd(n.StartByte(), d.last.EndByte()), w.src)
	return s
}

// withBody ends the signature where body starts and hashes the signature
// text with the body. A grouped declaration keeps the hash of its whole
// span, so a change to any overload or accessor shows. A nil body leaves the symbol unchanged.
func (w *walker) withBody(s *extract.Symbol, d decl, body parse.Node) {
	if body == nil {
		return
	}
	s.Signature, s.SigText = extract.SignatureSpan(d.sig, body.StartByte(), w.src)
	if !d.grouped() {
		s.BodyHash = extract.CodeHash(s.SigText, w.src, body, body.StartByte(), body.EndByte())
	}
}

// spanHash hashes the whole span of d, which may cover several sibling
// nodes (overloads, an accessor pair): their parent holds the literals.
func (w *walker) spanHash(d decl) string {
	root := d.first
	if d.first.StartByte() != d.last.StartByte() {
		if p := d.first.Parent(); p != nil {
			root = p
		}
	}
	return extract.CodeHash("", w.src, root, d.first.StartByte(), d.last.EndByte())
}

// lineEnd returns the offset of the first line break in [from, end), or
// end when there is none.
func (w *walker) lineEnd(from, end uint) uint {
	for i := from; i < end; i++ {
		if w.src[i] == '\n' {
			return i
		}
	}
	return end
}

// add records a symbol; unnamed declarations are skipped.
func (w *walker) add(s extract.Symbol) {
	if s.Name != "" {
		w.res.Symbols = append(w.res.Symbols, s)
	}
}

// name returns the text of n's name field, without quotes for string
// names, or "" when absent.
func (w *walker) name(n parse.Node) string {
	c := n.ChildByField("name")
	if c == nil {
		return ""
	}
	text := c.Text(w.src)
	if c.Kind() == "string" && len(text) >= 2 {
		return text[1 : len(text)-1]
	}
	return text
}

func topVisibility(exported bool) extract.Visibility {
	if exported {
		return extract.Public
	}
	return extract.Package
}
