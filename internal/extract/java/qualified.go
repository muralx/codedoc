package java

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// packageChain returns the segments of n when n is a chain of plain
// identifiers joined by field accesses that starts with a package name
// (java.util.List), with the index of its first type segment; it returns
// nil, -1 for any other expression.
func (r *refWalker) packageChain(n parse.Node) ([]parse.Node, int) {
	var segs []parse.Node
	for n != nil && n.Kind() == "field_access" {
		segs = append(segs, n.ChildByField("field"))
		n = n.ChildByField("object")
	}
	if n == nil || n.Kind() != "identifier" || len(segs) == 0 || slices.Contains(segs, nil) {
		return nil, -1
	}
	segs = append(segs, n)
	slices.Reverse(segs)
	texts := make([]string, len(segs))
	for i, s := range segs {
		texts[i] = s.Text(r.src)
	}
	if t := typeSegment(texts); t > 0 {
		return segs, t
	}
	return nil, -1
}

// qualifiedType records a package-qualified expression such as
// java.util.Locale.US: a type reference to Locale qualified by its package,
// then a field reference for each later segment, qualified from the type
// on (US on Locale). It reports whether n was such an expression.
func (r *refWalker) qualifiedType(n parse.Node) bool {
	segs, t := r.packageChain(n)
	if segs == nil {
		return false
	}
	text := func(from, to int) string {
		parts := make([]string, 0, to-from)
		for _, s := range segs[from:to] {
			parts = append(parts, s.Text(r.src))
		}
		return strings.Join(parts, ".")
	}
	r.add(extract.RefType, segs[t], text(0, t), -1)
	for j := t + 1; j < len(segs); j++ {
		r.add(extract.RefField, segs[j], text(t, j), -1)
	}
	return true
}

// stripPackage drops the package prefix of a receiver chain, so that
// java.util.List.of() qualifies a call as List.of().
func stripPackage(q string) string {
	segs := strings.Split(q, ".")
	if t := typeSegment(segs); t > 0 {
		return strings.Join(segs[t:], ".")
	}
	return q
}

// typeSegment returns the index of the type in a package-qualified chain:
// one or more lowercase plain names followed by a capitalized plain name.
// After a single package segment the type must also contain a lowercase
// letter, so that variable.CONSTANT is not read as a type. It returns -1
// when segs does not start with a package.
func typeSegment(segs []string) int {
	if len(segs) < 2 || segs[0] == "this" || segs[0] == "super" {
		return -1
	}
	for i, s := range segs {
		if s == "" || strings.ContainsAny(s, "()") {
			return -1
		}
		if !startsUpper(s) {
			continue
		}
		if i == 0 || (i == 1 && !strings.ContainsFunc(s, unicode.IsLower)) {
			return -1
		}
		return i
	}
	return -1
}

func startsUpper(s string) bool {
	c, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(c)
}
