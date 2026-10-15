package extract

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/cespare/xxhash/v2"

	"github.com/muralx/codedoc/internal/parse"
)

// SpanOf returns the span of a node.
func SpanOf(n parse.Node) Span {
	return SpanBetween(n, n)
}

// SpanBetween returns the span from the start of first to the end of last.
func SpanBetween(first, last parse.Node) Span {
	return Span{
		StartByte: first.StartByte(), EndByte: last.EndByte(),
		StartLine: int(first.StartRow()) + 1, EndLine: int(last.EndRow()) + 1,
	}
}

// SignatureSpan returns the span from the start of from up to end (an
// exclusive byte offset), with trailing whitespace dropped, and the
// whitespace-collapsed text of that range.
func SignatureSpan(from parse.Node, end uint, src []byte) (Span, string) {
	raw := strings.TrimRightFunc(string(src[from.StartByte():end]), unicode.IsSpace)
	span := Span{
		StartByte: from.StartByte(), EndByte: from.StartByte() + uint(len(raw)),
		StartLine: int(from.StartRow()) + 1,
	}
	span.EndLine = span.StartLine + strings.Count(raw, "\n")
	return span, Collapse(raw)
}

// LineEnd returns the offset where n's first line ends (exclusive), or the
// end of n when it fits on one line.
func LineEnd(n parse.Node, src []byte) uint {
	for i := n.StartByte(); i < n.EndByte(); i++ {
		if src[i] == '\n' {
			return i
		}
	}
	return n.EndByte()
}

// NamedChildren returns the named children of n whose kind is one of kinds,
// in source order.
func NamedChildren(n parse.Node, kinds ...string) []parse.Node {
	var out []parse.Node
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); slices.Contains(kinds, c.Kind()) {
			out = append(out, c)
		}
	}
	return out
}

// Collapse joins the whitespace-separated words of s with single spaces and
// drops the spaces a line break leaves just inside parentheses.
func Collapse(s string) string {
	out := strings.Join(strings.Fields(s), " ")
	return strings.NewReplacer("( ", "(", " )", ")").Replace(out)
}

// BodyHash returns the hex xxhash of text with all whitespace removed.
func BodyHash(text string) string {
	return fmt.Sprintf("%016x", xxhash.Sum64String(stripSpace(text)))
}

func stripSpace(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
}

// literalKinds are the node kinds, across the grammars, that hold the
// text of a string or character literal: their whitespace is part of the
// value. Interpolations (f-strings, template strings) are separate nodes,
// so the code inside them is not among these.
var literalKinds = map[string]bool{
	"interpreted_string_literal_content": true, "raw_string_literal_content": true, "rune_literal": true, // Go
	"string_fragment": true, "multiline_string_fragment": true, "character_literal": true, // Java, TS/JS
	"string_content": true, // Python
}

// NodeHash is CodeHash over the whole of n, without a prefix.
func NodeHash(src []byte, n parse.Node) string {
	return CodeHash("", src, n, n.StartByte(), n.EndByte())
}

// CodeHash returns the body hash of prefix (a signature text) followed by
// src[from:to], a range of the code under root: the hex xxhash of the
// text with whitespace removed everywhere except inside the string and
// character literals of root in the range, so reformatting leaves the
// hash alone while editing a literal's spaces changes it. Without
// literals it equals BodyHash(prefix + src[from:to]).
func CodeHash(prefix string, src []byte, root parse.Node, from, to uint) string {
	var b strings.Builder
	b.WriteString(stripSpace(prefix))
	pos := from
	eachLiteral(root, from, to, func(n parse.Node) {
		b.WriteString(stripSpace(string(src[pos:n.StartByte()])))
		b.Write(src[n.StartByte():n.EndByte()])
		pos = n.EndByte()
	})
	b.WriteString(stripSpace(string(src[pos:to])))
	return fmt.Sprintf("%016x", xxhash.Sum64String(b.String()))
}

// eachLiteral calls fn, in source order, for every literal node under n
// that lies within [from, to).
func eachLiteral(n parse.Node, from, to uint, fn func(parse.Node)) {
	if n.EndByte() <= from || n.StartByte() >= to {
		return
	}
	if literalKinds[n.Kind()] && n.StartByte() >= from && n.EndByte() <= to {
		fn(n)
		return
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		eachLiteral(n.Child(i), from, to, fn)
	}
}

// CountErrors returns the number of ERROR and missing-token nodes under n.
func CountErrors(n parse.Node) int {
	if !n.HasError() {
		return 0
	}
	count := 0
	if n.Kind() == "ERROR" {
		count++
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		count += CountErrors(n.Child(i))
	}
	if count == 0 {
		// HasError without an ERROR descendant means a missing token.
		count = 1
	}
	return count
}
