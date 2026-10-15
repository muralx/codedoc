package golang

import (
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// callType is valueType for a call: the callee followed by "()" for a
// function or a package's function (kit.New()), whose first result the
// resolver reads from its signature, and the declared result of a
// method called on a typed value (extract.ResultOf). A callee that is a
// variable (a function value), or a method of a variable of unknown
// type (kit := m; kit.New()), is no function the resolver can look up:
// its result is not evident.
func (r *refWalker) callType(call parse.Node) string {
	fn, args := call.ChildByField("function"), call.ChildByField("arguments")
	if fn == nil {
		return ""
	}
	switch fn.Kind() {
	case "identifier":
		name := fn.Text(r.src)
		if name == "new" && args != nil && args.NamedChildCount() == 1 {
			return r.typeName(args.NamedChild(0))
		}
		if _, local := r.locals.Lookup(name); !local && !predeclared[name] {
			return name + "()"
		}
	case "selector_expression":
		return r.methodType(fn.ChildByField("operand"), fn.ChildByField("field"))
	}
	return ""
}

// methodType is callType for the call of op.method(...).
func (r *refWalker) methodType(op, method parse.Node) string {
	if op == nil || method == nil {
		return ""
	}
	if op.Kind() == "identifier" {
		if _, local := r.locals.Lookup(op.Text(r.src)); !local {
			return op.Text(r.src) + "." + method.Text(r.src) + "()"
		}
	}
	return extract.ResultOf(r.exprType(op), method.Text(r.src))
}

// exprType returns the type of an operand as far as declarations make
// it evident: the type of a variable, a name no scope declares as the
// root of a type path (extract.OuterPrefix: the method receiver or a
// package variable, which the resolver tells apart), a field of either
// (x.f), the result of a call (see callType). It is empty for anything
// else.
func (r *refWalker) exprType(v parse.Node) string {
	if v == nil {
		return ""
	}
	switch v.Kind() {
	case "identifier":
		if typ, local := r.locals.Lookup(v.Text(r.src)); local {
			return typ
		}
		return extract.OuterPrefix + v.Text(r.src)
	case "selector_expression":
		if field := v.ChildByField("field"); field != nil {
			return extract.FieldOf(r.exprType(v.ChildByField("operand")), field.Text(r.src))
		}
	case "parenthesized_expression":
		if v.NamedChildCount() == 1 {
			return r.exprType(v.NamedChild(0))
		}
	case "call_expression":
		return r.callType(v)
	}
	return ""
}

// typeName returns the name of a named type, through a pointer and
// without type arguments: T, pkg.T, and extract.BuiltinType for a
// predeclared type and for a type a function declares (see localType);
// for a slice, an array or a map extract.ArrayPrefix and the type of
// its elements. It is empty for any other type expression (functions,
// channels, ...), whose values have no methods.
func (r *refWalker) typeName(typ parse.Node) string {
	if typ == nil {
		return ""
	}
	switch typ.Kind() {
	case "pointer_type":
		if typ.NamedChildCount() == 1 {
			return r.typeName(typ.NamedChild(0))
		}
	case "slice_type", "array_type", "implicit_length_array_type":
		return extract.ArrayOf(r.typeName(typ.ChildByField("element")))
	case "map_type":
		return extract.ArrayOf(r.typeName(typ.ChildByField("value")))
	case "generic_type":
		return r.typeName(typ.ChildByField("type"))
	case "qualified_type":
		return typ.Text(r.src)
	case "type_identifier":
		name := typ.Text(r.src)
		if _, local := r.locals.Lookup(name); local || predeclared[name] {
			return extract.BuiltinType
		}
		return name
	}
	return ""
}
