package java

import (
	"fmt"
	"strings"

	"github.com/cespare/xxhash/v2"

	"github.com/muralx/codedoc/internal/parse"
)

// sigHash returns the first four hex digits of the xxhash of the
// normalized parameter type list (spec 5.1): simple type names joined by
// commas, generics erased (List<String> is List), qualification dropped
// (java.util.Map is Map), array dimensions kept wherever they are written,
// varargs as T... and annotations and modifiers ignored. A missing list
// hashes like an empty one.
func (w *walker) sigHash(params parse.Node) string {
	var types []string
	for i := uint(0); params != nil && i < params.NamedChildCount(); i++ {
		switch p := params.NamedChild(i); p.Kind() {
		case "formal_parameter":
			types = append(types, w.typeName(p.ChildByField("type"))+compact(w.text(p.ChildByField("dimensions"))))
		case "spread_parameter":
			for j := uint(0); j < p.NamedChildCount(); j++ {
				if c := p.NamedChild(j); c.Kind() != "modifiers" && c.Kind() != "variable_declarator" {
					types = append(types, w.typeName(c)+"...")
					break
				}
			}
		}
	}
	return fmt.Sprintf("%016x", xxhash.Sum64String(strings.Join(types, ",")))[:4]
}

// typeName renders a type node in normalized form.
func (w *walker) typeName(t parse.Node) string {
	if t == nil {
		return ""
	}
	switch t.Kind() {
	case "generic_type": // the raw type comes first
		return w.typeName(t.NamedChild(0))
	case "annotated_type": // the annotations come first
		return w.typeName(t.NamedChild(t.NamedChildCount() - 1))
	case "scoped_type_identifier":
		return w.lastSegment(t)
	case "array_type":
		return w.typeName(t.ChildByField("element")) + compact(w.text(t.ChildByField("dimensions")))
	}
	return compact(t.Text(w.src))
}

// text returns the source text of n, or "" for a nil node.
func (w *walker) text(n parse.Node) string {
	if n == nil {
		return ""
	}
	return n.Text(w.src)
}

// compact removes all whitespace.
func compact(s string) string { return strings.Join(strings.Fields(s), "") }
