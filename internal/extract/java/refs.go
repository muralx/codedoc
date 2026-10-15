package java

import (
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// chains describes Java receiver expressions for qualifier text.
var chains = extract.ChainGrammar{
	Names:       map[string]bool{"identifier": true, "this": true, "super": true, "type_identifier": true},
	Selectors:   map[string][2]string{"field_access": {"object", "field"}, "scoped_identifier": {"scope", "name"}},
	Invocations: map[string][2]string{"method_invocation": {"object", "name"}},
	Calls:       map[string]string{"object_creation_expression": "type"},
}

// javaLangAnnotations are the java.lang annotations, which never resolve to
// anything in the index.
var javaLangAnnotations = map[string]bool{
	"Override": true, "Deprecated": true, "SuppressWarnings": true, "FunctionalInterface": true,
	"SafeVarargs": true,
}

// builtins are the java.lang types that unqualified uses never resolve to
// anything in the index.
var builtins = map[string]bool{
	"String": true, "Object": true, "Integer": true, "Long": true, "Short": true, "Byte": true,
	"Character": true, "Boolean": true, "Float": true, "Double": true, "Void": true, "var": true,
}

// refWalker collects the references and imports of one Java file. It runs
// after symbol extraction so every reference gets its enclosing symbol.
type refWalker struct {
	*walker
	enc *extract.Encloser
}

// refs walks the whole tree below root in source order.
func (w *walker) refs(root parse.Node) {
	r := &refWalker{walker: w, enc: extract.NewEncloser(w.res.Symbols)}
	r.children(root)
}

func (r *refWalker) children(n parse.Node) {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		r.visit(n.NamedChild(i), n)
	}
}

// visit records the reference n stands for, if any, and descends into the
// children that can hold further references.
func (r *refWalker) visit(n, parent parse.Node) {
	if r.whole(n, parent) {
		return
	}
	switch n.Kind() {
	case "method_invocation":
		r.add(extract.RefCall, n.ChildByField("name"), r.qualifier(n.ChildByField("object")), extract.ArgCount(n.ChildByField("arguments")))
		r.configArgs(n)
	case "object_creation_expression":
		r.instantiate(n)
		return
	}
	r.children(n)
}

// whole handles the nodes whose references are recorded without the
// generic descent, and reports whether n was one.
func (r *refWalker) whole(n, parent parse.Node) bool {
	switch n.Kind() {
	case "import_declaration":
		r.importDecl(n)
	case "package_declaration":
	case "marker_annotation", "annotation":
		r.annotation(n)
	case "superclass", "extends_interfaces":
		r.heritage(extract.RefExtend, n)
	case "super_interfaces":
		r.heritage(extract.RefImplement, n)
	case "field_access":
		if r.qualifiedType(n) {
			return true
		}
		r.add(extract.RefField, n.ChildByField("field"), r.qualifier(n.ChildByField("object")), -1)
		r.visit(n.ChildByField("object"), n)
	case "type_identifier":
		if parent.Kind() != "type_parameter" {
			r.add(extract.RefType, n, "", -1)
		}
	case "scoped_type_identifier":
		r.typeRef(extract.RefType, n)
	default:
		return false
	}
	return true
}

// instantiate records new T<...>(...) as RefInstantiate of T, then visits
// the type arguments, the arguments and an anonymous class body.
func (r *refWalker) instantiate(n parse.Node) {
	t, arity := n.ChildByField("type"), extract.ArgCount(n.ChildByField("arguments"))
	if t != nil && t.Kind() == "generic_type" {
		r.typeRefArity(extract.RefInstantiate, t.NamedChild(0), arity)
		r.children(t.NamedChild(t.NamedChildCount() - 1))
	} else {
		r.typeRefArity(extract.RefInstantiate, t, arity)
	}
	r.configArgs(n)
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); !extract.IsField(n, "type", c) {
			r.visit(c, n)
		}
	}
}

// heritage records each type an extends or implements clause names, and
// the type references inside their type arguments.
func (r *refWalker) heritage(kind extract.RefKind, clause parse.Node) {
	for _, list := range []parse.Node{clause, firstNamed(clause, "type_list")} {
		for i := uint(0); list != nil && i < list.NamedChildCount(); i++ {
			switch c := list.NamedChild(i); c.Kind() {
			case "type_identifier", "scoped_type_identifier":
				r.typeRef(kind, c)
			case "generic_type":
				r.typeRef(kind, c.NamedChild(0))
				r.children(c.NamedChild(c.NamedChildCount() - 1))
			}
		}
	}
}

// typeRef records a reference to a plain or scoped type name.
func (r *refWalker) typeRef(kind extract.RefKind, t parse.Node) { r.typeRefArity(kind, t, -1) }

func (r *refWalker) typeRefArity(kind extract.RefKind, t parse.Node, arity int) {
	switch {
	case t == nil:
	case t.Kind() == "scoped_type_identifier" && t.NamedChildCount() > 0:
		name := t.NamedChild(t.NamedChildCount() - 1)
		r.add(kind, name, strings.TrimSuffix(compact(string(r.src[t.StartByte():name.StartByte()])), "."), arity)
	case t.Kind() == "type_identifier":
		r.add(kind, t, "", arity)
	}
}

// qualifier renders a receiver expression, or "" when there is none. A
// package prefix is dropped: java.util.List becomes List.
func (r *refWalker) qualifier(n parse.Node) string {
	if n == nil {
		return ""
	}
	return stripPackage(chains.Qualifier(n, r.src))
}

// annotation records the annotation type as a type reference (java.lang
// annotations aside), the placeholders of a Spring @Value, and the
// references in its arguments: @JsonDeserialize(using = Foo.class) refers
// to Foo.
func (r *refWalker) annotation(n parse.Node) {
	name := n.ChildByField("name")
	switch {
	case name == nil:
		return
	case name.Kind() == "scoped_identifier":
		r.add(extract.RefType, name.ChildByField("name"), r.qualifier(name.ChildByField("scope")), -1)
	case !javaLangAnnotations[name.Text(r.src)]:
		r.add(extract.RefType, name, "", -1)
	}
	if args := n.ChildByField("arguments"); args != nil {
		r.valuePlaceholders(name, args)
		r.children(args)
	}
}

// add appends a reference named by name; unqualified java.lang builtins
// are skipped.
func (r *refWalker) add(kind extract.RefKind, name parse.Node, qualifier string, arity int) {
	if name == nil || (qualifier == "" && builtins[name.Text(r.src)]) {
		return
	}
	r.res.References = append(r.res.References, extract.Reference{
		Kind: kind, Name: name.Text(r.src), Qualifier: qualifier, Enclosing: r.enc.At(name.StartByte()),
		Line: extract.Line(name), Col: extract.Col(name), Arity: arity,
	})
}

// firstNamed returns the first named child of n of the given kind, or nil.
func firstNamed(n parse.Node, kind string) parse.Node {
	if c := extract.NamedChildren(n, kind); len(c) > 0 {
		return c[0]
	}
	return nil
}
