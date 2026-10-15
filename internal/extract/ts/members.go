package ts

import (
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// class adds a class and its members. Overload signatures and accessor
// pairs collapse into one method symbol (see decls).
func (w *walker) class(n parse.Node, d decl, exported bool) {
	body := n.ChildByField("body")
	s := w.symbol(extract.KindClass, w.name(n), "", d, n, topVisibility(exported))
	w.withBody(&s, d, body)
	w.add(s)
	if body == nil {
		return
	}
	for _, m := range w.decls(body, memberKey) {
		switch m.last.Kind() {
		case "method_definition", "method_signature", "abstract_method_signature":
			w.member(extract.KindMethod, m, s.Qualified, exported)
		case "public_field_definition":
			w.member(extract.KindField, m, s.Qualified, exported)
		}
	}
}

// iface adds an interface with its method and property signatures, in
// source order.
func (w *walker) iface(n parse.Node, d decl, exported bool) {
	body := n.ChildByField("body")
	s := w.symbol(extract.KindInterface, w.name(n), "", d, n, topVisibility(exported))
	w.withBody(&s, d, body)
	w.add(s)
	w.signatures(body, s.Qualified, exported)
}

// signatures adds the method and property signatures of an interface
// body or of an object type as members of container.
func (w *walker) signatures(body parse.Node, container string, exported bool) {
	if body == nil {
		return
	}
	for _, m := range w.decls(body, interfaceKey) {
		switch m.last.Kind() {
		case "method_signature":
			w.member(extract.KindMethod, m, container, exported)
		case "property_signature":
			w.member(extract.KindField, m, container, exported)
		}
	}
}

// objectAlias adds the members of a type alias of an object type (type
// T = { a: A; m(): B }) as an interface's: references reach the types
// its properties declare through them.
func (w *walker) objectAlias(n parse.Node, container string, exported bool) {
	if value := n.ChildByField("value"); value != nil && value.Kind() == "object_type" {
		w.signatures(value, container, exported)
	}
}

// member adds a class or interface member. It is private when marked
// private or #named, public when its container is exported, and package
// visible otherwise (including protected members).
func (w *walker) member(kind extract.Kind, d decl, container string, exported bool) {
	m := d.last
	vis := topVisibility(exported)
	for _, mod := range extract.NamedChildren(m, "accessibility_modifier") {
		switch strings.TrimSpace(mod.Text(w.src)) {
		case "private":
			vis = extract.Private
		case "protected":
			vis = extract.Package
		}
	}
	if name := m.ChildByField("name"); name != nil && name.Kind() == "private_property_identifier" {
		vis = extract.Private
	}
	s := w.symbol(kind, w.name(m), container, d, m, vis)
	if kind == extract.KindMethod {
		w.withBody(&s, d, m.ChildByField("body"))
	}
	if readonly(m) {
		s.Value, _ = stringLiteral(m.ChildByField("value"), w.src)
	}
	w.add(s)
}

// readonly reports whether a class field is declared readonly.
func readonly(m parse.Node) bool {
	for i := uint(0); i < m.ChildCount(); i++ {
		if m.Child(i).Kind() == "readonly" {
			return true
		}
	}
	return false
}
