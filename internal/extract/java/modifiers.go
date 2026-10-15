package java

import (
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// skipped are the children a signature does not start with.
var skipped = map[string]bool{
	"marker_annotation": true, "annotation": true, "line_comment": true, "block_comment": true,
}

// sigStart returns the first token of declaration n after its annotations
// and comments.
func sigStart(n parse.Node) parse.Node {
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c.Kind() != "modifiers" {
			if !skipped[c.Kind()] {
				return c
			}
			continue
		}
		for j := uint(0); j < c.ChildCount(); j++ {
			if m := c.Child(j); !skipped[m.Kind()] {
				return m
			}
		}
	}
	return n
}

// modifiers returns the modifiers node of a declaration, or nil.
func modifiers(n parse.Node) parse.Node {
	if mods := extract.NamedChildren(n, "modifiers"); len(mods) > 0 {
		return mods[0]
	}
	return nil
}

// hasModifier reports whether declaration n carries keyword kw.
func (w *walker) hasModifier(n parse.Node, kw string) bool {
	mods := modifiers(n)
	for i := uint(0); mods != nil && i < mods.ChildCount(); i++ {
		if mods.Child(i).Kind() == kw {
			return true
		}
	}
	return false
}

// visibility maps the access modifier: public is Public, private is
// Private, protected and package-private are Package. Members of an
// interface are Public unless private.
func (w *walker) visibility(n parse.Node, iface bool) extract.Visibility {
	switch {
	case w.hasModifier(n, "private"):
		return extract.Private
	case iface || w.hasModifier(n, "public"):
		return extract.Public
	}
	return extract.Package
}

// annotations returns the simple names of the annotations on n.
func (w *walker) annotations(n parse.Node) []string {
	mods := modifiers(n)
	if mods == nil {
		return nil
	}
	var names []string
	for _, a := range extract.NamedChildren(mods, "marker_annotation", "annotation") {
		if name := a.ChildByField("name"); name != nil {
			names = append(names, w.lastSegment(name))
		}
	}
	return names
}

// lastSegment returns the final identifier of a possibly scoped name:
// a.b.C gives C.
func (w *walker) lastSegment(n parse.Node) string {
	if n.Kind() == "scoped_identifier" || n.Kind() == "scoped_type_identifier" {
		if n.NamedChildCount() > 0 {
			return w.lastSegment(n.NamedChild(n.NamedChildCount() - 1))
		}
	}
	return n.Text(w.src)
}
