package java

import (
	"slices"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// walker collects the records of one Java file.
type walker struct {
	src      []byte
	testFile bool
	res      *extract.FileResult
}

// typeKinds maps type declarations to symbol kinds.
var typeKinds = map[string]extract.Kind{
	"class_declaration": extract.KindClass, "record_declaration": extract.KindClass,
	"interface_declaration": extract.KindInterface, "annotation_type_declaration": extract.KindInterface,
	"enum_declaration": extract.KindEnum,
}

// testAnnotations mark JUnit test methods.
var testAnnotations = []string{"Test", "ParameterizedTest", "RepeatedTest"}

// member adds the declaration n found at top level (container "") or in
// the body of container. iface is true in an interface or annotation type
// body, whose members are implicitly public.
func (w *walker) member(n parse.Node, container string, iface bool) {
	switch k := n.Kind(); {
	case typeKinds[k] != "":
		w.typeDecl(n, container, iface)
	case k == "method_declaration" || k == "annotation_type_element_declaration":
		w.method(n, w.name(n), container, iface)
	case k == "constructor_declaration":
		w.method(n, "<init>", container, iface)
	case k == "field_declaration" || k == "constant_declaration":
		w.fields(n, container, iface)
	}
}

// typeDecl adds a class, interface, enum, record or annotation type and
// walks its body; enum constants become const symbols.
func (w *walker) typeDecl(n parse.Node, container string, iface bool) {
	kind := typeKinds[n.Kind()]
	s := w.symbol(kind, w.name(n), container, n, iface)
	body := n.ChildByField("body")
	if body == nil {
		w.add(s)
		return
	}
	w.withBody(&s, n, body)
	w.add(s)
	if n.Kind() == "record_declaration" {
		w.recordComponents(n, s.Qualified)
	}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		switch c := body.NamedChild(i); c.Kind() {
		case "compact_constructor_declaration":
			w.compactConstructor(c, n, s.Qualified)
		case "enum_constant":
			k := w.symbol(extract.KindConst, w.name(c), s.Qualified, c, true)
			k.Signature, k.SigText = extract.SignatureSpan(c, w.lineEnd(c.StartByte(), c.EndByte()), w.src)
			w.add(k)
		case "enum_body_declarations":
			for j := uint(0); j < c.NamedChildCount(); j++ {
				w.member(c.NamedChild(j), s.Qualified, false)
			}
		default:
			w.member(c, s.Qualified, kind == extract.KindInterface)
		}
	}
}

// method adds a method, constructor or annotation element with its
// signature hash; test methods carry a JUnit test annotation.
func (w *walker) method(n parse.Node, name, container string, iface bool) {
	s := w.symbol(extract.KindMethod, name, container, n, iface)
	s.SigHash = w.sigHash(n.ChildByField("parameters"))
	if body := n.ChildByField("body"); body != nil {
		w.withBody(&s, n, body)
	}
	s.IsTest = w.testFile && slices.ContainsFunc(w.annotations(n), func(a string) bool {
		return slices.Contains(testAnnotations, a)
	})
	w.add(s)
}

// fields adds one field per declarator, a const when it is static final
// or declared in an interface. The signature is the declaration's first
// line.
func (w *walker) fields(n parse.Node, container string, iface bool) {
	kind := extract.KindField
	if iface || n.Kind() == "constant_declaration" || (w.hasModifier(n, "static") && w.hasModifier(n, "final")) {
		kind = extract.KindConst
	}
	for _, d := range extract.NamedChildren(n, "variable_declarator") {
		s := w.symbol(kind, w.name(d), container, n, iface)
		from := sigStart(n)
		s.Signature, s.SigText = extract.SignatureSpan(from, w.lineEnd(from.StartByte(), declEnd(n)), w.src)
		if kind == extract.KindConst || w.hasModifier(n, "final") {
			s.Value, _ = w.literal(d.ChildByField("value"))
		}
		w.add(s)
	}
}

// symbol fills the shared fields. The span covers n with its annotations;
// the signature runs from the first modifier keyword after the
// annotations to the end of the declaration until withBody narrows it.
func (w *walker) symbol(kind extract.Kind, name, container string, n parse.Node, iface bool) extract.Symbol {
	q := name
	if container != "" {
		q = container + "." + name
	}
	s := extract.Symbol{
		Kind: kind, Name: name, Qualified: q, Container: container, Span: extract.SpanOf(n),
		Visibility: w.visibility(n, iface), BodyHash: extract.NodeHash(w.src, n), Doc: w.docFor(n),
	}
	s.Signature, s.SigText = extract.SignatureSpan(sigStart(n), declEnd(n), w.src)
	return s
}

// withBody ends the signature where body starts and hashes the signature
// text with the body.
func (w *walker) withBody(s *extract.Symbol, n, body parse.Node) {
	s.Signature, s.SigText = extract.SignatureSpan(sigStart(n), body.StartByte(), w.src)
	s.BodyHash = extract.CodeHash(s.SigText, w.src, body, body.StartByte(), body.EndByte())
}

func (w *walker) add(s extract.Symbol) {
	if s.Name != "" {
		w.res.Symbols = append(w.res.Symbols, s)
	}
}

// name returns the text of n's name field, or "" when absent.
func (w *walker) name(n parse.Node) string {
	if c := n.ChildByField("name"); c != nil {
		return c.Text(w.src)
	}
	return ""
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

// declEnd returns the end of n without its terminating semicolon.
func declEnd(n parse.Node) uint {
	if last := n.Child(n.ChildCount() - 1); last != nil && last.Kind() == ";" {
		return last.StartByte()
	}
	return n.EndByte()
}
