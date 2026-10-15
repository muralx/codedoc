package golang

import (
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// typeSymbols adds one symbol per type_spec or type_alias of a type
// declaration, plus struct fields and interface methods.
func (w *walker) typeSymbols(decl parse.Node) {
	isGrouped := grouped(decl)
	for _, spec := range extract.NamedChildren(decl, "type_spec", "type_alias") {
		from := spec
		if !isGrouped {
			from = decl
		}
		name := w.field(spec, "name")
		typ := spec.ChildByField("type")
		kind := typeKind(spec, typ)
		s := w.symbol(kind, name, "", from)
		if kind == extract.KindStruct || kind == extract.KindInterface {
			s.Signature, s.SigText = extract.SignatureSpan(from, openBrace(typ), w.src)
		}
		s.Doc = w.specDoc(spec, decl, isGrouped)
		s.GroupDoc = w.groupDoc(s.Doc, decl, isGrouped)
		w.add(s)
		switch kind {
		case extract.KindStruct:
			w.fields(typ, s.Qualified)
		case extract.KindInterface:
			w.methodElems(typ, s.Qualified)
		}
	}
}

func typeKind(spec, typ parse.Node) extract.Kind {
	if spec.Kind() == "type_spec" && typ != nil {
		switch typ.Kind() {
		case "struct_type":
			return extract.KindStruct
		case "interface_type":
			return extract.KindInterface
		}
	}
	return extract.KindTypeAlias
}

// openBrace returns the offset of the "{" that opens a struct or interface
// body, or the end of the type when there is none.
func openBrace(typ parse.Node) uint {
	for i := uint(0); i < typ.ChildCount(); i++ {
		if c := typ.Child(i); c.Kind() == "{" || c.Kind() == "field_declaration_list" {
			return c.StartByte()
		}
	}
	return typ.EndByte()
}

// fields adds one field symbol per name of every field declaration, and one
// per embedded type (named after the type, as Go does).
func (w *walker) fields(structType parse.Node, container string) {
	for _, list := range extract.NamedChildren(structType, "field_declaration_list") {
		for _, fd := range extract.NamedChildren(list, "field_declaration") {
			names := w.texts(extract.NamedChildren(fd, "field_identifier"))
			if len(names) == 0 {
				names = []string{baseTypeName(w.field(fd, "type"))}
			}
			end := fd.EndByte()
			if tag := fd.ChildByField("tag"); tag != nil {
				end = tag.StartByte()
			}
			for _, name := range names {
				s := w.symbol(extract.KindField, name, container, fd)
				s.Signature, s.SigText = extract.SignatureSpan(fd, end, w.src)
				s.Doc = w.docFor(fd)
				w.add(s)
			}
		}
	}
}

// methodElems adds interface methods; they have no body. The text of
// the other elements (embedded interfaces, type constraints) is recorded
// for the interface's body hash.
func (w *walker) methodElems(iface parse.Node, container string) {
	var embedded []string
	for i := uint(0); i < iface.NamedChildCount(); i++ {
		m := iface.NamedChild(i)
		switch m.Kind() {
		case "comment":
		case "method_elem":
			s := w.symbol(extract.KindMethod, w.field(m, "name"), container, m)
			s.Doc = w.docFor(m)
			w.add(s)
		default:
			embedded = append(embedded, m.Text(w.src))
		}
	}
	if len(embedded) > 0 {
		if w.embedded == nil {
			w.embedded = map[string]string{}
		}
		w.embedded[container] = strings.Join(embedded, "\n")
	}
}

// valueSymbols adds one const or var symbol per declared name. The
// signature is the first line of the spec, so large literals stay out.
func (w *walker) valueSymbols(decl parse.Node) {
	kind := extract.KindVar
	if decl.Kind() == "const_declaration" {
		kind = extract.KindConst
	}
	isGrouped := grouped(decl)
	specs := extract.NamedChildren(decl, "const_spec", "var_spec")
	for _, list := range extract.NamedChildren(decl, "var_spec_list") {
		specs = append(specs, extract.NamedChildren(list, "var_spec")...)
	}
	for _, spec := range specs {
		from := spec
		if !isGrouped {
			from = decl
		}
		var values []string
		if kind == extract.KindConst {
			values = specValues(spec, w.src)
		}
		for i, name := range w.texts(extract.NamedChildren(spec, "identifier")) {
			s := w.symbol(kind, name, "", from)
			if i < len(values) {
				s.Value = values[i]
			}
			s.Signature, s.SigText = extract.SignatureSpan(from, extract.LineEnd(from, w.src), w.src)
			s.Doc = w.specDoc(spec, decl, isGrouped)
			s.GroupDoc = w.groupDoc(s.Doc, decl, isGrouped)
			w.add(s)
		}
	}
}

// specDoc returns the spec's own doc, falling back to the declaration's doc
// for an ungrouped declaration.
func (w *walker) specDoc(spec, decl parse.Node, isGrouped bool) *extract.Doc {
	if d := w.docFor(spec); d != nil || isGrouped {
		return d
	}
	return w.docFor(decl)
}

// groupDoc reports whether an undocumented spec of a parenthesized
// declaration is documented by the declaration's own doc.
func (w *walker) groupDoc(own *extract.Doc, decl parse.Node, isGrouped bool) bool {
	return own == nil && isGrouped && w.docFor(decl) != nil
}

// grouped reports whether a declaration uses the parenthesized form.
func grouped(decl parse.Node) bool {
	for i := uint(0); i < decl.ChildCount(); i++ {
		if k := decl.Child(i).Kind(); k == "(" || k == "var_spec_list" {
			return true
		}
	}
	return false
}

func (w *walker) texts(nodes []parse.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Text(w.src))
	}
	return out
}
