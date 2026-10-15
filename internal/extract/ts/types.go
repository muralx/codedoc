package ts

import (
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// arrays are the generic types of the standard library a for...of loop
// takes elements of the type argument from.
var arrays = map[string]bool{"Array": true, "ReadonlyArray": true, "Set": true, "ReadonlySet": true, "Iterable": true}

// annotated returns the type a type annotation names (see typeName).
func (r *refWalker) annotated(annotation parse.Node) string {
	if annotation == nil || annotation.NamedChildCount() != 1 {
		return ""
	}
	return r.typeName(annotation.NamedChild(0))
}

// typeName returns the named type a type expression stands for, without
// type arguments: T, ns.T, and through "| undefined" or "| null" the
// type they make optional; extract.BuiltinType for a builtin and a
// predefined type (string, number); for an array (T[], readonly T[],
// Array<T>) extract.ArrayPrefix and the type of its elements. It is
// empty for a type parameter in scope and for any other type (unions,
// literals, ...).
func (r *refWalker) typeName(typ parse.Node) string {
	switch typ.Kind() {
	case "predefined_type":
		return extract.BuiltinType
	case "array_type":
		return r.arrayOf(typ)
	case "readonly_type", "parenthesized_type", "union_type":
		return r.wrapped(typ)
	case "generic_type":
		name := typ.ChildByField("name")
		if args := typ.ChildByField("type_arguments"); name != nil && arrays[name.Text(r.src)] && args != nil {
			return r.arrayOf(args)
		}
		typ = name
	}
	name := r.plainName(typ, "type_identifier", "nested_type_identifier")
	if name == "" || isTypeParameter(typ, name, r.src) {
		return ""
	}
	return name
}

// wrapped is typeName for a type that wraps one other: readonly T, (T),
// and a union that only adds undefined or null to T.
func (r *refWalker) wrapped(typ parse.Node) string {
	inner := []parse.Node{}
	if typ.Kind() == "union_type" {
		inner = r.optional(typ, inner)
	} else if typ.NamedChildCount() == 1 {
		inner = append(inner, typ.NamedChild(0))
	}
	if len(inner) != 1 {
		return ""
	}
	return r.typeName(inner[0])
}

// arrayOf returns the array type of the one type holder names (the
// element of an array type, the argument of Array<T>).
func (r *refWalker) arrayOf(holder parse.Node) string {
	if holder.NamedChildCount() != 1 {
		return extract.BuiltinType
	}
	return extract.ArrayOf(r.typeName(holder.NamedChild(0)))
}

// optional appends to out the members of a union type that are neither
// undefined nor null, through nested unions.
func (r *refWalker) optional(union parse.Node, out []parse.Node) []parse.Node {
	for i := uint(0); i < union.NamedChildCount(); i++ {
		m := union.NamedChild(i)
		switch text := m.Text(r.src); {
		case m.Kind() == "union_type":
			out = r.optional(m, out)
		case text != "undefined" && text != "null":
			out = append(out, m)
		}
	}
	return out
}

// typeOf returns the type a value makes evident from declared types
// alone: the type of the variable it names, the class it constructs,
// this, the declared type of a property of such a value (a.b, a type
// path: see extract.FieldOf), the declared result of a function or of a
// method called on one (see resultOf), and an awaited one
// (extract.Awaited). It is empty for any other value.
func (r *refWalker) typeOf(v parse.Node) string {
	if v == nil {
		return ""
	}
	switch v.Kind() {
	case "identifier":
		typ, _ := r.locals.Lookup(v.Text(r.src))
		return typ
	case "this":
		return extract.SelfRoot
	case "new_expression":
		return r.constructed(v)
	case "parenthesized_expression", "non_null_expression":
		return r.typeOf(v.NamedChild(0))
	case "await_expression":
		return extract.Awaited(r.typeOf(v.NamedChild(0)))
	case "member_expression":
		if typ := r.ownProperty(v); typ != "" {
			return typ
		}
		return r.memberType(v, extract.FieldOf)
	case "call_expression":
		return r.resultOf(v.ChildByField("function"))
	}
	return ""
}

// resultOf returns the type of a call of fn: the declared result of a
// method of a typed object (extract.ResultOf), or for a function no
// variable or parameter hides its name followed by "()", whose declared
// result the resolver reads from its signature.
func (r *refWalker) resultOf(fn parse.Node) string {
	switch {
	case fn == nil:
	case fn.Kind() == "member_expression":
		return r.memberType(fn, extract.ResultOf)
	case fn.Kind() == "identifier":
		if name := fn.Text(r.src); !r.locals.Inner(name) && !builtins[name] {
			return name + "()"
		}
	}
	return ""
}

// memberType returns the type path of a member of an object: step
// (extract.FieldOf or extract.ResultOf) taken from the type of the
// object.
func (r *refWalker) memberType(m parse.Node, step func(typ, name string) string) string {
	obj, prop := m.ChildByField("object"), m.ChildByField("property")
	if obj == nil || prop == nil || prop.Kind() != "property_identifier" {
		return ""
	}
	return step(r.typeOf(obj), prop.Text(r.src))
}

// ownProperty returns the type recorded for this.x where the class
// itself makes it evident (this.x = new T(), a constructor parameter
// property), "" for any other member.
func (r *refWalker) ownProperty(m parse.Node) string {
	obj, prop := m.ChildByField("object"), m.ChildByField("property")
	if obj == nil || prop == nil || obj.Kind() != "this" {
		return ""
	}
	return r.locals.Property(r.enc.At(m.StartByte()), prop.Text(r.src))
}

// plainName returns the text of a name node of kind leaf
// (extract.BuiltinType for a builtin), or of kind dotted when it is a
// chain of plain names; anything else gives "".
func (r *refWalker) plainName(n parse.Node, leaf, dotted string) string {
	if n == nil {
		return ""
	}
	switch n.Kind() {
	case leaf:
		if name := n.Text(r.src); !builtins[name] {
			return name
		}
		return extract.BuiltinType
	case dotted:
		if q := chains.Qualifier(n, r.src); q != "" && !hasPlaceholder(q) {
			return q
		}
	}
	return ""
}

// hasPlaceholder reports whether a qualifier chain holds a segment that
// is not a plain name.
func hasPlaceholder(q string) bool {
	for i := 0; i+1 < len(q); i++ {
		if q[i] == '(' && q[i+1] == ')' {
			return true
		}
	}
	return false
}

// isTypeParameter reports whether name is a type parameter declared by
// an ancestor of n (function, method, class, interface or type alias).
func isTypeParameter(n parse.Node, name string, src []byte) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		params := p.ChildByField("type_parameters")
		if params == nil {
			continue
		}
		for _, tp := range extract.NamedChildren(params, "type_parameter") {
			if id := tp.ChildByField("name"); id != nil && id.Text(src) == name {
				return true
			}
		}
	}
	return false
}
